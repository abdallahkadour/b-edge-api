package booking

// A booking's artist, store and service must belong together.
//
// Until 2026-09-25 they did not have to. Every entry point that takes all
// three IDs - the guest hold, POST /bookings, the slots endpoint and the
// waitlist - validated that each ID EXISTED and never that they belonged to
// each other. Measured live, both returned 201:
//
//	Rania + her store + another salon's $50 "nails session"
//	    -> booked Rania at the other salon's price, and filed the booking
//	       under the OTHER salon (salon_id is copied from the service)
//	Rania + her service + another salon's store
//	    -> booked her at a location she does not work at
//
// Service IDs are public - every artist profile lists them - so anyone could
// find the cheapest service on the platform and book any artist at that price.
//
// Every test here names the production change that would make it fail. Each
// cross-salon case also asserts NOTHING WAS WRITTEN, because refusing while
// still creating the row is the worst outcome and the easiest to miss.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

var otherSalonID = uuid.MustParse("0b0b0b0b-0000-4000-8000-000000000002")

func appErrOf(t *testing.T, err error) *apperror.AppError {
	t.Helper()
	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr, "must be a typed error, got %v", err)
	return appErr
}

func holdReq() HoldGuestSlotRequest {
	return HoldGuestSlotRequest{
		ArtistID:  uuid.New().String(),
		StoreID:   uuid.New().String(),
		ServiceID: uuid.New().String(),
		StartTime: time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339),
	}
}

func foreignService() *SalonService {
	s := defaultService()
	s.SalonID = otherSalonID
	return s
}

func foreignStore() *Store {
	s := defaultStore()
	s.SalonID = otherSalonID
	return s
}

// ── the guest hold ─────────────────────────────────────────────────────────

func TestHoldGuestSlot_MatchingParties_Succeeds(t *testing.T) {
	// POSITIVE CONTROL. Breaks if the guard refuses a legitimate booking -
	// which would satisfy every refusal test below while making the product
	// unusable.
	repo := &mockRepo{getServiceSvc: defaultService(), getStoreStore: defaultStore()}

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdReq())

	require.NoError(t, err)
	require.NotNil(t, repo.createBookingCaptured, "a matching booking must be written")
}

func TestHoldGuestSlot_ServiceFromAnotherSalon_NotFoundAndNothingWritten(t *testing.T) {
	// Breaks if the service is not compared with the artist's salon - the
	// live exploit: any artist booked at any salon's price.
	repo := &mockRepo{getServiceSvc: foreignService(), getStoreStore: defaultStore()}

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdReq())

	e := appErrOf(t, err)
	assert.Equal(t, 404, e.HTTPStatus)
	assert.Equal(t, "SERVICE_NOT_FOUND", e.Code)
	assert.Nil(t, repo.createBookingCaptured, "REFUSED BUT STILL WROTE THE BOOKING")
}

func TestHoldGuestSlot_StoreFromAnotherSalon_NotFoundAndNothingWritten(t *testing.T) {
	// Breaks if the store is not compared with the artist's salon.
	repo := &mockRepo{getServiceSvc: defaultService(), getStoreStore: foreignStore()}

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdReq())

	e := appErrOf(t, err)
	assert.Equal(t, 404, e.HTTPStatus)
	assert.Equal(t, "STORE_NOT_FOUND", e.Code)
	assert.Nil(t, repo.createBookingCaptured, "REFUSED BUT STILL WROTE THE BOOKING")
}

func TestHoldGuestSlot_StoreArtistDoesNotWorkAt_NotFound(t *testing.T) {
	// Same salon, but no artist_stores link. Breaks if only the salon is
	// checked: the customer funnel never offers a store the artist is not
	// linked to, so a request naming one was crafted.
	repo := &mockRepo{
		getServiceSvc:     defaultService(),
		getStoreStore:     defaultStore(),
		placementUnlinked: true,
	}

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdReq())

	assert.Equal(t, "STORE_NOT_FOUND", appErrOf(t, err).Code)
	assert.Nil(t, repo.createBookingCaptured)
}

func TestHoldGuestSlot_ArtistWithNoSalon_ServiceNotFound(t *testing.T) {
	// An artist who has not onboarded has no salon, so every service is
	// foreign to her. Breaks if a nil salon is treated as a match or
	// dereferenced.
	repo := &mockRepo{
		getServiceSvc:    defaultService(),
		getStoreStore:    defaultStore(),
		placementNoSalon: true,
	}

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdReq())

	assert.Equal(t, "SERVICE_NOT_FOUND", appErrOf(t, err).Code)
	assert.Nil(t, repo.createBookingCaptured)
}

func TestHoldGuestSlot_UnknownStore_404Not500(t *testing.T) {
	// Breaks if ErrStoreNotFound is wrapped as an internal error. It was: an
	// unknown store_id answered 500 INTERNAL_ERROR, which tells the client
	// nothing and pages whoever watches the error rate.
	repo := &mockRepo{getServiceSvc: defaultService(), getStoreErr: ErrStoreNotFound}

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdReq())

	e := appErrOf(t, err)
	assert.Equal(t, 404, e.HTTPStatus)
	assert.Equal(t, "STORE_NOT_FOUND", e.Code)
}

