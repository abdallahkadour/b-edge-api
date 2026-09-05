// Package money parses the money strings that arrive on request bodies.
//
// # WHY THIS EXISTS
//
// Money crosses the API boundary as a string, deliberately - a JSON number
// would go through a float64 and lose exactness on the way. What was missing
// is a single answer to "is this string a price", and the absence showed:
// before this package, five domains each decided separately, and three did
// not decide at all.
//
//	artist   create service         decimal.NewFromString
//	product  create / update        decimal.NewFromString
//	artist   update store fee       decimal.NewFromString
//	billing  plans                  parseNonNegativeDecimal (also barred negatives)
//	artist   UPDATE service         nothing
//	onboarding first service        nothing
//
// The two that validated nothing put the raw string into SQL. Postgres
// accepts 'NaN'::numeric, so `{"price":"NaN"}` was stored verbatim - after
// which every read of that row failed, because scanning NaN into a
// decimal.Decimal errors, and SUM(price) over the salon returned NaN and
// poisoned any earnings figure that touched it. The row could not be
// repaired through the API, because the API could no longer read it.
// Found by the security pass, 2026-09-05, test INJ-04.
//
// # WHY A WHITELIST RATHER THAN A LIST OF REJECTIONS
//
// decimal.NewFromString is more permissive than a price field wants: it
// accepts "1e3" (a thousand), "+5", and unlimited scale. None of those are
// wrong as decimals; they are wrong as MONEY, and the failure mode is silent
// - "10.999" was accepted by Go and then quietly rounded to 11.00 by
// NUMERIC(10,2). The security plan's expected behaviour for INJ-04 is
// explicit that this must not happen: "no silent rounding in the payer's
// favour."
//
// So the accepted form is stated positively and everything else is refused.
// Enumerating rejections instead would mean the next surprising-but-valid
// decimal literal is accepted by default, which is exactly how "NaN" got in.
package money

import (
	"regexp"

	"github.com/shopspring/decimal"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// canonical is the only shape a money string may take.
//
// Up to 8 integer digits and at most 2 decimal places, because every money
// column in the schema is NUMERIC(10,2) - verified against the database, not
// assumed. The bound is part of the contract rather than a guess: a value
// past it used to reach Postgres and come back as SQLSTATE 22003, which the
// error handler reports as a 500. An out-of-range price is a bad request,
// and the caller deserves to be told which field.
//
// No sign, because a negative price is not a discount and there is no
// meaning to assign it. No exponent, because "1e3" in a price box is far
// more likely to be a mistake than an intent to charge a thousand dollars.
var canonical = regexp.MustCompile(`^\d{1,8}(\.\d{1,2})?$`)

// Parse converts a request-body money string into a decimal.
//
// field names the offending input so the error can point at it - the same
// shape billing's parseNonNegativeDecimal used, kept because the frontend
// already renders these codes.
//
// Callers with an optional field check nil first; an empty string is not a
// zero, it is a missing value, and is rejected as such.
func Parse(raw, field string) (decimal.Decimal, error) {
	if !canonical.MatchString(raw) {
		return decimal.Zero, invalid(field)
	}

	// Unreachable for anything canonical matches, but returning the error
	// rather than discarding it means a future change to the pattern cannot
	// silently produce a zero amount.
	value, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, invalid(field)
	}

	return value, nil
}

// ParseOptional is Parse for a pointer field that may be absent.
//
// Absent and present-but-invalid are different answers: absent leaves the
// stored value alone, which is what a PATCH means.
func ParseOptional(raw *string, field string) (*decimal.Decimal, error) {
	if raw == nil {
		return nil, nil
	}
	value, err := Parse(*raw, field)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// invalid builds the 400 every rejection returns.
//
// One message for every rejected form on purpose. Telling a caller which
// rule they broke ("too many decimals" vs "not a number") is a small
// convenience, and it is not worth maintaining a branch per rule for a
// field whose valid shape fits in one sentence.
func invalid(field string) error {
	return apperror.BadRequest(
		"INVALID_"+upper(field),
		field+" must be an amount with at most 2 decimal places, between 0 and 99999999.99",
	)
}

// upper uppercases an ASCII field name for the error code. strings.ToUpper
// would do, but field names here are ASCII snake_case by construction and
// this keeps the package's only import list to decimal and apperror.
func upper(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 32
		}
	}
	return string(out)
}
