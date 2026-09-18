package report

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
)

type mockRepo struct {
	created      *Report
	createErr    error
	belongs      bool
	belongsErr   error
	artistID     uuid.UUID
	artistErr    error
	resolveRows  int64
	resolveErr   error
	resolveCalls int
	lastStatus   Status
}

func (m *mockRepo) Create(_ context.Context, r *Report) error {
	if m.createErr != nil {
		return m.createErr
	}
	r.ID = uuid.New()
	r.Status = StatusOpen
	m.created = r
	return nil
}
func (m *mockRepo) ListForReporter(context.Context, uuid.UUID) ([]*Report, error) { return nil, nil }
func (m *mockRepo) ListQueue(context.Context, bool) ([]*AdminResponse, error)     { return nil, nil }
func (m *mockRepo) Resolve(_ context.Context, _, _ uuid.UUID, s Status, _ *string) (int64, error) {
	m.resolveCalls++
	m.lastStatus = s
	return m.resolveRows, m.resolveErr
}
func (m *mockRepo) BookingBelongsTo(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return m.belongs, m.belongsErr
}
func (m *mockRepo) ArtistIDForBooking(context.Context, uuid.UUID) (uuid.UUID, error) {
	return m.artistID, m.artistErr
}

type mockAudit struct{ events []audit.Event }

func (m *mockAudit) Log(_ context.Context, e audit.Event) error {
	m.events = append(m.events, e)
	return nil
}

func newSvc(r Repository) *Service { return NewService(r, &mockAudit{}) }

func strp(s string) *string { return &s }

// ── Who may report what ───────────────────────────────────────────────────────

// The ownership check is what stops someone filing reports about strangers'
// appointments, and it is the reason reports require an account at all.
func TestCreate_RefusesABookingTheReporterIsNotPartyTo(t *testing.T) {
	repo := &mockRepo{belongs: false}
	svc := newSvc(repo)
	id := uuid.New().String()

	_, err := svc.Create(context.Background(), uuid.New(), "customer", CreateReportRequest{
		BookingID: &id, Category: "did_not_attend", Description: "They never turned up at all",
	})

	require.Error(t, err)
	assert.Nil(t, repo.created, "nothing should have been written")
}

// A foreign booking and a nonexistent one must be indistinguishable, or the
// response confirms that an id is real.
func TestCreate_ForeignBookingReadsAsNotFound(t *testing.T) {
	svc := newSvc(&mockRepo{belongs: false})
	id := uuid.New().String()

	_, err := svc.Create(context.Background(), uuid.New(), "customer", CreateReportRequest{
		BookingID: &id, Category: "other", Description: "Something went wrong here",
	})

	assert.Contains(t, err.Error(), "not found")
}

func TestCreate_AcceptsABookingTheReporterIsPartyTo(t *testing.T) {
	artistID := uuid.New()
	repo := &mockRepo{belongs: true, artistID: artistID}
	svc := newSvc(repo)
	id := uuid.New().String()

	out, err := svc.Create(context.Background(), uuid.New(), "customer", CreateReportRequest{
		BookingID: &id, Category: "wrong_payment_details",
		Description: "Was messaged a different Whish number to the one in the app",
	})

	require.NoError(t, err)
	assert.Equal(t, "open", out.Status)
	assert.Equal(t, "Asked to pay different details", out.CategoryLabel)
}

// Attribution to the artist is what makes "how many payment-details reports
// against this one" answerable - which is how an impersonation campaign is
// caught. The reporter never supplies it; it is resolved from the booking.
func TestCreate_AttributesToTheArtistBehindTheBooking(t *testing.T) {
	artistID := uuid.New()
	repo := &mockRepo{belongs: true, artistID: artistID}
	svc := newSvc(repo)
	id := uuid.New().String()

	_, err := svc.Create(context.Background(), uuid.New(), "customer", CreateReportRequest{
		BookingID: &id, Category: "impersonation", Description: "Profile photos belong to someone else",
	})

	require.NoError(t, err)
	require.NotNil(t, repo.created.ArtistID)
	assert.Equal(t, artistID, *repo.created.ArtistID)
}

// ── Validation ────────────────────────────────────────────────────────────────

