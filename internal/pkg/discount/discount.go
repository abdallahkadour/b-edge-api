// Package discount turns a price and a set of promotions into what the
// customer actually owes.
//
// # PURE, BY DESIGN
//
// No database, no clock, no configuration. Everything that varies - which
// rules apply, whether a code is valid, whether this is a first booking -
// is decided by the caller and handed in. That is what makes every rule in
// D3 expressible as a named test with no fixtures.
//
// # THE PIPELINE (decision D3, accepted 2026-09-06)
//
//	base        = service.price
//	+ surcharge   early-bird, if the slot qualifies        -> subtotal
//	- automatic   first-time / off-peak: fixed, then %
//	- one code    customer promo:        fixed, then %
//	= final                                                (never below zero)
//
//	deposit  unchanged by any of the above
//	balance  final - deposit          <- where the discount is felt
//
// Each of those orderings is a decision with a reason, and each is one
// plausible-looking change away from being reversed:
//
// SURCHARGE BEFORE DISCOUNT. The customer is quoted one number, and "20% off"
// means off the price they were shown. Discounting first would let the
// early-bird fee silently eat the promotion, so the customer pays more than
// the advertised discount implies and cannot tell why.
//
// FIXED BEFORE PERCENTAGE. Matching Fresha. Worth stating because it is
// counter-intuitive and gives the customer LESS: $10 off $100 then 20% leaves
// $72; 20% off $100 then $10 leaves $70. TestFixedBeforePercentage_IsWorseForTheCustomer
// exists so nobody "fixes" it later.
//
// THE DEPOSIT NEVER MOVES. It is not a payment step here - it is a commitment
// device, collected out of band over OMT or Whish and matched by hand against
// a transfer notification by a single admin. A per-promo deposit would make
// every discounted booking a reconciliation question in a flow with no
// automation to absorb it, and would weaken the exact thing the deposit exists
// for: a cheaper booking with the same no-show cost to the artist.
package discount

import "github.com/shopspring/decimal"

// Kind distinguishes the two ways a promotion reduces a price.
type Kind string

const (
	// Fixed takes an absolute amount off.
	Fixed Kind = "fixed"
	// Percentage takes a proportion off, expressed 0-100.
	Percentage Kind = "percentage"
)

// Rule is one promotion, reduced to what the arithmetic needs.
//
// Deliberately not the database row: eligibility, expiry, usage limits and
// ownership are the caller's problem, decided before this package is called.
// A Rule that reaches here is one that has already been ruled applicable.
type Rule struct {
	// Code identifies the rule in the resulting breakdown. Empty for an
	// automatic rule, which has no customer-facing code.
	Code string
	Kind Kind
	// Value is an amount for Fixed, or 0-100 for Percentage.
	Value decimal.Decimal
}

// Input is everything the resolver needs. Nothing is read from anywhere else.
type Input struct {
	// Base is the service or product price before anything is applied.
	Base decimal.Decimal
	// Surcharge is the early-bird fee, already determined to apply. Zero when
	// it does not.
	Surcharge decimal.Decimal
	// Automatic are business-set rules (first-time client, off-peak). They are
	// NOT codes and may combine with a code - that is a business-set price,
	// not a second coupon.
	Automatic []Rule
	// Code is the single customer-applied promo code, or nil. One per booking:
	// matching Fresha, and the only rule explainable to a customer in one
	// sentence and auditable by the artist in one row.
	Code *Rule
	// Deposit is the service's configured deposit. Never modified here; it is
	// an input only so the breakdown can report the balance and so the
	// discount can be capped - see Resolve.
	Deposit decimal.Decimal
}

// Breakdown is what the customer is shown and what the booking stores.
//
// Every field is rounded to 2 decimal places, so the numbers a customer reads
// add up exactly. A breakdown whose parts do not sum to its total is worse
// than no breakdown - it invites a dispute the artist cannot answer.
type Breakdown struct {
	Base      decimal.Decimal
	Surcharge decimal.Decimal
	// Subtotal is Base + Surcharge: the price before any reduction, and the
	// number a percentage is taken from.
	Subtotal decimal.Decimal

	AutomaticAmount decimal.Decimal
	CodeAmount      decimal.Decimal
	// DiscountTotal is AutomaticAmount + CodeAmount, after any capping.
	DiscountTotal decimal.Decimal

	Final   decimal.Decimal
	Deposit decimal.Decimal
	Balance decimal.Decimal

	// AppliedCode is the code that actually reduced the price, empty if none
	// did. A code that resolved to zero (because the cap absorbed it) is NOT
	// reported as applied - see Resolve.
	AppliedCode string
}

