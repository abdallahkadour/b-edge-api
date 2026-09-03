// Package booking contains unit tests for the booking service layer.
// These tests use mock repositories - no database required.
package booking

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/billing"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// ── TestMain ──────────────────────────────────────────────────────────────────

// TestMain runs before all tests in this package.
// Sets environment variables required by the service.
func TestMain(m *testing.M) {
	os.Setenv("JWT_SECRET", "test-secret-that-is-long-enough-for-testing-purposes-only")
	os.Setenv("JWT_REFRESH_SECRET", "test-refresh-secret-that-is-long-enough-for-testing-only")
	os.Setenv("APP_ENV", "test")
	os.Exit(m.Run())
}

// ── Mock repository ───────────────────────────────────────────────────────────

// mockRepo implements the Repository interface with controllable behaviour.
// Each field holds the value the method will return when called.
// enqueuedNotification records a single EnqueueNotification call so tests
// can assert on what got queued - which template, which message text,
// which booking/user - without needing a real database.
type enqueuedNotification struct {
	BookingID    *uuid.UUID
	UserID       uuid.UUID
	TemplateName string
	Message      string
}

type mockRepo struct {
	// Bulk schedule preview (migration 029)
	enrichedForDay                        []*EnrichedBooking
	enrichedForDayErr                     error
	getStoreStore                         *Store
	getStoreErr                           error
	getBusinessHoursBH                    *BusinessHours
	getBusinessHoursErr                   error
	getBusinessHoursExceptionEx           *BusinessHoursException
	getBusinessHoursExceptionErr          error
	getServiceSvc                         *SalonService
	getServiceErr                         error
	getArtistBookingsBookings             []*Booking
	getArtistBookingsErr                  error
	getCrossStoreBookings                 []*Booking
	getCrossStoreErr                      error
	getArtistStoreBufferBuf               *ArtistStoreBuffer
	getArtistStoreBufferErr               error
	createBookingErr                      error
	getBookingByIDBooking                 *Booking
	getBookingByIDErr                     error
	getBookingsByArtistBookings           []*Booking
	getBookingsByArtistErr                error
	getBookingsByCustomerBookings         []*Booking
	getBookingsByCustomerErr              error
	getBookingsBySalonBookings            []*Booking
	getBookingsBySalonErr                 error
	updateBookingStatusErr                error
	attachGuestAndSubmitErr               error
	getEnrichedBookingByIDBooking         *EnrichedBooking
	getEnrichedBookingByIDErr             error
	listEnrichedByArtistBookings          []*EnrichedBooking
	listEnrichedByArtistErr               error
	listEnrichedForWeekBookings           []*EnrichedBooking
	listEnrichedForWeekErr                error
	listEnrichedByCustomerBookings        []*EnrichedBooking
	listEnrichedByCustomerErr             error
	approveBookingErr                     error
	approveBookingDepositDeadline         time.Time
	approveBookingCalendarToken           string
	markRefundedErr                       error
	markRefundedReference                 *string
	confirmDepositErr                     error
	cancelBookingErr                      error
	completeBookingErr                    error
	completeBookingToken                  string
	notificationContextCustomer           string
	notificationContextService            string
	notificationContextErr                error
	enqueuedNotifications                 []enqueuedNotification
	markNoShowErr                         error
	releaseExpiredHoldsFreed              []FreedSlot
	releaseExpiredHoldsErr                error
	releaseExpiredHoldsCalled             bool
	expireDeadlineBookingsFreed           []FreedSlot
	expireDeadlineBookingsErr             error
	expireDeadlineBookingsCalled          bool
	expireStalePendingCount               int64
	expireStalePendingErr                 error
	expireStalePendingCalled              bool
	expireStalePendingArtistID            uuid.UUID
	getArtistIDByUserIDArtistID           uuid.UUID
	getArtistIDByUserIDErr                error
	confirmDepositReceivedErr             error
	confirmDepositReceivedReferenceCalled *string
	createBookingCaptured                 *Booking

	// waitlist
	createWaitlistEntryID       uuid.UUID
	createWaitlistEntryErr      error
	getWaitlistByArtistEntries  []*WaitlistEntryResponse
	getWaitlistByArtistErr      error
	notifyNextWaitlistCalled    bool
	notifyNextWaitlistCount     int
	staleWaitlistGroups         []WaitlistGroup
	staleWaitlistErr            error
	shiftedIDs                  []uuid.UUID
	shiftedMinutes              int
	shiftBookingsErr            error
	enqueueNotificationErr      error
	notifyNextWaitlistArtistID  uuid.UUID
	notifyNextWaitlistStoreID   uuid.UUID
	notifyNextWaitlistServiceID uuid.UUID
	notifyNextWaitlistDate      time.Time
	notifyNextWaitlistErr       error
}

func (m *mockRepo) GetStore(_ context.Context, _ uuid.UUID) (*Store, error) {
	return m.getStoreStore, m.getStoreErr
}
func (m *mockRepo) GetBusinessHours(_ context.Context, _ uuid.UUID, _ int) (*BusinessHours, error) {
	return m.getBusinessHoursBH, m.getBusinessHoursErr
}
func (m *mockRepo) GetBusinessHoursException(_ context.Context, _ uuid.UUID, _ time.Time) (*BusinessHoursException, error) {
	return m.getBusinessHoursExceptionEx, m.getBusinessHoursExceptionErr
}
func (m *mockRepo) GetService(_ context.Context, _ uuid.UUID) (*SalonService, error) {
	return m.getServiceSvc, m.getServiceErr
}
func (m *mockRepo) GetArtistBookingsForDate(_ context.Context, _ uuid.UUID, _ time.Time) ([]*Booking, error) {
	return m.getArtistBookingsBookings, m.getArtistBookingsErr
}
func (m *mockRepo) GetArtistCrossStoreBookings(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ time.Time) ([]*Booking, error) {
	return m.getCrossStoreBookings, m.getCrossStoreErr
}
func (m *mockRepo) GetArtistStoreBuffer(_ context.Context, _ uuid.UUID, _ uuid.UUID, _ uuid.UUID) (*ArtistStoreBuffer, error) {
	return m.getArtistStoreBufferBuf, m.getArtistStoreBufferErr
}
func (m *mockRepo) CreateGuestUser(_ context.Context, _ string, _ string) (uuid.UUID, error) {
	return uuid.New(), nil
}
func (m *mockRepo) CreateBooking(_ context.Context, b *Booking) error {
	b.CreatedAt = time.Now()
	b.UpdatedAt = time.Now()
	m.createBookingCaptured = b
	return m.createBookingErr
}
func (m *mockRepo) GetBookingByID(_ context.Context, _ uuid.UUID) (*Booking, error) {
	return m.getBookingByIDBooking, m.getBookingByIDErr
}
func (m *mockRepo) GetBookingsByArtist(_ context.Context, _ uuid.UUID, _ time.Time, _ int) ([]*Booking, error) {
	return m.getBookingsByArtistBookings, m.getBookingsByArtistErr
}
func (m *mockRepo) GetBookingsByCustomer(_ context.Context, _ uuid.UUID, _ time.Time, _ int) ([]*Booking, error) {
	return m.getBookingsByCustomerBookings, m.getBookingsByCustomerErr
}
func (m *mockRepo) GetBookingsBySalon(_ context.Context, _ uuid.UUID, _ time.Time, _ int) ([]*Booking, error) {
	return m.getBookingsBySalonBookings, m.getBookingsBySalonErr
}
func (m *mockRepo) UpdateBookingStatus(_ context.Context, _ uuid.UUID, _ string) error {
	return m.updateBookingStatusErr
}
func (m *mockRepo) GetArtistIDByUserID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return m.getArtistIDByUserIDArtistID, m.getArtistIDByUserIDErr
}
func (m *mockRepo) ConfirmDepositReceived(_ context.Context, _ uuid.UUID, reference *string) error {
	m.confirmDepositReceivedReferenceCalled = reference
	return m.confirmDepositReceivedErr
}
func (m *mockRepo) AttachGuestAndSubmit(_ context.Context, _, _ uuid.UUID, _ *string) error {
	return m.attachGuestAndSubmitErr
}
func (m *mockRepo) GetEnrichedBookingByID(_ context.Context, _ uuid.UUID) (*EnrichedBooking, error) {
	return m.getEnrichedBookingByIDBooking, m.getEnrichedBookingByIDErr
}
func (m *mockRepo) ListEnrichedBookingsByArtist(_ context.Context, _ uuid.UUID, _ string, _ time.Time, _ int) ([]*EnrichedBooking, error) {
	return m.listEnrichedByArtistBookings, m.listEnrichedByArtistErr
}
func (m *mockRepo) ListEnrichedBookingsForDay(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]*EnrichedBooking, error) {
	return m.enrichedForDay, m.enrichedForDayErr
}

func (m *mockRepo) ListEnrichedBookingsForWeek(_ context.Context, _ uuid.UUID, _ time.Time) ([]*EnrichedBooking, error) {
	return m.listEnrichedForWeekBookings, m.listEnrichedForWeekErr
}
func (m *mockRepo) ListEnrichedBookingsByCustomer(_ context.Context, _ uuid.UUID, _ time.Time, _ int) ([]*EnrichedBooking, error) {
	return m.listEnrichedByCustomerBookings, m.listEnrichedByCustomerErr
}
func (m *mockRepo) ApproveBooking(_ context.Context, _ uuid.UUID, depositDeadline time.Time) (string, error) {
	m.approveBookingDepositDeadline = depositDeadline
	if m.approveBookingErr != nil {
		return "", m.approveBookingErr
	}
	if m.approveBookingCalendarToken != "" {
		return m.approveBookingCalendarToken, nil
	}
	return strings.Repeat("a", 64), nil
}
func (m *mockRepo) ConfirmDeposit(_ context.Context, _ uuid.UUID) error {
	return m.confirmDepositErr
}
func (m *mockRepo) MarkRefunded(_ context.Context, _ uuid.UUID, reference *string) error {
	m.markRefundedReference = reference
	return m.markRefundedErr
}
func (m *mockRepo) CancelBooking(_ context.Context, _ uuid.UUID, _ string, _ bool) error {
	return m.cancelBookingErr
}
func (m *mockRepo) CompleteBooking(_ context.Context, _ uuid.UUID) (string, error) {
	return m.completeBookingToken, m.completeBookingErr
}
func (m *mockRepo) GetBookingNotificationContext(_ context.Context, _ uuid.UUID) (string, string, error) {
	return m.notificationContextCustomer, m.notificationContextService, m.notificationContextErr
}
func (m *mockRepo) EnqueueNotification(_ context.Context, bookingID *uuid.UUID, userID uuid.UUID, templateName, message string) error {
	m.enqueuedNotifications = append(m.enqueuedNotifications, enqueuedNotification{
		BookingID: bookingID, UserID: userID, TemplateName: templateName, Message: message,
	})
	return m.enqueueNotificationErr
}
func (m *mockRepo) CreateWaitlistEntry(_ context.Context, _, _, _, _ uuid.UUID, _ time.Time) (uuid.UUID, error) {
	return m.createWaitlistEntryID, m.createWaitlistEntryErr
}
func (m *mockRepo) GetWaitlistByArtist(_ context.Context, _ uuid.UUID) ([]*WaitlistEntryResponse, error) {
	return m.getWaitlistByArtistEntries, m.getWaitlistByArtistErr
}
func (m *mockRepo) NotifyNextWaitlistEntry(_ context.Context, artistID, storeID, serviceID uuid.UUID, date time.Time) error {
	m.notifyNextWaitlistCalled = true
	m.notifyNextWaitlistCount++
	m.notifyNextWaitlistArtistID = artistID
	m.notifyNextWaitlistStoreID = storeID
	m.notifyNextWaitlistServiceID = serviceID
	m.notifyNextWaitlistDate = date
	return m.notifyNextWaitlistErr
}
func (m *mockRepo) MarkNoShow(_ context.Context, _ uuid.UUID) error {
	return m.markNoShowErr
}
func (m *mockRepo) ShiftBookings(_ context.Context, ids []uuid.UUID, minutes int) error {
	m.shiftedIDs = ids
	m.shiftedMinutes = minutes
	return m.shiftBookingsErr
}
func (m *mockRepo) FindStaleWaitlistGroups(_ context.Context, _ time.Time) ([]WaitlistGroup, error) {
	return m.staleWaitlistGroups, m.staleWaitlistErr
}
func (m *mockRepo) ReleaseExpiredHolds(_ context.Context) ([]FreedSlot, error) {
	m.releaseExpiredHoldsCalled = true
	return m.releaseExpiredHoldsFreed, m.releaseExpiredHoldsErr
}
func (m *mockRepo) ExpireDeadlineBookings(_ context.Context) ([]FreedSlot, error) {
	m.expireDeadlineBookingsCalled = true
	return m.expireDeadlineBookingsFreed, m.expireDeadlineBookingsErr
}
func (m *mockRepo) ExpireStalePendingBookings(_ context.Context, artistID uuid.UUID) (int64, error) {
	m.expireStalePendingCalled = true
	m.expireStalePendingArtistID = artistID
	return m.expireStalePendingCount, m.expireStalePendingErr
}

