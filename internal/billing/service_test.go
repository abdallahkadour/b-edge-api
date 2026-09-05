// Package billing's service tests. These use a hand-written mock
// repository — no database required, matching the pattern in
// internal/review and internal/booking. There is deliberately no
// repository-layer test here: this codebase has no database test
// infrastructure (TEST_DB_NAME is read only by cmd/migrate), so the SQL
// guards themselves are out of scope and only the service's mapping of
// their errors is covered.
package billing

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// ── Mock repository ───────────────────────────────────────────────────────────

// invoiceKey mirrors migration 025's UNIQUE (subscription_id, period_start)
// index. CreateInvoiceIfMissing's idempotency lives entirely in that index —
// the Go code has no in-process dedupe — so the mock has to model it or
// tests of repeated generation would pass against a mock that is more
// forgiving than the real database.
type invoiceKey struct {
	subscriptionID uuid.UUID
	periodStart    time.Time
}

type mockRepo struct {
	publicPlans    []*Plan
	publicPlansErr error
	allPlans       []*Plan
	allPlansErr    error
	planByCode     *Plan
	planByCodeErr  error
	createPlanErr  error
	updatePlanErr  error

	artistIDByUser    uuid.UUID
	artistIDByUserErr error

	subByArtist    *Subscription
	subByArtistErr error
	subByID        *Subscription
	subByIDErr     error
	allSubs        []*Subscription
	allSubsErr     error
	updateSubErr   error
	overviewRows   []*SubscriptionOverviewRow
	overviewErr    error

	invoiceByID       *Invoice
	invoiceByIDErr    error
	invoicesByArtist  []*Invoice
	invoicesByArtErr  error
	invoicesByStatus  []*Invoice
	invoicesByStatErr error
	createInvoiceErr  error
	submitInvoiceErr  error
	confirmInvoice    *Invoice
	confirmInvoiceErr error
	voidInvoiceErr    error

	// captured args for assertions
	createdInvoices    []*Invoice
	seenInvoiceKeys    map[invoiceKey]bool
	lastCreatedPlan    *Plan
	lastUpdatedPlan    *Plan
	lastUpdatedSub     *Subscription
	lastSubmitID       uuid.UUID
	lastSubmitRef      string
	lastConfirmID      uuid.UUID
	lastConfirmedBy    uuid.UUID
	lastVoidID         uuid.UUID
	lastVoidReason     string
	lastStatusFilter   string
	planByCodeRequests []string
}

func (m *mockRepo) ListPublicPlans(_ context.Context) ([]*Plan, error) {
	return m.publicPlans, m.publicPlansErr
}

func (m *mockRepo) ListAllPlans(_ context.Context) ([]*Plan, error) {
	return m.allPlans, m.allPlansErr
}

func (m *mockRepo) GetPlanByCode(_ context.Context, code string) (*Plan, error) {
	m.planByCodeRequests = append(m.planByCodeRequests, code)
	return m.planByCode, m.planByCodeErr
}

func (m *mockRepo) CreatePlan(_ context.Context, p *Plan) error {
	m.lastCreatedPlan = p
	return m.createPlanErr
}

func (m *mockRepo) UpdatePlan(_ context.Context, p *Plan) error {
	m.lastUpdatedPlan = p
	return m.updatePlanErr
}

func (m *mockRepo) GetArtistIDByUserID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return m.artistIDByUser, m.artistIDByUserErr
}

func (m *mockRepo) GetSubscriptionByArtistID(_ context.Context, _ uuid.UUID) (*Subscription, error) {
	return m.subByArtist, m.subByArtistErr
}

func (m *mockRepo) GetSubscriptionByID(_ context.Context, _ uuid.UUID) (*Subscription, error) {
	return m.subByID, m.subByIDErr
}

func (m *mockRepo) ListAllSubscriptions(_ context.Context) ([]*Subscription, error) {
	return m.allSubs, m.allSubsErr
}

func (m *mockRepo) UpdateSubscription(_ context.Context, s *Subscription) error {
	m.lastUpdatedSub = s
	return m.updateSubErr
}

func (m *mockRepo) ListSubscriptionsOverview(_ context.Context) ([]*SubscriptionOverviewRow, error) {
	return m.overviewRows, m.overviewErr
}