func TestHoldGuestSlot_ForeignService_IndistinguishableFromMissing(t *testing.T) {
	// Enumeration resistance, the house rule: an object you may not use must
	// look exactly like one that does not exist. Breaks if the cross-salon
	// branch returns its own code or wording - which would let anyone probe
	// which service IDs are real.
	missing := &mockRepo{getServiceErr: ErrServiceNotFound, getStoreStore: defaultStore()}
	foreign := &mockRepo{getServiceSvc: foreignService(), getStoreStore: defaultStore()}

	_, errMissing := newTestService(missing).HoldGuestSlot(context.Background(), holdReq())
	_, errForeign := newTestService(foreign).HoldGuestSlot(context.Background(), holdReq())

	a, b := appErrOf(t, errMissing), appErrOf(t, errForeign)
	assert.Equal(t, a.HTTPStatus, b.HTTPStatus)
	assert.Equal(t, a.Code, b.Code)
	assert.Equal(t, a.Message, b.Message)
}

// ── the other three entry points ───────────────────────────────────────────
//
// One test each. Every one of them breaks if the guard exists but is NOT
// CALLED on that path - which is how the original bug survived: the check
// that should have existed would have had to be remembered four times.

func TestCreateBooking_ServiceFromAnotherSalon_NotFound(t *testing.T) {
	repo := &mockRepo{getServiceSvc: foreignService(), getStoreStore: defaultStore()}

	_, err := newTestService(repo).CreateBooking(context.Background(), CreateBookingRequest{
		ArtistID:  uuid.New().String(),
		StoreID:   uuid.New().String(),
		ServiceID: uuid.New().String(),
		StartTime: time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339),
		Channel:   "customer_pwa",
	}, uuid.New())

	assert.Equal(t, "SERVICE_NOT_FOUND", appErrOf(t, err).Code)
	assert.Nil(t, repo.createBookingCaptured, "REFUSED BUT STILL WROTE THE BOOKING")
}

func TestGetAvailableSlots_ServiceFromAnotherSalon_NotFound(t *testing.T) {
	// A slot list is a promise that the time can be booked. Offering slots
	// for a pairing the hold will refuse is a promise the API then breaks.
	repo := &mockRepo{getServiceSvc: foreignService(), getStoreStore: defaultStore()}

	_, err := newTestService(repo).GetAvailableSlots(context.Background(), validSlotsReq())

	assert.Equal(t, "SERVICE_NOT_FOUND", appErrOf(t, err).Code)
}

func TestJoinWaitlist_ServiceFromAnotherSalon_NotFoundAndNothingWritten(t *testing.T) {
	// The waitlist took all three IDs and checked NOTHING - not even that
	// they existed. A cross-salon entry later notifies a customer about a
	// slot she can never book.
	repo := &mockRepo{
		getServiceSvc:         foreignService(),
		getStoreStore:         defaultStore(),
		createWaitlistEntryID: uuid.New(),
	}

	_, err := newTestService(repo).JoinWaitlist(context.Background(), JoinWaitlistRequest{
		ArtistID:      uuid.New().String(),
		StoreID:       uuid.New().String(),
		ServiceID:     uuid.New().String(),
		RequestedDate: time.Now().UTC().Add(72 * time.Hour).Format("2006-01-02"),
		Name:          "Sarah",
		Phone:         "70123456",
	})

	assert.Equal(t, "SERVICE_NOT_FOUND", appErrOf(t, err).Code)
	assert.False(t, repo.createWaitlistEntryCalled, "REFUSED BUT STILL WROTE THE WAITLIST ENTRY")
}

func TestHoldGuestSlot_ServiceNotOffered_NotFoundAndNothingWritten(t *testing.T) {
	// PP-4: a service she has switched off is refused exactly like a missing
	// one. The repository reports both as ErrServiceNotFound; this asserts the
	// guard turns that into the SAME 404 and writes nothing.
	repo := &mockRepo{getServiceErr: ErrServiceNotFound, getStoreStore: defaultStore()}

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdReq())

	assert.Equal(t, "SERVICE_NOT_FOUND", appErrOf(t, err).Code)
	assert.Nil(t, repo.createBookingCaptured)
}

func TestHoldGuestSlot_DatabaseErrorOnService_IsNotA404(t *testing.T) {
	// Breaks if every error is mapped to "not found": a database outage
	// would then tell customers the service does not exist.
	repo := &mockRepo{getServiceErr: errors.New("connection reset"), getStoreStore: defaultStore()}

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdReq())

	require.Error(t, err)
	var appErr *apperror.AppError
	assert.False(t, errors.As(err, &appErr), "a database error must surface as an internal error, got %v", err)
}
