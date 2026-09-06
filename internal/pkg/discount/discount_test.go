// discount_test.go — one named test per rule in decision D3.
//
// Money rules are the class of thing that gets "tidied" by someone who has not
// read the reasoning, because several of them look wrong in isolation. Fixed
// before percentage genuinely gives the customer less. The deposit genuinely
// does not move. Each of those has a test whose name says it is deliberate.
package discount

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dec(s string) decimal.Decimal { return decimal.RequireFromString(s) }

func fixed(code, amount string) Rule {
	return Rule{Code: code, Kind: Fixed, Value: dec(amount)}
}
func pct(code, percent string) Rule {
	return Rule{Code: code, Kind: Percentage, Value: dec(percent)}
}

// ── D3.3 — fixed before percentage ───────────────────────────────────────────

// TestFixedBeforePercentage_IsWorseForTheCustomer_AndDeliberate is the test the
// proposal explicitly asked for. $10 off $100 then 20% leaves $72; the other
// order leaves $70. We match Fresha, and this exists so the "bug" is not fixed.
func TestFixedBeforePercentage_IsWorseForTheCustomer_AndDeliberate(t *testing.T) {
	got := Resolve(Input{
		Base:      dec("100.00"),
		Automatic: []Rule{fixed("", "10.00"), pct("", "20")},
	})

	assert.True(t, got.Final.Equal(dec("72.00")),
		"fixed then percentage = 72.00, got %s", got.Final)
	assert.True(t, got.DiscountTotal.Equal(dec("28.00")))
}

// TestPercentageAppliesToTheRemainder_NotTheOriginal - the ordering above only
// means anything if the percentage takes its cut of what is left.
func TestPercentageAppliesToTheRemainder_NotTheOriginal(t *testing.T) {
	got := Resolve(Input{
		Base:      dec("100.00"),
		Automatic: []Rule{fixed("", "10.00")},
		Code:      &Rule{Code: "SAVE20", Kind: Percentage, Value: dec("20")},
	})

	// 20% of the remaining 90, not of the original 100.
	assert.True(t, got.CodeAmount.Equal(dec("18.00")), "got %s", got.CodeAmount)
}

// TestRuleOrderInSliceDoesNotChangeThePrice - ordering is by KIND, not by the
// order a query happened to return rows in. Otherwise the same promotion costs
// different amounts depending on an ORDER BY nobody thought about.
func TestRuleOrderInSliceDoesNotChangeThePrice(t *testing.T) {
	forward := Resolve(Input{Base: dec("100.00"), Automatic: []Rule{fixed("", "10.00"), pct("", "20")}})
	reverse := Resolve(Input{Base: dec("100.00"), Automatic: []Rule{pct("", "20"), fixed("", "10.00")}})

	assert.True(t, forward.Final.Equal(reverse.Final),
		"%s vs %s", forward.Final, reverse.Final)
}

// ── D3.4 — surcharge before discount ─────────────────────────────────────────

// TestSurchargeAppliesBeforeDiscount - "20% off" means off the price the
// customer was shown. The other order would let the early-bird fee silently
// eat the promotion.
func TestSurchargeAppliesBeforeDiscount(t *testing.T) {
	got := Resolve(Input{
		Base:      dec("100.00"),
		Surcharge: dec("20.00"),
		Code:      &Rule{Code: "SAVE20", Kind: Percentage, Value: dec("20")},
	})

	assert.True(t, got.Subtotal.Equal(dec("120.00")))
	// 20% of 120, not of 100.
	assert.True(t, got.CodeAmount.Equal(dec("24.00")), "got %s", got.CodeAmount)
	assert.True(t, got.Final.Equal(dec("96.00")), "got %s", got.Final)
}

// ── D3.1 — the deposit never moves ───────────────────────────────────────────

