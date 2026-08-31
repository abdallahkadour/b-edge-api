// Package subscription's tests. This is the kernel of the subscription
// state machine — internal/billing, internal/middleware, internal/booking,
// internal/discovery and internal/artist all depend on it agreeing with
// itself — so its branches and boundaries are tested here directly rather
// than only through one consumer.
package subscription

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func at(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 12, 0, 0, 0, time.UTC)
}

func ptr(t time.Time) *time.Time { return &t }

var now = at(2026, time.August, 30)

// ── The eight branches, in evaluation order ───────────────────────────────────

func TestDerive_Cancelled_ReturnsCancelled(t *testing.T) {
	future := now.AddDate(0, 0, 30) // would otherwise be active
	assert.Equal(t, StatusCancelled,
		Derive("starter", nil, &future, ptr(now.AddDate(0, 0, -1)), now))
}

func TestDerive_Comped_ReturnsActive(t *testing.T) {
	assert.Equal(t, StatusActive, Derive(CompedPlanCode, nil, nil, nil, now))
}

func TestDerive_WithinTrial_ReturnsTrialing(t *testing.T) {
	assert.Equal(t, StatusTrialing,
		Derive("starter", ptr(now.AddDate(0, 0, 5)), nil, nil, now))
}

// No period ever paid for is past_due, deliberately not suspended — the same
// posture as a lapsed payer rather than a harsher distinct state.
func TestDerive_NoPeriodEnd_ReturnsPastDue(t *testing.T) {
	assert.Equal(t, StatusPastDue,
		Derive("starter", ptr(now.AddDate(0, 0, -1)), nil, nil, now))
}

func TestDerive_WithinPaidPeriod_ReturnsActive(t *testing.T) {
	assert.Equal(t, StatusActive,
		Derive("starter", nil, ptr(now.AddDate(0, 0, 10)), nil, now))
}

func TestDerive_WithinGraceWindow_ReturnsGrace(t *testing.T) {
	assert.Equal(t, StatusGrace,
		Derive("starter", nil, ptr(now.AddDate(0, 0, -3)), nil, now))
}

func TestDerive_PastGraceWithinPastDue_ReturnsPastDue(t *testing.T) {
	assert.Equal(t, StatusPastDue,
		Derive("starter", nil, ptr(now.AddDate(0, 0, -10)), nil, now))
}

func TestDerive_PastAllWindows_ReturnsSuspended(t *testing.T) {
	assert.Equal(t, StatusSuspended,
		Derive("starter", nil, ptr(now.AddDate(0, 0, -30)), nil, now))
}

// ── Exclusive-Before boundaries ───────────────────────────────────────────────
//
// Every comparison is time.Before, which is strictly exclusive, so each
// boundary instant belongs to the LATER state. A change from Before to
// !After would flip all four at once and no branch test above would notice.

func TestDerive_ExactlyAtPeriodEnd_ReturnsGrace(t *testing.T) {
	assert.Equal(t, StatusGrace, Derive("starter", nil, &now, nil, now),
		"an artist is never Active at exactly their expiry instant")
}

func TestDerive_ExactlyAtGraceBoundary_ReturnsPastDue(t *testing.T) {
	assert.Equal(t, StatusPastDue,
		Derive("starter", nil, ptr(now.AddDate(0, 0, -GraceDays)), nil, now))
}

func TestDerive_ExactlyAtPastDueBoundary_ReturnsSuspended(t *testing.T) {
	assert.Equal(t, StatusSuspended,
		Derive("starter", nil, ptr(now.AddDate(0, 0, -PastDueDays)), nil, now))
}

func TestDerive_ExactlyAtTrialEnd_NotTrialing(t *testing.T) {
	assert.Equal(t, StatusPastDue, Derive("starter", &now, nil, nil, now),
		"the instant a trial ends it falls through to the period checks")
}

// ── Precedence between branches ───────────────────────────────────────────────
//
// The switch is first-match-wins, so case ORDER is load-bearing. These fail
// if anyone reorders them.

func TestDerive_CancelledComped_CancelledWins(t *testing.T) {
	assert.Equal(t, StatusCancelled,
		Derive(CompedPlanCode, nil, nil, ptr(now.AddDate(0, 0, -1)), now),
		"cancellation is checked before the comped shortcut")
}

// This is what keeps Rania's account working without anyone maintaining a
// period on it.
func TestDerive_CompedWithExpiredPeriod_StillActive(t *testing.T) {
	assert.Equal(t, StatusActive,
		Derive(CompedPlanCode, nil, ptr(now.AddDate(0, -6, 0)), nil, now),
		"comped is checked before any period arithmetic")
}

func TestDerive_TrialingWithLapsedPeriod_TrialingWins(t *testing.T) {
	assert.Equal(t, StatusTrialing,
		Derive("starter", ptr(now.AddDate(0, 0, 3)), ptr(now.AddDate(0, 0, -30)), nil, now),
		"a live trial is checked before the period windows")
}

// ── Window invariants ─────────────────────────────────────────────────────────

func TestEnforcementWindows_AreOrdered(t *testing.T) {
	assert.Less(t, GraceDays, PastDueDays,
		"grace must expire before past_due, or the Grace branch is unreachable")
	assert.Positive(t, GraceDays,
		"a non-positive grace window makes Active unreachable at expiry")
}

// Calendar-day arithmetic, not fixed 24-hour multiples: AddDate is used so
// the windows land on the same wall-clock time across a DST transition
// rather than drifting by an hour. Lebanon observes DST, and
// current_period_end is a timestamptz that may be loaded in a non-UTC zone.
func TestDerive_AcrossDSTTransition_UsesCalendarDays(t *testing.T) {
	beirut, err := time.LoadLocation("Asia/Beirut")
	if err != nil {
		t.Skip("tzdata unavailable")
	}
	// Lebanon springs forward in late March.
	periodEnd := time.Date(2026, time.March, 25, 12, 0, 0, 0, beirut)
	justInsideGrace := periodEnd.AddDate(0, 0, GraceDays).Add(-time.Minute)
	justPastGrace := periodEnd.AddDate(0, 0, GraceDays).Add(time.Minute)

	assert.Equal(t, StatusGrace, Derive("starter", nil, &periodEnd, nil, justInsideGrace))
	assert.Equal(t, StatusPastDue, Derive("starter", nil, &periodEnd, nil, justPastGrace))
}