// CreateInvoiceIfMissing models migration 025's unique index: a second
// insert for the same (subscription_id, period_start) is a silent no-op
// returning created=false, never an error.
func (m *mockRepo) CreateInvoiceIfMissing(_ context.Context, inv *Invoice) (bool, error) {
	if m.createInvoiceErr != nil {
		return false, m.createInvoiceErr
	}
	if m.seenInvoiceKeys == nil {
		m.seenInvoiceKeys = make(map[invoiceKey]bool)
	}
	k := invoiceKey{subscriptionID: inv.SubscriptionID, periodStart: inv.PeriodStart}
	if m.seenInvoiceKeys[k] {
		return false, nil
	}
	m.seenInvoiceKeys[k] = true
	m.createdInvoices = append(m.createdInvoices, inv)
	return true, nil
}

func (m *mockRepo) GetInvoiceByID(_ context.Context, _ uuid.UUID) (*Invoice, error) {
	return m.invoiceByID, m.invoiceByIDErr
}

func (m *mockRepo) ListInvoicesByArtist(_ context.Context, _ uuid.UUID) ([]*Invoice, error) {
	return m.invoicesByArtist, m.invoicesByArtErr
}

func (m *mockRepo) ListInvoicesByStatus(_ context.Context, status string) ([]*Invoice, error) {
	m.lastStatusFilter = status
	return m.invoicesByStatus, m.invoicesByStatErr
}

func (m *mockRepo) SubmitInvoice(_ context.Context, id uuid.UUID, paymentReference string) error {
	m.lastSubmitID = id
	m.lastSubmitRef = paymentReference
	return m.submitInvoiceErr
}

func (m *mockRepo) ConfirmInvoice(_ context.Context, id, confirmedBy uuid.UUID) (*Invoice, error) {
	m.lastConfirmID = id
	m.lastConfirmedBy = confirmedBy
	return m.confirmInvoice, m.confirmInvoiceErr
}

func (m *mockRepo) VoidInvoice(_ context.Context, id uuid.UUID, reason string) error {
	m.lastVoidID = id
	m.lastVoidReason = reason
	return m.voidInvoiceErr
}

// mockAudit records every event it's asked to log, so a test can assert on
// what got written without touching a real audit_events table. Mirrors
// internal/admin/service_test.go's mock of the same name.
type mockAudit struct {
	events []audit.Event
	err    error
}

func (m *mockAudit) Log(_ context.Context, e audit.Event) error {
	m.events = append(m.events, e)
	return m.err
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func newTestService(repo Repository, a audit.Repository) *Service {
	return NewService(repo, a)
}

// dec parses a decimal for test fixtures. Decimal assertions must use
// .Equal() rather than assert.Equal — reflect.DeepEqual fails on equal
// decimals carrying different exponents. See the extended note at
// internal/earnings/service_test.go:128-133.
func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

// ── Smoke tests for the scaffold ──────────────────────────────────────────────

func TestListPublicPlans_ReturnsRepoPlans(t *testing.T) {
	want := []*Plan{{Code: "starter", Name: "Starter", MonthlyPrice: dec("7.00"), Currency: "USD"}}
	repo := &mockRepo{publicPlans: want}
	svc := newTestService(repo, nil)

	got, err := svc.ListPublicPlans(context.Background())

	require.NoError(t, err)
	assert.Equal(t, want, got)
}

func TestListPublicPlans_RepoError_Propagates(t *testing.T) {
	repo := &mockRepo{publicPlansErr: errors.New("db down")}
	svc := newTestService(repo, nil)

	_, err := svc.ListPublicPlans(context.Background())

	require.Error(t, err)
}

// NewService must tolerate a nil audit repository — internal/admin
// established this so tests can construct a Service without an audit table,
// and billing's own NewService doc comment promises it explicitly. A
// regression here would surface as a nil-pointer panic on the first
// confirm/void, which are the two paths that audit.
func TestNewService_NilAudit_DoesNotPanic(t *testing.T) {
	svc := newTestService(&mockRepo{}, nil)

	require.NotNil(t, svc)
	assert.NotNil(t, svc.audit, "nil auditRepo must fall back to audit.NopRepository")
}

// The mock's invoice dedupe mirrors the real unique index. If this ever
// fails, every ensureInvoicesUpTo test built on it is testing against a
// mock more permissive than the database.
func TestMockRepo_CreateInvoiceIfMissing_DedupesOnPeriodStart(t *testing.T) {
	repo := &mockRepo{}
	subID := uuid.New()
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)

	created1, err1 := repo.CreateInvoiceIfMissing(context.Background(),
		&Invoice{SubscriptionID: subID, PeriodStart: start})
	created2, err2 := repo.CreateInvoiceIfMissing(context.Background(),
		&Invoice{SubscriptionID: subID, PeriodStart: start})

	require.NoError(t, err1)
	require.NoError(t, err2)
	assert.True(t, created1, "first insert for a period must create")
	assert.False(t, created2, "second insert for the same period must be a silent no-op")
	assert.Len(t, repo.createdInvoices, 1)
}