// TestDepositIsUnchangedByAnyDiscount is the rule with the most operational
// weight: deposits are matched by hand against OMT/Whish transfers by one
// admin, and a per-promo deposit would make every discounted booking a
// reconciliation question.
func TestDepositIsUnchangedByAnyDiscount(t *testing.T) {
	got := Resolve(Input{
		Base:      dec("100.00"),
		Deposit:   dec("30.00"),
		Automatic: []Rule{pct("", "10")},
		Code:      &Rule{Code: "SAVE10", Kind: Fixed, Value: dec("10.00")},
	})

	assert.True(t, got.Deposit.Equal(dec("30.00")), "deposit must not move")
}

// TestDiscountIsFeltOnTheBalance - the other half of the same rule.
func TestDiscountIsFeltOnTheBalance(t *testing.T) {
	without := Resolve(Input{Base: dec("100.00"), Deposit: dec("30.00")})
	with := Resolve(Input{
		Base:    dec("100.00"),
		Deposit: dec("30.00"),
		Code:    &Rule{Code: "SAVE20", Kind: Fixed, Value: dec("20.00")},
	})

	assert.True(t, without.Balance.Equal(dec("70.00")))
	assert.True(t, with.Balance.Equal(dec("50.00")), "the whole discount lands here")
	assert.True(t, without.Deposit.Equal(with.Deposit))
}

// ── The cap: a decision D3 did not cover ─────────────────────────────────────

// TestDiscountCannotPushThePriceBelowTheDeposit.
//
// $50 service, $20 deposit, $40 discount would otherwise mean a $10 final
// price and a NEGATIVE balance - the customer having overpaid via a bank
// transfer already sent. "The customer is owed $10 back" is exactly the manual
// reconciliation burden D3.1 refused to create, so the discount stops at the
// deposit.
func TestDiscountCannotPushThePriceBelowTheDeposit(t *testing.T) {
	got := Resolve(Input{
		Base:    dec("50.00"),
		Deposit: dec("20.00"),
		Code:    &Rule{Code: "HALFOFF", Kind: Fixed, Value: dec("40.00")},
	})

	assert.True(t, got.Final.Equal(dec("20.00")), "floored at the deposit, got %s", got.Final)
	assert.True(t, got.DiscountTotal.Equal(dec("30.00")), "only 30 of the 40 applied")
	assert.True(t, got.Balance.IsZero(), "balance is zero, never negative")
}

// TestCapTrimsTheCodeBeforeTheAutomaticRule - an automatic rule is a
// business-set price the customer was already shown; a code is something they
// added. Taking back the implicit promise is worse than short-changing the
// coupon.
func TestCapTrimsTheCodeBeforeTheAutomaticRule(t *testing.T) {
	got := Resolve(Input{
		Base:      dec("50.00"),
		Deposit:   dec("20.00"),
		Automatic: []Rule{fixed("", "20.00")},
		Code:      &Rule{Code: "EXTRA20", Kind: Fixed, Value: dec("20.00")},
	})

	assert.True(t, got.AutomaticAmount.Equal(dec("20.00")), "automatic survives intact")
	assert.True(t, got.CodeAmount.Equal(dec("10.00")), "the code absorbs the trim, got %s", got.CodeAmount)
	assert.True(t, got.Final.Equal(dec("20.00")))
}

// TestFullyTrimmedCode_IsNotReportedAsApplied - a code that ended up giving
// nothing must not appear on the receipt as though it did.
func TestFullyTrimmedCode_IsNotReportedAsApplied(t *testing.T) {
	got := Resolve(Input{
		Base:      dec("50.00"),
		Deposit:   dec("50.00"),
		Automatic: nil,
		Code:      &Rule{Code: "USELESS", Kind: Fixed, Value: dec("10.00")},
	})

	assert.True(t, got.CodeAmount.IsZero())
	assert.Empty(t, got.AppliedCode, "a code that gave nothing is not an applied code")
}

func TestAppliedCode_ReportedWhenItActuallyReduced(t *testing.T) {
	got := Resolve(Input{
		Base: dec("100.00"),
		Code: &Rule{Code: "SAVE10", Kind: Fixed, Value: dec("10.00")},
	})

	assert.Equal(t, "SAVE10", got.AppliedCode)
}

// ── Arithmetic safety ────────────────────────────────────────────────────────

