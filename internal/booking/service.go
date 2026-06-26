// Package booking implements the booking domain for B-Edge.
package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// cancellationWindow is how far before the appointment a customer
// can cancel and receive a full refund.
const cancellationWindow = 24 * time.Hour

// depositDeadlineDefault is the default number of hours before the
// appointment by which the deposit must be paid.
const depositDeadlineDefault = 48 * time.Hour

// defaultPageSize is the number of bookings returned per page.
const defaultPageSize = 20

// weekdays are Mon-Thu. Fri-Sun are weekends for travel buffer purposes.
// In Lebanon, the weekend is Friday-Sunday.
var weekdays = map[time.Weekday]bool{
	time.Monday:    true,
	time.Tuesday:   true,
	time.Wednesday: true,
	time.Thursday:  true,
}

// Service handles all booking business logic.
// It knows nothing about HTTP — no fiber.Ctx, no status codes.
// It knows nothing about SQL — all DB access goes through Repository.
type Service struct {
	repo     Repository
	validate *validator.Validate
}

// NewService creates a new booking Service.
func NewService(repo Repository) *Service {
	return &Service{
		repo:     repo,
		validate: validator.New(),
	}
}

// ── Slot availability ─────────────────────────────────────────────────────────

// GetAvailableSlots runs the 7-step slot availability algorithm and returns
// a list of valid time windows for the given artist, store, service, and date.
func (s *Service) GetAvailableSlots(ctx context.Context, req GetAvailableSlotsRequest) ([]*TimeSlot, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	artistID, err := uuid.Parse(req.ArtistID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_ARTIST_ID", "Invalid artist ID")
	}
	storeID, err := uuid.Parse(req.StoreID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_STORE_ID", "Invalid store ID")
	}
	serviceID, err := uuid.Parse(req.ServiceID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_SERVICE_ID", "Invalid service ID")
	}

	date, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_DATE", "Date must be in YYYY-MM-DD format")
	}

	// ── Step 1: Check store is open ───────────────────────────────────────

	store, err := s.repo.GetStore(ctx, storeID)
	if err != nil {
		return nil, fmt.Errorf("get available slots: get store: %w", err)
	}

	// Check for holiday or special hours on this specific date
	exception, err := s.repo.GetBusinessHoursException(ctx, storeID, date)
	if err != nil {
		return nil, fmt.Errorf("get available slots: get exception: %w", err)
	}
	if exception != nil && exception.IsClosed {
		return []*TimeSlot{}, nil // store is closed — return empty
	}

	// Get regular hours for this day of week
	bh, err := s.repo.GetBusinessHours(ctx, storeID, int(date.Weekday()))
	if err != nil {
		return nil, fmt.Errorf("get available slots: get hours: %w", err)
	}
	if bh == nil || !bh.IsOpen {
		return []*TimeSlot{}, nil // store is closed this day
	}

	// Parse open and close times for this date in UTC
	openTime, err := parseStoreTime(date, bh.OpenTime)
	if err != nil {
		return nil, fmt.Errorf("get available slots: parse open time: %w", err)
	}
	closeTime, err := parseStoreTime(date, bh.CloseTime)
	if err != nil {
		return nil, fmt.Errorf("get available slots: parse close time: %w", err)
	}

	// If exception has custom hours, override
	if exception != nil && exception.OpenTime != nil && exception.CloseTime != nil {
		openTime, _ = parseStoreTime(date, *exception.OpenTime)
		closeTime, _ = parseStoreTime(date, *exception.CloseTime)
	}

	// ── Step 2: Same-day minimum notice ──────────────────────────────────

	earliestStart := openTime
	if isToday(date) {
		minNotice := time.Now().UTC().Add(time.Duration(store.SameDayNoticeHours) * time.Hour)
		if minNotice.After(earliestStart) {
			earliestStart = minNotice
		}
	}

	// ── Step 3: Get service info ──────────────────────────────────────────

	service, err := s.repo.GetService(ctx, serviceID)
	if err != nil {
		return nil, fmt.Errorf("get available slots: get service: %w", err)
	}

	serviceDuration := time.Duration(service.DurationMin) * time.Minute

	// ── Step 4: Build blocked ranges from existing bookings ───────────────

	existingBookings, err := s.repo.GetArtistBookingsForDate(ctx, artistID, date)
	if err != nil {
		return nil, fmt.Errorf("get available slots: get bookings: %w", err)
	}

	var blocked []TimeRange
	for _, b := range existingBookings {
		blocked = append(blocked, TimeRange{Start: b.StartTime, End: b.EndTime})
	}

	// ── Step 5: Travel buffer for cross-store bookings ────────────────────

	crossStoreBookings, err := s.repo.GetArtistCrossStoreBookings(ctx, artistID, storeID, date)
	if err != nil {
		return nil, fmt.Errorf("get available slots: get cross store bookings: %w", err)
	}

	isWeekend := !weekdays[date.Weekday()]

	for _, csb := range crossStoreBookings {
		bufferMins := store.WeekdayBufferMin
		if isWeekend {
			bufferMins = store.WeekendBufferMin
		}

		// Try to get artist-specific buffer override
		buf, err := s.repo.GetArtistStoreBuffer(ctx, artistID, csb.StoreID, storeID)
		if err != nil {
			return nil, fmt.Errorf("get available slots: get buffer: %w", err)
		}
		if buf != nil {
			bufferMins = buf.WeekdayBufferMin
			if isWeekend {
				bufferMins = buf.WeekendBufferMin
			}
		}

		bufferDuration := time.Duration(bufferMins) * time.Minute

		// Block: buffer before the cross-store booking starts
		// (artist needs time to travel TO the other store)
		blocked = append(blocked, TimeRange{
			Start: csb.StartTime.Add(-bufferDuration),
			End:   csb.StartTime,
		})

		// Block: buffer after the cross-store booking ends
		// (artist needs time to travel BACK)
		blocked = append(blocked, TimeRange{
			Start: csb.EndTime,
			End:   csb.EndTime.Add(bufferDuration),
		})
	}

	// ── Step 6: Early bird config ─────────────────────────────────────────

	var earlyBirdCutoff *time.Time
	if store.EarlyBirdCutoff != nil {
		t, err := parseStoreTime(date, *store.EarlyBirdCutoff)
		if err == nil {
			earlyBirdCutoff = &t
		}
	}

	// ── Step 7: Generate valid slots ──────────────────────────────────────

	var slots []*TimeSlot
	current := earliestStart

	for current.Add(serviceDuration).Before(closeTime) || current.Add(serviceDuration).Equal(closeTime) {
		slotEnd := current.Add(serviceDuration)

		candidate := TimeRange{Start: current, End: slotEnd}
		overlap := false

		for _, b := range blocked {
			if candidate.Overlaps(b) {
				overlap = true
				break
			}
		}

		if !overlap {
			slot := &TimeSlot{
				StartTime: current,
				EndTime:   slotEnd,
			}

			// Flag early bird
			if earlyBirdCutoff != nil && current.Before(*earlyBirdCutoff) {
				slot.IsEarlyBird = true
				slot.EarlyBirdFee = store.EarlyBirdFee
			}

			slots = append(slots, slot)
		}

		// Advance by 15-minute increments — standard booking granularity
		current = current.Add(15 * time.Minute)
	}

	return slots, nil
}