func TestCreate_RequiresASubject(t *testing.T) {
	svc := newSvc(&mockRepo{})

	_, err := svc.Create(context.Background(), uuid.New(), "customer", CreateReportRequest{
		Category: "other", Description: "Something happened but I will not say what",
	})

	assert.Error(t, err)
}

func TestCreate_RejectsAnUnknownCategory(t *testing.T) {
	svc := newSvc(&mockRepo{belongs: true})
	id := uuid.New().String()

	_, err := svc.Create(context.Background(), uuid.New(), "customer", CreateReportRequest{
		BookingID: &id, Category: "aliens", Description: "A perfectly long description here",
	})

	assert.Error(t, err)
}

// "bad" is not something an admin can act on, and a report nobody can act on
// wastes the reporter's time as much as the reviewer's.
func TestCreate_RejectsADescriptionTooShortToActOn(t *testing.T) {
	svc := newSvc(&mockRepo{belongs: true})
	id := uuid.New().String()

	_, err := svc.Create(context.Background(), uuid.New(), "customer", CreateReportRequest{
		BookingID: &id, Category: "other", Description: "  bad  ",
	})

	assert.Error(t, err)
}

func TestCreate_MapsADuplicateToAReadableConflict(t *testing.T) {
	svc := newSvc(&mockRepo{belongs: true, createErr: ErrDuplicateOpen})
	id := uuid.New().String()

	_, err := svc.Create(context.Background(), uuid.New(), "customer", CreateReportRequest{
		BookingID: &id, Category: "other", Description: "Reporting this a second time",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already reported")
}

// ── Resolution ────────────────────────────────────────────────────────────────

// Closing a report without saying what was done leaves the next person looking
// at the pattern with nothing.
func TestResolve_RequiresANoteWhenClosing(t *testing.T) {
	repo := &mockRepo{resolveRows: 1}
	svc := newSvc(repo)

	err := svc.Resolve(context.Background(), uuid.New(), uuid.New(),
		ResolveReportRequest{Status: StatusResolved}, "")

	require.Error(t, err)
	assert.Zero(t, repo.resolveCalls, "validation must reject before anything is written")
}

// Moving to 'reviewing' needs no note - nothing has been decided yet.
func TestResolve_AllowsReviewingWithoutANote(t *testing.T) {
	repo := &mockRepo{resolveRows: 1}
	svc := newSvc(repo)

	err := svc.Resolve(context.Background(), uuid.New(), uuid.New(),
		ResolveReportRequest{Status: StatusReviewing}, "")

	require.NoError(t, err)
	assert.Equal(t, StatusReviewing, repo.lastStatus)
}

func TestResolve_DismissalAlsoNeedsANote(t *testing.T) {
	svc := newSvc(&mockRepo{resolveRows: 1})

	err := svc.Resolve(context.Background(), uuid.New(), uuid.New(),
		ResolveReportRequest{Status: StatusDismissed, Note: strp("   ")}, "")

	assert.Error(t, err)
}

// Two admins acting at once must not overwrite each other: the repo guards on
// the report still being open, and zero rows is a conflict rather than a
// silent win.
func TestResolve_AlreadyDealtWithIsAConflict(t *testing.T) {
	svc := newSvc(&mockRepo{resolveRows: 0})

	err := svc.Resolve(context.Background(), uuid.New(), uuid.New(),
		ResolveReportRequest{Status: StatusResolved, Note: strp("Refund confirmed")}, "")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already been dealt with")
}

func TestResolve_AuditsTheDecision(t *testing.T) {
	repo := &mockRepo{resolveRows: 1}
	a := &mockAudit{}
	svc := NewService(repo, a)
	id := uuid.New()

	err := svc.Resolve(context.Background(), id, uuid.New(),
		ResolveReportRequest{Status: StatusResolved, Note: strp("Artist returned the deposit")}, "9.9.9.9")

	require.NoError(t, err)
	require.Len(t, a.events, 1)
	assert.Equal(t, "report_resolved", a.events[0].Action)
	assert.Equal(t, id, a.events[0].EntityID)
	assert.Equal(t, "9.9.9.9", a.events[0].IPAddress)
}

func TestCategory_Valid(t *testing.T) {
	assert.True(t, CategoryWrongPaymentDetails.Valid())
	assert.False(t, Category("nonsense").Valid())
	assert.Len(t, Categories(), 7)
}
