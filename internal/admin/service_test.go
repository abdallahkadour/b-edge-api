// Package admin tests. In-memory mock repository and a mock audit
// repository, no database needed - matching every other domain in this
// codebase.
package admin

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

type mockRepo struct {
	pendingResult []*PendingArtist
	pendingErr    error

	updateStatusRows int64
	updateStatusErr  error

	lastArtistID    uuid.UUID
	lastStatus      string
	lastTrialPlan   string
	lastTrialEndsAt time.Time
}

func (m *mockRepo) ListPending(_ context.Context) ([]*PendingArtist, error) {
	return m.pendingResult, m.pendingErr
}

func (m *mockRepo) UpdateStatus(_ context.Context, artistID uuid.UUID, newStatus string) (int64, error) {
	m.lastArtistID = artistID
	m.lastStatus = newStatus
	return m.updateStatusRows, m.updateStatusErr
}

func (m *mockRepo) ApproveWithTrialSubscription(_ context.Context, artistID uuid.UUID, trialPlanCode string, trialEndsAt time.Time) (int64, error) {
	m.lastArtistID = artistID
	m.lastStatus = "active"
	m.lastTrialPlan = trialPlanCode
	m.lastTrialEndsAt = trialEndsAt
	return m.updateStatusRows, m.updateStatusErr
}

// mockAudit records every event it's asked to log, so a test can assert
// on what got written without touching a real audit_events table.
type mockAudit struct {
	events []audit.Event
	err    error
}

func (m *mockAudit) Log(_ context.Context, e audit.Event) error {
	m.events = append(m.events, e)
	return m.err
}

func newTestService(repo Repository, a audit.Repository) Service {
	return NewService(repo, a)
}

// ── ListPending ──────────────────────────────────────────────────────────────

func TestListPending_ReturnsQueue(t *testing.T) {
	want := []*PendingArtist{
		{ArtistID: uuid.New(), Name: "Sarah", Email: "sarah@example.com"},
	}
	repo := &mockRepo{pendingResult: want}
	svc := newTestService(repo, nil)

	got, err := svc.ListPending(context.Background())

	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestListPending_Empty_ReturnsNoError(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	got, err := svc.ListPending(context.Background())

	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestListPending_RepoError_Propagates(t *testing.T) {
	repo := &mockRepo{pendingErr: errors.New("db down")}
	svc := newTestService(repo, nil)

	_, err := svc.ListPending(context.Background())

	require.Error(t, err)
}

// ── Approve ──────────────────────────────────────────────────────────────────

func TestApprove_Pending_Succeeds(t *testing.T) {
	artistID := uuid.New()
	adminID := uuid.New()
	repo := &mockRepo{updateStatusRows: 1}
	a := &mockAudit{}
	svc := newTestService(repo, a)

	err := svc.Approve(context.Background(), artistID, adminID, "1.2.3.4")

	require.NoError(t, err)
	assert.Equal(t, artistID, repo.lastArtistID)
	assert.Equal(t, "active", repo.lastStatus, "approve must transition the artist to active")
}

func TestApprove_Pending_CreatesTrialSubscription(t *testing.T) {
	// Regression guard for the gap this closes: before
	// ApproveWithTrialSubscription existed, Approve called plain
	// UpdateStatus and no subscription was ever created, so every artist
	// approved after Aug 29, 2026 read as permanently past_due under
	// billing.DeriveStatus with no subscriptions row at all.
	artistID := uuid.New()
	repo := &mockRepo{updateStatusRows: 1}
	svc := newTestService(repo, &mockAudit{})

	before := time.Now()
	err := svc.Approve(context.Background(), artistID, uuid.New(), "1.2.3.4")
	after := time.Now()

	require.NoError(t, err)
	assert.Equal(t, artistID, repo.lastArtistID)
	assert.NotEmpty(t, repo.lastTrialPlan, "approval must create a subscription with a real plan code")
	assert.True(t, repo.lastTrialEndsAt.After(before), "trial end must be in the future")
	assert.True(t, repo.lastTrialEndsAt.Before(after.AddDate(0, 0, 31)), "trial must not exceed ~30 days")
}

func TestApprove_NotPending_ReturnsConflict(t *testing.T) {
	// The guarded UPDATE matched zero rows - already decided once (active
	// or rejected) or never submitted. Must not be treated as success.
	artistID := uuid.New()
	repo := &mockRepo{updateStatusRows: 0}
	svc := newTestService(repo, &mockAudit{})

	err := svc.Approve(context.Background(), artistID, uuid.New(), "1.2.3.4")

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "NOT_PENDING", appErr.Code)
}

func TestApprove_RepoError_Propagates(t *testing.T) {
	repo := &mockRepo{updateStatusErr: errors.New("db down")}
	svc := newTestService(repo, &mockAudit{})

	err := svc.Approve(context.Background(), uuid.New(), uuid.New(), "1.2.3.4")

	require.Error(t, err)
}

func TestApprove_Success_WritesAuditEvent(t *testing.T) {
	artistID := uuid.New()
	adminID := uuid.New()
	repo := &mockRepo{updateStatusRows: 1}
	a := &mockAudit{}
	svc := newTestService(repo, a)

	err := svc.Approve(context.Background(), artistID, adminID, "1.2.3.4")

	require.NoError(t, err)
	require.Len(t, a.events, 1)
	evt := a.events[0]
	assert.Equal(t, &adminID, evt.ActorID)
	assert.Equal(t, "admin", evt.ActorRole)
	assert.Equal(t, "artist", evt.EntityType)
	assert.Equal(t, artistID, evt.EntityID)
	assert.Equal(t, "approved", evt.Action)
	assert.Equal(t, "1.2.3.4", evt.IPAddress)
}

func TestApprove_NotPending_NeverWritesAuditEvent(t *testing.T) {
	// A rejected transition must not leave a phantom "approved" record.
	repo := &mockRepo{updateStatusRows: 0}
	a := &mockAudit{}
	svc := newTestService(repo, a)

	err := svc.Approve(context.Background(), uuid.New(), uuid.New(), "1.2.3.4")

	require.Error(t, err)
	assert.Empty(t, a.events)
}

func TestApprove_AuditLogFailure_DoesNotFailTheApproval(t *testing.T) {
	// Best-effort by design - an approval that already committed in
	// Postgres must never be undone or reported as failed just because
	// the audit write failed.
	repo := &mockRepo{updateStatusRows: 1}
	a := &mockAudit{err: errors.New("audit table unreachable")}
	svc := newTestService(repo, a)

	err := svc.Approve(context.Background(), uuid.New(), uuid.New(), "1.2.3.4")

	require.NoError(t, err)
}

func TestApprove_NilAuditRepository_UsesNopWithoutPanicking(t *testing.T) {
	// NewService substitutes a NopRepository when auditRepo is nil - a
	// caller passing nil (as a test easily might) must not panic.
	repo := &mockRepo{updateStatusRows: 1}
	svc := newTestService(repo, nil)

	err := svc.Approve(context.Background(), uuid.New(), uuid.New(), "1.2.3.4")

	require.NoError(t, err)
}

// ── Reject ───────────────────────────────────────────────────────────────────

func TestReject_Pending_Succeeds(t *testing.T) {
	artistID := uuid.New()
	repo := &mockRepo{updateStatusRows: 1}
	svc := newTestService(repo, &mockAudit{})

	err := svc.Reject(context.Background(), artistID, uuid.New(), DecisionRequest{}, "1.2.3.4")

	require.NoError(t, err)
	assert.Equal(t, artistID, repo.lastArtistID)
	assert.Equal(t, "rejected", repo.lastStatus)
}

func TestReject_NotPending_ReturnsConflict(t *testing.T) {
	repo := &mockRepo{updateStatusRows: 0}
	svc := newTestService(repo, &mockAudit{})

	err := svc.Reject(context.Background(), uuid.New(), uuid.New(), DecisionRequest{}, "1.2.3.4")

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "NOT_PENDING", appErr.Code)
}

