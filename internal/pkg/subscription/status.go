// Package subscription holds the subscription lifecycle state machine: the
// enforcement windows and the single derivation of an artist's current
// state from their subscription dates.
//
// It exists as a leaf package — importing nothing else in this codebase —
// because two packages need the same answer and cannot import each other.
// internal/billing owns subscriptions, and internal/middleware enforces
// them, but billing/handler.go imports middleware for RequireAuth, so
// middleware importing billing would close an import cycle. Before this
// package existed, middleware resolved that by hand-copying the 21-day
// threshold and re-implementing the derivation inline, with a comment
// asking whoever changed one to remember the other. Nothing enforced it.
//
// Everything here is deliberately expressed over primitives rather than
// over billing.Subscription, so this package never needs to know the shape
// of a database row.
package subscription

import "time"

// Status is an artist's derived subscription lifecycle state.
//
// Deliberately never stored — always computed at read time by Derive from
// the subscription's date columns alone. B-Edge has no background scheduler
// by design; a stored status column would be the first thing that needed
// one. This way a subscription nobody has looked at in months is exactly as
// correct the next time it is read as if a cron had been ticking all along.
type Status string

// The six derived subscription states, in the order Derive checks them.
const (
	StatusTrialing  Status = "trialing"
	StatusActive    Status = "active"
	StatusGrace     Status = "grace"
	StatusPastDue   Status = "past_due"
	StatusSuspended Status = "suspended"
	StatusCancelled Status = "cancelled"
)

// CompedPlanCode is the plan whose accounts are never billed and never
// lapse — Rania and internal test accounts. Derive short-circuits on it
// before any date arithmetic.
const CompedPlanCode = "comped"

// GraceDays and PastDueDays are the graduated enforcement windows measured
// from current_period_end. Enforcement is graduated (hide from Discover
// before locking the dashboard) rather than an immediate hard cutoff
// because an overdue artist may still have confirmed bookings with
// customers who already paid a real deposit — see
// B-Edge-Monetization-Implementation-Spec-v1.md section 6.1.
//
// Both are exported and both have real second consumers:
//   - GraceDays is interpolated into SQL by internal/discovery and
//     internal/artist's visibility filters.
//   - PastDueDays is read by internal/middleware's suspension guard.
//
// Keeping them here is what makes those consumers provably agree with
// Derive rather than agree by convention.
const (
	GraceDays   = 7
	PastDueDays = 21
)

// Derive computes an artist's current subscription state from their plan
// code and dates alone. now is an explicit parameter rather than a
// time.Now() call so every boundary is deterministically testable.
//
// Evaluated top-down, first match wins. The ORDER is load-bearing: a
// cancelled comped account reads as cancelled, and a comped account with a
// long-expired period still reads as active.
func Derive(planCode string, trialEndsAt, currentPeriodEnd, cancelledAt *time.Time, now time.Time) Status {
	switch {
	case cancelledAt != nil:
		return StatusCancelled
	case planCode == CompedPlanCode:
		return StatusActive
	case trialEndsAt != nil && now.Before(*trialEndsAt):
		return StatusTrialing
	case currentPeriodEnd == nil:
		// Trial ended (or there never was one) and no period has ever been
		// paid for — the same enforcement posture as a lapsed payer, not a
		// harsher distinct state.
		return StatusPastDue
	case now.Before(*currentPeriodEnd):
		return StatusActive
	case now.Before(currentPeriodEnd.AddDate(0, 0, GraceDays)):
		return StatusGrace
	case now.Before(currentPeriodEnd.AddDate(0, 0, PastDueDays)):
		return StatusPastDue
	default:
		return StatusSuspended
	}
}
