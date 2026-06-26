// Package booking contains unit tests for the booking service layer.
// These tests use mock repositories — no database required.
package booking

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
type mockRepo struct {
	getStoreStore                  *Store
	getStoreErr                    error
	getBusinessHoursBH             *BusinessHours
	getBusinessHoursErr            error
	getBusinessHoursExceptionEx    *BusinessHoursException
	getBusinessHoursExceptionErr   error
	getServiceSvc                  *SalonService
	getServiceErr                  error
	getArtistBookingsBookings      []*Booking
	getArtistBookingsErr           error
	getCrossStoreBookings          []*Booking
	getCrossStoreErr               error
	getArtistStoreBufferBuf        *ArtistStoreBuffer
	getArtistStoreBufferErr        error
	createBookingErr               error
	getBookingByIDBooking          *Booking
	getBookingByIDErr              error
	getBookingsByArtistBookings    []*Booking
	getBookingsByArtistErr         error
	getBookingsByCustomerBookings  []*Booking
	getBookingsByCustomerErr       error
	getBookingsBySalonBookings     []*Booking
	getBookingsBySalonErr          error
	updateBookingStatusErr         error
	attachGuestAndSubmitErr        error
	getEnrichedBookingByIDBooking  *EnrichedBooking
	getEnrichedBookingByIDErr      error
	listEnrichedByArtistBookings   []*EnrichedBooking
	listEnrichedByArtistErr        error
	listEnrichedForWeekBookings    []*EnrichedBooking
	listEnrichedForWeekErr         error
	listEnrichedByCustomerBookings []*EnrichedBooking
	listEnrichedByCustomerErr      error
	approveBookingErr              error
	confirmDepositErr              error
	cancelBookingErr               error
	completeBookingErr             error
	markNoShowErr                  error
	releaseExpiredHoldsCount       int64
	releaseExpiredHoldsErr         error
	expireDeadlineBookingsCount    int64
	expireDeadlineBookingsErr      error
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
func (m *mockRepo) AttachGuestAndSubmit(_ context.Context, _, _ uuid.UUID, _ *string) error {
	return m.attachGuestAndSubmitErr
}
func (m *mockRepo) GetEnrichedBookingByID(_ context.Context, _ uuid.UUID) (*EnrichedBooking, error) {
	return m.getEnrichedBookingByIDBooking, m.getEnrichedBookingByIDErr
}
func (m *mockRepo) ListEnrichedBookingsByArtist(_ context.Context, _ uuid.UUID, _ string, _ time.Time, _ int) ([]*EnrichedBooking, error) {
	return m.listEnrichedByArtistBookings, m.listEnrichedByArtistErr
}
func (m *mockRepo) ListEnrichedBookingsForWeek(_ context.Context, _ uuid.UUID, _ time.Time) ([]*EnrichedBooking, error) {
	return m.listEnrichedForWeekBookings, m.listEnrichedForWeekErr
}
func (m *mockRepo) ListEnrichedBookingsByCustomer(_ context.Context, _ uuid.UUID, _ time.Time, _ int) ([]*EnrichedBooking, error) {
	return m.listEnrichedByCustomerBookings, m.listEnrichedByCustomerErr
}
func (m *mockRepo) ApproveBooking(_ context.Context, _ uuid.UUID, _ time.Time) error {
	return m.approveBookingErr
}
func (m *mockRepo) ConfirmDeposit(_ context.Context, _ uuid.UUID) error {
	return m.confirmDepositErr
}
func (m *mockRepo) CancelBooking(_ context.Context, _ uuid.UUID, _ string, _ bool) error {
	return m.cancelBookingErr
}
func (m *mockRepo) CompleteBooking(_ context.Context, _ uuid.UUID) error {
	return m.completeBookingErr
}
func (m *mockRepo) MarkNoShow(_ context.Context, _ uuid.UUID) error {
	return m.markNoShowErr
}
func (m *mockRepo) ReleaseExpiredHolds(_ context.Context) (int64, error) {
	return m.releaseExpiredHoldsCount, m.releaseExpiredHoldsErr
}
func (m *mockRepo) ExpireDeadlineBookings(_ context.Context) (int64, error) {
	return m.expireDeadlineBookingsCount, m.expireDeadlineBookingsErr
}

// ── Test helpers ──────────────────────────────────────────────────────────────

func newTestService(repo Repository) *Service {
	return NewService(repo)
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
	}
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

// TestGetAvailableSlots_StoreClosed — store has is_open = false for this day.
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

// TestGetAvailableSlots_HolidayClosed — exception exists with is_closed = true.
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

// TestGetAvailableSlots_InvalidDate — date is not in YYYY-MM-DD format.
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

// TestGetAvailableSlots_InvalidUUID — artist_id is not a valid UUID.
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

// TestGetAvailableSlots_ReturnsSlots — happy path, no bookings, full day open.
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
	// First slot must start at store open time
	assert.Equal(t, "09:00:00", slots[0].StartTime.Format("15:04:05"))
	// Last slot must end at or before store close time (18:00)
	lastSlot := slots[len(slots)-1]
	assert.LessOrEqual(t, lastSlot.EndTime.Format("15:04:05"), "18:00:00")
}

