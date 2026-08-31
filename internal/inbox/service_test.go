// Package inbox service tests. Hand-written mock repository, no database -
// matching the pattern in internal/review and internal/billing.
//
// Bundling and the ownership scoping are enforced by SQL (migration 030's
// partial unique index, and a user_id predicate in each UPDATE), so those
// are verified against the real database rather than asserted here. What
// these cover is the service's contract: what it returns, what it maps to
// an HTTP status, and the invariants a client depends on.
package inbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// ── Mock repository ───────────────────────────────────────────────────────────

type mockRepo struct {
	feed        []Notification
	feedErr     error
	unread      int
	unreadErr   error
	markReadErr error
	markAllRows int64
	markAllErr  error
	archiveErr  error
	createErr   error
	// captured args for assertions
	lastCreate     *CreateParams
	lastUnreadOnly bool
	lastLimit      int
}

func (m *mockRepo) Create(_ context.Context, p CreateParams) error {
	m.lastCreate = &p
	return m.createErr
}
func (m *mockRepo) ListFeed(_ context.Context, _ uuid.UUID, unreadOnly bool, limit int) ([]Notification, error) {
	m.lastUnreadOnly, m.lastLimit = unreadOnly, limit
	return m.feed, m.feedErr
}
func (m *mockRepo) CountUnread(_ context.Context, _ uuid.UUID) (int, error) {
	return m.unread, m.unreadErr
}
func (m *mockRepo) MarkRead(_ context.Context, _, _ uuid.UUID) error { return m.markReadErr }
func (m *mockRepo) MarkAllRead(_ context.Context, _ uuid.UUID) (int64, error) {
	return m.markAllRows, m.markAllErr
}
func (m *mockRepo) Archive(_ context.Context, _, _ uuid.UUID) error { return m.archiveErr }

func newTestService(repo Repository) *Service { return NewService(repo) }

func strPtr(s string) *string { return &s }

func notif(unread bool) Notification {
	n := Notification{ID: uuid.New(), Kind: KindDeliveryFailed, Level: LevelActionRequired,
		Title: "A customer could not be notified", ItemCount: 1, CreatedAt: time.Now()}
	if !unread {
		t := time.Now()
		n.ReadAt = &t
	}
	return n
}

// ── Feed ──────────────────────────────────────────────────────────────────────

// The badge and the panel are fetched together on purpose. Fetching them in
// two calls lets a read landing between them show an empty list under a
// non-zero badge, which reads as a bug to the user.
func TestGetFeed_ReturnsCountAlongsideList(t *testing.T) {
	repo := &mockRepo{feed: []Notification{notif(true), notif(true)}, unread: 2}
	svc := newTestService(repo)

	got, err := svc.GetFeed(context.Background(), uuid.New(), false, 20)

	require.NoError(t, err)
	assert.Len(t, got.Notifications, 2)
	assert.Equal(t, 2, got.UnreadCount)
}

// An empty feed must serialise as [] rather than null, so a client can
// iterate without a nil check.
func TestGetFeed_Empty_ReturnsEmptySliceNotNil(t *testing.T) {
	repo := &mockRepo{feed: []Notification{}, unread: 0}
	svc := newTestService(repo)

	got, err := svc.GetFeed(context.Background(), uuid.New(), false, 20)

	require.NoError(t, err)
	assert.NotNil(t, got.Notifications)
	assert.Empty(t, got.Notifications)
}

func TestGetFeed_UnreadOnly_PassedThrough(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.GetFeed(context.Background(), uuid.New(), true, 5)

	require.NoError(t, err)
	assert.True(t, repo.lastUnreadOnly)
	assert.Equal(t, 5, repo.lastLimit)
}

func TestGetFeed_RepoError_Propagates(t *testing.T) {
	svc := newTestService(&mockRepo{feedErr: errors.New("db down")})

	_, err := svc.GetFeed(context.Background(), uuid.New(), false, 20)

	require.Error(t, err)
}

// A failing count must not return a feed with a silently-wrong badge - a
// zero badge over unread items is worse than an error.
func TestGetFeed_CountFails_DoesNotReturnPartialResult(t *testing.T) {
	repo := &mockRepo{feed: []Notification{notif(true)}, unreadErr: errors.New("db down")}
	svc := newTestService(repo)

	_, err := svc.GetFeed(context.Background(), uuid.New(), false, 20)

	require.Error(t, err, "a wrong badge is worse than no answer")
}