// ── Test helpers ──────────────────────────────────────────────────────────────

// activeSubReader is a SubscriptionStatusReader that always reports the
// target artist as active (PlanCode="comped" → DeriveStatus returns Active
// regardless of dates). Used as the default in newTestService so all
// existing booking tests continue to pass without modification.
type activeSubReader struct{}

func (activeSubReader) GetSubscriptionByArtistID(_ context.Context, artistID uuid.UUID) (*billing.Subscription, error) {
	return &billing.Subscription{ArtistID: artistID, PlanCode: "comped"}, nil
}

// suspendedSubReader returns a past-due subscription so checkArtistAcceptsNewBookings blocks.
type suspendedSubReader struct{}

func (suspendedSubReader) GetSubscriptionByArtistID(_ context.Context, artistID uuid.UUID) (*billing.Subscription, error) {
	past := time.Now().Add(-30 * 24 * time.Hour)
	return &billing.Subscription{ArtistID: artistID, CurrentPeriodEnd: &past}, nil
}

func newTestService(repo Repository) *Service {
	return NewService(repo, activeSubReader{})
}

// defaultStore returns a standard open store for testing.
func defaultStore() *Store {
	cutoff := "09:00:00"
	return &Store{
		ID:                 uuid.New(),
		SalonID:            uuid.New(),
		Name:               "Beirut Downtown",
		City:               "Beirut",
		SameDayNoticeHours: 4,
		EarlyBirdCutoff:    &cutoff,
		EarlyBirdFee:       decimal.NewFromFloat(10.00),
		WeekdayBufferMin:   150,
		WeekendBufferMin:   90,
		IsActive:           true,
		Timezone:           "Asia/Beirut",
	}
}

// dec parses a decimal literal for test assertions.
func dec(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

// defaultBusinessHours returns standard open hours 09:00-18:00.
func defaultBusinessHours() *BusinessHours {
	return &BusinessHours{
		ID:        uuid.New(),
		StoreID:   uuid.New(),
		DayOfWeek: 1,
		OpenTime:  "09:00:00",
		CloseTime: "18:00:00",
		IsOpen:    true,
	}
}

// defaultService returns a 60-minute service with deposit.
func defaultService() *SalonService {
	return &SalonService{
		ID:                   uuid.New(),
		SalonID:              uuid.New(),
		Name:                 "Full Makeup",
		DurationMin:          60,
		Price:                decimal.NewFromFloat(150.00),
		DepositAmount:        decimal.NewFromFloat(50.00),
		DepositDeadlineHours: 48,
		IsActive:             true,
	}
}

// validSlotsReq returns a valid GetAvailableSlotsRequest for a fixed future Monday.
func validSlotsReq() GetAvailableSlotsRequest {
	return GetAvailableSlotsRequest{
		ArtistID:  uuid.New().String(),
		StoreID:   uuid.New().String(),
		ServiceID: uuid.New().String(),
		Date:      "2027-03-01", // Fixed future Monday
	}
}

// beirutTime builds an instant from a wall-clock time in the default store's
// zone. Business hours and the early-bird cutoff are local wall-clock values,
// so fixtures that need to line up with them (a booking that blocks the 09:00
// opening slot, a cross-store job ending at 09:00) must be expressed locally
// too. Hardcoding UTC would make these fixtures drift by the zone's offset
// and drift differently either side of a DST transition.
func beirutTime(year int, month time.Month, day, hour, min int) time.Time {
	loc := storeLocation(defaultStore())
	return time.Date(year, month, day, hour, min, 0, 0, loc)
}

// existingBooking returns a confirmed booking blocking a specific time range.
func existingBooking(start, end time.Time) *Booking {
	return &Booking{
		ID:        uuid.New(),
		ArtistID:  uuid.New(),
		StartTime: start,
		EndTime:   end,
		Status:    StatusConfirmed,
	}
}

// ── GetAvailableSlots tests ───────────────────────────────────────────────────

// TestGetAvailableSlots_StoreClosed - store has is_open = false for this day.
// Expect: empty slot list, no error.
func TestGetAvailableSlots_StoreClosed(t *testing.T) {
	repo := &mockRepo{
		getStoreStore:               defaultStore(),
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          &BusinessHours{IsOpen: false},
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)
	assert.Empty(t, slots)
}

// TestGetAvailableSlots_HolidayClosed - exception exists with is_closed = true.
// Expect: empty slot list, no error.
func TestGetAvailableSlots_HolidayClosed(t *testing.T) {
	repo := &mockRepo{
		getStoreStore: defaultStore(),
		getBusinessHoursExceptionEx: &BusinessHoursException{
			IsClosed: true,
		},
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)
	assert.Empty(t, slots)
}

// TestGetAvailableSlots_InvalidDate - date is not in YYYY-MM-DD format.
// Expect: error returned, no slots.
func TestGetAvailableSlots_InvalidDate(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	req := validSlotsReq()
	req.Date = "not-a-date"

	slots, err := svc.GetAvailableSlots(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, slots)
}

// TestGetAvailableSlots_InvalidUUID - artist_id is not a valid UUID.
// Expect: validation error returned.
func TestGetAvailableSlots_InvalidUUID(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	req := validSlotsReq()
	req.ArtistID = "not-a-uuid"

	slots, err := svc.GetAvailableSlots(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, slots)
}

// TestGetAvailableSlots_ReturnsSlots - happy path, no bookings, full day open.
// Expect: slots returned starting at 09:00.
func TestGetAvailableSlots_ReturnsSlots(t *testing.T) {
	repo := &mockRepo{
		getStoreStore:               defaultStore(),
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          defaultBusinessHours(),
		getServiceSvc:               defaultService(),
		getArtistBookingsBookings:   nil,
		getCrossStoreBookings:       nil,
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)
	assert.NotEmpty(t, slots)
	// Business hours are wall-clock LOCAL times; slot times are UTC instants.
	bLoc := storeLocation(defaultStore())
	// First slot must start at store open time
	assert.Equal(t, "09:00:00", slots[0].StartTime.In(bLoc).Format("15:04:05"))
	// Last slot must end at or before store close time (18:00)
	lastSlot := slots[len(slots)-1]
	assert.LessOrEqual(t, lastSlot.EndTime.In(bLoc).Format("15:04:05"), "18:00:00")
}

// TestGetAvailableSlots_SweepsExpiredHolds guards the lazy-expiry fix: found
// live while executing E2E-TEST-PLAN.md Suite 3 - with no background
// scheduler ever running ReleaseExpiredHolds, an abandoned hold (a guest who
// picked a slot and never submitted) blocked its slot forever. Every
// availability read must now sweep expired holds first, on the same
// self-healing basis the codebase already uses elsewhere (lazy waitlist
// expiry, migration 016).
func TestGetAvailableSlots_SweepsExpiredHolds(t *testing.T) {
	repo := &mockRepo{
		getStoreStore:               defaultStore(),
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          defaultBusinessHours(),
		getServiceSvc:               defaultService(),
	}
	svc := newTestService(repo)

	_, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)
	assert.True(t, repo.releaseExpiredHoldsCalled, "GetAvailableSlots must sweep expired holds before reading blocked ranges")
}

// TestGetAvailableSlots_ExpiredHoldsSweepFails_StillReturnsSlots - the sweep
// is best-effort. A failure there must never fail the availability request
// itself; worst case a stale hold keeps blocking for one more request.
func TestGetAvailableSlots_ExpiredHoldsSweepFails_StillReturnsSlots(t *testing.T) {
	repo := &mockRepo{
		getStoreStore:               defaultStore(),
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          defaultBusinessHours(),
		getServiceSvc:               defaultService(),
		releaseExpiredHoldsErr:      errors.New("boom"),
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err, "a sweep failure must not fail the slots request")
	assert.NotEmpty(t, slots)
}

// TestGetAvailableSlots_SweepsExpiredDeadlineBookings guards a second real
// gap in the same family: 'approved' is in BlockingStatuses, so a booking
// whose deposit_deadline lapsed without payment - and nobody manually
// cancelled - blocks its slot forever, exactly like a stale hold. Found
// while auditing this fix for a second real issue: ExpireDeadlineBookings
// existed and was correct but, like ReleaseExpiredHolds before it, was
// never called from anywhere.
func TestGetAvailableSlots_SweepsExpiredDeadlineBookings(t *testing.T) {
	repo := &mockRepo{
		getStoreStore:               defaultStore(),
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          defaultBusinessHours(),
		getServiceSvc:               defaultService(),
	}
	svc := newTestService(repo)

	_, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)
	assert.True(t, repo.expireDeadlineBookingsCalled, "GetAvailableSlots must sweep deposit-deadline-lapsed approvals before reading blocked ranges")
}