// ── ensureInvoicesUpTo ────────────────────────────────────────────────────────

// billableSub returns a subscription that ensureInvoicesUpTo will actually
// process: a real plan, no trial, no cancellation.
func billableSub(periodEnd time.Time) *Subscription {
	return &Subscription{
		ID:               uuid.New(),
		ArtistID:         uuid.New(),
		PlanCode:         "starter",
		Seats:            1,
		MonthlyPrice:     dec("7.00"),
		Currency:         "USD",
		CurrentPeriodEnd: &periodEnd,
		CreatedAt:        periodEnd.AddDate(0, -1, 0),
	}
}

// ── the three early exits ──

func TestEnsureInvoicesUpTo_Comped_GeneratesNothing(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	sub := billableSub(now.AddDate(0, -6, 0)) // long lapsed
	sub.PlanCode = "comped"
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))
	assert.Empty(t, repo.createdInvoices, "a comped account must never be invoiced")
}

func TestEnsureInvoicesUpTo_Cancelled_GeneratesNothing(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	sub := billableSub(now.AddDate(0, -6, 0))
	cancelled := now.AddDate(0, 0, -1)
	sub.CancelledAt = &cancelled
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))
	assert.Empty(t, repo.createdInvoices, "a cancelled subscription must never be invoiced")
}

func TestEnsureInvoicesUpTo_Trialing_GeneratesNothing(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	trialEnd := now.AddDate(0, 0, 5)
	sub := &Subscription{
		ID: uuid.New(), ArtistID: uuid.New(), PlanCode: "starter", Seats: 1,
		MonthlyPrice: dec("7.00"), Currency: "USD",
		TrialEndsAt: &trialEnd, CreatedAt: now.AddDate(0, 0, -10),
	}
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))
	assert.Empty(t, repo.createdInvoices, "a live trial must not be invoiced")
}

// ── periodStart precedence: CreatedAt < TrialEndsAt < CurrentPeriodEnd ──

// The three assignments are sequential with no else, so the LAST non-nil one
// wins. CurrentPeriodEnd overrides TrialEndsAt overrides CreatedAt.
func TestEnsureInvoicesUpTo_CurrentPeriodEndOverridesTrialEnd(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	trialEnd := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	sub := billableSub(periodEnd)
	sub.TrialEndsAt = &trialEnd
	sub.CreatedAt = time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC)
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))

	require.NotEmpty(t, repo.createdInvoices)
	assert.Equal(t, periodEnd, repo.createdInvoices[0].PeriodStart,
		"current_period_end must win over trial_ends_at and created_at")
}

// With no period ever paid and no trial, billing starts from CreatedAt.
func TestEnsureInvoicesUpTo_NoTrialNoPeriod_StartsFromCreatedAt(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	created := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	sub := &Subscription{
		ID: uuid.New(), ArtistID: uuid.New(), PlanCode: "starter", Seats: 1,
		MonthlyPrice: dec("7.00"), Currency: "USD", CreatedAt: created,
	}
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))

	require.NotEmpty(t, repo.createdInvoices)
	assert.Equal(t, created, repo.createdInvoices[0].PeriodStart)
}

// ── generation itself ──

// A period still running generates nothing — periodStart.After(now) exits
// before any insert.
func TestEnsureInvoicesUpTo_PeriodStillRunning_GeneratesNothing(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	sub := billableSub(now.AddDate(0, 0, 10)) // paid through the future
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))
	assert.Empty(t, repo.createdInvoices)
}

func TestEnsureInvoicesUpTo_OnePeriodBehind_GeneratesOne(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	sub := billableSub(periodEnd)
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))
	assert.Len(t, repo.createdInvoices, 1)
}

// Calling twice must not double-invoice. Idempotency lives entirely in the
// database's unique index — the Go loop has no in-process dedupe — so this
// is really a test that the loop produces a STABLE period_start for the same
// inputs, which is what lets the index do its job.
func TestEnsureInvoicesUpTo_CalledTwice_DoesNotDuplicate(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	sub := billableSub(time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC))
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))
	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))

	assert.Len(t, repo.createdInvoices, 1, "a second call must be fully absorbed by the unique index")
}

// ── invoice field snapshot ──

