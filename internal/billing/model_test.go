// Package billing contains unit tests for the billing domain's pure
// functions.
//
// DeriveStatus's branch, boundary and precedence behaviour is NOT tested
// here — it lives in internal/pkg/subscription, which owns the state
// machine, and is tested directly there. What this file covers is the
// adapter: that billing's re-exported constants match the leaf package's,
// and that DeriveStatus (which takes a Subscription struct) never disagrees
// with subscription.Derive (which takes primitives, and is what
// internal/middleware calls with columns scanned straight from SQL).
package billing

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/subscription"
)

func timePtr(t time.Time) *time.Time { return &t }

// The graduated windows must stay ordered.
func TestEnforcementWindows_AreOrdered(t *testing.T) {
	assert.Less(t, GraceDays, PastDueDays,
		"grace must expire before past_due, or the Grace branch is unreachable")
	assert.Positive(t, GraceDays, "a non-positive grace window makes Active unreachable at expiry")
}

// billing's re-exported constants must stay identical to the leaf package
// that declares them. GraceDays in particular is interpolated into SQL by
// discovery and artist, so a divergence would silently change who is
// visible on Discover.
func TestReExportedConstants_MatchLeafPackage(t *testing.T) {
	assert.Equal(t, subscription.GraceDays, GraceDays)
	assert.Equal(t, subscription.PastDueDays, PastDueDays)
	assert.Equal(t, subscription.StatusSuspended, StatusSuspended)
	assert.Equal(t, subscription.CompedPlanCode, "comped",
		"ensureInvoicesUpTo still compares against the literal \"comped\"")
}

// DeriveStatus must agree with the leaf Derive it delegates to, across every
// state. This is the real anti-drift guard: internal/middleware calls
// subscription.Derive directly with columns scanned from SQL, while every
// other consumer goes through billing.DeriveStatus with a struct. Before the
// leaf package existed, middleware re-implemented the logic inline against a
// hand-copied 21-day constant.
func TestDeriveStatus_AgreesWithLeafDerive(t *testing.T) {
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	cases := map[string]*Subscription{
		"active":    {PlanCode: "starter", CurrentPeriodEnd: timePtr(now.AddDate(0, 0, 5))},
		"grace":     {PlanCode: "starter", CurrentPeriodEnd: timePtr(now.AddDate(0, 0, -3))},
		"past_due":  {PlanCode: "starter", CurrentPeriodEnd: timePtr(now.AddDate(0, 0, -10))},
		"suspended": {PlanCode: "starter", CurrentPeriodEnd: timePtr(now.AddDate(0, 0, -30))},
		"comped":    {PlanCode: "comped"},
		"trialing":  {PlanCode: "starter", TrialEndsAt: timePtr(now.AddDate(0, 0, 2))},
		"cancelled": {PlanCode: "starter", CancelledAt: timePtr(now.AddDate(0, 0, -1))},
		"no dates":  {PlanCode: "starter"},
		"at expiry": {PlanCode: "starter", CurrentPeriodEnd: &now},
	}

	for name, s := range cases {
		s.ID, s.ArtistID = uuid.New(), uuid.New()
		viaStruct := DeriveStatus(s, now)
		viaPrimitives := subscription.Derive(s.PlanCode, s.TrialEndsAt, s.CurrentPeriodEnd, s.CancelledAt, now)
		assert.Equal(t, viaPrimitives, viaStruct,
			"billing.DeriveStatus and subscription.Derive must never disagree (%s)", name)
	}
}