// TestGetAvailableSlots_ExpireDeadlineBookingsFails_StillReturnsSlots - same
// best-effort guarantee as the holds sweep: a failure here must never fail
// the availability request itself.
func TestGetAvailableSlots_ExpireDeadlineBookingsFails_StillReturnsSlots(t *testing.T) {
	repo := &mockRepo{
		getStoreStore:               defaultStore(),
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          defaultBusinessHours(),
		getServiceSvc:               defaultService(),
		expireDeadlineBookingsErr:   errors.New("boom"),
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err, "a sweep failure must not fail the slots request")
	assert.NotEmpty(t, slots)
}

// TestGetAvailableSlots_EarlyBirdFlagged - slots before 09:00 cutoff are
// flagged as early bird with the correct fee.
func TestGetAvailableSlots_EarlyBirdFlagged(t *testing.T) {
	cutoff := "09:00:00"
	store := defaultStore()
	store.EarlyBirdCutoff = &cutoff
	store.EarlyBirdFee = decimal.NewFromFloat(15.50)

	bh := defaultBusinessHours()
	bh.OpenTime = "07:00:00"
	bh.CloseTime = "12:00:00"

	repo := &mockRepo{
		getStoreStore:               store,
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          bh,
		getServiceSvc:               defaultService(),
		getArtistBookingsBookings:   nil,
		getCrossStoreBookings:       nil,
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)
	require.NotEmpty(t, slots)

	// Business hours and the cutoff are wall-clock LOCAL times, and slot
	// StartTime is a UTC instant, so assert in the store's zone rather than
	// on the raw UTC clock.
	loc := storeLocation(store)

	// 07:00 local slot - before the 09:00 local cutoff - must be early bird
	firstSlot := slots[0]
	assert.Equal(t, "07:00:00", firstSlot.StartTime.In(loc).Format("15:04:05"))
	assert.True(t, firstSlot.IsEarlyBird, "07:00 slot must be early bird")
	assert.True(t, firstSlot.EarlyBirdFee.Equal(decimal.NewFromFloat(15.50)))

	// 09:00 local slot - at the cutoff - must NOT be early bird
	var standardSlot *TimeSlot
	for _, s := range slots {
		if s.StartTime.In(loc).Format("15:04:05") == "09:00:00" {
			standardSlot = s
			break
		}
	}
	require.NotNil(t, standardSlot, "must have a 09:00 slot")
	assert.False(t, standardSlot.IsEarlyBird, "09:00 slot must not be early bird")
}

// TestGetAvailableSlots_FullyBooked - artist has back-to-back confirmed bookings
// covering the entire open window. Expect: no available slots.
func TestGetAvailableSlots_FullyBooked(t *testing.T) {
	// Block the entire 09:00-18:00 window with one booking
	date := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	blockStart := beirutTime(2027, 3, 1, 9, 0)
	blockEnd := beirutTime(2027, 3, 1, 18, 0)

	repo := &mockRepo{
		getStoreStore:               defaultStore(),
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          defaultBusinessHours(),
		getServiceSvc:               defaultService(),
		getArtistBookingsBookings:   []*Booking{existingBooking(blockStart, blockEnd)},
		getCrossStoreBookings:       nil,
	}
	svc := newTestService(repo)

	req := validSlotsReq()
	req.Date = date.Format("2006-01-02")
	slots, err := svc.GetAvailableSlots(context.Background(), req)

	require.NoError(t, err)
	assert.Empty(t, slots, "no slots should be available when artist is fully booked")
}

// TestGetAvailableSlots_ExistingBookingBlocksSlot - artist has a 60-min booking
// at 10:00. The 10:00 and overlapping slots must be blocked.
func TestGetAvailableSlots_ExistingBookingBlocksSlot(t *testing.T) {
	blockStart := beirutTime(2027, 3, 1, 10, 0)
	blockEnd := beirutTime(2027, 3, 1, 11, 0)

	repo := &mockRepo{
		getStoreStore:               defaultStore(),
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          defaultBusinessHours(),
		getServiceSvc:               defaultService(),
		getArtistBookingsBookings:   []*Booking{existingBooking(blockStart, blockEnd)},
		getCrossStoreBookings:       nil,
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)
	require.NotEmpty(t, slots)

	// Confirm no slot overlaps with 10:00-11:00
	for _, s := range slots {
		overlapStart := s.StartTime.Before(blockEnd)
		overlapEnd := s.EndTime.After(blockStart)
		assert.False(t, overlapStart && overlapEnd,
			"slot %s-%s overlaps with blocked range",
			s.StartTime.Format("15:04:05"),
			s.EndTime.Format("15:04:05"),
		)
	}
}

// TestGetAvailableSlots_TravelBufferWeekday - artist has a cross-store booking
// on a weekday. 150-minute buffer must block slots before and after.
func TestGetAvailableSlots_TravelBufferWeekday(t *testing.T) {
	// Cross-store booking at Tripoli ends at 09:00 on a Monday (weekday)
	// 150-min weekday buffer → Beirut slots before 11:30 must be blocked
	crossStart := beirutTime(2027, 3, 1, 8, 0)
	crossEnd := beirutTime(2027, 3, 1, 9, 0)

	crossBooking := existingBooking(crossStart, crossEnd)

	repo := &mockRepo{
		getStoreStore:               defaultStore(), // weekday_buffer_min = 150
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          defaultBusinessHours(), // 09:00-18:00
		getServiceSvc:               defaultService(),
		getArtistBookingsBookings:   nil,
		getCrossStoreBookings:       []*Booking{crossBooking},
		getArtistStoreBufferBuf:     nil, // use store default
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)

	// No slot should start before 11:30 (09:00 + 150 min)
	for _, s := range slots {
		assert.False(t,
			s.StartTime.Before(beirutTime(2027, 3, 1, 11, 30)),
			"slot at %s violates weekday travel buffer",
			s.StartTime.Format("15:04:05"),
		)
	}
}

// TestGetAvailableSlots_TravelBufferWeekend - same scenario on a weekend.
// 90-minute buffer → slots before 10:30 must be blocked.
func TestGetAvailableSlots_TravelBufferWeekend(t *testing.T) {
	// Friday 2027-03-05
	crossEnd := beirutTime(2027, 3, 5, 9, 0)
	crossStart := beirutTime(2027, 3, 5, 8, 0)
	crossBooking := existingBooking(crossStart, crossEnd)

	repo := &mockRepo{
		getStoreStore:               defaultStore(), // weekend_buffer_min = 90
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          defaultBusinessHours(),
		getServiceSvc:               defaultService(),
		getArtistBookingsBookings:   nil,
		getCrossStoreBookings:       []*Booking{crossBooking},
		getArtistStoreBufferBuf:     nil,
	}
	svc := newTestService(repo)

	req := validSlotsReq()
	req.Date = "2027-03-05" // Friday

	slots, err := svc.GetAvailableSlots(context.Background(), req)

	require.NoError(t, err)

	// No slot should start before 10:30 (09:00 + 90 min)
	for _, s := range slots {
		assert.False(t,
			s.StartTime.Before(beirutTime(2027, 3, 5, 10, 30)),
			"slot at %s violates weekend travel buffer",
			s.StartTime.Format("15:04:05"),
		)
	}
}

// TestGetAvailableSlots_ExceptionCustomHours - store has exception with custom
// open/close hours (not closed, just different hours).
func TestGetAvailableSlots_ExceptionCustomHours(t *testing.T) {
	customOpen := "10:00:00"
	customClose := "14:00:00"

	repo := &mockRepo{
		getStoreStore: defaultStore(),
		getBusinessHoursExceptionEx: &BusinessHoursException{
			IsClosed:  false,
			OpenTime:  &customOpen,
			CloseTime: &customClose,
		},
		getBusinessHoursBH:    defaultBusinessHours(), // regular 09:00-18:00 - overridden
		getServiceSvc:         defaultService(),
		getCrossStoreBookings: nil,
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)
	require.NotEmpty(t, slots)

	// First slot must respect custom open time (wall-clock local)
	cLoc := storeLocation(defaultStore())
	assert.Equal(t, "10:00:00", slots[0].StartTime.In(cLoc).Format("15:04:05"))

	// Last slot must end at or before custom close time
	lastSlot := slots[len(slots)-1]
	assert.LessOrEqual(t, lastSlot.EndTime.In(cLoc).Format("15:04:05"), "14:00:00")
}

// TestGetAvailableSlots_SlotExceedsClosingTime - a candidate slot whose end time
// would exceed store closing must not be offered.
func TestGetAvailableSlots_SlotExceedsClosingTime(t *testing.T) {
	// 90-min service, store closes at 18:00
	// Last valid slot: 16:30 (ends 18:00)
	// 16:31 slot would end at 18:01 - must be excluded
	svc90 := defaultService()
	svc90.DurationMin = 90

	repo := &mockRepo{
		getStoreStore:               defaultStore(),
		getBusinessHoursExceptionEx: nil,
		getBusinessHoursBH:          defaultBusinessHours(), // 09:00-18:00
		getServiceSvc:               svc90,
		getCrossStoreBookings:       nil,
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)
	require.NotEmpty(t, slots)

	// No slot should end after 18:00
	for _, s := range slots {
		assert.False(t,
			s.EndTime.After(beirutTime(2027, 3, 1, 18, 0)),
			"slot ending at %s exceeds store closing time",
			s.EndTime.Format("15:04:05"),
		)
	}

	// Last slot must start at 16:30 local (18:00 close - 90 min)
	dLoc := storeLocation(defaultStore())
	lastSlot := slots[len(slots)-1]
	assert.Equal(t, "16:30:00", lastSlot.StartTime.In(dLoc).Format("15:04:05"))
}

// ── CancelBooking tests ───────────────────────────────────────────────────────

// TestCancelBooking_CustomerLateCancelNoRefund - customer cancels less than
// 24 hours before the appointment. Deposit is forfeited. Status: cancelled.
func TestCancelBooking_CustomerLateCancelNoRefund(t *testing.T) {
	customerID := uuid.New()
	artistID := uuid.New()

	// Appointment in 12 hours - within 24h cancellation window
	startTime := time.Now().UTC().Add(12 * time.Hour)

	booking := &Booking{
		ID:            uuid.New(),
		CustomerID:    customerID,
		ArtistID:      artistID,
		StartTime:     startTime,
		Status:        StatusConfirmed,
		DepositAmount: decimal.NewFromFloat(50.00),
	}

	repo := &mockRepo{
		getBookingByIDBooking: booking,
		cancelBookingErr:      nil,
	}
	svc := newTestService(repo)

	result, err := svc.CancelBooking(
		context.Background(),
		booking.ID,
		customerID,
		RoleCustomer,
		CancelBookingRequest{},
	)

	require.NoError(t, err)
	// Less than 24h → deposit forfeited → status cancelled, not refund_due
	assert.Equal(t, StatusCancelled, result.Status)
}

