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
//
// ── Why 21 and 45, and not the 7 and 21 these started at ────────────────
//
// Decided 2026-08-31 (decision D2), deliberately less aggressive than the
// original values. Three reasons, in order of weight:
//
//  1. B-Edge cannot auto-charge anyone. Every published SaaS dunning
//     benchmark (7-14 days, sometimes 3-7) assumes a DECLINED CARD that a
//     processor retries automatically within hours. Here there is nothing
//     to retry: the artist must initiate an OMT/Whish transfer, submit the
//     reference by hand, and an admin must confirm it. That is a
//     three-step human loop spanning at least one business day and easily
//     a weekend. Applying card-dunning timings to a manual-transfer market
//     punishes latency in our own collection process.
//
//  2. Nobody is being told. There is no dunning notification of any kind
//     yet: the WhatsApp worker has no live Twilio sender, so the only
//     signal an overdue artist receives is a banner they must log in to
//     see. Hiding someone from Discover a week after a payment they were
//     never reminded about is a penalty for our missing feature, not
//     theirs. Revisit these numbers once reminders actually send.
//
//  3. Hiding an artist costs B-Edge too. This is a marketplace: an artist
//     hidden from Discover stops earning, which removes both their ability
//     and their reason to pay. Standard B2B collections data puts 90-98%
//     of invoices as recoverable inside 30 days, so waiting is cheap and
//     cutting someone off early is not.
//
// The resulting ladder, from current_period_end:
//
//	 0-21 days   grace       visible, bookable, editable
//	21-45 days   past_due    hidden from Discover, no NEW bookings, still editable
//	  45+ days   suspended   also blocked from modifying their account
//
// 21 days is the founder's call and roughly three missed weekly reminder
// cycles. 45 puts the industry's 30-day "serious delinquency" threshold
// comfortably inside past_due rather than at its edge.
//
// Existing confirmed bookings are honoured at EVERY stage, including
// suspended - a customer who already transferred a real deposit is never
// punished for their artist's billing.
const (
	GraceDays   = 21
	PastDueDays = 45
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

// ── Enforcement policy ───────────────────────────────────────────────────────

// Enforcement is what a given subscription state permits.
//
// Before this existed the policy was not written down anywhere - it had to
// be reconstructed by reading three unrelated call sites (a Fiber
// middleware, a booking service branch, and a SQL fragment in two
// repositories) and inferring the ladder they collectively implemented.
// Nothing stopped one of them drifting, and nothing stated the intent.
type Enforcement struct {
	// VisibleInDiscovery - the artist appears in Discover and is reachable
	// by direct profile link.
	VisibleInDiscovery bool
	// AcceptsNewBookings - customers can book NEW appointments. Existing
	// confirmed bookings are always honoured regardless of this flag; a
	// customer who already paid a deposit is never punished for the
	// artist's billing.
	AcceptsNewBookings bool
	// CanModifyAccount - the artist can mutate their own data (profile,
	// services, products, media). Billing routes are always reachable, so
	// a blocked artist can still submit a payment and recover.
	CanModifyAccount bool
}

// Enforce returns what the given state permits. See the GraceDays comment
// for the reasoning behind the ladder and its timings.
func Enforce(s Status) Enforcement {
	switch s {
	case StatusTrialing, StatusActive, StatusGrace:
		return Enforcement{VisibleInDiscovery: true, AcceptsNewBookings: true, CanModifyAccount: true}

	case StatusPastDue:
		// The real pressure point: invisible and unable to take new work,
		// but still able to fix their listing and pay.
		return Enforcement{VisibleInDiscovery: false, AcceptsNewBookings: false, CanModifyAccount: true}

	case StatusSuspended:
		return Enforcement{VisibleInDiscovery: false, AcceptsNewBookings: false, CanModifyAccount: false}

	case StatusCancelled:
		// A cancelled subscription is a deliberate exit, not a debt. The
		// artist keeps control of their own data so they can export it or
		// resubscribe; they simply stop being sold.
		//
		// Note this is MORE permissive than suspended on CanModifyAccount,
		// which is intentional - being cut off for not paying and choosing
		// to leave are different situations.
		return Enforcement{VisibleInDiscovery: false, AcceptsNewBookings: false, CanModifyAccount: true}

	default:
		// Unknown state: fail closed on selling, open on self-service. An
		// unrecognised status should never silently grant visibility, but
		// it must not lock someone out of their own account either.
		return Enforcement{VisibleInDiscovery: false, AcceptsNewBookings: false, CanModifyAccount: true}
	}
}