// TestGetAvailableSlots_EarlyBirdFlagged — slots before 09:00 cutoff are
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

	// 07:00 slot — before cutoff — must be early bird
	firstSlot := slots[0]
	assert.Equal(t, "07:00:00", firstSlot.StartTime.Format("15:04:05"))
	assert.True(t, firstSlot.IsEarlyBird, "07:00 slot must be early bird")
	assert.True(t, firstSlot.EarlyBirdFee.Equal(decimal.NewFromFloat(15.50)))

	// 09:00 slot — at cutoff — must NOT be early bird
	var standardSlot *TimeSlot
	for _, s := range slots {
		if s.StartTime.Format("15:04:05") == "09:00:00" {
			standardSlot = s
			break
		}
	}
	require.NotNil(t, standardSlot, "must have a 09:00 slot")
	assert.False(t, standardSlot.IsEarlyBird, "09:00 slot must not be early bird")
}

// TestGetAvailableSlots_FullyBooked — artist has back-to-back confirmed bookings
// covering the entire open window. Expect: no available slots.
func TestGetAvailableSlots_FullyBooked(t *testing.T) {
	// Block the entire 09:00-18:00 window with one booking
	date := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	blockStart := time.Date(2027, 3, 1, 9, 0, 0, 0, time.UTC)
	blockEnd := time.Date(2027, 3, 1, 18, 0, 0, 0, time.UTC)

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

// TestGetAvailableSlots_ExistingBookingBlocksSlot — artist has a 60-min booking
// at 10:00. The 10:00 and overlapping slots must be blocked.
func TestGetAvailableSlots_ExistingBookingBlocksSlot(t *testing.T) {
	blockStart := time.Date(2027, 3, 1, 10, 0, 0, 0, time.UTC)
	blockEnd := time.Date(2027, 3, 1, 11, 0, 0, 0, time.UTC)

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

// TestGetAvailableSlots_TravelBufferWeekday — artist has a cross-store booking
// on a weekday. 150-minute buffer must block slots before and after.
func TestGetAvailableSlots_TravelBufferWeekday(t *testing.T) {
	// Cross-store booking at Tripoli ends at 09:00 on a Monday (weekday)
	// 150-min weekday buffer → Beirut slots before 11:30 must be blocked
	crossStart := time.Date(2027, 3, 1, 8, 0, 0, 0, time.UTC)
	crossEnd := time.Date(2027, 3, 1, 9, 0, 0, 0, time.UTC)

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
			s.StartTime.Before(time.Date(2027, 3, 1, 11, 30, 0, 0, time.UTC)),
			"slot at %s violates weekday travel buffer",
			s.StartTime.Format("15:04:05"),
		)
	}
}

// TestGetAvailableSlots_TravelBufferWeekend — same scenario on a weekend.
// 90-minute buffer → slots before 10:30 must be blocked.
func TestGetAvailableSlots_TravelBufferWeekend(t *testing.T) {
	// Friday 2027-03-05
	crossEnd := time.Date(2027, 3, 5, 9, 0, 0, 0, time.UTC)
	crossStart := time.Date(2027, 3, 5, 8, 0, 0, 0, time.UTC)
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
			s.StartTime.Before(time.Date(2027, 3, 5, 10, 30, 0, 0, time.UTC)),
			"slot at %s violates weekend travel buffer",
			s.StartTime.Format("15:04:05"),
		)
	}
}

// TestGetAvailableSlots_ExceptionCustomHours — store has exception with custom
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
		getBusinessHoursBH:    defaultBusinessHours(), // regular 09:00-18:00 — overridden
		getServiceSvc:         defaultService(),
		getCrossStoreBookings: nil,
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), validSlotsReq())

	require.NoError(t, err)
	require.NotEmpty(t, slots)

	// First slot must respect custom open time
	assert.Equal(t, "10:00:00", slots[0].StartTime.Format("15:04:05"))

	// Last slot must end at or before custom close time
	lastSlot := slots[len(slots)-1]
	assert.LessOrEqual(t, lastSlot.EndTime.Format("15:04:05"), "14:00:00")
}

// TestGetAvailableSlots_SlotExceedsClosingTime — a candidate slot whose end time
// would exceed store closing must not be offered.
func TestGetAvailableSlots_SlotExceedsClosingTime(t *testing.T) {
	// 90-min service, store closes at 18:00
	// Last valid slot: 16:30 (ends 18:00)
	// 16:31 slot would end at 18:01 — must be excluded
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
			s.EndTime.After(time.Date(2027, 3, 1, 18, 0, 0, 0, time.UTC)),
			"slot ending at %s exceeds store closing time",
			s.EndTime.Format("15:04:05"),
		)
	}

	// Last slot must start at 16:30 (18:00 - 90 min)
	lastSlot := slots[len(slots)-1]
	assert.Equal(t, "16:30:00", lastSlot.StartTime.Format("15:04:05"))
}