// TestCancelBooking_ArtistCancelAlwaysRefund - artist cancels any booking.
// Deposit must always be refunded regardless of timing.
func TestCancelBooking_ArtistCancelAlwaysRefund(t *testing.T) {
	artistID := uuid.New()
	customerID := uuid.New()

	// Appointment in 1 hour - normally no refund for customer
	// But artist cancelling always triggers refund
	startTime := time.Now().UTC().Add(1 * time.Hour)

	booking := &Booking{
		ID:            uuid.New(),
		CustomerID:    customerID,
		ArtistID:      artistID,
		StartTime:     startTime,
		Status:        StatusConfirmed,
		DepositAmount: decimal.NewFromFloat(50.00),
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		cancelBookingErr:            nil,
		getArtistIDByUserIDArtistID: artistID,
	}
	svc := newTestService(repo)

	result, err := svc.CancelBooking(
		context.Background(),
		booking.ID,
		artistID,
		RoleArtist,
		CancelBookingRequest{},
	)

	require.NoError(t, err)
	// Artist cancels → always refund_due
	assert.Equal(t, StatusRefundDue, result.Status)
}

// TestCancelBooking_TriggersWaitlistCheck guards the actual integration
// point this session's waitlist work depends on: cancelling a booking must
// check whether anyone is waiting for that exact
// (artist, store, service, date) combination - using the CANCELLED
// booking's own values, not some other booking's. Without this test, a
// typo swapping StoreID for ServiceID in the CancelBooking call site would
// compile fine and silently never notify anyone.
func TestCancelBooking_TriggersWaitlistCheck(t *testing.T) {
	artistID := uuid.New()
	storeID := uuid.New()
	serviceID := uuid.New()
	customerID := uuid.New()
	startTime := time.Now().UTC().Add(48 * time.Hour) // well outside the cancellation window

	booking := &Booking{
		ID:         uuid.New(),
		CustomerID: customerID,
		ArtistID:   artistID,
		StoreID:    storeID,
		ServiceID:  serviceID,
		StartTime:  startTime,
		Status:     StatusConfirmed,
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
	}
	svc := newTestService(repo)

	_, err := svc.CancelBooking(context.Background(), booking.ID, artistID, RoleArtist, CancelBookingRequest{})

	require.NoError(t, err)
	assert.True(t, repo.notifyNextWaitlistCalled, "cancelling a booking must check the waitlist for its exact slot group")
	assert.Equal(t, artistID, repo.notifyNextWaitlistArtistID)
	assert.Equal(t, storeID, repo.notifyNextWaitlistStoreID)
	assert.Equal(t, serviceID, repo.notifyNextWaitlistServiceID)
	assert.Equal(t, startTime.Year(), repo.notifyNextWaitlistDate.Year())
	assert.Equal(t, startTime.Month(), repo.notifyNextWaitlistDate.Month())
	assert.Equal(t, startTime.Day(), repo.notifyNextWaitlistDate.Day())
}

// TestCancelBooking_WaitlistCheckFails_StillSucceeds - a waitlist-check
// failure must never fail the cancellation that already succeeded, same
// best-effort principle as every notification tonight.
func TestCancelBooking_WaitlistCheckFails_StillSucceeds(t *testing.T) {
	artistID := uuid.New()
	booking := &Booking{
		ID:            uuid.New(),
		ArtistID:      artistID,
		StartTime:     time.Now().UTC().Add(48 * time.Hour),
		Status:        StatusConfirmed,
		DepositAmount: decimal.NewFromFloat(50.00),
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
		notifyNextWaitlistErr:       errors.New("boom"),
	}
	svc := newTestService(repo)

	result, err := svc.CancelBooking(context.Background(), booking.ID, artistID, RoleArtist, CancelBookingRequest{})

	require.NoError(t, err, "a waitlist-check failure must not fail the cancellation itself")
	assert.Equal(t, StatusRefundDue, result.Status)
}

// ── JoinWaitlist tests ───────────────────────────────────────────────────────

func TestJoinWaitlist_Success(t *testing.T) {
	entryID := uuid.New()
	repo := &mockRepo{createWaitlistEntryID: entryID}
	svc := newTestService(repo)

	result, err := svc.JoinWaitlist(context.Background(), JoinWaitlistRequest{
		ArtistID:      uuid.New().String(),
		StoreID:       uuid.New().String(),
		ServiceID:     uuid.New().String(),
		RequestedDate: "2026-08-15",
		Name:          "Sarah",
		Phone:         "70123456",
	})

	require.NoError(t, err)
	assert.Equal(t, entryID, result)
}

func TestJoinWaitlist_InvalidDate_BadRequest(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.JoinWaitlist(context.Background(), JoinWaitlistRequest{
		ArtistID:      uuid.New().String(),
		StoreID:       uuid.New().String(),
		ServiceID:     uuid.New().String(),
		RequestedDate: "15-08-2026", // wrong format
		Name:          "Sarah",
		Phone:         "70123456",
	})

	assert.Error(t, err)
}

func TestJoinWaitlist_InvalidArtistID_BadRequest(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.JoinWaitlist(context.Background(), JoinWaitlistRequest{
		ArtistID:      "not-a-uuid",
		StoreID:       uuid.New().String(),
		ServiceID:     uuid.New().String(),
		RequestedDate: "2026-08-15",
		Name:          "Sarah",
		Phone:         "70123456",
	})

	assert.Error(t, err)
}

func TestJoinWaitlist_MissingName_ValidationError(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.JoinWaitlist(context.Background(), JoinWaitlistRequest{
		ArtistID:      uuid.New().String(),
		StoreID:       uuid.New().String(),
		ServiceID:     uuid.New().String(),
		RequestedDate: "2026-08-15",
		Name:          "",
		Phone:         "70123456",
	})

	assert.Error(t, err)
}

// ── GetWaitlistByArtist tests ────────────────────────────────────────────────

func TestGetWaitlistByArtist_Success(t *testing.T) {
	artistID := uuid.New()
	userID := uuid.New()
	entries := []*WaitlistEntryResponse{
		{ID: uuid.New(), ServiceName: "Bridal Makeup", CustomerName: "Sarah", Status: WaitlistStatusWaiting},
	}
	repo := &mockRepo{
		getArtistIDByUserIDArtistID: artistID,
		getWaitlistByArtistEntries:  entries,
	}
	svc := newTestService(repo)

	result, err := svc.GetWaitlistByArtist(context.Background(), artistID, userID, RoleArtist)

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "Sarah", result[0].CustomerName)
}

func TestGetWaitlistByArtist_WrongArtist_Forbidden(t *testing.T) {
	requestedArtistID := uuid.New()
	actualArtistID := uuid.New() // the requester's real artist_id - different
	userID := uuid.New()

	repo := &mockRepo{getArtistIDByUserIDArtistID: actualArtistID}
	svc := newTestService(repo)

	_, err := svc.GetWaitlistByArtist(context.Background(), requestedArtistID, userID, RoleArtist)

	assert.Error(t, err, "an artist must not be able to view another artist's waitlist")
}

// ── Artist ownership resolution tests ───────────────────────────────────────
//
// bookings.artist_id references artists.id, not users.id - every artist-facing
// action must resolve the requester's JWT user_id through GetArtistIDByUserID
// before comparing. These tests guard against the class of bug where that
// resolution step is skipped and the raw user_id is compared directly against
// artists.id, which never matches and 403s a real artist on their own booking.

func TestApproveBooking_Success(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:        uuid.New(),
		ArtistID:  artistID,
		Status:    StatusPending,
		StartTime: time.Now().UTC().Add(48 * time.Hour),
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
		getServiceSvc:               defaultService(),
	}
	svc := newTestService(repo)

	result, err := svc.ApproveBooking(context.Background(), booking.ID, userID)

	require.NoError(t, err)
	assert.Equal(t, StatusApproved, result.Status)
}

func TestApproveBooking_WrongArtist_Forbidden(t *testing.T) {
	userID := uuid.New()
	bookingArtistID := uuid.New()
	someOtherArtistID := uuid.New()

	booking := &Booking{
		ID:        uuid.New(),
		ArtistID:  bookingArtistID,
		Status:    StatusPending,
		StartTime: time.Now().UTC().Add(48 * time.Hour),
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: someOtherArtistID,
	}
	svc := newTestService(repo)

	_, err := svc.ApproveBooking(context.Background(), booking.ID, userID)

	assert.Error(t, err)
}

// TestApproveBooking_StartTimeInPast_Conflict guards against approving a
// pending request whose appointment time has already come and gone - there's
// no background job that auto-expires stale pending bookings (see
// ExpireDeadlineBookings/ReleaseExpiredHolds, neither wired to a scheduler),
// so without this check a forgotten request could sit in 'pending' past its
// own start_time and still be approved, computing a deposit deadline that's
// also already in the past.
func TestApproveBooking_StartTimeInPast_Conflict(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:        uuid.New(),
		ArtistID:  artistID,
		Status:    StatusPending,
		StartTime: time.Now().UTC().Add(-72 * time.Hour),
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
		getServiceSvc:               defaultService(),
	}
	svc := newTestService(repo)

	_, err := svc.ApproveBooking(context.Background(), booking.ID, userID)

	require.Error(t, err, "a booking whose start_time has already passed must not be approvable")
}

// TestApproveBooking_StartTimeSecondsInFuture_Allowed is the boundary tick
// just before the guard triggers - a booking starting a few seconds from now
// is still legitimately approvable. Pairs with the _InPast test above to
// pin the exact boundary (StartTime.Before(time.Now())), not an approximate
// one, since off-by-one boundary bugs are exactly what this class of guard
// is prone to.
func TestApproveBooking_StartTimeSecondsInFuture_Allowed(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:        uuid.New(),
		ArtistID:  artistID,
		Status:    StatusPending,
		StartTime: time.Now().UTC().Add(5 * time.Second),
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
		getServiceSvc:               defaultService(),
	}
	svc := newTestService(repo)

	result, err := svc.ApproveBooking(context.Background(), booking.ID, userID)

	require.NoError(t, err, "a booking that hasn't started yet, even by a few seconds, must remain approvable")
	assert.Equal(t, StatusApproved, result.Status)
}

// TestApproveBooking_SqueezedDepositWindow_UsesGraceFallback covers a request
// that sat unapproved long enough that start_time minus the service's
// deposit_deadline_hours has already passed by approval time (e.g. a 24h
// deposit window on an appointment that's only 10h away). Storing that
// already-past deadline would leave the booking with no real way to ever
// receive its deposit in time, and would tell the customer "24 hours" over
// WhatsApp while the field had already expired. The fallback must land
// strictly between now and start_time.
func TestApproveBooking_SqueezedDepositWindow_UsesGraceFallback(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()
	now := time.Now().UTC()

	booking := &Booking{
		ID:        uuid.New(),
		ArtistID:  artistID,
		Status:    StatusPending,
		StartTime: now.Add(10 * time.Hour), // sooner than the service's 48h deposit window
	}

	svc24h := defaultService()
	svc24h.DepositDeadlineHours = 48

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
		getServiceSvc:               svc24h,
	}
	service := newTestService(repo)

	result, err := service.ApproveBooking(context.Background(), booking.ID, userID)

	require.NoError(t, err)
	require.NotNil(t, result.DepositDeadline)
	assert.True(t, result.DepositDeadline.After(now),
		"fallback deposit deadline must be in the future, not the already-past start_time-48h")
	assert.True(t, result.DepositDeadline.Before(booking.StartTime),
		"fallback deposit deadline must still land before the appointment itself")
	assert.Equal(t, *result.DepositDeadline, repo.approveBookingDepositDeadline,
		"the deadline returned to the caller must match what was actually persisted")
}