// DueDate == PeriodStart: an invoice is due the instant it is issued, with
// no net-N terms. Worth pinning because it is unusual enough that someone
// will eventually assume a grace period is built into the due date — it is
// not; the grace lives in DeriveStatus instead.
func TestEnsureInvoicesUpTo_InvoiceFields_DueDateEqualsPeriodStart(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	sub := billableSub(periodEnd)
	sub.Seats = 4
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))

	require.Len(t, repo.createdInvoices, 1)
	inv := repo.createdInvoices[0]
	assert.Equal(t, periodEnd, inv.PeriodStart)
	assert.Equal(t, periodEnd.AddDate(0, 1, 0), inv.PeriodEnd)
	assert.Equal(t, inv.PeriodStart, inv.DueDate, "invoices are due on issue, not net-N")
	assert.Equal(t, sub.ID, inv.SubscriptionID)
	assert.Equal(t, sub.ArtistID, inv.ArtistID)
	assert.Equal(t, "starter", inv.PlanCode)
	assert.Equal(t, "USD", inv.Currency)
	assert.Equal(t, 4, inv.SeatsBilled, "seats are recorded on the invoice")
	assert.True(t, dec("7.00").Equal(inv.Amount),
		"amount is the flat monthly price — seat overage is deliberately not billed yet")
}

// Seats are recorded but never priced. This pins the deliberate deferral
// documented in the loop body, so that if seat billing is added later it
// fails here loudly rather than silently changing everyone's invoice.
func TestEnsureInvoicesUpTo_ManySeats_AmountIgnoresSeatCount(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	sub := billableSub(time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC))
	sub.Seats = 25
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))

	require.Len(t, repo.createdInvoices, 1)
	assert.True(t, dec("7.00").Equal(repo.createdInvoices[0].Amount),
		"25 seats must still bill the flat monthly price until seat overage ships")
}

// CHARACTERIZATION: month-end billing dates drift forward permanently.
//
// Go's AddDate NORMALIZES rather than clamps: Jan 31 + 1 month is Feb 31,
// which normalizes to Mar 3 (Feb 2026 has 28 days). It does not become
// Feb 28. Two consequences, both pinned here:
//
//  1. The first period is 31 days long, not a month — the artist is billed
//     for [Jan 31 → Mar 3], covering three days of March.
//  2. The billing date shifts forward permanently and never returns to
//     month-end: Jan 31 → Mar 3 → Apr 3 → May 3.
//
// Anyone whose period lands on the 29th, 30th or 31st is affected. This is
// a real consequence of AddDate's semantics, not a deliberate design — but
// it is NOT changed here, because the fix (clamp to month-end, or bill on a
// fixed day-of-month) is a product decision about what an artist's billing
// date means, not a mechanical correction.
func TestEnsureInvoicesUpTo_MonthEndStart_DriftsForwardPermanently(t *testing.T) {
	now := time.Date(2026, 5, 15, 0, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC)
	sub := billableSub(periodEnd)
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))

	require.GreaterOrEqual(t, len(repo.createdInvoices), 3)
	assert.Equal(t, time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC), repo.createdInvoices[0].PeriodStart)
	assert.Equal(t, time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC), repo.createdInvoices[1].PeriodStart,
		"Jan 31 + 1 month overflows to Mar 3, skipping a February billing date entirely")
	assert.Equal(t, time.Date(2026, 4, 3, 0, 0, 0, 0, time.UTC), repo.createdInvoices[2].PeriodStart,
		"the overflowed date carries forward — the billing day permanently becomes the 3rd")

	// The first invoiced period is longer than a calendar month.
	first := repo.createdInvoices[0]
	assert.Equal(t, 31, int(first.PeriodEnd.Sub(first.PeriodStart).Hours()/24),
		"a Jan-31 period bills 31 days, not a month")
}

// ── error propagation ──

func TestEnsureInvoicesUpTo_RepoError_Propagates(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	sub := billableSub(time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC))
	repo := &mockRepo{createInvoiceErr: errors.New("db down")}
	svc := newTestService(repo, nil)

	err := svc.ensureInvoicesUpTo(context.Background(), sub, now)

	require.Error(t, err)
}

// ── CHARACTERIZATION: arrears stacking ────────────────────────────────────────