// ── Booking lifecycle ─────────────────────────────────────────────────────────

// CreateBooking holds a slot and creates a pending booking.
// The GIST constraint is the final atomic guard against double booking.
//
// FIXED: salon_id is now derived from the service (which owns it), not the JWT.
// This ensures authenticated customers can create bookings without a salon_id in their token.
func (s *Service) CreateBooking(ctx context.Context, req CreateBookingRequest, customerID uuid.UUID) (*BookingResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	artistID, err := uuid.Parse(req.ArtistID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_ARTIST_ID", "Invalid artist ID")
	}
	storeID, err := uuid.Parse(req.StoreID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_STORE_ID", "Invalid store ID")
	}
	serviceID, err := uuid.Parse(req.ServiceID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_SERVICE_ID", "Invalid service ID")
	}

	startTime, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_START_TIME", "start_time must be in RFC3339 format e.g. 2026-06-01T10:00:00Z")
	}

	// Fetch service for duration and pricing — and derive salon_id from it
	service, err := s.repo.GetService(ctx, serviceID)
	if err != nil {
		return nil, apperror.NotFound("SERVICE_NOT_FOUND", "Service not found or no longer available")
	}

	endTime := startTime.Add(time.Duration(service.DurationMin) * time.Minute)

	// Set held_until — slot is reserved for 10 minutes during checkout
	heldUntil := time.Now().UTC().Add(SlotHoldDuration)

	b := &Booking{
		ID:              uuid.New(),
		SalonID:         service.SalonID, // DERIVED from service, not JWT
		StoreID:         storeID,
		ArtistID:        artistID,
		CustomerID:      customerID,
		ServiceID:       serviceID,
		StartTime:       startTime.UTC(),
		EndTime:         endTime.UTC(),
		HeldUntil:       &heldUntil,
		Status:          StatusHeld,
		OriginalPrice:   service.Price,
		DiscountAmount:  zeroDecimal(),
		FinalPrice:      service.Price,
		DepositAmount:   service.DepositAmount,
		Channel:         req.Channel,
		SpecialRequests: req.SpecialRequests,
	}

	if err := s.repo.CreateBooking(ctx, b); err != nil {
		if errors.Is(err, ErrSlotUnavailable) {
			return nil, apperror.Conflict("SLOT_UNAVAILABLE", "This slot was just taken. Please choose another time.")
		}
		return nil, fmt.Errorf("create booking: %w", err)
	}

	return toResponse(b), nil
}

