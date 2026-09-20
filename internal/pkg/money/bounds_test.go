package money

import (
	"strings"
	"testing"
)

// These tests pin internal/pkg/money to the SHAPE OF THE COLUMNS it writes
// into. They are not about parsing; parse behaviour is covered elsewhere.
//
// WHY THEY EXIST
//
// Money is validated in two places that cannot see each other: this package
// (a whitelist regex, before the value reaches SQL) and the database (the
// NUMERIC(10,2) column type plus the not-NaN CHECK constraints added in
// migration 034).
//
// CLAUDE.md records why the split is uneven: scale can NEVER be defended at
// the database level, because a NUMERIC(10,2) column coerces excess scale
// before any CHECK runs. `"10.999"` would silently become `11.00`. So this
// package is not defence-in-depth for scale - it is the ONLY defence, and a
// change that loosens it has no backstop.
//
// The magnitude half is the reverse: the column is the hard limit, and this
// package must not accept a value the column cannot hold. Verified against
// the live schema on 2026-09-21: every money column in the database is
// NUMERIC(10,2) - 18 of them - and the only NUMERIC(3,2) columns are
// artists.rating and stores.rating, which are not money and never pass
// through here.

// NUMERIC(10,2) holds 10 significant digits with 2 after the point, so the
// largest value is 99999999.99 - eight integer digits.
const columnMaxIntegerDigits = 8

func TestParse_AcceptsTheLargestValueTheColumnCanHold(t *testing.T) {
	max := strings.Repeat("9", columnMaxIntegerDigits) + ".99" // 99999999.99
	got, err := Parse(max, "price")
	if err != nil {
		t.Fatalf("Parse(%q) rejected a value NUMERIC(10,2) can store: %v", max, err)
	}
	if got.String() != max {
		t.Fatalf("Parse(%q) = %s, want %s", max, got.String(), max)
	}
}

// The important direction. If this package ever accepted a wider value, the
// insert would fail at the database with a numeric overflow - a 500 on a
// booking rather than a 400 on a form.
func TestParse_RejectsAnythingWiderThanTheColumn(t *testing.T) {
	tooWide := strings.Repeat("9", columnMaxIntegerDigits+1) // 999999999
	if _, err := Parse(tooWide, "price"); err == nil {
		t.Fatalf("Parse(%q) was accepted, but NUMERIC(10,2) cannot store it - "+
			"this would become a 500 at insert time instead of a 400 at the edge", tooWide)
	}
}

// Scale has no database backstop: the column coerces before any CHECK runs,
// so "10.999" would be stored as 11.00 without complaint. This package is
// the only thing that says no.
func TestParse_RejectsExcessScale_TheOnlyDefence(t *testing.T) {
	for _, in := range []string{"10.999", "0.001", "1.234567"} {
		if _, err := Parse(in, "price"); err == nil {
			t.Errorf("Parse(%q) was accepted. NUMERIC(10,2) would silently round it, "+
				"and no CHECK can catch that - this package is the only defence.", in)
		}
	}
}

// NaN is the one non-finite value PostgreSQL accepts into a numeric column.
// A stored NaN made every later read of that row fail with a 500, poisoned
// SUM(price), and could not be repaired through the API (security test
// INJ-04). Migration 034 added CHECK constraints, but this must reject it
// first - the constraints exist for values that never pass through here.
func TestParse_RejectsNaNAndFriends(t *testing.T) {
	for _, in := range []string{"NaN", "nan", "Infinity", "-Infinity", "inf"} {
		if _, err := Parse(in, "price"); err == nil {
			t.Errorf("Parse(%q) was accepted - PostgreSQL stores NaN happily "+
				"and every later read of that row then fails", in)
		}
	}
}

// Scientific notation was the original hole: decimal.NewFromString accepts
// "1e3" as one thousand. The whitelist rejects it by construction, which is
// the point of a whitelist over a list of rejections.
func TestParse_RejectsScientificNotation(t *testing.T) {
	for _, in := range []string{"1e3", "1E3", "1e-2"} {
		if _, err := Parse(in, "price"); err == nil {
			t.Errorf("Parse(%q) was accepted and would mean %s", in, "1000")
		}
	}
}

// Negative money has no meaning on any column this guards, and the database
// does not forbid it - several money columns have no >= 0 CHECK.
func TestParse_RejectsNegative(t *testing.T) {
	for _, in := range []string{"-1", "-0.01"} {
		if _, err := Parse(in, "price"); err == nil {
			t.Errorf("Parse(%q) was accepted", in)
		}
	}
}