// ── CancelBooking tests ───────────────────────────────────────────────────────

// TestCancelBooking_CustomerLateCancelNoRefund — customer cancels less than
// 24 hours before the appointment. Deposit is forfeited. Status: cancelled.
func TestCancelBooking_CustomerLateCancelNoRefund(t *testing.T) {
	customerID := uuid.New()
	artistID := uuid.New()

	// Appointment in 12 hours — within 24h cancellation window
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

// TestCancelBooking_ArtistCancelAlwaysRefund — artist cancels any booking.
// Deposit must always be refunded regardless of timing.
func TestCancelBooking_ArtistCancelAlwaysRefund(t *testing.T) {
	artistID := uuid.New()
	customerID := uuid.New()

	// Appointment in 1 hour — normally no refund for customer
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
		getBookingByIDBooking: booking,
		cancelBookingErr:      nil,
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

// ── Guest two-step booking tests ──────────────────────────────────────────────

// TestHoldGuestSlot_Success — guest taps a valid future slot.
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

// TestHoldGuestSlot_PastTime — guest tries to hold a slot in the past.
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

// TestHoldGuestSlot_SlotTaken — GIST constraint fires (slot already held).
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

// TestSubmitGuestBooking_Success — submit a live held placeholder booking.
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

// TestSubmitGuestBooking_HoldExpired — held_until already passed.
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

// TestSubmitGuestBooking_NotHeld — booking is not in held status anymore.
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

// TestListEnrichedBookingsByArtist_InvalidStatus — an unknown ?status= value is
// rejected with an error rather than silently returning an empty list.
func TestListEnrichedBookingsByArtist_InvalidStatus(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, _, err := svc.ListEnrichedBookingsByArtist(
		context.Background(), uuid.New(), "not_a_real_status", time.Now().UTC(), 20,
	)

	require.Error(t, err)
}

// TestListEnrichedBookingsByArtist_ValidStatus — a known status passes validation
// and the repo result is returned.
func TestListEnrichedBookingsByArtist_ValidStatus(t *testing.T) {
	repo := &mockRepo{
		listEnrichedByArtistBookings: []*EnrichedBooking{
			{Booking: Booking{ID: uuid.New(), Status: StatusPending}, CustomerName: "Maya"},
		},
	}
	svc := newTestService(repo)

	res, hasMore, err := svc.ListEnrichedBookingsByArtist(
		context.Background(), uuid.New(), StatusPending, time.Now().UTC(), 20,
	)

	require.NoError(t, err)
	assert.False(t, hasMore)
	require.Len(t, res, 1)
	assert.Equal(t, "Maya", res[0].CustomerName)
}

// TestListEnrichedBookingsByArtist_EmptyStatusAllowed — empty status ("all") is
// valid and does not trigger the INVALID_STATUS path.
func TestListEnrichedBookingsByArtist_EmptyStatusAllowed(t *testing.T) {
	repo := &mockRepo{listEnrichedByArtistBookings: nil}
	svc := newTestService(repo)

	res, _, err := svc.ListEnrichedBookingsByArtist(
		context.Background(), uuid.New(), "", time.Now().UTC(), 20,
	)

	require.NoError(t, err)
	assert.Empty(t, res)
}

// TestListEnrichedBookingsForWeek_ReturnsRows — the calendar method returns the
// repo's rows converted to responses.
func TestListEnrichedBookingsForWeek_ReturnsRows(t *testing.T) {
	repo := &mockRepo{
		listEnrichedForWeekBookings: []*EnrichedBooking{
			{Booking: Booking{ID: uuid.New(), Status: StatusConfirmed}, CustomerName: "Rania", ServiceName: "Bridal"},
		},
	}
	svc := newTestService(repo)

	res, err := svc.ListEnrichedBookingsForWeek(
		context.Background(), uuid.New(), time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC),
	)

	require.NoError(t, err)
	require.Len(t, res, 1)
	assert.Equal(t, "Bridal", res[0].ServiceName)
	assert.Equal(t, StatusConfirmed, res[0].Status)
}

// TestListEnrichedBookingsForWeek_Empty — no bookings in the window yields an
// empty (non-nil) slice.
func TestListEnrichedBookingsForWeek_Empty(t *testing.T) {
	repo := &mockRepo{listEnrichedForWeekBookings: nil}
	svc := newTestService(repo)

	res, err := svc.ListEnrichedBookingsForWeek(
		context.Background(), uuid.New(), time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC),
	)

	require.NoError(t, err)
	assert.Empty(t, res)
}