// This test documents ACTUAL behaviour, which contradicts the doc comment on
// ensureInvoicesUpTo and the claim in
// B-Edge-Monetization-Implementation-Spec-v1.md section 12 that "an unpaid
// subscription never accumulates more than one outstanding invoice, because
// current_period_end only advances on confirmed payment."
//
// current_period_end indeed does not advance — but the loop advances its own
// local periodStart on every iteration regardless, so an artist four months
// behind gets four separate unpaid invoices, not one.
//
// Whether that is right is a product decision (bill arrears month by month,
// or carry a single growing balance?) and is NOT changed here. This test
// pins what the code does today so the decision is made deliberately rather
// than discovered from a customer's invoice list.
func TestEnsureInvoicesUpTo_FourMonthsUnpaid_StacksFourInvoices(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	periodEnd := time.Date(2026, 4, 15, 0, 0, 0, 0, time.UTC) // ~4.5 months lapsed
	sub := billableSub(periodEnd)
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	require.NoError(t, svc.ensureInvoicesUpTo(context.Background(), sub, now))

	assert.Len(t, repo.createdInvoices, 5,
		"CHARACTERIZATION: unpaid periods stack one invoice each — the doc comment's "+
			"'never more than one outstanding invoice' claim does not hold")
}

// The 36-iteration cap turns a corrupted date into a silently-truncated
// catch-up rather than an unbounded loop. It returns nil, not an error, so
// nothing anywhere logs that truncation happened.
func TestEnsureInvoicesUpTo_AbsurdlyStaleSubscription_TruncatesAt36(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	sub := billableSub(now.AddDate(-20, 0, 0)) // 20 years lapsed
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	err := svc.ensureInvoicesUpTo(context.Background(), sub, now)

	require.NoError(t, err, "truncation is silent by design — no error, no log")
	assert.Len(t, repo.createdInvoices, maxInvoiceGenerationIterations,
		"generation must stop at the iteration cap")
}

// ── Invoice lifecycle: submit ─────────────────────────────────────────────────

// requireAppErr asserts the error is an AppError with the given code and
// HTTP status, matching the three-line pattern used across this codebase.
func requireAppErr(t *testing.T, err error, wantCode string, wantStatus int) {
	t.Helper()
	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, wantCode, appErr.Code)
	assert.Equal(t, wantStatus, appErr.HTTPStatus)
}

func issuedInvoice(artistID uuid.UUID) *Invoice {
	return &Invoice{
		ID: uuid.New(), SubscriptionID: uuid.New(), ArtistID: artistID,
		Amount: dec("7.00"), Currency: "USD", Status: InvoiceIssued,
	}
}

func TestSubmitInvoicePayment_Success(t *testing.T) {
	artistID := uuid.New()
	inv := issuedInvoice(artistID)
	repo := &mockRepo{artistIDByUser: artistID, invoiceByID: inv}
	svc := newTestService(repo, nil)

	got, err := svc.SubmitInvoicePayment(context.Background(), uuid.New(), inv.ID,
		SubmitInvoicePaymentRequest{PaymentReference: "OMT-12345"})

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, inv.ID, repo.lastSubmitID)
	assert.Equal(t, "OMT-12345", repo.lastSubmitRef,
		"the artist's OMT reference must reach the repository verbatim")
}

// The ownership failure is deliberately a 404, not a 403 — see the comment
// at service.go:283-287. A caller probing invoice IDs that aren't theirs
// must not be able to tell a real-but-foreign invoice from a missing one.
// This is the single most important test in this file: it is a money
// endpoint, and the codebase has an unfixed instance of exactly this gap
// elsewhere (the artist review-list endpoint, per E2E-TEST-PLAN.md).
func TestSubmitInvoicePayment_ForeignInvoice_Returns404NotForbidden(t *testing.T) {
	callerArtistID := uuid.New()
	someoneElsesInvoice := issuedInvoice(uuid.New())
	repo := &mockRepo{artistIDByUser: callerArtistID, invoiceByID: someoneElsesInvoice}
	svc := newTestService(repo, nil)

	_, err := svc.SubmitInvoicePayment(context.Background(), uuid.New(), someoneElsesInvoice.ID,
		SubmitInvoicePaymentRequest{})

	requireAppErr(t, err, "INVOICE_NOT_FOUND", fiber.StatusNotFound)
	assert.Equal(t, uuid.Nil, repo.lastSubmitID,
		"a foreign invoice must never reach the repository's submit call")
}

func TestSubmitInvoicePayment_WrongStatus_ReturnsConflict(t *testing.T) {
	artistID := uuid.New()
	inv := issuedInvoice(artistID)
	repo := &mockRepo{
		artistIDByUser:   artistID,
		invoiceByID:      inv,
		submitInvoiceErr: ErrInvoiceWrongStatus,
	}
	svc := newTestService(repo, nil)

	_, err := svc.SubmitInvoicePayment(context.Background(), uuid.New(), inv.ID,
		SubmitInvoicePaymentRequest{})

	requireAppErr(t, err, "INVOICE_NOT_ISSUED", fiber.StatusConflict)
}