// ── Guest two-step booking ────────────────────────────────────────────────────
//
// Matches the real screen flow: C-04 picks the slot (HoldGuestSlot), C-05 collects
// name + phone and submits (SubmitGuestBooking). The slot is genuinely protected
// for the full 10 minutes the customer spends on the details form.
//
// The guest user is created only on successful submit, so abandoned holds leave
// NO orphan user rows — the held booking points at SystemGuestPlaceholderID and is
// swept by the existing ReleaseExpiredHolds job.

// HoldGuestSlot creates a held booking when a guest taps a slot on C-04.
//
// No identity is known yet, so the booking is pointed at SystemGuestPlaceholderID
// to satisfy the customer_id FK. The GIST exclusion constraint guarantees
// first-write-wins: if two guests race for the same slot, only one succeeds and
// the other receives SLOT_UNAVAILABLE. The hold lasts SlotHoldDuration (10 min).
func (s *Service) HoldGuestSlot(ctx context.Context, req HoldGuestSlotRequest) (*HoldGuestSlotResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	artistID, err := uuid.Parse(req.ArtistID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_ARTIST_ID", "Invalid artist ID")
	}
	storeID, err := uuid.Parse(req.StoreID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_STORE_ID", "Invalid store ID")
	}
	serviceID, err := uuid.Parse(req.ServiceID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_SERVICE_ID", "Invalid service ID")
	}

	startTime, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_START_TIME", "start_time must be in RFC3339 format e.g. 2026-06-15T10:00:00Z")
	}

	// Reject holds for times in the past before touching the database.
	if startTime.UTC().Before(time.Now().UTC()) {
		return nil, apperror.BadRequest("BOOKING_IN_PAST", "Cannot book a time in the past")
	}

	// GetService filters on is_active = TRUE, so inactive services return not found.
	service, err := s.repo.GetService(ctx, serviceID)
	if err != nil {
		return nil, apperror.NotFound("SERVICE_NOT_FOUND", "Service not found or no longer available")
	}

	endTime := startTime.Add(time.Duration(service.DurationMin) * time.Minute)
	heldUntil := time.Now().UTC().Add(SlotHoldDuration)

	b := &Booking{
		ID:             uuid.New(),
		SalonID:        service.SalonID, // resolved from service, not a JWT
		StoreID:        storeID,
		ArtistID:       artistID,
		CustomerID:     SystemGuestPlaceholderID, // real guest user created on submit
		ServiceID:      serviceID,
		StartTime:      startTime.UTC(),
		EndTime:        endTime.UTC(),
		HeldUntil:      &heldUntil,
		Status:         StatusHeld,
		OriginalPrice:  service.Price,
		DiscountAmount: zeroDecimal(),
		FinalPrice:     service.Price,
		DepositAmount:  service.DepositAmount,
		Channel:        ChannelCustomerPWA,
	}

	if err := s.repo.CreateBooking(ctx, b); err != nil {
		if errors.Is(err, ErrSlotUnavailable) {
			return nil, apperror.Conflict("SLOT_UNAVAILABLE", "This slot was just taken. Please choose another time.")
		}
		return nil, fmt.Errorf("hold guest slot: %w", err)
	}

	return &HoldGuestSlotResponse{
		BookingID: b.ID,
		HeldUntil: heldUntil,
		StartTime: b.StartTime,
		EndTime:   b.EndTime,
	}, nil
}

