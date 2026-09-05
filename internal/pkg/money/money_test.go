// money_test.go pins the accepted shape of a money string.
//
// The rejected list is not hypothetical: every value in it was submitted to
// a live endpoint during the 2026-09-05 security pass (INJ-04), and the
// comment beside each records what the API did with it before this package
// existed.
package money

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParse_CanonicalAmounts_Accepted(t *testing.T) {
	// Compared by VALUE, not by string: decimal normalises "045" to 45 and
	// "45.00" to 45, which is correct - they are the same amount. Asserting
	// on String() here would be testing the library's formatting rather than
	// this package's contract.
	cases := map[string]string{
		"0":           "0",
		"0.00":        "0",
		"5":           "5",
		"45.00":       "45",
		"10.99":       "10.99",
		"0.01":        "0.01",
		"99999999.99": "99999999.99", // the NUMERIC(10,2) ceiling, inclusive
		"045":         "45",          // leading zeros are ugly, not wrong
		"5.0":         "5",           // two decimals is the maximum, not the requirement
	}
	for raw, want := range cases {
		got, err := Parse(raw, "price")
		require.NoError(t, err, "expected %q to be accepted", raw)
		assert.True(t, got.Equal(decimal.RequireFromString(want)),
			"%q should parse to %s, got %s", raw, want, got.String())
	}
}

// TestParse_ValueSurvivesExactly guards the reason money is a string on the
// wire at all: a float64 round-trip would not hold these.
func TestParse_ValueSurvivesExactly(t *testing.T) {
	got, err := Parse("0.07", "price")
	require.NoError(t, err)
	assert.True(t, got.Equal(mustParse(t, "0.07")))
	assert.Equal(t, "0.07", got.String())
}

func TestParse_RejectedForms_ReturnBadRequest(t *testing.T) {
	cases := map[string]string{
		// The finding. Accepted by Postgres as 'NaN'::numeric, stored, and
		// every subsequent read of the row then failed with a 500.
		"NaN": "corrupted the row permanently",
		// Rejected by Postgres, but as SQLSTATE 22003 -> surfaced as a 500.
		"Infinity":  "500 rather than 400",
		"-Infinity": "500 rather than 400",
		// Accepted by Go, then silently rounded by NUMERIC(10,2).
		"10.999": "silently became 11.00",
		// Accepted by Go as one thousand.
		"1e3": "silently became 1000.00",
		"1E3": "silently became 1000.00",
		// Accepted by Go as five.
		"+5": "sign stripped silently",
		// Negative money has no meaning here.
		"-1":    "500 rather than 400",
		"-0.01": "500 rather than 400",
		// Past NUMERIC(10,2) -> overflow -> 500.
		"100000000":    "one digit over the ceiling",
		"999999999.99": "overflow",
		"1e309":        "overflow",
		// Not numbers at all.
		"":            "empty is missing, not zero",
		" ":           "whitespace",
		"45.00 ":      "trailing whitespace",
		" 45.00":      "leading whitespace",
		"abc":         "not a number",
		"45,00":       "comma decimal separator",
		"$45":         "currency symbol",
		"45.00.00":    "two separators",
		"0x10":        "hex",
		"1_000":       "digit separator",
		"45.000":      "three decimals",
		"'; DROP--":   "injection attempt",
		"Infinity\n0": "newline smuggling",
	}
	for raw, why := range cases {
		_, err := Parse(raw, "price")
		require.Error(t, err, "expected %q to be rejected (%s)", raw, why)
		assert.Contains(t, err.Error(), "price", "error must name the field")
	}
}

// TestParse_ErrorCodeNamesTheField - the frontend renders these codes, so
// the mapping from field to code is part of the contract.
func TestParse_ErrorCodeNamesTheField(t *testing.T) {
	_, err := Parse("NaN", "deposit_amount")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "deposit_amount")
}

// TestParseOptional_Nil_IsNotAnError - a PATCH that omits a field means
// "leave it alone", which must not be confused with sending a bad value.
func TestParseOptional_Nil_IsNotAnError(t *testing.T) {
	got, err := ParseOptional(nil, "price")
	require.NoError(t, err)
	assert.Nil(t, got)
}

func TestParseOptional_Present_IsValidated(t *testing.T) {
	got, err := ParseOptional(ptr("45.00"), "price")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.True(t, got.Equal(decimal.RequireFromString("45")))

	_, err = ParseOptional(ptr("NaN"), "price")
	assert.Error(t, err)
}

// TestParseOptional_EmptyString_IsRejected - distinct from nil. An empty
// string is a field that was sent and left blank, which is a bad request,
// not an instruction to skip.
func TestParseOptional_EmptyString_IsRejected(t *testing.T) {
	_, err := ParseOptional(ptr(""), "price")
	assert.Error(t, err)
}

func ptr(s string) *string { return &s }

func mustParse(t *testing.T, s string) decimal.Decimal {
	t.Helper()
	d, err := Parse(s, "price")
	require.NoError(t, err)
	return d
}