// TestConfirmDepositReceived_Success covers the single-action deposit flow:
// the artist confirms the moment she sees the OMT/Wish transfer land, and the
// booking moves straight from approved to confirmed in one call.
func TestConfirmDepositReceived_WithReference_PassedToRepo(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:       uuid.New(),
		ArtistID: artistID,
		Status:   StatusApproved,
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
	}
	svc := newTestService(repo)

	ref := "Whish Code #94821"
	result, err := svc.ConfirmDepositReceived(context.Background(), booking.ID, userID, &ref)

	require.NoError(t, err)
	require.NotNil(t, repo.confirmDepositReceivedReferenceCalled,
		"reference must be passed through to the repository, not silently dropped")
	assert.Equal(t, ref, *repo.confirmDepositReceivedReferenceCalled)
	require.NotNil(t, result.DepositReference)
	assert.Equal(t, ref, *result.DepositReference)
}

func TestConfirmDepositReceived_ReferenceTooLong_BadRequest(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:       uuid.New(),
		ArtistID: artistID,
		Status:   StatusApproved,
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
	}
	svc := newTestService(repo)

	tooLong := strings.Repeat("x", 256)
	_, err := svc.ConfirmDepositReceived(context.Background(), booking.ID, userID, &tooLong)

	assert.Error(t, err)
}

func TestConfirmDepositReceived_Success(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:       uuid.New(),
		ArtistID: artistID,
		Status:   StatusApproved,
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
	}
	svc := newTestService(repo)

	result, err := svc.ConfirmDepositReceived(context.Background(), booking.ID, userID, nil)

	require.NoError(t, err)
	assert.Equal(t, StatusConfirmed, result.Status)
	require.NotNil(t, result.DepositPaidAt)
}

// ── CompleteBooking tests ────────────────────────────────────────────────────
//
// Had zero test coverage before tonight's notification work - worth real
// coverage now that this method also generates the review token and
// enqueues a message, not just flips a status.

// TestCompleteBooking_Success_TransitionsToCompleted verifies the status
// transition itself. Note: BookingResponse (what this method returns)
// deliberately does NOT expose review_token - that field is scoped to the
// artist-facing EnrichedBookingResponse only (see model.go). The token's
// actual content is verified where it matters - inside the enqueued
// notification message - by TestCompleteBooking_EnqueuesReviewRequest below.
func TestCompleteBooking_Success_TransitionsToCompleted(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:       uuid.New(),
		ArtistID: artistID,
		Status:   StatusConfirmed,
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
		completeBookingToken:        "abc123token",
	}
	svc := newTestService(repo)

	result, err := svc.CompleteBooking(context.Background(), booking.ID, userID)

	require.NoError(t, err)
	assert.Equal(t, StatusCompleted, result.Status)
}

// TestCompleteBooking_EnqueuesReviewRequest guards the actual point of
// tonight's work: completing a booking must queue a WhatsApp message
// containing a real, correctly-formed review link - not just silently
// generate a token nobody ever receives.
func TestCompleteBooking_EnqueuesReviewRequest(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()
	customerID := uuid.New()

	booking := &Booking{
		ID:         uuid.New(),
		ArtistID:   artistID,
		CustomerID: customerID,
		Status:     StatusConfirmed,
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
		completeBookingToken:        "xyz789token",
		notificationContextCustomer: "Sarah",
		notificationContextService:  "Bridal Makeup",
	}
	svc := newTestService(repo)

	_, err := svc.CompleteBooking(context.Background(), booking.ID, userID)

	require.NoError(t, err)
	require.Len(t, repo.enqueuedNotifications, 1,
		"completing a booking must enqueue exactly one review-request notification")

	n := repo.enqueuedNotifications[0]
	assert.Equal(t, customerID, n.UserID, "notification must target the customer, not the artist")
	assert.Equal(t, "review_request", n.TemplateName)
	assert.Contains(t, n.Message, "Sarah")
	assert.Contains(t, n.Message, "Bridal Makeup")
	assert.Contains(t, n.Message, "/review/xyz789token",
		"message must contain a real, correctly-formed review link")
}

// TestCompleteBooking_NotificationContextFails_StillSucceeds - a failure
// resolving the customer/service names for the message must not fail the
// actual completion. The booking transition already succeeded; a
// notification-side lookup failing is a secondary concern, not a reason to
// tell the artist their "mark complete" tap failed.
func TestCompleteBooking_NotificationContextFails_StillSucceeds(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:       uuid.New(),
		ArtistID: artistID,
		Status:   StatusConfirmed,
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
		completeBookingToken:        "sometoken",
		notificationContextErr:      errors.New("boom"),
	}
	svc := newTestService(repo)

	result, err := svc.CompleteBooking(context.Background(), booking.ID, userID)

	require.NoError(t, err, "a notification-context failure must not fail the booking completion")
	assert.Equal(t, StatusCompleted, result.Status)
	assert.Empty(t, repo.enqueuedNotifications, "no notification should be queued if context resolution failed")
}

// TestCompleteBooking_FutureAppointment_Conflict guards against marking a
// booking "completed" - and firing off its review-request WhatsApp message -
// before the appointment has even happened.
func TestCompleteBooking_FutureAppointment_Conflict(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:        uuid.New(),
		ArtistID:  artistID,
		Status:    StatusConfirmed,
		StartTime: time.Now().UTC().Add(48 * time.Hour),
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
	}
	svc := newTestService(repo)

	_, err := svc.CompleteBooking(context.Background(), booking.ID, userID)

	require.Error(t, err, "a booking whose appointment hasn't started yet must not be completable")
}

// TestMarkNoShow_Success_PastAppointment covers the normal case: a confirmed
// booking whose appointment time has already passed can be marked no-show.
func TestMarkNoShow_Success_PastAppointment(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:        uuid.New(),
		ArtistID:  artistID,
		Status:    StatusConfirmed,
		StartTime: time.Now().UTC().Add(-2 * time.Hour),
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
	}
	svc := newTestService(repo)

	result, err := svc.MarkNoShow(context.Background(), booking.ID, userID)

	require.NoError(t, err)
	assert.Equal(t, StatusNoShow, result.Status)
}

// TestMarkNoShow_FutureAppointment_Conflict guards against marking a
// customer a "no-show" for an appointment that hasn't happened yet - found
// live while executing E2E-TEST-PLAN.md Suite 3.8: a booking 4 days out
// could be marked no-show with no server-side objection.
func TestMarkNoShow_FutureAppointment_Conflict(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:        uuid.New(),
		ArtistID:  artistID,
		Status:    StatusConfirmed,
		StartTime: time.Now().UTC().Add(48 * time.Hour),
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
	}
	svc := newTestService(repo)

	_, err := svc.MarkNoShow(context.Background(), booking.ID, userID)

	require.Error(t, err, "a booking whose appointment hasn't started yet must not be markable as no-show")
}

// TestMarkNoShow_StartTimeSecondsAgo_Allowed and
// TestCompleteBooking_StartTimeSecondsAgo_Allowed pin the exact boundary
// (StartTime.After(time.Now())) from the other side: an appointment that
// just started a few seconds ago is already actionable, not blocked.
func TestMarkNoShow_StartTimeSecondsAgo_Allowed(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:        uuid.New(),
		ArtistID:  artistID,
		Status:    StatusConfirmed,
		StartTime: time.Now().UTC().Add(-5 * time.Second),
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
	}
	svc := newTestService(repo)

	result, err := svc.MarkNoShow(context.Background(), booking.ID, userID)

	require.NoError(t, err, "an appointment that started moments ago must already be markable as no-show")
	assert.Equal(t, StatusNoShow, result.Status)
}

func TestCompleteBooking_StartTimeSecondsAgo_Allowed(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:        uuid.New(),
		ArtistID:  artistID,
		Status:    StatusConfirmed,
		StartTime: time.Now().UTC().Add(-5 * time.Second),
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
	}
	svc := newTestService(repo)

	result, err := svc.CompleteBooking(context.Background(), booking.ID, userID)

	require.NoError(t, err, "an appointment that started moments ago must already be completable")
	assert.Equal(t, StatusCompleted, result.Status)
}

func TestConfirmDepositReceived_WrongArtist_Forbidden(t *testing.T) {
	userID := uuid.New()
	bookingArtistID := uuid.New()
	someOtherArtistID := uuid.New()

	booking := &Booking{
		ID:       uuid.New(),
		ArtistID: bookingArtistID,
		Status:   StatusApproved,
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: someOtherArtistID,
	}
	svc := newTestService(repo)

	_, err := svc.ConfirmDepositReceived(context.Background(), booking.ID, userID, nil)

	assert.Error(t, err)
}

func TestConfirmDepositReceived_NotApproved_Conflict(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:       uuid.New(),
		ArtistID: artistID,
		Status:   StatusPending, // not yet approved
	}

	repo := &mockRepo{
		getBookingByIDBooking:       booking,
		getArtistIDByUserIDArtistID: artistID,
	}
	svc := newTestService(repo)

	_, err := svc.ConfirmDepositReceived(context.Background(), booking.ID, userID, nil)

	assert.Error(t, err)
}

// TestConfirmDepositReceived_NoArtistProfile_Forbidden - a requester with no
// artists row at all (ErrArtistNotFound from the resolver) must be rejected
// as a permissions failure, not surfaced as an internal error.
func TestConfirmDepositReceived_NoArtistProfile_Forbidden(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	booking := &Booking{
		ID:       uuid.New(),
		ArtistID: artistID,
		Status:   StatusApproved,
	}

	repo := &mockRepo{
		getBookingByIDBooking:  booking,
		getArtistIDByUserIDErr: ErrArtistNotFound,
	}
	svc := newTestService(repo)

	_, err := svc.ConfirmDepositReceived(context.Background(), booking.ID, userID, nil)

	assert.Error(t, err)
}

// ── Guest two-step booking tests ──────────────────────────────────────────────

// TestHoldGuestSlot_Success - guest taps a valid future slot.
// Expect: held booking returned with a held_until in the future.
func TestHoldGuestSlot_Success(t *testing.T) {
	repo := &mockRepo{
		getServiceSvc:    defaultService(),
		createBookingErr: nil,
	}
	svc := newTestService(repo)

	req := HoldGuestSlotRequest{
		ArtistID:  uuid.New().String(),
		StoreID:   uuid.New().String(),
		ServiceID: uuid.New().String(),
		StartTime: time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339),
	}

	res, err := svc.HoldGuestSlot(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.NotEqual(t, uuid.Nil, res.BookingID)
	assert.True(t, res.HeldUntil.After(time.Now().UTC()), "held_until must be in the future")
}