// SubmitGuestBooking attaches the guest's identity and moves held → pending (C-05).
//
// No authentication is required — the booking ID plus an unexpired held_until
// window is the guard. Validates the booking is still held and not expired,
// creates the real guest user from the submitted name + phone, repoints the
// booking from the placeholder to that user, attaches special requests, and
// transitions to pending.
//
// AttachGuestAndSubmit performs the repoint + status change in a single guarded
// UPDATE so a concurrent ReleaseExpiredHolds run cannot cause a lost update.
func (s *Service) SubmitGuestBooking(ctx context.Context, bookingID uuid.UUID, req SubmitGuestBookingRequest) (*BookingResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("submit guest booking: get booking: %w", err)
	}

	// Guard: must still be a held guest booking that has not expired.
	if b.Status != StatusHeld || b.CustomerID != SystemGuestPlaceholderID {
		return nil, apperror.Conflict("HOLD_EXPIRED", "This slot hold is no longer active. Please choose your time again.")
	}
	if b.HeldUntil == nil || b.HeldUntil.Before(time.Now().UTC()) {
		return nil, apperror.Conflict("HOLD_EXPIRED", "Your 10-minute hold expired. Please choose your time again.")
	}

	// Create the real guest user now that the customer has completed the form.
	guestUserID, err := s.repo.CreateGuestUser(ctx, req.Name, req.Phone)
	if err != nil {
		return nil, fmt.Errorf("submit guest booking: create guest user: %w", err)
	}

	// Atomically repoint customer_id and transition held → pending. Guarded on
	// status = held AND held_until > NOW() so an expiry race cannot resurrect it.
	if err := s.repo.AttachGuestAndSubmit(ctx, bookingID, guestUserID, req.SpecialRequests); err != nil {
		if errors.Is(err, ErrBookingNotHeld) {
			return nil, apperror.Conflict("HOLD_EXPIRED", "Your 10-minute hold expired. Please choose your time again.")
		}
		return nil, fmt.Errorf("submit guest booking: %w", err)
	}

	b.CustomerID = guestUserID
	b.Status = StatusPending
	b.HeldUntil = nil
	b.SpecialRequests = req.SpecialRequests
	return toResponse(b), nil
}

// SubmitBooking transitions a held booking to pending.
// Called when the customer completes and submits the booking form.
func (s *Service) SubmitBooking(ctx context.Context, bookingID uuid.UUID, customerID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("submit booking: get booking: %w", err)
	}

	// Only the customer who created the booking can submit it
	if b.CustomerID != customerID {
		return nil, apperror.Forbidden("NOT_BOOKING_OWNER", "You do not have permission to act on this booking")
	}

	if b.Status != StatusHeld {
		return nil, apperror.Conflict("BOOKING_NOT_HELD", "Only held bookings can be submitted")
	}

	if err := s.repo.UpdateBookingStatus(ctx, bookingID, StatusPending); err != nil {
		return nil, fmt.Errorf("submit booking: update status: %w", err)
	}

	b.Status = StatusPending
	return toResponse(b), nil
}

// GetBookingByID returns a booking by ID.
// Validates that the requesting user is the customer, artist, or admin.
func (s *Service) GetBookingByID(ctx context.Context, bookingID uuid.UUID, requesterID uuid.UUID, requesterRole string) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("get booking by id: %w", err)
	}

	// Admins can see any booking.
	// Artists and customers can only see bookings they are part of.
	if requesterRole != "admin" && b.CustomerID != requesterID && b.ArtistID != requesterID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to view this booking")
	}

	return toResponse(b), nil
}

// GetBookingsByArtist returns paginated bookings for an artist.
func (s *Service) GetBookingsByArtist(ctx context.Context, artistID uuid.UUID, cursor time.Time, limit int) ([]*BookingResponse, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}

	bookings, err := s.repo.GetBookingsByArtist(ctx, artistID, cursor, limit)
	if err != nil {
		return nil, false, fmt.Errorf("get bookings by artist: %w", err)
	}

	hasMore := len(bookings) > limit
	if hasMore {
		bookings = bookings[:limit]
	}

	var result []*BookingResponse
	for _, b := range bookings {
		result = append(result, toResponse(b))
	}

	return result, hasMore, nil
}