func TestSubmitInvoicePayment_InvoiceNotFound_Returns404(t *testing.T) {
	repo := &mockRepo{artistIDByUser: uuid.New(), invoiceByIDErr: ErrInvoiceNotFound}
	svc := newTestService(repo, nil)

	_, err := svc.SubmitInvoicePayment(context.Background(), uuid.New(), uuid.New(),
		SubmitInvoicePaymentRequest{})

	requireAppErr(t, err, "INVOICE_NOT_FOUND", fiber.StatusNotFound)
}

func TestSubmitInvoicePayment_NotAnArtist_Returns404(t *testing.T) {
	repo := &mockRepo{artistIDByUserErr: ErrArtistNotFound}
	svc := newTestService(repo, nil)

	_, err := svc.SubmitInvoicePayment(context.Background(), uuid.New(), uuid.New(),
		SubmitInvoicePaymentRequest{})

	requireAppErr(t, err, "ARTIST_NOT_FOUND", fiber.StatusNotFound)
}

// ── Invoice lifecycle: confirm ────────────────────────────────────────────────

func TestConfirmInvoice_Success_WritesAuditEvent(t *testing.T) {
	adminID := uuid.New()
	inv := issuedInvoice(uuid.New())
	inv.Status = InvoicePaid
	repo := &mockRepo{confirmInvoice: inv}
	aud := &mockAudit{}
	svc := newTestService(repo, aud)

	got, err := svc.ConfirmInvoice(context.Background(), inv.ID, adminID, "1.2.3.4")

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, adminID, repo.lastConfirmedBy)
	require.Len(t, aud.events, 1, "confirming money received must be audited")
	assert.Equal(t, "confirmed_paid", aud.events[0].Action)
	assert.Equal(t, "invoice", aud.events[0].EntityType)
}

// Confirming anything not currently 'submitted' — including an already-paid
// invoice — is a conflict, never a silent no-op. This is what stops a
// double-click extending a subscription period twice.
func TestConfirmInvoice_NotSubmitted_ReturnsConflict(t *testing.T) {
	repo := &mockRepo{confirmInvoiceErr: ErrInvoiceWrongStatus}
	svc := newTestService(repo, nil)

	_, err := svc.ConfirmInvoice(context.Background(), uuid.New(), uuid.New(), "1.2.3.4")

	requireAppErr(t, err, "INVOICE_NOT_SUBMITTED", fiber.StatusConflict)
}

// The audit write is best-effort: a failure to record the event must never
// undo a confirmation that already committed in the database.
func TestConfirmInvoice_AuditFails_ConfirmationStillSucceeds(t *testing.T) {
	inv := issuedInvoice(uuid.New())
	repo := &mockRepo{confirmInvoice: inv}
	aud := &mockAudit{err: errors.New("audit table gone")}
	svc := newTestService(repo, aud)

	got, err := svc.ConfirmInvoice(context.Background(), inv.ID, uuid.New(), "1.2.3.4")

	require.NoError(t, err, "a failed audit write must not fail the confirmation")
	assert.NotNil(t, got)
}

// ── Invoice lifecycle: void ───────────────────────────────────────────────────

func TestVoidInvoice_Success_WritesAuditEvent(t *testing.T) {
	invoiceID := uuid.New()
	repo := &mockRepo{}
	aud := &mockAudit{}
	svc := newTestService(repo, aud)

	err := svc.VoidInvoice(context.Background(), invoiceID, uuid.New(),
		VoidInvoiceRequest{Reason: "duplicate charge"}, "1.2.3.4")

	require.NoError(t, err)
	assert.Equal(t, invoiceID, repo.lastVoidID)
	assert.Equal(t, "duplicate charge", repo.lastVoidReason)
	require.Len(t, aud.events, 1, "voiding a financial record must be audited")
	assert.Equal(t, "voided", aud.events[0].Action)
}

func TestVoidInvoice_AlreadyTerminal_ReturnsConflict(t *testing.T) {
	repo := &mockRepo{voidInvoiceErr: ErrInvoiceWrongStatus}
	svc := newTestService(repo, nil)

	err := svc.VoidInvoice(context.Background(), uuid.New(), uuid.New(),
		VoidInvoiceRequest{Reason: "mistake"}, "1.2.3.4")

	requireAppErr(t, err, "INVOICE_NOT_VOIDABLE", fiber.StatusConflict)
}