// TestHoldGuestSlot_PastTime - guest tries to hold a slot in the past.
// Expect: BOOKING_IN_PAST error, no booking created.
func TestHoldGuestSlot_PastTime(t *testing.T) {
	repo := &mockRepo{getServiceSvc: defaultService()}
	svc := newTestService(repo)

	req := HoldGuestSlotRequest{
		ArtistID:  uuid.New().String(),
		StoreID:   uuid.New().String(),
		ServiceID: uuid.New().String(),
		StartTime: time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339),
	}

	res, err := svc.HoldGuestSlot(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, res)
}

// TestHoldGuestSlot_SlotTaken - GIST constraint fires (slot already held).
// Expect: SLOT_UNAVAILABLE surfaced.
func TestHoldGuestSlot_SlotTaken(t *testing.T) {
	repo := &mockRepo{
		getServiceSvc:    defaultService(),
		createBookingErr: ErrSlotUnavailable,
	}
	svc := newTestService(repo)

	req := HoldGuestSlotRequest{
		ArtistID:  uuid.New().String(),
		StoreID:   uuid.New().String(),
		ServiceID: uuid.New().String(),
		StartTime: time.Now().UTC().Add(48 * time.Hour).Format(time.RFC3339),
	}

	res, err := svc.HoldGuestSlot(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, res)
}

// ── Early-bird surcharge tests ──────────────────────────────────────────────
//
// The picker badges a slot as early-bird using GetAvailableSlots' cutoff
// comparison; these tests guard that HoldGuestSlot applies the exact same
// surcharge to FinalPrice, so a badged slot never gets booked at the
// un-surcharged price.

// The cutoff is a wall-clock LOCAL time in the store's zone. 09:00 Beirut is
// 06:00Z while Lebanon is on DST (UTC+3), so a 05:45Z slot is before the
// cutoff and a 07:00Z slot is after it.
func TestIsEarlyBirdSlot_BeforeCutoff_True(t *testing.T) {
	store := defaultStore() // cutoff 09:00 Asia/Beirut
	startTime := time.Date(2026, 8, 4, 5, 45, 0, 0, time.UTC)

	assert.True(t, isEarlyBirdSlot(store, startTime))
}

func TestIsEarlyBirdSlot_AfterCutoff_False(t *testing.T) {
	store := defaultStore() // cutoff 09:00 Asia/Beirut
	startTime := time.Date(2026, 8, 4, 7, 0, 0, 0, time.UTC)

	assert.False(t, isEarlyBirdSlot(store, startTime))
}

// TestIsEarlyBirdSlot_SurvivesDSTTransition is the regression guard for the
// whole change. Lebanon leaves DST on 25 Oct 2026 (UTC+3 -> UTC+2), so the
// same 09:00 Beirut cutoff resolves to a different UTC instant either side of
// it. Pre-converting and storing a fixed UTC time would pass the August case
// and silently fail the November one, charging or sparing the surcharge an
// hour off for half the year.
func TestIsEarlyBirdSlot_SurvivesDSTTransition(t *testing.T) {
	store := defaultStore() // cutoff 09:00 Asia/Beirut

	// Summer (UTC+3): 09:00 Beirut == 06:00Z
	assert.True(t, isEarlyBirdSlot(store, time.Date(2026, 8, 4, 5, 59, 0, 0, time.UTC)),
		"05:59Z is 08:59 Beirut in summer - before the cutoff")
	assert.False(t, isEarlyBirdSlot(store, time.Date(2026, 8, 4, 6, 1, 0, 0, time.UTC)),
		"06:01Z is 09:01 Beirut in summer - after the cutoff")

	// Winter (UTC+2): 09:00 Beirut == 07:00Z
	assert.True(t, isEarlyBirdSlot(store, time.Date(2026, 11, 10, 6, 59, 0, 0, time.UTC)),
		"06:59Z is 08:59 Beirut in winter - before the cutoff")
	assert.False(t, isEarlyBirdSlot(store, time.Date(2026, 11, 10, 7, 1, 0, 0, time.UTC)),
		"07:01Z is 09:01 Beirut in winter - after the cutoff")
}

// A store in a zone with no DST must stay stable across the same dates.
func TestIsEarlyBirdSlot_NonDSTZone(t *testing.T) {
	store := defaultStore()
	store.Timezone = "Asia/Dubai" // UTC+4 year-round

	// 09:00 Dubai == 05:00Z in both August and November
	assert.True(t, isEarlyBirdSlot(store, time.Date(2026, 8, 4, 4, 59, 0, 0, time.UTC)))
	assert.False(t, isEarlyBirdSlot(store, time.Date(2026, 8, 4, 5, 1, 0, 0, time.UTC)))
	assert.True(t, isEarlyBirdSlot(store, time.Date(2026, 11, 10, 4, 59, 0, 0, time.UTC)))
	assert.False(t, isEarlyBirdSlot(store, time.Date(2026, 11, 10, 5, 1, 0, 0, time.UTC)))
}

// An empty or unloadable zone must not panic - it falls back to UTC.
func TestIsEarlyBirdSlot_EmptyTimezone_FallsBackToUTC(t *testing.T) {
	store := defaultStore()
	store.Timezone = ""

	// Falls back to UTC, so 09:00:00 is read as 09:00Z
	assert.True(t, isEarlyBirdSlot(store, time.Date(2026, 8, 4, 8, 0, 0, 0, time.UTC)))
	assert.False(t, isEarlyBirdSlot(store, time.Date(2026, 8, 4, 10, 0, 0, 0, time.UTC)))
}

func TestIsEarlyBirdSlot_NoCutoffConfigured_False(t *testing.T) {
	store := defaultStore()
	store.EarlyBirdCutoff = nil
	startTime := time.Date(2026, 8, 4, 7, 45, 0, 0, time.UTC)

	assert.False(t, isEarlyBirdSlot(store, startTime))
}

func TestIsEarlyBirdSlot_NilStore_False(t *testing.T) {
	startTime := time.Date(2026, 8, 4, 7, 45, 0, 0, time.UTC)

	assert.False(t, isEarlyBirdSlot(nil, startTime))
}

func TestHoldGuestSlot_EarlyBird_SurchargeApplied(t *testing.T) {
	repo := &mockRepo{
		getServiceSvc: defaultService(), // price 150.00
		getStoreStore: defaultStore(),   // cutoff 09:00:00, fee 10.00
	}
	svc := newTestService(repo)

	// 7:45 AM Beirut tomorrow - before the 09:00 local cutoff.
	// Built in the store's zone: the cutoff is wall-clock local, so a UTC
	// literal here would land on the wrong side of it.
	loc := storeLocation(defaultStore())
	tomorrow := time.Now().In(loc).Add(24 * time.Hour)
	startTime := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 7, 45, 0, 0, loc)

	req := HoldGuestSlotRequest{
		ArtistID:  uuid.New().String(),
		StoreID:   uuid.New().String(),
		ServiceID: uuid.New().String(),
		StartTime: startTime.Format(time.RFC3339),
	}

	res, err := svc.HoldGuestSlot(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, repo.createBookingCaptured)
	assert.True(t, repo.createBookingCaptured.FinalPrice.Equal(dec("160.00")),
		"expected 150 + 10 surcharge = 160, got %s", repo.createBookingCaptured.FinalPrice.String())
	assert.True(t, repo.createBookingCaptured.OriginalPrice.Equal(dec("150.00")))
}

func TestHoldGuestSlot_NotEarlyBird_NoSurcharge(t *testing.T) {
	repo := &mockRepo{
		getServiceSvc: defaultService(), // price 150.00
		getStoreStore: defaultStore(),   // cutoff 09:00:00, fee 10.00
	}
	svc := newTestService(repo)

	// 2:00 PM Beirut tomorrow - well after the 09:00 local cutoff
	loc := storeLocation(defaultStore())
	tomorrow := time.Now().In(loc).Add(24 * time.Hour)
	startTime := time.Date(tomorrow.Year(), tomorrow.Month(), tomorrow.Day(), 14, 0, 0, 0, loc)

	req := HoldGuestSlotRequest{
		ArtistID:  uuid.New().String(),
		StoreID:   uuid.New().String(),
		ServiceID: uuid.New().String(),
		StartTime: startTime.Format(time.RFC3339),
	}

	res, err := svc.HoldGuestSlot(context.Background(), req)

	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, repo.createBookingCaptured)
	assert.True(t, repo.createBookingCaptured.FinalPrice.Equal(dec("150.00")),
		"expected no surcharge outside the early-bird window, got %s", repo.createBookingCaptured.FinalPrice.String())
}

// TestSubmitGuestBooking_Success - submit a live held placeholder booking.
// Expect: transitions to pending.
func TestSubmitGuestBooking_Success(t *testing.T) {
	heldUntil := time.Now().UTC().Add(5 * time.Minute)
	booking := &Booking{
		ID:         uuid.New(),
		CustomerID: SystemGuestPlaceholderID,
		Status:     StatusHeld,
		HeldUntil:  &heldUntil,
	}

	repo := &mockRepo{
		getBookingByIDBooking:   booking,
		attachGuestAndSubmitErr: nil,
	}
	svc := newTestService(repo)

	req := SubmitGuestBookingRequest{Name: "Maya Test", Phone: "+96170123456"}
	res, err := svc.SubmitGuestBooking(context.Background(), booking.ID, req)

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, StatusPending, res.Status)
}

// TestSubmitGuestBooking_HoldExpired - held_until already passed.
// Expect: HOLD_EXPIRED error.
func TestSubmitGuestBooking_HoldExpired(t *testing.T) {
	heldUntil := time.Now().UTC().Add(-1 * time.Minute) // expired
	booking := &Booking{
		ID:         uuid.New(),
		CustomerID: SystemGuestPlaceholderID,
		Status:     StatusHeld,
		HeldUntil:  &heldUntil,
	}

	repo := &mockRepo{getBookingByIDBooking: booking}
	svc := newTestService(repo)

	req := SubmitGuestBookingRequest{Name: "Maya Test", Phone: "+96170123456"}
	res, err := svc.SubmitGuestBooking(context.Background(), booking.ID, req)

	require.Error(t, err)
	assert.Nil(t, res)
}

// TestSubmitGuestBooking_NotHeld - booking is not in held status anymore.
// Expect: HOLD_EXPIRED error.
func TestSubmitGuestBooking_NotHeld(t *testing.T) {
	heldUntil := time.Now().UTC().Add(5 * time.Minute)
	booking := &Booking{
		ID:         uuid.New(),
		CustomerID: SystemGuestPlaceholderID,
		Status:     StatusPending, // already submitted
		HeldUntil:  &heldUntil,
	}

	repo := &mockRepo{getBookingByIDBooking: booking}
	svc := newTestService(repo)

	req := SubmitGuestBookingRequest{Name: "Maya Test", Phone: "+96170123456"}
	res, err := svc.SubmitGuestBooking(context.Background(), booking.ID, req)

	require.Error(t, err)
	assert.Nil(t, res)
}

// ── Artist list status filter + calendar tests ────────────────────────────────

// TestListEnrichedBookingsByArtist_InvalidStatus - an unknown ?status= value is
// rejected with an error rather than silently returning an empty list.
func TestListEnrichedBookingsByArtist_InvalidStatus(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	// RoleAdmin bypasses the artist-ownership check - this test is about
	// status validation, not authz. Authz has its own tests below.
	_, _, err := svc.ListEnrichedBookingsByArtist(
		context.Background(), uuid.New(), uuid.New(), RoleAdmin, "not_a_real_status", time.Now().UTC(), 20,
	)

	require.Error(t, err)
}

