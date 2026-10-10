package booking

// A booking is only written for a time the slot list would offer (audit
// 2026-10-10, T1-T3).
//
// Measured on the live stack before this: with the artist booked in Beirut
// 10:00-11:00, POST /bookings/guest/hold at Tripoli 11:30 - inside the
// 150 minutes of travel the slot list correctly withheld - answered 201. So
// did a hold at 03:00 with the store closed, and one at 13:07, off the
// grid. Neither write path asked the slot generator; the database's GIST
// constraint covers an appointment and its cleanup, never travel, hours or
// notice. The list a customer picks from IS the rule now, as it already was
// for a reschedule.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// openMonday is a future Monday, 09:00-18:00 at the default store, nothing
// booked: 10:00 is a time the slot list offers.
func openMonday() *mockRepo {
	return &mockRepo{
		getStoreStore:      defaultStore(),
		getBusinessHoursBH: defaultBusinessHours(),
		getServiceSvc:      defaultService(),
	}
}

func holdAt(start time.Time) HoldGuestSlotRequest {
	return HoldGuestSlotRequest{
		ArtistID: uuid.New().String(), StoreID: uuid.New().String(), ServiceID: uuid.New().String(),
		StartTime: start.UTC().Format(time.RFC3339),
	}
}

func refusedAsUnavailable(t *testing.T, err error, repo *mockRepo) {
	t.Helper()
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "SLOT_UNAVAILABLE", ae.Code)
	assert.Equal(t, 409, ae.HTTPStatus)
	assert.Nil(t, repo.createBookingCaptured, "nothing may be written for a time that is not offered")
}

func TestHoldGuestSlot_AnOfferedTime_IsHeld(t *testing.T) {
	repo := openMonday()

	res, err := newTestService(repo).HoldGuestSlot(context.Background(), holdAt(beirutTime(2027, 3, 1, 10, 0)))

	require.NoError(t, err)
	require.NotNil(t, repo.createBookingCaptured)
	assert.True(t, res.StartTime.Equal(beirutTime(2027, 3, 1, 10, 0)))
}

func TestHoldGuestSlot_InsideTravelFromAnotherStore_IsRefused(t *testing.T) {
	repo := openMonday()
	beirut := existingBooking(beirutTime(2027, 3, 1, 10, 0), beirutTime(2027, 3, 1, 11, 0))
	beirut.StoreID = uuid.New()
	repo.getCrossStoreBookings = []*Booking{beirut}

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdAt(beirutTime(2027, 3, 1, 11, 30)))

	refusedAsUnavailable(t, err, repo)
}

func TestHoldGuestSlot_WhenTheStoreIsClosed_IsRefused(t *testing.T) {
	repo := openMonday()

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdAt(beirutTime(2027, 3, 1, 3, 0)))

	refusedAsUnavailable(t, err, repo)
}

func TestHoldGuestSlot_OffTheGrid_IsRefused(t *testing.T) {
	repo := openMonday()

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdAt(beirutTime(2027, 3, 1, 13, 7)))

	refusedAsUnavailable(t, err, repo)
}

func TestCreateBooking_InsideTravelFromAnotherStore_IsRefused(t *testing.T) {
	repo := openMonday()
	beirut := existingBooking(beirutTime(2027, 3, 1, 10, 0), beirutTime(2027, 3, 1, 11, 0))
	beirut.StoreID = uuid.New()
	repo.getCrossStoreBookings = []*Booking{beirut}
	req := CreateBookingRequest{
		ArtistID: uuid.New().String(), StoreID: uuid.New().String(), ServiceID: uuid.New().String(),
		StartTime: beirutTime(2027, 3, 1, 11, 30).UTC().Format(time.RFC3339), Channel: ChannelCustomerPWA,
	}

	_, err := newTestService(repo).CreateBooking(context.Background(), req, uuid.New())

	refusedAsUnavailable(t, err, repo)
}

func TestHoldGuestSlot_CarriesTheTravelItNeeds_ToTheTransaction(t *testing.T) {
	// The second half of the guard: two holds in the same second can both
	// read "free". The transaction re-checks under a per-artist lock, and it
	// needs to know how far each other store is.
	repo := openMonday()
	here := repo.getStoreStore
	tripoli := defaultStore()
	tripoli.WeekdayBufferMin = 30
	repo.storesByID = storesByID(here, tripoli)
	repo.artistStoreIDs = []uuid.UUID{here.ID, tripoli.ID}
	req := holdAt(beirutTime(2027, 3, 1, 10, 0))
	req.StoreID = here.ID.String()

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, repo.createBookingCaptured)
	assert.Equal(t, map[uuid.UUID]int{tripoli.ID: 150}, repo.createBookingCaptured.travel,
		"the longer of the two stores, and never the store itself")
}

// A store the salon has switched off still exists, and the artist may still
// be placed at it (Rania is: QATEST_ Store 2 on the dev database). The travel
// lookup read it through GetStore, which only returns ACTIVE stores, so every
// hold for her answered 500 - caught by verify-uc1 B1 on 2026-10-10, before
// this shipped.
func TestHoldGuestSlot_ArtistAlsoPlacedAtAClosedBranch_StillHolds(t *testing.T) {
	repo := openMonday()
	here := repo.getStoreStore
	closed := defaultStore()
	repo.storesByID = storesByID(here, closed)
	repo.inactiveStoreIDs = map[uuid.UUID]bool{closed.ID: true}
	repo.artistStoreIDs = []uuid.UUID{here.ID, closed.ID}
	req := holdAt(beirutTime(2027, 3, 1, 10, 0))
	req.StoreID = here.ID.String()

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), req)

	require.NoError(t, err)
	assert.Contains(t, repo.createBookingCaptured.travel, closed.ID,
		"a booking still on the closed branch's calendar is still somewhere she has to travel to")
}

func TestSlots_ABookingAtAClosedBranch_StillCastsItsTravel(t *testing.T) {
	repo := openMonday()
	closed := defaultStore()
	repo.storesByID = storesByID(repo.getStoreStore, closed)
	repo.inactiveStoreIDs = map[uuid.UUID]bool{closed.ID: true}
	there := existingBooking(beirutTime(2027, 3, 1, 9, 0), beirutTime(2027, 3, 1, 10, 0))
	there.StoreID = closed.ID
	repo.getCrossStoreBookings = []*Booking{there}
	req := validSlotsReq()
	req.StoreID = repo.getStoreStore.ID.String()

	slots, err := newTestService(repo).GetAvailableSlots(context.Background(), req)

	require.NoError(t, err)
	assert.False(t, offered(slots, beirutTime(2027, 3, 1, 12, 0)), "10:00 + 150 minutes is 12:30")
	assert.True(t, offered(slots, beirutTime(2027, 3, 1, 12, 30)))
}