// A reason is required — unlike rejection reasons elsewhere in this codebase,
// this one is not optional, because voiding corrects a financial record.
func TestVoidInvoice_MissingReason_ReturnsValidationError(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo, nil)

	err := svc.VoidInvoice(context.Background(), uuid.New(), uuid.New(),
		VoidInvoiceRequest{Reason: ""}, "1.2.3.4")

	requireAppErr(t, err, "VALIDATION_ERROR", fiber.StatusUnprocessableEntity)
	assert.Equal(t, uuid.Nil, repo.lastVoidID, "validation must reject before touching the repo")
}

// ── Plan CRUD ─────────────────────────────────────────────────────────────────

func validPlanReq() CreatePlanRequest {
	return CreatePlanRequest{Code: "growth", Name: "Growth", MonthlyPrice: "19.00"}
}

// Every default the service fills in when the caller omits a field.
func TestCreatePlan_OmittedFields_GetDefaults(t *testing.T) {
	repo := &mockRepo{planByCode: &Plan{Code: "growth"}}
	svc := newTestService(repo, nil)

	_, err := svc.CreatePlan(context.Background(), validPlanReq())

	require.NoError(t, err)
	require.NotNil(t, repo.lastCreatedPlan)
	p := repo.lastCreatedPlan
	assert.Equal(t, "USD", p.Currency, "currency defaults to USD")
	assert.Equal(t, 1, p.IncludedSeats, "included_seats defaults to 1")
	assert.True(t, p.IsPublic, "plans default to public")
	assert.NotNil(t, p.Features, "features must default to an empty slice, never nil")
	assert.Empty(t, p.Features)
	assert.True(t, decimal.Zero.Equal(p.SeatPrice), "seat_price defaults to zero")
}

// USD is the only supported currency (decided 2026-08-30). Rejected in the
// service so it's a 400, and again by migration 026's CHECK constraint so
// the invariant holds even for a write that bypasses this path.
func TestCreatePlan_NonUSDCurrency_Rejected(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo, nil)
	req := validPlanReq()
	req.Currency = "LBP"

	_, err := svc.CreatePlan(context.Background(), req)

	requireAppErr(t, err, "UNSUPPORTED_CURRENCY", fiber.StatusBadRequest)
	assert.Nil(t, repo.lastCreatedPlan, "a non-USD plan must never reach the repository")
}

// Lowercase "usd" is normalised, not rejected — the uppercase happens before
// the check.
func TestCreatePlan_LowercaseUSD_Accepted(t *testing.T) {
	repo := &mockRepo{planByCode: &Plan{}}
	svc := newTestService(repo, nil)
	req := validPlanReq()
	req.Currency = "usd"

	_, err := svc.CreatePlan(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, "USD", repo.lastCreatedPlan.Currency)
}

func TestCreatePlan_MixedCaseCode_IsNormalised(t *testing.T) {
	repo := &mockRepo{planByCode: &Plan{}}
	svc := newTestService(repo, nil)
	req := validPlanReq()
	req.Code = "  GrowTH  "

	_, err := svc.CreatePlan(context.Background(), req)

	require.NoError(t, err)
	assert.Equal(t, "growth", repo.lastCreatedPlan.Code,
		"plan codes are slugs — trimmed and lowercased before storage")
}

func TestCreatePlan_DuplicateCode_ReturnsConflict(t *testing.T) {
	repo := &mockRepo{createPlanErr: ErrPlanCodeExists}
	svc := newTestService(repo, nil)

	_, err := svc.CreatePlan(context.Background(), validPlanReq())

	requireAppErr(t, err, "PLAN_CODE_EXISTS", fiber.StatusConflict)
}

func TestCreatePlan_NegativePrice_ReturnsBadRequest(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo, nil)
	req := validPlanReq()
	req.MonthlyPrice = "-1.00"

	_, err := svc.CreatePlan(context.Background(), req)

	requireAppErr(t, err, "INVALID_MONTHLY_PRICE", fiber.StatusBadRequest)
	assert.Nil(t, repo.lastCreatedPlan, "a negative price must never reach the repository")
}

func TestCreatePlan_UnparseablePrice_ReturnsBadRequest(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo, nil)
	req := validPlanReq()
	req.MonthlyPrice = "free"

	_, err := svc.CreatePlan(context.Background(), req)

	requireAppErr(t, err, "INVALID_MONTHLY_PRICE", fiber.StatusBadRequest)
}