// two is the scale of every money column in this schema, NUMERIC(10,2).
const two int32 = 2

// Resolve computes the breakdown.
//
// # THE DISCOUNT CANNOT PUSH THE PRICE BELOW THE DEPOSIT
//
// This case is not in D3 and had to be decided: a $50 service with a $20
// deposit and a $40 discount would otherwise resolve to a $10 final price and
// a NEGATIVE $10 balance - the customer having overpaid through a deposit
// already sent by bank transfer.
//
// In a flow where deposits move over OMT and Whish and are matched by hand by
// a single admin, "the customer is owed $10 back" is exactly the
// reconciliation burden D3.1 refused to create when it kept discounts off the
// deposit. So the discount is capped at the balance: it may reduce the price
// to the deposit and no further, and the unused remainder is simply not
// applied.
//
// The customer is told the real numbers either way; the breakdown reports the
// discount that was actually given, not the one the code advertises.
func Resolve(in Input) Breakdown {
	base := in.Base.Round(two)
	surcharge := in.Surcharge.Round(two)
	subtotal := base.Add(surcharge)

	// Automatic rules first, then the code, each applying fixed before
	// percentage within its own group. A percentage always takes its cut of
	// what remains at that point, never of the original.
	running := subtotal
	automatic, running := applyGroup(in.Automatic, running)

	var codeAmount decimal.Decimal
	var appliedCode string
	if in.Code != nil {
		var applied decimal.Decimal
		applied, running = applyGroup([]Rule{*in.Code}, running)
		codeAmount = applied
	}

	deposit := in.Deposit.Round(two)

	// Cap. Never let the total reduction take the price below the deposit,
	// and never below zero when there is no deposit.
	floor := deposit
	if floor.GreaterThan(subtotal) {
		// A deposit larger than the price is a misconfiguration, not something
		// to enforce here. Fall back to zero so a bad configuration cannot
		// make the discount negative.
		floor = decimal.Zero
	}
	total := automatic.Add(codeAmount)
	maxDiscount := subtotal.Sub(floor)
	if total.GreaterThan(maxDiscount) {
		// Trim the CODE first and the automatic rules second: an automatic
		// rule is a business-set price the customer was shown, whereas a code
		// is something they added on top. Taking back the thing they were
		// promised implicitly is worse than not honouring the last few dollars
		// of a coupon.
		over := total.Sub(maxDiscount)
		if codeAmount.GreaterThanOrEqual(over) {
			codeAmount = codeAmount.Sub(over)
		} else {
			automatic = automatic.Sub(over.Sub(codeAmount))
			codeAmount = decimal.Zero
		}
		total = maxDiscount
	}

	if in.Code != nil && codeAmount.IsPositive() {
		appliedCode = in.Code.Code
	}

	final := subtotal.Sub(total).Round(two)
	if final.IsNegative() {
		final = decimal.Zero
	}

	balance := final.Sub(deposit).Round(two)
	if balance.IsNegative() {
		balance = decimal.Zero
	}

	return Breakdown{
		Base:            base,
		Surcharge:       surcharge,
		Subtotal:        subtotal,
		AutomaticAmount: automatic.Round(two),
		CodeAmount:      codeAmount.Round(two),
		DiscountTotal:   total.Round(two),
		Final:           final,
		Deposit:         deposit,
		Balance:         balance,
		AppliedCode:     appliedCode,
	}
}

// applyGroup reduces `from` by every rule, fixed before percentage, and
// returns the total taken plus what remains.
//
// Ordering within the group is by KIND, not by the order the caller supplied.
// Making the arithmetic depend on slice order would mean the same set of rules
// produced different prices depending on how a query happened to sort them.
func applyGroup(rules []Rule, from decimal.Decimal) (decimal.Decimal, decimal.Decimal) {
	remaining := from
	total := decimal.Zero

	for _, r := range rules {
		if r.Kind != Fixed {
			continue
		}
		amount := r.Value.Round(two)
		if amount.GreaterThan(remaining) {
			amount = remaining
		}
		if amount.IsNegative() {
			continue
		}
		total = total.Add(amount)
		remaining = remaining.Sub(amount)
	}

	for _, r := range rules {
		if r.Kind != Percentage {
			continue
		}
		pct := r.Value
		if pct.IsNegative() {
			continue
		}
		if pct.GreaterThan(decimal.NewFromInt(100)) {
			pct = decimal.NewFromInt(100)
		}
		amount := remaining.Mul(pct).Div(decimal.NewFromInt(100)).Round(two)
		total = total.Add(amount)
		remaining = remaining.Sub(amount)
	}

	return total, remaining
}