// GetBookingsByCustomer returns paginated bookings for a customer.
func (s *Service) GetBookingsByCustomer(ctx context.Context, customerID uuid.UUID, cursor time.Time, limit int) ([]*BookingResponse, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}

	bookings, err := s.repo.GetBookingsByCustomer(ctx, customerID, cursor, limit)
	if err != nil {
		return nil, false, fmt.Errorf("get bookings by customer: %w", err)
	}

	hasMore := len(bookings) > limit
	if hasMore {
		bookings = bookings[:limit]
	}

	var result []*BookingResponse
	for _, b := range bookings {
		result = append(result, toResponse(b))
	}

	return result, hasMore, nil
}

// ── Enriched reads (joined display names) ─────────────────────────────────────

// GetEnrichedBookingByID returns one booking with joined display names.
// Access: admin, or the customer/artist on the booking.
func (s *Service) GetEnrichedBookingByID(ctx context.Context, bookingID uuid.UUID, requesterID uuid.UUID, requesterRole string) (*EnrichedBookingResponse, error) {
	e, err := s.repo.GetEnrichedBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("get enriched booking by id: %w", err)
	}

	if requesterRole != RoleAdmin && e.CustomerID != requesterID && e.ArtistID != requesterID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to view this booking")
	}

	return toEnrichedResponse(e), nil
}

// ListEnrichedBookingsByArtist returns an artist's bookings with display names.
// If status is non-empty it must be a known booking status; results are then
// restricted to that status (dashboard tabs, deposit queue, refund queue).
func (s *Service) ListEnrichedBookingsByArtist(ctx context.Context, artistID uuid.UUID, status string, cursor time.Time, limit int) ([]*EnrichedBookingResponse, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}

	// Reject unknown status values with a clear error rather than silently
	// returning an empty list (which would look like "no bookings" to the UI).
	if status != "" && !ValidBookingStatuses[status] {
		return nil, false, apperror.BadRequest("INVALID_STATUS", "Unknown booking status filter")
	}

	rows, err := s.repo.ListEnrichedBookingsByArtist(ctx, artistID, status, cursor, limit)
	if err != nil {
		return nil, false, fmt.Errorf("list enriched bookings by artist: %w", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	result := make([]*EnrichedBookingResponse, 0, len(rows))
	for _, e := range rows {
		result = append(result, toEnrichedResponse(e))
	}
	return result, hasMore, nil
}

// ListEnrichedBookingsForWeek returns the artist's committed appointments for the
// 7-day window beginning at weekStart (calendar grid). No pagination — the whole
// week is returned, ordered by start time.
func (s *Service) ListEnrichedBookingsForWeek(ctx context.Context, artistID uuid.UUID, weekStart time.Time) ([]*EnrichedBookingResponse, error) {
	// Normalise to the start of the day in UTC so the half-open window aligns to
	// midnight boundaries regardless of any time component the client sent.
	weekStart = time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(), 0, 0, 0, 0, time.UTC)

	rows, err := s.repo.ListEnrichedBookingsForWeek(ctx, artistID, weekStart)
	if err != nil {
		return nil, fmt.Errorf("list enriched bookings for week: %w", err)
	}

	result := make([]*EnrichedBookingResponse, 0, len(rows))
	for _, e := range rows {
		result = append(result, toEnrichedResponse(e))
	}
	return result, nil
}

// ListEnrichedBookingsByCustomer returns a customer's bookings with display names.
func (s *Service) ListEnrichedBookingsByCustomer(ctx context.Context, customerID uuid.UUID, cursor time.Time, limit int) ([]*EnrichedBookingResponse, bool, error) {
	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}

	rows, err := s.repo.ListEnrichedBookingsByCustomer(ctx, customerID, cursor, limit)
	if err != nil {
		return nil, false, fmt.Errorf("list enriched bookings by customer: %w", err)
	}

	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}

	result := make([]*EnrichedBookingResponse, 0, len(rows))
	for _, e := range rows {
		result = append(result, toEnrichedResponse(e))
	}
	return result, hasMore, nil
}