// Zero is explicitly allowed — it's how a free or comped tier is priced.
func TestCreatePlan_ZeroPrice_Accepted(t *testing.T) {
	repo := &mockRepo{planByCode: &Plan{}}
	svc := newTestService(repo, nil)
	req := validPlanReq()
	req.MonthlyPrice = "0"

	_, err := svc.CreatePlan(context.Background(), req)

	require.NoError(t, err, "a zero price is legitimate — comped and free tiers depend on it")
}

// Excess precision is REJECTED. This test previously ran as
// TestCreatePlan_ExcessPrecision_SilentlyAccepted and pinned the opposite
// behaviour: "19.999" was accepted by Go and silently rounded to 20.00 by
// NUMERIC(10,2), so an admin typing one digit too many changed the price and
// was told nothing.
//
// That characterization test named the open decision - "whether to reject or
// round in Go is a decision" - and the decision was made on 2026-09-05, when
// the security pass (INJ-04) found the same permissiveness had let "NaN"
// through two unvalidated paths and corrupt a row beyond repair. Rejecting is
// now the rule everywhere, in internal/pkg/money.
//
// Deliberately a behaviour change: a client sending 3 decimal places now gets
// a 400 where it used to get a silent round.
func TestCreatePlan_ExcessPrecision_Rejected(t *testing.T) {
	repo := &mockRepo{planByCode: &Plan{}}
	svc := newTestService(repo, nil)
	req := validPlanReq()
	req.MonthlyPrice = "19.999"

	_, err := svc.CreatePlan(context.Background(), req)

	require.Error(t, err, "no silent rounding in the payer's favour")
	assert.Contains(t, err.Error(), "monthly_price", "the error must name the offending field")
}

// TestCreatePlan_CorruptingValues_Rejected is the billing-domain guard for
// the INJ-04 finding. "NaN" is the one that mattered: Postgres accepts
// 'NaN'::numeric, and a stored NaN made every later read of the row fail.
func TestCreatePlan_CorruptingValues_Rejected(t *testing.T) {
	for _, raw := range []string{"NaN", "Infinity", "-1", "1e3", "+5", "999999999.99", ""} {
		repo := &mockRepo{planByCode: &Plan{}}
		svc := newTestService(repo, nil)
		req := validPlanReq()
		req.MonthlyPrice = raw

		_, err := svc.CreatePlan(context.Background(), req)

		require.Error(t, err, "expected %q to be rejected as a monthly_price", raw)
	}
}

func TestUpdatePlan_PartialUpdate_PreservesUnsetFields(t *testing.T) {
	existing := &Plan{
		Code: "growth", Name: "Growth", MonthlyPrice: dec("19.00"), Currency: "USD",
		Description: "keep me", Features: []string{"a", "b"}, IncludedSeats: 3, IsPublic: true,
	}
	repo := &mockRepo{planByCode: existing}
	svc := newTestService(repo, nil)
	newPrice := "25.00"

	_, err := svc.UpdatePlan(context.Background(), "growth", UpdatePlanRequest{MonthlyPrice: &newPrice})

	require.NoError(t, err)
	require.NotNil(t, repo.lastUpdatedPlan)
	assert.True(t, dec("25.00").Equal(repo.lastUpdatedPlan.MonthlyPrice))
	assert.Equal(t, "keep me", repo.lastUpdatedPlan.Description,
		"an unset field must survive a partial update")
	assert.Equal(t, []string{"a", "b"}, repo.lastUpdatedPlan.Features)
	assert.Equal(t, 3, repo.lastUpdatedPlan.IncludedSeats)
}

func TestUpdatePlan_UnknownCode_Returns404(t *testing.T) {
	repo := &mockRepo{planByCodeErr: ErrPlanNotFound}
	svc := newTestService(repo, nil)

	_, err := svc.UpdatePlan(context.Background(), "nope", UpdatePlanRequest{})

	requireAppErr(t, err, "PLAN_NOT_FOUND", fiber.StatusNotFound)
}

// CHARACTERIZATION: create and update disagree about an empty seat_price.
// On create, "" is short-circuited to zero (service.go:75) and never
// reaches the parser. On update, a non-nil pointer to "" goes straight to
// parseNonNegativeDecimal and is rejected. So the same value is a default
// in one path and a 400 in the other.
func TestUpdatePlan_EmptySeatPrice_RejectedUnlikeCreate(t *testing.T) {
	repo := &mockRepo{planByCode: &Plan{Code: "growth"}}
	svc := newTestService(repo, nil)
	empty := ""

	_, err := svc.UpdatePlan(context.Background(), "growth", UpdatePlanRequest{SeatPrice: &empty})

	requireAppErr(t, err, "INVALID_SEAT_PRICE", fiber.StatusBadRequest)
}