// TestListEnrichedBookingsByArtist_ValidStatus - a known status passes validation
// and the repo result is returned.
func TestListEnrichedBookingsByArtist_ValidStatus(t *testing.T) {
	repo := &mockRepo{
		listEnrichedByArtistBookings: []*EnrichedBooking{
			{Booking: Booking{ID: uuid.New(), Status: StatusPending}, CustomerName: "Maya"},
		},
	}
	svc := newTestService(repo)

	res, hasMore, err := svc.ListEnrichedBookingsByArtist(
		context.Background(), uuid.New(), uuid.New(), RoleAdmin, StatusPending, time.Now().UTC(), 20,
	)

	require.NoError(t, err)
	assert.False(t, hasMore)
	require.Len(t, res, 1)
	assert.Equal(t, "Maya", res[0].CustomerName)
}

// TestListEnrichedBookingsByArtist_SweepsStalePendingForThisArtist guards the
// lazy-expiry fix for stale pending requests: a pending booking whose own
// start_time has passed can no longer be approved (ApproveBooking's own
// guard), but was otherwise left sitting in 'pending' forever with nothing
// to ever move it out of the artist's Pending tab. Every list read now
// sweeps this specific artist's stale pending rows first, the same
// self-healing shape already used for expired holds in GetAvailableSlots.
func TestListEnrichedBookingsByArtist_SweepsStalePendingForThisArtist(t *testing.T) {
	artistID := uuid.New()
	repo := &mockRepo{listEnrichedByArtistBookings: nil}
	svc := newTestService(repo)

	_, _, err := svc.ListEnrichedBookingsByArtist(
		context.Background(), artistID, uuid.New(), RoleAdmin, "", time.Now().UTC(), 20,
	)

	require.NoError(t, err)
	assert.True(t, repo.expireStalePendingCalled, "ListEnrichedBookingsByArtist must sweep stale pending bookings before reading")
	assert.Equal(t, artistID, repo.expireStalePendingArtistID, "the sweep must be scoped to the artist actually being read, not a stray/empty ID")
}

// TestListEnrichedBookingsByArtist_StalePendingSweepFails_StillReturnsList -
// the sweep is best-effort, mirroring GetAvailableSlots' equivalent
// guarantee: a failure there must never fail the list request itself.
func TestListEnrichedBookingsByArtist_StalePendingSweepFails_StillReturnsList(t *testing.T) {
	repo := &mockRepo{
		listEnrichedByArtistBookings: []*EnrichedBooking{
			{Booking: Booking{ID: uuid.New(), Status: StatusPending}, CustomerName: "Maya"},
		},
		expireStalePendingErr: errors.New("boom"),
	}
	svc := newTestService(repo)

	res, _, err := svc.ListEnrichedBookingsByArtist(
		context.Background(), uuid.New(), uuid.New(), RoleAdmin, "", time.Now().UTC(), 20,
	)

	require.NoError(t, err, "a sweep failure must not fail the list request")
	require.Len(t, res, 1)
}

// TestListEnrichedBookingsByArtist_EmptyStatusAllowed - empty status ("all") is
// valid and does not trigger the INVALID_STATUS path.
func TestListEnrichedBookingsByArtist_EmptyStatusAllowed(t *testing.T) {
	repo := &mockRepo{listEnrichedByArtistBookings: nil}
	svc := newTestService(repo)

	res, _, err := svc.ListEnrichedBookingsByArtist(
		context.Background(), uuid.New(), uuid.New(), RoleAdmin, "", time.Now().UTC(), 20,
	)

	require.NoError(t, err)
	assert.Empty(t, res)
}

// TestListEnrichedBookingsForWeek_ReturnsRows - the calendar method returns the
// repo's rows converted to responses.
func TestListEnrichedBookingsForWeek_ReturnsRows(t *testing.T) {
	repo := &mockRepo{
		listEnrichedForWeekBookings: []*EnrichedBooking{
			{Booking: Booking{ID: uuid.New(), Status: StatusConfirmed}, CustomerName: "Rania", ServiceName: "Bridal"},
		},
	}
	svc := newTestService(repo)

	res, err := svc.ListEnrichedBookingsForWeek(
		context.Background(), uuid.New(), uuid.New(), RoleAdmin, time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC),
	)

	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "Bridal", res[0].ServiceName)
	assert.Equal(t, StatusConfirmed, res[0].Status)
}

// TestListEnrichedBookingsForWeek_Empty - no bookings in the window yields an
// empty (non-nil) slice.
func TestListEnrichedBookingsForWeek_Empty(t *testing.T) {
	repo := &mockRepo{listEnrichedForWeekBookings: nil}
	svc := newTestService(repo)

	res, err := svc.ListEnrichedBookingsForWeek(
		context.Background(), uuid.New(), uuid.New(), RoleAdmin, time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC),
	)

	require.NoError(t, err)
	assert.Empty(t, res)
}

// ── Cross-tenant authorization tests ─────────────────────────────────────────
//
// These guard the exact vulnerability class found in the August 2026 security
// audit: endpoints taking artist_id from the URL enforced RequireRole("artist")
// but never verified the caller WAS that artist. Any registered artist could
// read any other artist's entire booking book - customer names, phone numbers,
// prices, deposits - by changing one UUID in the URL.
//
// Not one test existed asserting "artist A gets 403 on artist B's resource."
// Had one existed, the bug would never have shipped. These are those tests.

func TestListEnrichedBookingsByArtist_OtherArtist_Forbidden(t *testing.T) {
	victimArtistID := uuid.New()
	attackerUserID := uuid.New()
	attackerArtistID := uuid.New() // a DIFFERENT artist

	repo := &mockRepo{getArtistIDByUserIDArtistID: attackerArtistID}
	svc := newTestService(repo)

	_, _, err := svc.ListEnrichedBookingsByArtist(
		context.Background(), victimArtistID, attackerUserID, RoleArtist,
		"", time.Now().UTC(), 20,
	)

	require.Error(t, err, "an artist must never read another artist's bookings")
}

func TestListEnrichedBookingsByArtist_OwnBookings_Allowed(t *testing.T) {
	artistID := uuid.New()
	userID := uuid.New()

	repo := &mockRepo{
		getArtistIDByUserIDArtistID:  artistID, // resolves to the SAME artist
		listEnrichedByArtistBookings: []*EnrichedBooking{},
	}
	svc := newTestService(repo)

	_, _, err := svc.ListEnrichedBookingsByArtist(
		context.Background(), artistID, userID, RoleArtist,
		"", time.Now().UTC(), 20,
	)

	require.NoError(t, err, "an artist must still be able to read their own bookings")
}

func TestListEnrichedBookingsForWeek_OtherArtist_Forbidden(t *testing.T) {
	victimArtistID := uuid.New()
	attackerUserID := uuid.New()

	repo := &mockRepo{getArtistIDByUserIDArtistID: uuid.New()}
	svc := newTestService(repo)

	_, err := svc.ListEnrichedBookingsForWeek(
		context.Background(), victimArtistID, attackerUserID, RoleArtist,
		time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC),
	)

	require.Error(t, err, "an artist must never read another artist's calendar")
}

func TestGetWaitlistByArtist_Admin_Allowed(t *testing.T) {
	// An admin has no `artists` row at all. Before assertArtistAccess handled
	// this explicitly, resolving one would fail with ErrArtistNotFound and
	// lock admins out of the endpoints their role exists to reach.
	repo := &mockRepo{
		getArtistIDByUserIDErr:     ErrArtistNotFound,
		getWaitlistByArtistEntries: []*WaitlistEntryResponse{},
	}
	svc := newTestService(repo)

	_, err := svc.GetWaitlistByArtist(context.Background(), uuid.New(), uuid.New(), RoleAdmin)

	require.NoError(t, err, "admins must not be locked out by the artist-ownership check")
}

// TestGetEnrichedBookingByID_ArtistCanViewOwnBooking guards a bug where the
// authz check compared e.ArtistID (artists.id) against requesterID
// (users.id) - different ID spaces, so the condition could never be true and
// every artist was silently 403'd on their own booking. It failed CLOSED, so
// it was a functional bug rather than a security hole, which is exactly why
// it survived: nothing broke loudly.
func TestGetEnrichedBookingByID_ArtistCanViewOwnBooking(t *testing.T) {
	artistID := uuid.New()
	artistUserID := uuid.New() // deliberately DIFFERENT from artistID

	repo := &mockRepo{
		getEnrichedBookingByIDBooking: &EnrichedBooking{
			Booking: Booking{
				ID:         uuid.New(),
				ArtistID:   artistID,
				CustomerID: uuid.New(),
			},
		},
		getArtistIDByUserIDArtistID: artistID, // this user IS that artist
	}
	svc := newTestService(repo)

	_, err := svc.GetEnrichedBookingByID(context.Background(), uuid.New(), artistUserID, RoleArtist)

	require.NoError(t, err, "an artist must be able to view a booking on their own profile")
}

func TestGetEnrichedBookingByID_OtherArtist_Forbidden(t *testing.T) {
	repo := &mockRepo{
		getEnrichedBookingByIDBooking: &EnrichedBooking{
			Booking: Booking{
				ID:         uuid.New(),
				ArtistID:   uuid.New(), // victim's artist id
				CustomerID: uuid.New(),
			},
		},
		getArtistIDByUserIDArtistID: uuid.New(), // attacker is a different artist
	}
	svc := newTestService(repo)

	_, err := svc.GetEnrichedBookingByID(context.Background(), uuid.New(), uuid.New(), RoleArtist)

	require.Error(t, err, "an artist must not view a booking on another artist's profile")
}

// ── Subscription gate tests ───────────────────────────────────────────────────

// TestCreateBooking_SuspendedArtist_Blocked - artist whose subscription has
// lapsed past the grace/past_due window must not receive new bookings.
func TestCreateBooking_SuspendedArtist_Blocked(t *testing.T) {
	svc := NewService(&mockRepo{}, suspendedSubReader{})

	req := CreateBookingRequest{
		ArtistID:  uuid.New().String(),
		StoreID:   uuid.New().String(),
		ServiceID: uuid.New().String(),
		StartTime: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
		Channel:   "customer_pwa",
	}
	_, err := svc.CreateBooking(context.Background(), req, uuid.New())

	require.Error(t, err, "suspended artist must not receive new bookings")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "ARTIST_NOT_ACCEPTING_BOOKINGS", appErr.Code)
}

// TestHoldGuestSlot_SuspendedArtist_Blocked - same gate on the guest path.
func TestHoldGuestSlot_SuspendedArtist_Blocked(t *testing.T) {
	svc := NewService(&mockRepo{}, suspendedSubReader{})

	req := HoldGuestSlotRequest{
		ArtistID:  uuid.New().String(),
		StoreID:   uuid.New().String(),
		ServiceID: uuid.New().String(),
		StartTime: time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339),
	}
	_, err := svc.HoldGuestSlot(context.Background(), req)

	require.Error(t, err, "suspended artist must not receive new guest holds")
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "ARTIST_NOT_ACCEPTING_BOOKINGS", appErr.Code)
}