// ApproveBooking transitions a pending booking to approved.
// Sets the deposit deadline based on the service configuration.
// Only the artist can approve a booking.
func (s *Service) ApproveBooking(ctx context.Context, bookingID uuid.UUID, artistID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("approve booking: get booking: %w", err)
	}

	// Only the artist on the booking can approve it
	if b.ArtistID != artistID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to approve this booking")
	}

	if b.Status != StatusPending {
		return nil, apperror.Conflict("BOOKING_NOT_PENDING", "Only pending bookings can be approved")
	}

	// Fetch service to get deposit deadline hours
	service, err := s.repo.GetService(ctx, b.ServiceID)
	if err != nil {
		return nil, fmt.Errorf("approve booking: get service: %w", err)
	}

	deadlineHours := time.Duration(service.DepositDeadlineHours) * time.Hour
	if deadlineHours == 0 {
		deadlineHours = depositDeadlineDefault
	}

	depositDeadline := b.StartTime.Add(-deadlineHours)

	if err := s.repo.ApproveBooking(ctx, bookingID, depositDeadline); err != nil {
		if errors.Is(err, ErrBookingNotPending) {
			return nil, apperror.Conflict("BOOKING_NOT_PENDING", "Only pending bookings can be approved")
		}
		return nil, fmt.Errorf("approve booking: %w", err)
	}

	b.Status = StatusApproved
	b.DepositDeadline = &depositDeadline
	return toResponse(b), nil
}

// ConfirmDeposit marks a deposit as received and confirms the booking.
// Only the artist can confirm a deposit.
func (s *Service) ConfirmDeposit(ctx context.Context, bookingID uuid.UUID, artistID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("confirm deposit: get booking: %w", err)
	}

	if b.ArtistID != artistID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to confirm this booking")
	}

	if b.Status != StatusDepositPaid {
		return nil, apperror.Conflict("BOOKING_NOT_DEPOSIT_PAID", "Booking must be in deposit_paid status to confirm")
	}

	if err := s.repo.ConfirmDeposit(ctx, bookingID); err != nil {
		if errors.Is(err, ErrBookingNotApproved) {
			return nil, apperror.Conflict("BOOKING_NOT_DEPOSIT_PAID", "Booking must be in deposit_paid status to confirm")
		}
		return nil, fmt.Errorf("confirm deposit: %w", err)
	}

	b.Status = StatusConfirmed
	return toResponse(b), nil
}

// MarkDepositReceived transitions approved → deposit_paid.
// Called by the artist after verifying the Wish Money transfer.
func (s *Service) MarkDepositReceived(ctx context.Context, bookingID uuid.UUID, artistID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("mark deposit received: get booking: %w", err)
	}

	if b.ArtistID != artistID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to act on this booking")
	}

	if b.Status != StatusApproved {
		return nil, apperror.Conflict("BOOKING_NOT_APPROVED", "Only approved bookings can have deposit marked as received")
	}

	if err := s.repo.UpdateBookingStatus(ctx, bookingID, StatusDepositPaid); err != nil {
		return nil, fmt.Errorf("mark deposit received: %w", err)
	}

	b.Status = StatusDepositPaid
	return toResponse(b), nil
}

// CancelBooking cancels a booking.
// Enforces the 24-hour cancellation policy for customers.
// Artists can always cancel but trigger a refund_due.
func (s *Service) CancelBooking(ctx context.Context, bookingID uuid.UUID, requesterID uuid.UUID, requesterRole string, req CancelBookingRequest) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("cancel booking: get booking: %w", err)
	}

	// Determine if requester is the customer or artist on this booking
	isCustomer := b.CustomerID == requesterID
	isArtist := b.ArtistID == requesterID
	isAdmin := requesterRole == "admin"

	if !isCustomer && !isArtist && !isAdmin {
		return nil, apperror.Forbidden("NOT_BOOKING_OWNER", "You do not have permission to cancel this booking")
	}

	// Determine if a refund is due
	refundDue := false

	if isArtist || isAdmin {
		// Artist cancelling always triggers a refund
		refundDue = b.DepositAmount.IsPositive()
	} else if isCustomer {
		// Customer cancelling: refund only if >24h before appointment
		timeUntilAppointment := time.Until(b.StartTime)
		if timeUntilAppointment > cancellationWindow {
			refundDue = b.DepositAmount.IsPositive()
		}
	}

	reason := ""
	if req.Reason != nil {
		reason = *req.Reason
	}

	if err := s.repo.CancelBooking(ctx, bookingID, reason, refundDue); err != nil {
		if errors.Is(err, ErrBookingNotCancellable) {
			return nil, apperror.Conflict("BOOKING_NOT_CANCELLABLE", "This booking cannot be cancelled in its current status")
		}
		return nil, fmt.Errorf("cancel booking: %w", err)
	}

	if refundDue {
		b.Status = StatusRefundDue
	} else {
		b.Status = StatusCancelled
	}
	return toResponse(b), nil
}

