package booking

// One travel rule between two stores (audit 2026-10-10, T4).
//
// Before, slot generation and the day shift each took the REQUESTED store's
// default and looked the artist's override up in one direction, so a 10:00
// in Beirut and an 11:30 in Tripoli were allowed when Tripoli was booked
// second (Tripoli's 30 minutes) and refused when Beirut was (Beirut's 150).
// Measured on the live stack. The road is the same both ways.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func twoStores(beirutWeekday, beirutWeekend, tripoliWeekday, tripoliWeekend int) (*Store, *Store) {
	a, b := defaultStore(), defaultStore()
	a.WeekdayBufferMin, a.WeekendBufferMin = beirutWeekday, beirutWeekend
	b.WeekdayBufferMin, b.WeekendBufferMin = tripoliWeekday, tripoliWeekend
	return a, b
}

var (
	tuesday = time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	friday  = time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC) // Lebanon's weekend starts Friday
)

func TestTravelMinutes_IsTheSameWhicheverStoreIsBooked(t *testing.T) {
	a, b := twoStores(150, 90, 30, 20)
	svc := newTestService(&mockRepo{})

	ab, err := svc.travelMinutes(context.Background(), uuid.New(), a, b, tuesday)
	require.NoError(t, err)
	ba, err := svc.travelMinutes(context.Background(), uuid.New(), b, a, tuesday)
	require.NoError(t, err)

	assert.Equal(t, 150, ab, "the longer of the two stores' defaults")
	assert.Equal(t, ab, ba)
}

func TestTravelMinutes_WeekendUsesTheWeekendDefaults(t *testing.T) {
	a, b := twoStores(150, 90, 30, 20)

	got, err := newTestService(&mockRepo{}).travelMinutes(context.Background(), uuid.New(), a, b, friday)

	require.NoError(t, err)
	assert.Equal(t, 90, got)
}

func TestTravelMinutes_AnOverrideInEitherDirectionCounts(t *testing.T) {
	a, b := twoStores(150, 90, 150, 90)
	repo := &mockRepo{buffersByPair: map[[2]uuid.UUID]*ArtistStoreBuffer{
		{b.ID, a.ID}: {WeekdayBufferMin: 45, WeekendBufferMin: 40}, // set only Tripoli -> Beirut
	}}
	svc := newTestService(repo)

	ab, err := svc.travelMinutes(context.Background(), uuid.New(), a, b, tuesday)
	require.NoError(t, err)
	ba, err := svc.travelMinutes(context.Background(), uuid.New(), b, a, tuesday)
	require.NoError(t, err)

	assert.Equal(t, 45, ab, "the artist's own figure beats the stores' defaults")
	assert.Equal(t, 45, ba, "and it is the same road back")
}

func TestTravelMinutes_TwoDifferentOverrides_TheLongerOne(t *testing.T) {
	a, b := twoStores(150, 90, 150, 90)
	repo := &mockRepo{buffersByPair: map[[2]uuid.UUID]*ArtistStoreBuffer{
		{a.ID, b.ID}: {WeekdayBufferMin: 45, WeekendBufferMin: 40},
		{b.ID, a.ID}: {WeekdayBufferMin: 60, WeekendBufferMin: 40},
	}}

	got, err := newTestService(repo).travelMinutes(context.Background(), uuid.New(), a, b, tuesday)

	require.NoError(t, err)
	assert.Equal(t, 60, got)
}