// ── Limit clamping ────────────────────────────────────────────────────────────

func TestClampLimit_Bounds(t *testing.T) {
	assert.Equal(t, defaultFeedLimit, clampLimit(0), "unspecified takes the default")
	assert.Equal(t, defaultFeedLimit, clampLimit(-5), "negative cannot mean unbounded")
	assert.Equal(t, maxFeedLimit, clampLimit(10000), "a caller cannot request the whole table")
	assert.Equal(t, 7, clampLimit(7))
}

// ── Ownership ─────────────────────────────────────────────────────────────────

// Another user's notification is a 404, not a 403 - the same posture as
// billing invoices and media tags, so IDs cannot be probed by watching the
// status code.
func TestMarkRead_ForeignNotification_Returns404NotForbidden(t *testing.T) {
	svc := newTestService(&mockRepo{markReadErr: ErrNotificationNotFound})

	err := svc.MarkRead(context.Background(), uuid.New(), uuid.New())

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "NOTIFICATION_NOT_FOUND", appErr.Code)
	assert.Equal(t, fiber.StatusNotFound, appErr.HTTPStatus)
}

func TestArchive_ForeignNotification_Returns404(t *testing.T) {
	svc := newTestService(&mockRepo{archiveErr: ErrNotificationNotFound})

	err := svc.Archive(context.Background(), uuid.New(), uuid.New())

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "NOTIFICATION_NOT_FOUND", appErr.Code)
}

// Re-reading an already-read notification is a no-op, not an error. A
// double-tap on a feed row must not produce a visible failure.
func TestMarkRead_AlreadyRead_Succeeds(t *testing.T) {
	svc := newTestService(&mockRepo{})

	require.NoError(t, svc.MarkRead(context.Background(), uuid.New(), uuid.New()))
}

func TestMarkAllRead_Succeeds(t *testing.T) {
	svc := newTestService(&mockRepo{markAllRows: 4})

	require.NoError(t, svc.MarkAllRead(context.Background(), uuid.New()))
}

// ── Notify ────────────────────────────────────────────────────────────────────

func TestNotify_FilesTheNotification(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)
	userID := uuid.New()
	body := "We could not deliver a WhatsApp message."

	err := svc.Notify(context.Background(), CreateParams{
		UserID: userID, Kind: KindDeliveryFailed, Level: LevelActionRequired,
		Title: "A customer could not be notified", Body: &body,
		GroupKey: strPtr("delivery_failed"),
	})

	require.NoError(t, err)
	require.NotNil(t, repo.lastCreate)
	assert.Equal(t, userID, repo.lastCreate.UserID)
	assert.Equal(t, LevelActionRequired, repo.lastCreate.Level)
	assert.Equal(t, "delivery_failed", *repo.lastCreate.GroupKey)
}

// Guard against a producer filing something unrenderable. A feed row with
// no title is a blank line the user cannot act on or dismiss meaningfully.
func TestNotify_MissingRequiredFields_Rejected(t *testing.T) {
	svc := newTestService(&mockRepo{})

	require.Error(t, svc.Notify(context.Background(), CreateParams{Kind: "x", Title: "y"}),
		"a notification with no user has nowhere to go")
	require.Error(t, svc.Notify(context.Background(), CreateParams{UserID: uuid.New(), Title: "y"}),
		"kind drives grouping and rendering")
	require.Error(t, svc.Notify(context.Background(), CreateParams{UserID: uuid.New(), Kind: "x"}),
		"a titleless row is a blank line in the feed")
}

// ── Level semantics ───────────────────────────────────────────────────────────

// action_required is the level that means a human must DO something. It is
// the whole reason delivery failures are surfaced, so it must survive the
// round trip rather than being defaulted away.
func TestNotify_ActionRequiredLevel_Preserved(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	err := svc.Notify(context.Background(), CreateParams{
		UserID: uuid.New(), Kind: KindRefundDue, Level: LevelActionRequired, Title: "Refund owed",
	})

	require.NoError(t, err)
	assert.Equal(t, LevelActionRequired, repo.lastCreate.Level)
}

func TestNotification_IsUnread(t *testing.T) {
	assert.True(t, notif(true).IsUnread())
	assert.False(t, notif(false).IsUnread())
}