// TestFinalIsNeverNegative - with no deposit, a discount larger than the price
// takes it to zero and stops.
func TestFinalIsNeverNegative(t *testing.T) {
	got := Resolve(Input{
		Base: dec("30.00"),
		Code: &Rule{Code: "TOOBIG", Kind: Fixed, Value: dec("100.00")},
	})

	assert.True(t, got.Final.IsZero(), "got %s", got.Final)
	assert.True(t, got.DiscountTotal.Equal(dec("30.00")), "only what was there")
}

// TestBreakdownPartsSumToTheTotal - a breakdown whose parts do not add up
// invites a dispute the artist cannot answer.
func TestBreakdownPartsSumToTheTotal(t *testing.T) {
	got := Resolve(Input{
		Base:      dec("87.33"),
		Surcharge: dec("12.50"),
		Deposit:   dec("15.00"),
		Automatic: []Rule{fixed("", "5.55"), pct("", "7")},
		Code:      &Rule{Code: "X", Kind: Percentage, Value: dec("13")},
	})

	assert.True(t, got.Subtotal.Equal(got.Base.Add(got.Surcharge)), "subtotal")
	assert.True(t, got.DiscountTotal.Equal(got.AutomaticAmount.Add(got.CodeAmount)),
		"discount parts must sum: %s + %s != %s", got.AutomaticAmount, got.CodeAmount, got.DiscountTotal)
	assert.True(t, got.Final.Equal(got.Subtotal.Sub(got.DiscountTotal)), "final")
	assert.Equal(t, int32(2), -got.Final.Exponent(), "two decimal places, matching NUMERIC(10,2)")
}

// TestNoRules_IsTheIdentity - the overwhelmingly common case must be exactly
// the price, not the price plus a rounding artefact.
func TestNoRules_IsTheIdentity(t *testing.T) {
	got := Resolve(Input{Base: dec("45.00"), Deposit: dec("10.00")})

	assert.True(t, got.Final.Equal(dec("45.00")))
	assert.True(t, got.DiscountTotal.IsZero())
	assert.True(t, got.Balance.Equal(dec("35.00")))
	assert.Empty(t, got.AppliedCode)
}

// TestMalformedRules_AreIgnoredNotHonoured - a negative or >100% rule is a
// configuration error. It must never INCREASE the price, which a negative
// discount would.
func TestMalformedRules_AreIgnoredNotHonoured(t *testing.T) {
	negativeFixed := Resolve(Input{Base: dec("100.00"), Automatic: []Rule{fixed("", "-50.00")}})
	assert.True(t, negativeFixed.Final.Equal(dec("100.00")), "a negative fixed rule must not add")

	negativePct := Resolve(Input{Base: dec("100.00"), Automatic: []Rule{pct("", "-20")}})
	assert.True(t, negativePct.Final.Equal(dec("100.00")), "a negative percentage must not add")

	over100 := Resolve(Input{Base: dec("100.00"), Automatic: []Rule{pct("", "150")}})
	assert.True(t, over100.Final.IsZero(), "clamped to 100%, got %s", over100.Final)
}

// TestDepositLargerThanPrice_DoesNotInvertTheCap - a misconfiguration must not
// make the maximum discount negative and therefore increase the price.
func TestDepositLargerThanPrice_DoesNotInvertTheCap(t *testing.T) {
	got := Resolve(Input{
		Base:    dec("10.00"),
		Deposit: dec("50.00"),
		Code:    &Rule{Code: "SAVE5", Kind: Fixed, Value: dec("5.00")},
	})

	assert.False(t, got.Final.GreaterThan(dec("10.00")), "must never exceed the base price")
	assert.True(t, got.Balance.IsZero())
}

// TestRoundingIsHalfUpPerComponent - 33.333% of 100 is 33.33, not 33.
func TestRoundingIsHalfUpPerComponent(t *testing.T) {
	got := Resolve(Input{Base: dec("100.00"), Automatic: []Rule{pct("", "33.335")}})

	require.Equal(t, int32(2), -got.AutomaticAmount.Exponent())
	assert.True(t, got.AutomaticAmount.Equal(dec("33.34")), "got %s", got.AutomaticAmount)
}
