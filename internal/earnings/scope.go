package earnings

import (
	"fmt"
	"strings"
)

// scope.go: the one definition of which bookings count as an artist's
// earnings, and whose bookings a caller may see.
//
// ── Why this file exists ─────────────────────────────────────────────────
//
// RevenueStatuses has declared the answer since the domain was written. It
// was read by nothing. The three aggregate queries in repository.go each
// hard-coded `status IN ('completed', 'no_show')` instead - the same list,
// written out three more times, directly below the constant that defines it.
//
// That is the shape this project keeps producing: a rule stated correctly in
// one place and not consulted from the places that need it.
// subscriptionVisibleCond was three hand-written copies and two of them were
// still wrong a day after the third was fixed; plans.seat_price and
// plans.included_seats are populated for all six plans and feed no
// arithmetic anywhere. Nothing had gone wrong here yet, which is the only
// reason this is a tidy-up rather than an incident.
//
// TestNoHardcodedRevenueStatuses fails the build if the list is written out
// again.

// EarnedCond returns the SQL predicate selecting bookings that count as
// earned revenue for the table aliased by alias.
//
// Pass "" when the query has no alias on bookings. The statuses come from
// RevenueStatuses, so adding a status there changes every aggregate at once
// rather than two out of three.
//
// The values are a package-level constant, never user input, so
// interpolation is safe here in a way it would not be for a request field -
// every money and identifier value in this domain still goes through
// parameters.
func EarnedCond(alias string) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}

	quoted := make([]string, 0, len(RevenueStatuses))
	for _, s := range RevenueStatuses {
		quoted = append(quoted, "'"+s+"'")
	}
	return fmt.Sprintf("%[1]sstatus IN (%[2]s)", prefix, strings.Join(quoted, ", "))
}

// PeriodCond returns the half-open date-range predicate the aggregates share.
//
// Half-open on purpose: `>= from AND < to`. A closed upper bound would count
// a booking that starts exactly at midnight into both the month that ends
// and the month that begins, which is how a revenue figure comes out higher
// than the sum of its parts.
//
// fromParam and toParam are the placeholder numbers in the caller's query,
// so the caller keeps control of argument order.
func PeriodCond(alias string, fromParam, toParam int) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return fmt.Sprintf("%[1]sstart_time >= $%[2]d AND %[1]sstart_time < $%[3]d",
		prefix, fromParam, toParam)
}

// ArtistScopeCond returns the predicate restricting an aggregate to one
// artist's bookings.
//
// Separated from EarnedCond because the two answer different questions and
// change for different reasons: EarnedCond is "does this booking count as
// money", ArtistScopeCond is "whose money is it". Phase 4 widens only the
// second - an owner sees every member's earnings - and keeping them apart
// means that change cannot accidentally alter which bookings are counted.
func ArtistScopeCond(alias string, artistParam int) string {
	prefix := ""
	if alias != "" {
		prefix = alias + "."
	}
	return fmt.Sprintf("%[1]sartist_id = $%[2]d", prefix, artistParam)
}