func TestReject_RepoError_Propagates(t *testing.T) {
	repo := &mockRepo{updateStatusErr: errors.New("db down")}
	svc := newTestService(repo, &mockAudit{})

	err := svc.Reject(context.Background(), uuid.New(), uuid.New(), DecisionRequest{}, "1.2.3.4")

	require.Error(t, err)
}

func TestReject_WithReason_WritesReasonInAuditEvent(t *testing.T) {
	artistID := uuid.New()
	adminID := uuid.New()
	reason := "Portfolio photos did not match the submitted category"
	repo := &mockRepo{updateStatusRows: 1}
	a := &mockAudit{}
	svc := newTestService(repo, a)

	err := svc.Reject(context.Background(), artistID, adminID, DecisionRequest{Reason: &reason}, "1.2.3.4")

	require.NoError(t, err)
	require.Len(t, a.events, 1)
	evt := a.events[0]
	assert.Equal(t, &adminID, evt.ActorID)
	assert.Equal(t, "admin", evt.ActorRole)
	assert.Equal(t, "artist", evt.EntityType)
	assert.Equal(t, artistID, evt.EntityID)
	assert.Equal(t, "rejected", evt.Action)

	values, ok := evt.NewValues.(map[string]any)
	require.True(t, ok, "reject must record the reason in new_values")
	assert.Equal(t, &reason, values["reason"])
}

func TestReject_NoReason_StillSucceeds(t *testing.T) {
	// A reason is optional per DecisionRequest's validate tag - nil must
	// not be treated as a validation failure at the service layer.
	repo := &mockRepo{updateStatusRows: 1}
	a := &mockAudit{}
	svc := newTestService(repo, a)

	err := svc.Reject(context.Background(), uuid.New(), uuid.New(), DecisionRequest{}, "1.2.3.4")

	require.NoError(t, err)
	require.Len(t, a.events, 1)
	assert.Nil(t, a.events[0].NewValues.(map[string]any)["reason"])
}

func TestReject_NotPending_NeverWritesAuditEvent(t *testing.T) {
	repo := &mockRepo{updateStatusRows: 0}
	a := &mockAudit{}
	svc := newTestService(repo, a)

	err := svc.Reject(context.Background(), uuid.New(), uuid.New(), DecisionRequest{}, "1.2.3.4")

	require.Error(t, err)
	assert.Empty(t, a.events)
}

func TestReject_AuditLogFailure_DoesNotFailTheRejection(t *testing.T) {
	repo := &mockRepo{updateStatusRows: 1}
	a := &mockAudit{err: errors.New("audit table unreachable")}
	svc := newTestService(repo, a)

	err := svc.Reject(context.Background(), uuid.New(), uuid.New(), DecisionRequest{}, "1.2.3.4")

	require.NoError(t, err)
}