// CompleteBooking marks a confirmed booking as completed.
// Only the artist can mark a booking as completed.
func (s *Service) CompleteBooking(ctx context.Context, bookingID uuid.UUID, artistID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("complete booking: get booking: %w", err)
	}

	if b.ArtistID != artistID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to complete this booking")
	}

	if b.Status != StatusConfirmed {
		return nil, apperror.Conflict("BOOKING_NOT_CONFIRMED", "Only confirmed bookings can be marked as completed")
	}

	if err := s.repo.CompleteBooking(ctx, bookingID); err != nil {
		return nil, fmt.Errorf("complete booking: %w", err)
	}

	b.Status = StatusCompleted
	return toResponse(b), nil
}

// MarkNoShow marks a confirmed booking as no_show.
// Only the artist can mark a no-show.
func (s *Service) MarkNoShow(ctx context.Context, bookingID uuid.UUID, artistID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("mark no show: get booking: %w", err)
	}

	if b.ArtistID != artistID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to act on this booking")
	}

	if b.Status != StatusConfirmed {
		return nil, apperror.Conflict("BOOKING_NOT_CONFIRMED", "Only confirmed bookings can be marked as no-show")
	}

	if err := s.repo.MarkNoShow(ctx, bookingID); err != nil {
		return nil, fmt.Errorf("mark no show: %w", err)
	}

	b.Status = StatusNoShow
	return toResponse(b), nil
}

// ── Background job methods ────────────────────────────────────────────────────

// ReleaseExpiredHolds releases all held bookings whose 10-minute window
// has passed. Called by the background job every minute.
func (s *Service) ReleaseExpiredHolds(ctx context.Context) (int64, error) {
	count, err := s.repo.ReleaseExpiredHolds(ctx)
	if err != nil {
		return 0, fmt.Errorf("release expired holds: %w", err)
	}
	return count, nil
}

// ExpireDeadlineBookings expires all approved bookings whose deposit
// deadline has passed. Called by the background job every minute.
func (s *Service) ExpireDeadlineBookings(ctx context.Context) (int64, error) {
	count, err := s.repo.ExpireDeadlineBookings(ctx)
	if err != nil {
		return 0, fmt.Errorf("expire deadline bookings: %w", err)
	}
	return count, nil
}

// ── Private helpers ───────────────────────────────────────────────────────────

// parseStoreTime parses a TIME string from PostgreSQL (e.g. "09:00:00")
// and combines it with a date to produce a time.Time in UTC.
func parseStoreTime(date time.Time, timeStr string) (time.Time, error) {
	// PostgreSQL TIME columns return strings like "09:00:00"
	t, err := time.Parse("15:04:05", timeStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse store time %q: %w", timeStr, err)
	}
	return time.Date(
		date.Year(), date.Month(), date.Day(),
		t.Hour(), t.Minute(), t.Second(), 0,
		time.UTC,
	), nil
}

// isToday returns true if the given date is today in UTC.
func isToday(date time.Time) bool {
	now := time.Now().UTC()
	return date.Year() == now.Year() &&
		date.Month() == now.Month() &&
		date.Day() == now.Day()
}

// zeroDecimal returns a zero decimal value.
func zeroDecimal() decimal.Decimal {
	return decimal.NewFromInt(0)
}

// mapValidationError converts go-playground/validator errors
// into structured apperror types.
func mapValidationError(err error) error {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return apperror.BadRequest("VALIDATION_ERROR", err.Error())
	}

	details := make([]apperror.FieldError, 0, len(ve))
	for _, fe := range ve {
		details = append(details, apperror.FieldError{
			Field:   fe.Field(),
			Message: validationMessage(fe),
		})
	}

	return apperror.UnprocessableEntity("VALIDATION_ERROR", details)
}

// validationMessage returns a human-readable message for a field validation failure.
func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + " is required"
	case "uuid":
		return fe.Field() + " must be a valid UUID"
	case "oneof":
		return fe.Field() + " must be one of: " + fe.Param()
	default:
		return fe.Field() + " is invalid"
	}
}
