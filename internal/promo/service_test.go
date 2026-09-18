package promo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// promo was the least-covered service in the codebase at 11.5%, and it is the
// newest thing that touches money. The pure resolver beneath it
// (internal/pkg/discount) is at 95%, so the arithmetic was never in doubt -
// what was untested is the layer that decides WHETHER to apply a code at all,
// and whether to record that it was used.

type mockRepo struct {
	byCode    *Discount
	byCodeErr error
	facts     Facts
	factsErr  error
	created   *Discount
	createErr error
	updated   *Discount
	updateErr error
	released  int
}

func (m *mockRepo) GetByCode(context.Context, uuid.UUID, string) (*Discount, error) {
	return m.byCode, m.byCodeErr
}
func (m *mockRepo) GetByID(context.Context, uuid.UUID) (*Discount, error) {
	return m.byCode, m.byCodeErr
}
func (m *mockRepo) GatherFacts(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (Facts, error) {
	return m.facts, m.factsErr
}
func (m *mockRepo) ListBySalon(context.Context, uuid.UUID) ([]*DiscountResponse, error) {
	return nil, nil
}
func (m *mockRepo) Create(_ context.Context, d *Discount) error {
	m.created = d
	return m.createErr
}
func (m *mockRepo) Update(context.Context, uuid.UUID, uuid.UUID, UpdateDiscountRequest) (*Discount, error) {
	return m.updated, m.updateErr
}
func (m *mockRepo) ReleaseForBooking(context.Context, uuid.UUID) error {
	m.released++
	return nil
}

func newSvc(r Repository) *Service { return NewService(r) }

func d(v string) decimal.Decimal { return decimal.RequireFromString(v) }

func liveDiscount(kind, value string) *Discount {
	return &Discount{
		ID: uuid.New(), SalonID: uuid.New(), Code: "SUMMER20",
		Kind: kind, Value: d(value), IsActive: true,
	}
}

func okFacts() Facts { return Facts{Now: time.Now(), CustomerIsNew: true} }

// ── The common path ───────────────────────────────────────────────────────────

// No code means no queries at all. If this ever started hitting the database
// it would put two round trips on every booking that never used a promo.
func TestResolve_EmptyCodeTouchesNoRepository(t *testing.T) {
	repo := &mockRepo{byCodeErr: errors.New("must not be called")}
	svc := newSvc(repo)

	res, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "",
		d("100"), decimal.Zero, d("20"))

	require.NoError(t, err)
	assert.False(t, res.Applied())
	assert.Empty(t, res.Reason, "no code is not a failure and must not read as one")
	assert.True(t, res.Breakdown.Final.Equal(d("100")))
}

func TestResolve_WhitespaceOnlyCodeIsTreatedAsNoCode(t *testing.T) {
	repo := &mockRepo{byCodeErr: errors.New("must not be called")}
	svc := newSvc(repo)

	res, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "   ",
		d("100"), decimal.Zero, d("20"))

	require.NoError(t, err)
	assert.False(t, res.Applied())
	assert.Empty(t, res.Reason)
}

// ── Applying ──────────────────────────────────────────────────────────────────

func TestResolve_AppliesAPercentageCode(t *testing.T) {
	repo := &mockRepo{byCode: liveDiscount(KindPercentage, "20"), facts: okFacts()}
	svc := newSvc(repo)

	res, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "SUMMER20",
		d("100"), decimal.Zero, d("10"))

	require.NoError(t, err)
	require.True(t, res.Applied(), "a valid code on a 100 booking must apply")
	assert.True(t, res.Breakdown.Final.Equal(d("80")), "got %s", res.Breakdown.Final)
	assert.Empty(t, res.Reason)
}

func TestResolve_AppliesAFixedCode(t *testing.T) {
	repo := &mockRepo{byCode: liveDiscount(KindFixed, "15"), facts: okFacts()}
	svc := newSvc(repo)

	res, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "SUMMER20",
		d("100"), decimal.Zero, d("10"))

	require.NoError(t, err)
	require.True(t, res.Applied())
	assert.True(t, res.Breakdown.Final.Equal(d("85")), "got %s", res.Breakdown.Final)
}

// The code is matched case-insensitively at the repository, but the service
// must not mangle it on the way there - customers type these off a poster.
func TestResolve_TrimsTheCodeBeforeLookup(t *testing.T) {
	repo := &mockRepo{byCode: liveDiscount(KindFixed, "15"), facts: okFacts()}
	svc := newSvc(repo)

	res, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "  SUMMER20  ",
		d("100"), decimal.Zero, d("10"))

	require.NoError(t, err)
	assert.True(t, res.Applied())
}

// ── Refusing, without failing the booking ─────────────────────────────────────

// A bad code must never abort the booking. The customer gets their appointment
// at full price and a reason, not an error page.
func TestResolve_UnknownCodeIsAReasonNotAnError(t *testing.T) {
	repo := &mockRepo{byCodeErr: ErrNotFound}
	svc := newSvc(repo)

	res, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "NOPE",
		d("100"), decimal.Zero, d("10"))

	require.NoError(t, err, "an unusable code is not an error condition")
	assert.False(t, res.Applied())
	assert.NotEmpty(t, res.Reason)
	assert.True(t, res.Breakdown.Final.Equal(d("100")), "the price must be untouched")
}