// ── Confirmation symmetry and refund closure ─────────────────────────────────
//
// Both blocks below cover defects found by enumerating the booking state
// machine rather than by a failing test - see
// B-Edge-Booking-State-Machine-Matrix-v1.md sections 5.1 and 5.2.

func strPtrB(v string) *string { return &v }

func confirmableBooking(status string, start time.Time, artistID uuid.UUID) *Booking {
	tok := strings.Repeat("c", 64)
	return &Booking{
		ID: uuid.New(), ArtistID: artistID, CustomerID: uuid.New(),
		Status: status, StartTime: start, CalendarToken: &tok,
	}
}

func confirmedNotifications(m *mockRepo) []enqueuedNotification {
	var out []enqueuedNotification
	for _, n := range m.enqueuedNotifications {
		if n.TemplateName == "booking_confirmed" {
			out = append(out, n)
		}
	}
	return out
}

// THE defect. Both routes reach `confirmed`, so both must tell the customer.
// Before this, ConfirmDeposit announced nothing, which meant whether a
// customer learned their booking was confirmed - and whether they got the
// calendar link, which rides on the same message - depended on which button
// the artist happened to press.
func TestConfirmDeposit_AnnouncesToTheCustomer(t *testing.T) {
	artistID := uuid.New()
	userID := uuid.New()
	repo := &mockRepo{
		getBookingByIDBooking:       confirmableBooking(StatusDepositPaid, time.Now().Add(72*time.Hour), artistID),
		getArtistIDByUserIDArtistID: artistID,
		notificationContextCustomer: "Layla",
		notificationContextService:  "Bridal makeup",
	}
	svc := newTestService(repo)

	_, err := svc.ConfirmDeposit(context.Background(), repo.getBookingByIDBooking.ID, userID)

	require.NoError(t, err)
	sent := confirmedNotifications(repo)
	require.Len(t, sent, 1, "the two-step route must not confirm silently")
	assert.Contains(t, sent[0].Message, "all confirmed")
}

// The two routes must stay indistinguishable to the customer. This is the
// property that was missing, so it is asserted directly rather than left
// implied by the two tests above and below passing separately.
func TestBothConfirmationRoutes_SendTheSameAnnouncement(t *testing.T) {
	artistID, userID := uuid.New(), uuid.New()
	start := time.Now().Add(72 * time.Hour)

	newRepo := func(status string) *mockRepo {
		return &mockRepo{
			getBookingByIDBooking:       confirmableBooking(status, start, artistID),
			getArtistIDByUserIDArtistID: artistID,
			notificationContextCustomer: "Layla",
			notificationContextService:  "Bridal makeup",
		}
	}

	oneStep := newRepo(StatusApproved)
	svcOne := newTestService(oneStep)
	_, err := svcOne.ConfirmDepositReceived(context.Background(), oneStep.getBookingByIDBooking.ID, userID, nil)
	require.NoError(t, err)

	twoStep := newRepo(StatusDepositPaid)
	svcTwo := newTestService(twoStep)
	_, err = svcTwo.ConfirmDeposit(context.Background(), twoStep.getBookingByIDBooking.ID, userID)
	require.NoError(t, err)

	a, b := confirmedNotifications(oneStep), confirmedNotifications(twoStep)
	require.Len(t, a, 1)
	require.Len(t, b, 1)
	assert.Equal(t, a[0].Message, b[0].Message,
		"a customer must not be able to tell which button the artist pressed")
}

// The calendar link rides on this message, so it must reach both routes too.
func TestConfirmDeposit_IncludesTheCalendarLink(t *testing.T) {
	artistID, userID := uuid.New(), uuid.New()
	repo := &mockRepo{
		getBookingByIDBooking:       confirmableBooking(StatusDepositPaid, time.Now().Add(72*time.Hour), artistID),
		getArtistIDByUserIDArtistID: artistID,
		notificationContextCustomer: "Layla",
		notificationContextService:  "Bridal makeup",
	}
	svc := newTestService(repo)

	_, err := svc.ConfirmDeposit(context.Background(), repo.getBookingByIDBooking.ID, userID)

	require.NoError(t, err)
	assert.Contains(t, confirmedNotifications(repo)[0].Message, "/c/"+strings.Repeat("c", 64))
}

// Decision 4.1: confirming a past appointment is legitimate bookkeeping - an
// artist reconciling a transfer that landed late - but "See you then!" about
// last Tuesday, with a calendar link for a date that has gone, is not.
// ApproveBooking refuses the same nonsense one step earlier.
func TestConfirmation_PastAppointment_ConfirmsSilently(t *testing.T) {
	artistID, userID := uuid.New(), uuid.New()
	past := time.Now().Add(-21 * 24 * time.Hour)

	for name, run := range map[string]func(*Service, uuid.UUID) error{
		"one-step": func(s *Service, id uuid.UUID) error {
			_, err := s.ConfirmDepositReceived(context.Background(), id, userID, nil)
			return err
		},
		"two-step": func(s *Service, id uuid.UUID) error {
			_, err := s.ConfirmDeposit(context.Background(), id, userID)
			return err
		},
	} {
		status := StatusApproved
		if name == "two-step" {
			status = StatusDepositPaid
		}
		repo := &mockRepo{
			getBookingByIDBooking:       confirmableBooking(status, past, artistID),
			getArtistIDByUserIDArtistID: artistID,
			notificationContextCustomer: "Layla",
			notificationContextService:  "Bridal makeup",
		}
		svc := newTestService(repo)

		require.NoError(t, run(svc, repo.getBookingByIDBooking.ID), "%s: the bookkeeping must still succeed", name)
		assert.Empty(t, confirmedNotifications(repo),
			"%s: must not message a customer about an appointment that already happened", name)
	}
}

// Defect 5.2: refund_due used to be terminal. Nothing transitioned out of it,
// so the notification centre's refund_due alert was unresolvable and the
// booking sat owing money forever.
func TestMarkRefunded_ClosesTheRefundLoop(t *testing.T) {
	artistID, userID := uuid.New(), uuid.New()
	b := confirmableBooking(StatusRefundDue, time.Now().Add(-48*time.Hour), artistID)
	repo := &mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}
	svc := newTestService(repo)

	ref := "Whish #94821"
	got, err := svc.MarkRefunded(context.Background(), b.ID, userID, &ref)

	require.NoError(t, err)
	assert.Equal(t, StatusRefunded, got.Status)
	require.NotNil(t, repo.markRefundedReference)
	assert.Equal(t, ref, *repo.markRefundedReference, "the artist's own reconciliation note")
}

// Only a booking that actually owes a refund can be closed. Otherwise this
// becomes a way to move arbitrary bookings into a terminal state.
func TestMarkRefunded_WrongStatus_Conflict(t *testing.T) {
	artistID, userID := uuid.New(), uuid.New()
	for _, status := range []string{
		StatusConfirmed, StatusCancelled, StatusCompleted, StatusRefunded, StatusPending,
	} {
		b := confirmableBooking(status, time.Now().Add(-48*time.Hour), artistID)
		repo := &mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}
		svc := newTestService(repo)

		_, err := svc.MarkRefunded(context.Background(), b.ID, userID, nil)

		require.Error(t, err, "status %q", status)
		var appErr *apperror.AppError
		require.ErrorAs(t, err, &appErr)
		assert.Equal(t, "BOOKING_NOT_REFUND_DUE", appErr.Code, "status %q", status)
	}
}

func TestMarkRefunded_WrongArtist_Forbidden(t *testing.T) {
	b := confirmableBooking(StatusRefundDue, time.Now().Add(-48*time.Hour), uuid.New())
	repo := &mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: uuid.New()}
	svc := newTestService(repo)

	_, err := svc.MarkRefunded(context.Background(), b.ID, uuid.New(), nil)

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 403, appErr.HTTPStatus)
}

// The customer was already told about the cancellation. A second message
// announcing an out-of-band bank transfer they either have or have not
// received would raise more questions than it answers.
func TestMarkRefunded_DoesNotMessageTheCustomer(t *testing.T) {
	artistID, userID := uuid.New(), uuid.New()
	b := confirmableBooking(StatusRefundDue, time.Now().Add(-48*time.Hour), artistID)
	repo := &mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}
	svc := newTestService(repo)

	_, err := svc.MarkRefunded(context.Background(), b.ID, userID, nil)

	require.NoError(t, err)
	assert.Empty(t, repo.enqueuedNotifications)
}

// ── Cancel after the appointment has started (matrix §4.4) ───────────────────

// Past the start time the honest outcomes are `completed` or `no_show`.
// Recording an appointment that happened as "cancelled" also misreports
// earnings. Complete and no-show already guard the same boundary from the
// other side; cancel was the only one of the three that did not.
func TestCancelBooking_ConfirmedAndStarted_Rejected(t *testing.T) {
	artistID, userID := uuid.New(), uuid.New()
	b := confirmableBooking(StatusConfirmed, time.Now().Add(-30*time.Minute), artistID)
	b.CustomerID = userID
	repo := &mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}
	svc := newTestService(repo)

	_, err := svc.CancelBooking(context.Background(), b.ID, userID, "customer", CancelBookingRequest{Reason: strPtrB("changed my mind")})

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "BOOKING_ALREADY_STARTED", appErr.Code)
}

func TestCancelBooking_ConfirmedAndUpcoming_Allowed(t *testing.T) {
	artistID, userID := uuid.New(), uuid.New()
	b := confirmableBooking(StatusConfirmed, time.Now().Add(48*time.Hour), artistID)
	b.CustomerID = userID
	repo := &mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}
	svc := newTestService(repo)

	_, err := svc.CancelBooking(context.Background(), b.ID, userID, "customer", CancelBookingRequest{Reason: strPtrB("changed my mind")})

	require.NoError(t, err)
}

// The guard is scoped to `confirmed` deliberately. A blanket "cannot cancel
// after the start time" would strand `pending` and `deposit_paid` bookings
// permanently: neither has an expiry sweep (only `held` and `approved` do),
// `pending` cannot be approved once its time passes, and cancel is their only
// disposal. This is the test that stops someone widening the guard later
// without noticing it creates a dead end.
func TestCancelBooking_UnconfirmedAndStarted_StillCancellable(t *testing.T) {
	for _, status := range []string{StatusPending, StatusApproved, StatusDepositPaid, StatusHeld} {
		artistID, userID := uuid.New(), uuid.New()
		b := confirmableBooking(status, time.Now().Add(-30*time.Minute), artistID)
		b.CustomerID = userID
		repo := &mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}
		svc := newTestService(repo)

		_, err := svc.CancelBooking(context.Background(), b.ID, userID, "customer", CancelBookingRequest{Reason: strPtrB("no longer needed")})

		require.NoError(t, err,
			"%s has no expiry sweep - blocking cancel here would strand it forever", status)
	}
}
