package promo

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/discount"
)

var now = time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)

func valid() *Discount {
	return &Discount{
		ID: uuid.New(), SalonID: uuid.New(), Code: "SUMMER20",
		Kind: KindPercentage, Value: decimal.RequireFromString("20"),
		IsActive: true,
	}
}
func at(offset time.Duration) *time.Time { t := now.Add(offset); return &t }
func intp(i int) *int                    { return &i }

func TestCheck_HappyPath_IsEligible(t *testing.T) {
	assert.True(t, Check(valid(), Facts{Now: now, CustomerIsNew: true}).OK())
}

// An inactive code is reported EXACTLY as a nonexistent one. Telling a
// stranger that a code exists but is switched off invites them to keep trying
// it — the artist withdrew it, and that is the whole message.
func TestCheck_Inactive_LooksLikeNoSuchCode(t *testing.T) {
	d := valid()
	d.IsActive = false

	assert.Equal(t, "That code isn't valid.", Check(d, Facts{Now: now}).Reason)
}

func TestCheck_Expired(t *testing.T) {
	d := valid()
	d.EndsAt = at(-time.Hour)

	assert.Equal(t, "That code has expired.", Check(d, Facts{Now: now}).Reason)
}

// The window is half-open: a code ending at noon is dead AT noon, not a
// second later. Matching every other interval in this codebase — see
// occupancy.go — so "ends 6 September" means the same thing everywhere.
func TestCheck_EndsAt_IsExclusive(t *testing.T) {
	d := valid()
	d.EndsAt = &now

	assert.False(t, Check(d, Facts{Now: now}).OK(), "expiry is exclusive")
}

func TestCheck_NotStarted(t *testing.T) {
	d := valid()
	d.StartsAt = at(time.Hour)

	assert.Equal(t, "That code isn't active yet.", Check(d, Facts{Now: now}).Reason)
}

func TestCheck_StartsAt_IsInclusive(t *testing.T) {
	d := valid()
	d.StartsAt = &now

	assert.True(t, Check(d, Facts{Now: now, CustomerIsNew: true}).OK(), "a code starting now is usable now")
}

func TestCheck_Exhausted(t *testing.T) {
	d := valid()
	d.MaxRedemptions = intp(100)

	assert.Equal(t, "That code has been fully claimed.",
		Check(d, Facts{Now: now, ConsumedRedemptions: 100}).Reason)
	assert.True(t, Check(d, Facts{Now: now, ConsumedRedemptions: 99, CustomerIsNew: true}).OK(),
		"the 100th use is still allowed")
}

func TestCheck_AlreadyUsedByThisCustomer(t *testing.T) {
	assert.Equal(t, "You've already used that code.",
		Check(valid(), Facts{Now: now, CustomerHasUsed: true}).Reason)
}

func TestCheck_FirstTimeOnly_RefusesAReturningCustomer(t *testing.T) {
	d := valid()
	d.FirstTimeOnly = true

	assert.Equal(t, "That code is for first-time clients only.",
		Check(d, Facts{Now: now, CustomerIsNew: false}).Reason)
	assert.True(t, Check(d, Facts{Now: now, CustomerIsNew: true}).OK())
}

// The customer sees ONE reason, so the order decides which. Most permanent
// first: "expired" is more useful than "already used" when both are true,
// because only one of them is ever going to change.
func TestCheck_ReportsTheMostPermanentReasonFirst(t *testing.T) {
	d := valid()
	d.IsActive = false
	d.EndsAt = at(-time.Hour)
	d.FirstTimeOnly = true

	assert.Equal(t, "That code isn't valid.",
		Check(d, Facts{Now: now, CustomerHasUsed: true, CustomerIsNew: false}).Reason,
		"inactive outranks everything")

	d.IsActive = true
	assert.Equal(t, "That code has expired.",
		Check(d, Facts{Now: now, CustomerHasUsed: true}).Reason,
		"expiry outranks already-used")
}

// No window and no limits means always valid while active — the common case
// must not need every optional field filled in.
func TestCheck_NoConstraints_IsAlwaysValid(t *testing.T) {
	d := valid()
	d.StartsAt, d.EndsAt, d.MaxRedemptions = nil, nil, nil

	assert.True(t, Check(d, Facts{Now: now}).OK())
}

func TestToRule_MapsBothKinds(t *testing.T) {
	d := valid()
	r, ok := ToRule(d)
	require.True(t, ok)
	assert.Equal(t, discount.Percentage, r.Kind)
	assert.Equal(t, "SUMMER20", r.Code)

	d.Kind = KindFixed
	r, ok = ToRule(d)
	require.True(t, ok)
	assert.Equal(t, discount.Fixed, r.Kind)
}

// An unrecognised kind must REFUSE, not degrade. A zero-value Fixed rule would
// apply a $0 discount and look exactly like the code doing nothing, which is
// the hardest kind of bug to notice.
func TestToRule_UnknownKind_Refuses(t *testing.T) {
	d := valid()
	d.Kind = "buy_one_get_one"

	_, ok := ToRule(d)
	assert.False(t, ok)
}
