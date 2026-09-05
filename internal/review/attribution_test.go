package review

import (
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func line(artist uuid.UUID, price string, mins int) ServiceLine {
	return ServiceLine{
		ArtistID:    artist,
		FinalPrice:  decimal.RequireFromString(price),
		DurationMin: mins,
	}
}

// TestPrimaryStylist_SingleService_IsThatArtist is the only case reachable
// today, and the one that must never regress: every current booking has one
// artist and one service.
func TestPrimaryStylist_SingleService_IsThatArtist(t *testing.T) {
	a := uuid.New()

	got, ok := PrimaryStylist([]ServiceLine{line(a, "45.00", 90)})

	require.True(t, ok)
	assert.Equal(t, a, got)
}

// TestPrimaryStylist_HigherPriceWins_EvenWhenShorter is D5.1 stated as a test:
// a 15-minute touch-up at $200 beats an hour-long blow-dry at $40, because
// price is the proxy for what the customer came for.
func TestPrimaryStylist_HigherPriceWins_EvenWhenShorter(t *testing.T) {
	touchUp, blowDry := uuid.New(), uuid.New()

	got, ok := PrimaryStylist([]ServiceLine{
		line(blowDry, "40.00", 60),
		line(touchUp, "200.00", 15),
	})

	require.True(t, ok)
	assert.Equal(t, touchUp, got, "price decides, not duration")
}

// TestPrimaryStylist_EqualPrice_LongestWins - duration is the tie-break, not
// the primary rule.
func TestPrimaryStylist_EqualPrice_LongestWins(t *testing.T) {
	short, long := uuid.New(), uuid.New()

	got, ok := PrimaryStylist([]ServiceLine{
		line(short, "100.00", 30),
		line(long, "100.00", 120),
	})

	require.True(t, ok)
	assert.Equal(t, long, got)
}

// TestPrimaryStylist_OrderIndependent is why the final tie-break exists. The
// same visit built in a different order must resolve to the same artist, or
// the rating lands on whoever the query happened to return first.
func TestPrimaryStylist_OrderIndependent(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	forward := []ServiceLine{line(a, "100.00", 60), line(b, "100.00", 60)}
	reverse := []ServiceLine{line(b, "100.00", 60), line(a, "100.00", 60)}

	gotForward, ok1 := PrimaryStylist(forward)
	gotReverse, ok2 := PrimaryStylist(reverse)

	require.True(t, ok1)
	require.True(t, ok2)
	assert.Equal(t, gotForward, gotReverse,
		"identical services must resolve identically regardless of slice order")
}

// TestPrimaryStylist_Empty_ReportsNothing - a visit with no services has no
// primary stylist. Returning uuid.Nil silently would attach a real customer's
// rating to no one.
func TestPrimaryStylist_Empty_ReportsNothing(t *testing.T) {
	got, ok := PrimaryStylist(nil)

	assert.False(t, ok)
	assert.Equal(t, uuid.Nil, got)
}

// TestPrimaryStylist_DecimalPrecision_IsNotFloat guards the comparison itself.
// 0.1+0.2 famously is not 0.3 in float64; decimal.Cmp must not inherit that,
// or two services one cent apart could compare equal and fall through to the
// duration tie-break.
func TestPrimaryStylist_DecimalPrecision_IsNotFloat(t *testing.T) {
	cheap, dear := uuid.New(), uuid.New()

	got, ok := PrimaryStylist([]ServiceLine{
		line(cheap, "0.30", 999),
		line(dear, "0.31", 1),
	})

	require.True(t, ok)
	assert.Equal(t, dear, got, "one cent must still decide, before duration is consulted")
}

// TestPrimaryStylist_SameArtistTwice_IsStillThatArtist - a visit where one
// artist performs two services. Trivial, but it is the shape a split booking
// most often takes, so it should be pinned rather than assumed.
func TestPrimaryStylist_SameArtistTwice_IsStillThatArtist(t *testing.T) {
	a := uuid.New()

	got, ok := PrimaryStylist([]ServiceLine{
		line(a, "45.00", 90),
		line(a, "20.00", 30),
	})

	require.True(t, ok)
	assert.Equal(t, a, got)
}
