package promo

// Whether a code may be used, separated from whether it exists.
//
// WHY THIS IS ITS OWN FILE AND ITS OWN PURE FUNCTION
//
// Eligibility is where a discount engine goes wrong quietly. Each rule is
// simple; the interactions are not, and a bug here does not crash — it gives
// somebody a discount they should not have had, or refuses one they earned,
// and nobody finds out until an artist complains about a number.
//
// Keeping it pure means every combination is a table row in a test instead of
// a fixture: a code that is inactive AND expired AND already used has one
// defined answer, and the test says which reason the customer is told.
//
// WHY THE ORDER OF CHECKS IS PART OF THE CONTRACT
//
// A customer sees ONE reason, so the order decides which. It runs from the
// most permanent to the most situational:
//
//	inactive  -> expired -> not started -> exhausted -> already used -> not new
//
// "This code has expired" is more useful than "you have already used this
// code" when both are true, because only one of them is ever going to change.

import (
	"time"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/discount"
)

// Eligibility is the full picture a caller needs about one code.
type Eligibility struct {
	// Reason is empty when the code may be used.
	Reason string
}

// OK reports whether the code may be applied.
func (e Eligibility) OK() bool { return e.Reason == "" }

// Facts are the situational inputs eligibility depends on, gathered by the
// caller so this function stays pure.
type Facts struct {
	Now time.Time
	// ConsumedRedemptions counts this code's consumed uses across all
	// customers, for max_redemptions.
	ConsumedRedemptions int
	// CustomerHasUsed is whether THIS customer already holds a consumed
	// redemption of this code. The database enforces it too; checking here
	// lets the customer be told why instead of meeting a constraint violation.
	CustomerHasUsed bool
	// CustomerIsNew is whether the customer has no prior completed booking at
	// this salon, for first_time_only.
	CustomerIsNew bool
}

// Check applies every rule and returns the first failure.
//
// Messages are written for a customer, not a log: they say what happened and,
// where there is one, what to do instead.
func Check(d *Discount, f Facts) Eligibility {
	if !d.IsActive {
		// Deliberately the same wording as "no such code". An artist
		// deactivating a code has withdrawn it; telling a stranger that it
		// exists but is switched off invites them to keep trying it.
		return Eligibility{Reason: "That code isn't valid."}
	}

	if d.EndsAt != nil && !f.Now.Before(*d.EndsAt) {
		return Eligibility{Reason: "That code has expired."}
	}

	if d.StartsAt != nil && f.Now.Before(*d.StartsAt) {
		return Eligibility{Reason: "That code isn't active yet."}
	}

	if d.MaxRedemptions != nil && f.ConsumedRedemptions >= *d.MaxRedemptions {
		return Eligibility{Reason: "That code has been fully claimed."}
	}

	if f.CustomerHasUsed {
		return Eligibility{Reason: "You've already used that code."}
	}

	if d.FirstTimeOnly && !f.CustomerIsNew {
		return Eligibility{Reason: "That code is for first-time clients only."}
	}

	return Eligibility{}
}

// ToRule converts an eligible discount into the arithmetic package's input.
//
// Returns false for a kind the resolver does not understand. That should be
// unreachable — the column has a CHECK constraint — but returning an
// unrecognised rule as a zero-value Fixed would silently apply a $0 discount
// and look like the code simply did nothing.
func ToRule(d *Discount) (discount.Rule, bool) {
	var kind discount.Kind
	switch d.Kind {
	case KindFixed:
		kind = discount.Fixed
	case KindPercentage:
		kind = discount.Percentage
	default:
		return discount.Rule{}, false
	}

	return discount.Rule{
		Code:  d.Code,
		Kind:  kind,
		Value: d.Value,
	}, true
}
