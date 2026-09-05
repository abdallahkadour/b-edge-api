package review

// Who a review's specialist score belongs to.
//
// WHY THIS EXISTS BEFORE IT IS REACHABLE
//
// A booking today has one artist and one service, so "the primary stylist" is
// always "the only stylist" and this function can never take its second
// branch. It is written anyway, as a named function with its own tests,
// because the alternative is that the rule gets inlined at the call site as
// `booking.ArtistID` and is quietly lost the day split bookings land
// (Sprint 13) - which is the exact moment it starts to matter.
//
// Decision D5.1, proposed in B-Edge-D3-D5-Proposals-v1.md and accepted
// 2026-09-06.
//
// THE RULE, AND WHY
//
// Highest final_price wins; ties broken by longest duration.
//
// The feasibility assessment §2.2 said "the longest OR highest-value service"
// without choosing. Price is the platform's own proxy for what the customer
// came for; duration is not. A 15-minute bridal touch-up at $200 matters more
// to the client than an hour-long blow-dry at $40, and it is the touch-up they
// are rating. Duration survives as the tie-break because it is deterministic
// and always present.
//
// PRICE, NOT ORIGINAL PRICE
//
// final_price is what the customer actually paid, after discounts and the
// early-bird surcharge. A discounted service is not less of the reason they
// came - but between two services, what was paid is the better signal of which
// one the visit was for, and it is the number the customer themselves saw.

import (
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ServiceLine is one service performed during a visit, reduced to only what
// attribution needs.
//
// Deliberately not the full booking row: this function must stay pure and
// testable without constructing a database record, and a narrow input makes it
// obvious that nothing else influences the answer.
type ServiceLine struct {
	ArtistID    uuid.UUID
	FinalPrice  decimal.Decimal
	DurationMin int
}

// PrimaryStylist returns the artist a visit's specialist rating belongs to.
//
// Reports false when there is nothing to attribute - an empty visit. Callers
// must handle that rather than defaulting to a zero UUID, which would attach a
// real customer's rating to no one.
func PrimaryStylist(lines []ServiceLine) (uuid.UUID, bool) {
	if len(lines) == 0 {
		return uuid.Nil, false
	}

	best := lines[0]
	for _, line := range lines[1:] {
		if beats(line, best) {
			best = line
		}
	}
	return best.ArtistID, true
}

// beats reports whether candidate should displace current.
//
// Strictly greater at every level, so an exact tie leaves the incumbent in
// place and the result cannot depend on comparison order.
func beats(candidate, current ServiceLine) bool {
	if c := candidate.FinalPrice.Cmp(current.FinalPrice); c != 0 {
		return c > 0
	}
	if candidate.DurationMin != current.DurationMin {
		return candidate.DurationMin > current.DurationMin
	}

	// Same price and same duration. Compare the ID purely so the answer does
	// not depend on the order the caller happened to build the slice in - two
	// identical-looking services must resolve the same way every time, or the
	// rating lands on a different artist per request.
	//
	// Which artist wins here is arbitrary and is not a judgement about them.
	// Determinism is the only property being bought.
	return candidate.ArtistID.String() < current.ArtistID.String()
}