// An ineligible code - expired, used up, first-time-only for a returning
// customer - behaves the same way.
func TestResolve_IneligibleCodeIsAReasonNotAnError(t *testing.T) {
	disc := liveDiscount(KindPercentage, "20")
	disc.FirstTimeOnly = true
	repo := &mockRepo{byCode: disc, facts: Facts{Now: time.Now(), CustomerIsNew: false}}
	svc := newSvc(repo)

	res, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "SUMMER20",
		d("100"), decimal.Zero, d("10"))

	require.NoError(t, err)
	assert.False(t, res.Applied())
	assert.NotEmpty(t, res.Reason)
	assert.True(t, res.Breakdown.Final.Equal(d("100")))
}

// A kind the pure resolver does not understand must refuse rather than
// silently apply nothing - otherwise a data-entry mistake becomes a code that
// appears to work and discounts zero.
func TestResolve_UnknownKindRefusesRatherThanApplyingNothing(t *testing.T) {
	repo := &mockRepo{byCode: liveDiscount("buy_one_get_one", "1"), facts: okFacts()}
	svc := newSvc(repo)

	res, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "SUMMER20",
		d("100"), decimal.Zero, d("10"))

	require.NoError(t, err)
	assert.False(t, res.Applied())
	assert.NotEmpty(t, res.Reason)
}

// THE ONE THAT COSTS A CUSTOMER SOMETHING REAL.
//
// The resolver caps a discount at the deposit, so a large code against a
// mostly-prepaid booking can resolve to zero. Reporting that as applied would
// write a redemption and burn the customer's single use of the code on a
// discount they never received.
func TestResolve_ZeroValueDiscountIsNotReportedAsApplied(t *testing.T) {
	repo := &mockRepo{byCode: liveDiscount(KindFixed, "50"), facts: okFacts()}
	svc := newSvc(repo)

	// deposit == base, so there is nothing the code can reduce.
	res, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "SUMMER20",
		d("100"), decimal.Zero, d("100"))

	require.NoError(t, err)
	assert.False(t, res.Applied(),
		"a zero-value discount must not consume the customer's one use of the code")
	assert.NotEmpty(t, res.Reason)
}

// A repository failure IS an error - it means the answer is unknown, which is
// different from knowing the code is unusable.
func TestResolve_RepositoryFailurePropagates(t *testing.T) {
	repo := &mockRepo{byCodeErr: errors.New("connection reset")}
	svc := newSvc(repo)

	_, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "SUMMER20",
		d("100"), decimal.Zero, d("10"))

	assert.Error(t, err)
}

func TestResolve_FactsFailurePropagates(t *testing.T) {
	repo := &mockRepo{byCode: liveDiscount(KindFixed, "10"), factsErr: errors.New("timeout")}
	svc := newSvc(repo)

	_, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "SUMMER20",
		d("100"), decimal.Zero, d("10"))

	assert.Error(t, err)
}

// ── Surcharges ────────────────────────────────────────────────────────────────

// The early-bird surcharge is the only other price modifier in the product,
// and it is added BEFORE the discount - so a percentage code discounts the
// surcharged total, not the base.
func TestResolve_DiscountsTheSurchargedTotal(t *testing.T) {
	repo := &mockRepo{byCode: liveDiscount(KindPercentage, "10"), facts: okFacts()}
	svc := newSvc(repo)

	res, err := svc.Resolve(context.Background(), uuid.New(), uuid.New(), "SUMMER20",
		d("100"), d("20"), d("10"))

	require.NoError(t, err)
	require.True(t, res.Applied())
	assert.True(t, res.Breakdown.Final.Equal(d("108")),
		"10%% off 120 is 108, not 110 - got %s", res.Breakdown.Final)
}

// ── Create ────────────────────────────────────────────────────────────────────

func TestCreate_StoresTheCodeUppercased(t *testing.T) {
	repo := &mockRepo{}
	svc := newSvc(repo)

	_, err := svc.Create(context.Background(), uuid.New(), CreateDiscountRequest{
		Code: "summer20", Kind: KindPercentage, Value: "20",
	})

	require.NoError(t, err)
	require.NotNil(t, repo.created)
	assert.Equal(t, "SUMMER20", repo.created.Code,
		"codes are typed off posters and matched case-insensitively; storing one "+
			"canonical form keeps the unique constraint meaningful")
}

func TestCreate_DuplicateCodeIsAReadableConflict(t *testing.T) {
	svc := newSvc(&mockRepo{createErr: ErrDuplicateCode})

	_, err := svc.Create(context.Background(), uuid.New(), CreateDiscountRequest{
		Code: "SUMMER20", Kind: KindPercentage, Value: "20",
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already have a code")
}

func TestCreate_RejectsMalformedMoney(t *testing.T) {
	// The same whitelist that guards every other money field: no exponents,
	// no NaN, at most two decimals.
	for _, v := range []string{"20.999", "1e2", "NaN", "-5", "abc", ""} {
		repo := &mockRepo{}
		svc := newSvc(repo)
		_, err := svc.Create(context.Background(), uuid.New(), CreateDiscountRequest{
			Code: "X", Kind: KindPercentage, Value: v,
		})
		assert.Error(t, err, "value %q must be rejected", v)
		assert.Nil(t, repo.created, "value %q must not reach the repository", v)
	}
}

// ── Release ───────────────────────────────────────────────────────────────────

// A code used on a booking that is later cancelled goes back into
// circulation. Without this the customer loses their one use to an
// appointment that never happened.
func TestReleaseForBooking_ReturnsTheCodeToCirculation(t *testing.T) {
	repo := &mockRepo{}
	svc := newSvc(repo)

	require.NoError(t, svc.ReleaseForBooking(context.Background(), uuid.New()))
	assert.Equal(t, 1, repo.released)
}
