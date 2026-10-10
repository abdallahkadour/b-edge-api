package booking

// Travel between an artist's stores in the slot list (audit 2026-10-10).
//
// T4: the time needed between two stores was the REQUESTED store's figure,
//     so it depended on which store was booked first.
// T5: the day's bookings were read by UTC date; Beirut's day begins two or
//     three hours earlier, so an early booking at another store cast no
//     travel into the morning.
// T6: travel after a booking at another store started when the client got
//     up, not when the artist had finished her cleanup there.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tripoliAndBeirut: the artist is asked for at Tripoli (30-minute weekday
// travel), with a booking at Beirut (150).
func tripoliAndBeirut(beirutBooking *Booking) (*mockRepo, GetAvailableSlotsRequest) {
	beirut, tripoli := defaultStore(), defaultStore()
	tripoli.WeekdayBufferMin = 30
	beirutBooking.StoreID = beirut.ID
	req := validSlotsReq() // Monday 2027-03-01, a weekday
	req.StoreID = tripoli.ID.String()
	return &mockRepo{
		storesByID:            storesByID(beirut, tripoli),
		getBusinessHoursBH:    defaultBusinessHours(), // 09:00-18:00
		getServiceSvc:         defaultService(),       // 60 minutes, no cleanup
		getCrossStoreBookings: []*Booking{beirutBooking},
	}, req
}

func storesByID(stores ...*Store) map[uuid.UUID]*Store {
	m := make(map[uuid.UUID]*Store, len(stores))
	for _, st := range stores {
		m[st.ID] = st
	}
	return m
}

func offered(slots []*TimeSlot, at time.Time) bool {
	for _, s := range slots {
		if s.StartTime.Equal(at) {
			return true
		}
	}
	return false
}

func TestSlots_TravelIsTheLongerOfTheTwoStores_NotTheOneBeingBooked(t *testing.T) {
	b := existingBooking(beirutTime(2027, 3, 1, 10, 0), beirutTime(2027, 3, 1, 11, 0))
	repo, req := tripoliAndBeirut(b)

	slots, err := newTestService(repo).GetAvailableSlots(context.Background(), req)

	require.NoError(t, err)
	assert.False(t, offered(slots, beirutTime(2027, 3, 1, 11, 30)), "30 minutes is Tripoli's figure; the road needs Beirut's 150")
	assert.False(t, offered(slots, beirutTime(2027, 3, 1, 13, 15)))
	assert.True(t, offered(slots, beirutTime(2027, 3, 1, 13, 30)), "11:00 + 150 minutes")
}

func TestSlots_TravelStartsAfterHerCleanupAtTheOtherStore(t *testing.T) {
	b := existingBooking(beirutTime(2027, 3, 1, 10, 0), beirutTime(2027, 3, 1, 11, 0))
	b.BlockedUntil = beirutTime(2027, 3, 1, 11, 30) // 30 minutes of cleanup in Beirut
	repo, req := tripoliAndBeirut(b)

	slots, err := newTestService(repo).GetAvailableSlots(context.Background(), req)

	require.NoError(t, err)
	assert.False(t, offered(slots, beirutTime(2027, 3, 1, 13, 30)), "she is still cleaning up in Beirut until 11:30")
	assert.True(t, offered(slots, beirutTime(2027, 3, 1, 14, 0)), "11:30 + 150 minutes")
}

func TestSlots_ReadTheDayOnTheStoresCalendar_WidenedByTravel(t *testing.T) {
	repo := &mockRepo{getStoreStore: defaultStore(), getBusinessHoursBH: defaultBusinessHours(), getServiceSvc: defaultService()}

	_, err := newTestService(repo).GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)
	localMidnight := beirutTime(2027, 3, 1, 0, 0) // 2027-02-28 22:00 UTC
	for _, w := range [][2]time.Time{{repo.dayWindowFrom, repo.dayWindowTo}, {repo.crossWindowFrom, repo.crossWindowTo}} {
		assert.True(t, !w[0].After(localMidnight.Add(-3*time.Hour)),
			"a booking ending late the night before still casts its travel into the morning (from %s)", w[0])
		assert.True(t, !w[1].Before(localMidnight.Add(27*time.Hour)),
			"and one early the next morning casts its travel back into the evening (to %s)", w[1])
	}
}
