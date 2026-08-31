// Package booking implements the booking domain for B-Edge.
package booking

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/billing"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/openinghours"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/subscription"
)

// toDayHours adapts this domain's BusinessHours row onto the shared
// openinghours value type. Returns nil for a nil row - the repository
// returns (nil, nil) when a weekday has no hours configured, which
// openinghours.Resolve reads as closed.
func toDayHours(bh *BusinessHours) *openinghours.DayHours {
	if bh == nil {
		return nil
	}
	return &openinghours.DayHours{
		IsOpen:    bh.IsOpen,
		OpenTime:  bh.OpenTime,
		CloseTime: bh.CloseTime,
	}
}

// toException adapts this domain's BusinessHoursException row onto the
// shared openinghours value type.
func toException(e *BusinessHoursException) *openinghours.Exception {
	if e == nil {
		return nil
	}
	return &openinghours.Exception{
		IsClosed:  e.IsClosed,
		OpenTime:  e.OpenTime,
		CloseTime: e.CloseTime,
	}
}

// SubscriptionStatusReader is the one capability this domain needs from the
// billing domain: given an artist, what is their subscription (if any)?
// Defined here rather than depending on billing.Repository's entire
// interface - this domain reads exactly one thing about billing and has no
// business depending on billing's whole persistence surface (plans,
// invoices, admin overview) just to satisfy a type. *billing.pgRepo (via
// billing.NewRepository) satisfies this structurally with no changes on
// that side.
type SubscriptionStatusReader interface {
	GetSubscriptionByArtistID(ctx context.Context, artistID uuid.UUID) (*billing.Subscription, error)
}

// cancellationWindow is how far before the appointment a customer
// can cancel and receive a full refund.
const cancellationWindow = 24 * time.Hour

// depositDeadlineDefault is the default number of hours before the
// appointment by which the deposit must be paid.
const depositDeadlineDefault = 48 * time.Hour

// depositGraceWindow is the fallback deposit window used when a service's
// configured deposit_deadline_hours doesn't fit before the appointment - a
// pending request that sat unapproved long enough that start_time minus the
// service's deposit window has already passed by the time the artist
// approves it. Rather than storing an instantly-expired deadline (and
// telling the customer they have "24 hours" while the real field already
// lapsed), the customer gets this much time from the moment of approval,
// capped so it never extends past the appointment itself.
const depositGraceWindow = 2 * time.Hour

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
// It knows nothing about HTTP - no fiber.Ctx, no status codes.
// It knows nothing about SQL - all DB access goes through Repository.
type Service struct {
	repo      Repository
	subReader SubscriptionStatusReader
	validate  *validator.Validate
	log       *zap.Logger
}

// NewService creates a new booking Service.
//
// subReader is required, unlike the variadic logger below - it gates
// whether a new booking is even allowed to be created (see
// checkArtistAcceptsNewBookings), so a nil default here would mean a
// wiring mistake silently disables Phase 4 enforcement rather than failing
// loudly. See B-Edge-Monetization-Implementation-Spec-v1.md section 6.1.
//
// The logger is variadic rather than a required parameter purely to avoid
// churning every existing call site (including every test's
// newTestService) for what is an observability addition. Omitting it
// yields a no-op logger, so behaviour is unchanged for callers that don't
// pass one - but the production wiring in RegisterRoutes does.
func NewService(repo Repository, subReader SubscriptionStatusReader, log ...*zap.Logger) *Service {
	l := zap.NewNop()
	if len(log) > 0 && log[0] != nil {
		l = log[0]
	}
	return &Service{
		repo:      repo,
		subReader: subReader,
		validate:  validator.New(),
		log:       l,
	}
}

// checkArtistAcceptsNewBookings enforces
// B-Edge-Monetization-Implementation-Spec-v1.md section 6.1's graduated
// enforcement: an artist whose subscription has derived status past_due or
// suspended stops receiving NEW bookings, while every existing booking is
// still honored untouched (nothing here ever reads or touches the bookings
// table). An artist with no subscriptions row at all is treated the same
// as past_due, exactly matching billing.DeriveStatus's own
// CurrentPeriodEnd==nil case - this should not happen for any artist
// approved after Aug 29, 2026 (see admin.Service.Approve), but stays
// correct even if it does.
func (s *Service) checkArtistAcceptsNewBookings(ctx context.Context, artistID uuid.UUID) error {
	sub, err := s.subReader.GetSubscriptionByArtistID(ctx, artistID)
	if err != nil && !errors.Is(err, billing.ErrSubscriptionNotFound) {
		return fmt.Errorf("check artist subscription status: %w", err)
	}
	if sub == nil {
		sub = &billing.Subscription{}
	}

	// Reads the shared enforcement ladder rather than naming statuses here.
	// Which states stop new bookings is a policy decision declared once in
	// internal/pkg/subscription; this call site enforces it but does not
	// get to define it.
	if !subscription.Enforce(billing.DeriveStatus(sub, time.Now())).AcceptsNewBookings {
		return apperror.Forbidden("ARTIST_NOT_ACCEPTING_BOOKINGS",
			"This artist isn't accepting new bookings right now")
	}
	return nil
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

	// Get regular hours for this day of week
	bh, err := s.repo.GetBusinessHours(ctx, storeID, int(date.Weekday()))
	if err != nil {
		return nil, fmt.Errorf("get available slots: get hours: %w", err)
	}

	// Resolving the trading window is shared with the public "Open now"
	// badge on the discovery profile - see internal/pkg/openinghours for
	// why it lives outside this domain rather than inline here.
	window, open, err := openinghours.Resolve(store.Timezone, date, toDayHours(bh), toException(exception))
	if err != nil {
		return nil, fmt.Errorf("get available slots: %w", err)
	}
	if !open {
		return []*TimeSlot{}, nil // store is closed this date - return empty
	}
	openTime, closeTime := window.OpenAt, window.CloseAt

	// ── Step 2: Same-day minimum notice ──────────────────────────────────

	storeLoc := openinghours.Location(store.Timezone)
	localDate := openinghours.LocalDate(date, storeLoc)
	earliestStart := openTime
	if openinghours.IsSameDayIn(date, time.Now(), storeLoc) {
		minNotice := time.Now().UTC().Add(time.Duration(store.SameDayNoticeHours) * time.Hour)
		if minNotice.After(earliestStart) {
			earliestStart = minNotice
		}
	}

	// ── Step 3: Get service info ──────────────────────────────────────────

	service, err := s.repo.GetService(ctx, serviceID)
	if err != nil {
		// A service ID that does not resolve is a client error, not a server
		// fault - mirror CreateBooking and HoldGuestSlot rather than falling
		// through to the generic 500 handler.
		return nil, apperror.NotFound("SERVICE_NOT_FOUND", "Service not found or no longer available")
	}

	serviceDuration := time.Duration(service.DurationMin) * time.Minute

	// ── Step 3.5: Lazily release stale holds and deposit-lapsed approvals ──
	//
	// A held booking nobody ever submitted stays in 'held' forever unless
	// something moves it past its 10-minute window - there is no background
	// scheduler running ReleaseExpiredHolds (found live while testing this
	// endpoint: several holds from days earlier were still permanently
	// blocking their slots). The same problem exists for 'approved' bookings
	// whose deposit_deadline lapsed without payment - StatusApproved is in
	// BlockingStatuses too, so an artist who never got paid and never
	// manually cancelled leaves that slot permanently unbookable, forever,
	// exactly like a stale hold. Rather than standing up a real scheduler,
	// both self-heal opportunistically on the read path every availability
	// query already takes: best-effort, since a sweep failure here should
	// never fail the slots request itself - worst case, a stale row keeps
	// blocking for one more request, exactly like before this fix.
	if _, err := s.repo.ReleaseExpiredHolds(ctx); err != nil {
		s.log.Warn("get available slots: release expired holds failed, continuing", zap.Error(err))
	}
	if _, err := s.repo.ExpireDeadlineBookings(ctx); err != nil {
		s.log.Warn("get available slots: expire deadline bookings failed, continuing", zap.Error(err))
	}

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
		t, err := parseStoreTimeIn(localDate, *store.EarlyBirdCutoff, storeLoc)
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

		// Advance by 15-minute increments - standard booking granularity
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

	if err := s.checkArtistAcceptsNewBookings(ctx, artistID); err != nil {
		return nil, err
	}

	startTime, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_START_TIME", "start_time must be in RFC3339 format e.g. 2026-06-01T10:00:00Z")
	}

	// Fetch service for duration and pricing - and derive salon_id from it
	service, err := s.repo.GetService(ctx, serviceID)
	if err != nil {
		return nil, apperror.NotFound("SERVICE_NOT_FOUND", "Service not found or no longer available")
	}

	store, err := s.repo.GetStore(ctx, storeID)
	if err != nil {
		return nil, fmt.Errorf("create booking: get store: %w", err)
	}

	// Same early-bird surcharge logic as HoldGuestSlot - see isEarlyBirdSlot.
	finalPrice := service.Price
	if isEarlyBirdSlot(store, startTime.UTC()) {
		finalPrice = finalPrice.Add(store.EarlyBirdFee)
	}

	endTime := startTime.Add(time.Duration(service.DurationMin) * time.Minute)

	// Set held_until - slot is reserved for 10 minutes during checkout
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
		FinalPrice:      finalPrice,
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
// NO orphan user rows - the held booking points at SystemGuestPlaceholderID and is
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

	if err := s.checkArtistAcceptsNewBookings(ctx, artistID); err != nil {
		return nil, err
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

	store, err := s.repo.GetStore(ctx, storeID)
	if err != nil {
		return nil, fmt.Errorf("hold guest slot: get store: %w", err)
	}

	// If this slot falls before the store's early-bird cutoff, the surcharge
	// the picker badged this slot with must actually be charged - otherwise
	// the badge is decorative and the customer is quoted a price that never
	// gets applied.
	finalPrice := service.Price
	if isEarlyBirdSlot(store, startTime.UTC()) {
		finalPrice = finalPrice.Add(store.EarlyBirdFee)
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
		FinalPrice:     finalPrice,
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
// No authentication is required - the booking ID plus an unexpired held_until
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

	// e.ArtistID is artists.id; requesterID is users.id - DIFFERENT ID
	// spaces. Comparing them directly (as this did) can never be true, so
	// an artist was silently 403'd on their own booking. Fails closed, so
	// it was a functional bug rather than a hole, but it's the same
	// artists.id-vs-users.id confusion already fixed in six other booking
	// methods; this occurrence was missed. Resolve first, compare like
	// with like.
	isArtist := false
	if requesterRole == RoleArtist {
		requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterID)
		if err != nil && !errors.Is(err, ErrArtistNotFound) {
			return nil, fmt.Errorf("get enriched booking by id: resolve artist: %w", err)
		}
		isArtist = err == nil && e.ArtistID == requesterArtistID
	}

	if requesterRole != RoleAdmin && e.CustomerID != requesterID && !isArtist {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to view this booking")
	}

	return toEnrichedResponse(e), nil
}

// ListEnrichedBookingsByArtist returns an artist's bookings with display names.
// If status is non-empty it must be a known booking status; results are then
// restricted to that status (dashboard tabs, deposit queue, refund queue).
func (s *Service) ListEnrichedBookingsByArtist(ctx context.Context, artistID uuid.UUID, requesterUserID uuid.UUID, requesterRole string, status string, cursor time.Time, limit int) ([]*EnrichedBookingResponse, bool, error) {
	// Ownership first, before any work - see assertArtistAccess. Without
	// this, any registered artist could read any other artist's entire
	// booking book (customer names, phone numbers, prices, deposits).
	if err := s.assertArtistAccess(ctx, artistID, requesterUserID, requesterRole); err != nil {
		return nil, false, err
	}

	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}

	// Reject unknown status values with a clear error rather than silently
	// returning an empty list (which would look like "no bookings" to the UI).
	if status != "" && !ValidBookingStatuses[status] {
		return nil, false, apperror.BadRequest("INVALID_STATUS", "Unknown booking status filter")
	}

	// Lazily expire this artist's own stale pending requests before reading -
	// same self-healing shape as GetAvailableSlots' ReleaseExpiredHolds call
	// above, best-effort so a sweep failure never fails the list itself.
	if _, err := s.repo.ExpireStalePendingBookings(ctx, artistID); err != nil {
		s.log.Warn("list bookings by artist: expire stale pending failed, continuing", zap.Error(err))
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
// 7-day window beginning at weekStart (calendar grid). No pagination - the whole
// week is returned, ordered by start time.
func (s *Service) ListEnrichedBookingsForWeek(ctx context.Context, artistID uuid.UUID, requesterUserID uuid.UUID, requesterRole string, weekStart time.Time) ([]*EnrichedBookingResponse, error) {
	// Ownership first - see assertArtistAccess. Without this, any registered
	// artist could read any other artist's full weekly schedule.
	if err := s.assertArtistAccess(ctx, artistID, requesterUserID, requesterRole); err != nil {
		return nil, err
	}

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
// ApproveBooking transitions a pending booking to approved and sets the deposit deadline.
// Only the artist can approve a booking.
func (s *Service) ApproveBooking(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("approve booking: get booking: %w", err)
	}

	// Resolve the JWT user_id to the caller's artists.id. bookings.artist_id
	// references artists.id, so we must compare like with like.
	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to approve this booking")
		}
		return nil, fmt.Errorf("approve booking: resolve artist: %w", err)
	}

	// Only the artist on the booking can approve it
	if b.ArtistID != requesterArtistID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to approve this booking")
	}

	if b.Status != StatusPending {
		return nil, apperror.Conflict("BOOKING_NOT_PENDING", "Only pending bookings can be approved")
	}

	// A pending request nobody acted on before its own appointment time
	// isn't approvable anymore - there's no slot left to honor, and
	// approving it would compute a deposit deadline that's also already
	// in the past (see depositDeadline below) and send the customer a
	// WhatsApp message asking them to pay for an appointment that
	// already happened. There's currently no background job that
	// auto-expires stale pending bookings, so this guard is the only
	// thing standing between "sat too long" and a nonsensical approval.
	if b.StartTime.Before(time.Now()) {
		return nil, apperror.Conflict("BOOKING_TIME_PASSED", "This booking's appointment time has already passed and can no longer be approved")
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

	// The configured window doesn't fit before the appointment (the request
	// sat unapproved too long, or the appointment is simply sooner than the
	// service's usual deposit window) - fall back to a short grace period
	// from right now instead of storing a deadline that's already passed,
	// capped so it never extends past the appointment itself.
	now := time.Now()
	if depositDeadline.Before(now) {
		depositDeadline = now.Add(depositGraceWindow)
		if depositDeadline.After(b.StartTime) {
			depositDeadline = b.StartTime
		}
	}

	if err := s.repo.ApproveBooking(ctx, bookingID, depositDeadline); err != nil {
		if errors.Is(err, ErrBookingNotPending) {
			return nil, apperror.Conflict("BOOKING_NOT_PENDING", "Only pending bookings can be approved")
		}
		return nil, fmt.Errorf("approve booking: %w", err)
	}

	b.Status = StatusApproved
	b.DepositDeadline = &depositDeadline

	customerName, serviceName, ctxErr := s.repo.GetBookingNotificationContext(ctx, bookingID)
	if ctxErr == nil {
		var message string
		if service.DepositAmount.IsPositive() {
			// Report the real time left until depositDeadline, not the
			// service's nominal window - when the grace-window fallback
			// above kicked in, deadlineHours would otherwise claim "24
			// hours" while the customer actually has 2 or less.
			hoursRemaining := max(int(math.Ceil(depositDeadline.Sub(now).Hours())), 1)
			message = fmt.Sprintf(
				"Hi %s! Your %s request for %s has been approved. Please send a $%s deposit within %d hours to confirm your spot.",
				customerName, serviceName, notificationTimeLabel(b.StartTime),
				service.DepositAmount.String(), hoursRemaining,
			)
		} else {
			// No deposit required, but this booking still isn't
			// StatusConfirmed yet - the artist confirms it as a separate
			// action even for $0-deposit services (see ConfirmDepositReceived).
			// Claiming "confirmed" here would say something false over
			// WhatsApp about the booking's actual state.
			message = fmt.Sprintf(
				"Hi %s! Your %s request for %s has been approved. You'll get a final confirmation shortly.",
				customerName, serviceName, notificationTimeLabel(b.StartTime),
			)
		}
		s.enqueueNotification(ctx, bookingID, b.CustomerID, "booking_approved", message)
	}

	return toResponse(b), nil
}

// ConfirmDeposit marks a deposit as received and confirms the booking.
// Only the artist can confirm a deposit.
func (s *Service) ConfirmDeposit(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("confirm deposit: get booking: %w", err)
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to confirm this booking")
		}
		return nil, fmt.Errorf("confirm deposit: resolve artist: %w", err)
	}

	if b.ArtistID != requesterArtistID {
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
func (s *Service) MarkDepositReceived(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("mark deposit received: get booking: %w", err)
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to act on this booking")
		}
		return nil, fmt.Errorf("mark deposit received: resolve artist: %w", err)
	}

	if b.ArtistID != requesterArtistID {
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

// ConfirmDepositReceived is the primary artist-facing deposit action: she
// checks her OMT/Wish transfer and confirms the moment it lands, so this
// moves a booking straight from approved to confirmed in one call, stamping
// deposit_paid_at along the way. The separate MarkDepositReceived / ConfirmDeposit
// pair remains available for edge cases (partial payment, disputed transfer)
// where the two steps genuinely need to happen apart.
//
// reference is an optional artist-entered note (e.g. an OMT/Wish transaction
// code) for her own reconciliation - never shown to the customer, never
// validated beyond a sanity length cap.
func (s *Service) ConfirmDepositReceived(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID, reference *string) (*BookingResponse, error) {
	if reference != nil && len(*reference) > 255 {
		return nil, apperror.BadRequest("REFERENCE_TOO_LONG", "Reference note must be 255 characters or fewer")
	}

	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("confirm deposit received: get booking: %w", err)
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to act on this booking")
		}
		return nil, fmt.Errorf("confirm deposit received: resolve artist: %w", err)
	}

	if b.ArtistID != requesterArtistID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to act on this booking")
	}

	if b.Status != StatusApproved {
		return nil, apperror.Conflict("BOOKING_NOT_APPROVED", "Only approved bookings can have a deposit confirmed")
	}

	if err := s.repo.ConfirmDepositReceived(ctx, bookingID, reference); err != nil {
		if errors.Is(err, ErrBookingNotApproved) {
			return nil, apperror.Conflict("BOOKING_NOT_APPROVED", "Only approved bookings can have a deposit confirmed")
		}
		return nil, fmt.Errorf("confirm deposit received: %w", err)
	}

	now := time.Now().UTC()
	b.Status = StatusConfirmed
	b.DepositPaidAt = &now
	if reference != nil {
		b.DepositReference = reference
	}

	customerName, serviceName, ctxErr := s.repo.GetBookingNotificationContext(ctx, bookingID)
	if ctxErr == nil {
		message := fmt.Sprintf(
			"Hi %s! You're all confirmed for %s on %s. See you then!",
			customerName, serviceName, notificationTimeLabel(b.StartTime),
		)
		s.enqueueNotification(ctx, bookingID, b.CustomerID, "booking_confirmed", message)
	}

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

	// Determine if requester is the customer or artist on this booking.
	// b.CustomerID is genuinely users.id, so that comparison is direct.
	// b.ArtistID is artists.id, so an "artist" requester must be resolved
	// from their JWT user_id first before comparing like with like.
	isCustomer := b.CustomerID == requesterID
	isAdmin := requesterRole == "admin"

	isArtist := false
	if requesterRole == "artist" {
		requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterID)
		if err != nil && !errors.Is(err, ErrArtistNotFound) {
			return nil, fmt.Errorf("cancel booking: resolve artist: %w", err)
		}
		isArtist = err == nil && b.ArtistID == requesterArtistID
	}

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

	// Only notify if the ARTIST cancelled - a customer who just cancelled
	// their own booking doesn't need to be told about it.
	if isArtist || isAdmin {
		customerName, serviceName, ctxErr := s.repo.GetBookingNotificationContext(ctx, bookingID)
		if ctxErr == nil {
			message := fmt.Sprintf(
				"Hi %s, your %s booking for %s has been cancelled.",
				customerName, serviceName, notificationTimeLabel(b.StartTime),
			)
			if refundDue {
				message += " Your deposit will be refunded."
			}
			if reason != "" {
				message += " Reason: " + reason
			}
			s.enqueueNotification(ctx, bookingID, b.CustomerID, "booking_cancelled", message)
		}
	}

	// A cancellation just freed up this exact date for this artist/store/
	// service - check whether anyone's waiting for it. Fires regardless of
	// who cancelled (artist or customer): either way, a real slot opened
	// up, and that's the only thing the waitlist cares about. Best-effort,
	// matching every other notification tonight - a failure here must
	// never fail the cancellation that already succeeded.
	waitlistDate := time.Date(b.StartTime.Year(), b.StartTime.Month(), b.StartTime.Day(), 0, 0, 0, 0, time.UTC)
	if err := s.repo.NotifyNextWaitlistEntry(ctx, b.ArtistID, b.StoreID, b.ServiceID, waitlistDate); err != nil {
		s.log.Error("failed to notify next waitlist entry - cancellation succeeded",
			zap.Error(err),
			zap.String("artist_id", b.ArtistID.String()),
			zap.String("service_id", b.ServiceID.String()),
		)
	}

	return toResponse(b), nil
}

// CompleteBooking marks a confirmed booking as completed.
// Only the artist can mark a booking as completed.
// JoinWaitlist adds a customer to the queue for a fully-booked (artist,
// store, service, date) combination. Public - no account, matching guest
// booking everywhere else. Identity is resolved by phone via the exact
// same CreateGuestUser lookup-or-create logic a guest booking already
// uses - reused directly rather than duplicated a third time, since it's
// already race-safe and de-duplicated (migration 014).
//
// Deliberately does NOT verify the slot is actually fully booked before
// allowing the join - the frontend only offers this option when a search
// already came back empty, so the practical risk of an unnecessary join is
// low, and adding that check here would couple this to the slot algorithm
// for marginal benefit. A reasonable simplification for the first version,
// not an oversight.
func (s *Service) JoinWaitlist(ctx context.Context, req JoinWaitlistRequest) (uuid.UUID, error) {
	if err := s.validate.Struct(req); err != nil {
		return uuid.Nil, mapValidationError(err)
	}

	artistID, err := uuid.Parse(req.ArtistID)
	if err != nil {
		return uuid.Nil, apperror.BadRequest("INVALID_ARTIST_ID", "Invalid artist ID")
	}
	storeID, err := uuid.Parse(req.StoreID)
	if err != nil {
		return uuid.Nil, apperror.BadRequest("INVALID_STORE_ID", "Invalid store ID")
	}
	serviceID, err := uuid.Parse(req.ServiceID)
	if err != nil {
		return uuid.Nil, apperror.BadRequest("INVALID_SERVICE_ID", "Invalid service ID")
	}
	date, err := time.Parse("2006-01-02", req.RequestedDate)
	if err != nil {
		return uuid.Nil, apperror.BadRequest("INVALID_DATE", "requested_date must be in YYYY-MM-DD format")
	}

	customerID, err := s.repo.CreateGuestUser(ctx, req.Name, req.Phone)
	if err != nil {
		return uuid.Nil, fmt.Errorf("join waitlist: resolve customer: %w", err)
	}

	entryID, err := s.repo.CreateWaitlistEntry(ctx, artistID, storeID, serviceID, customerID, date)
	if err != nil {
		return uuid.Nil, fmt.Errorf("join waitlist: %w", err)
	}
	return entryID, nil
}

// GetWaitlistByArtist returns an artist's active waitlist queue. Bearer
// the requester must resolve to this artist (or admin).
// assertArtistAccess verifies the caller is genuinely the artist whose data
// they're asking for. This is the ownership check that must guard EVERY
// endpoint taking an artist_id from the URL - RequireRole("artist","admin")
// only proves the caller is *an* artist, never that they are *that* artist.
// Its absence on the bookings-list and calendar endpoints was a real
// cross-tenant data leak (any registered artist could read any other
// artist's full booking book, customer phone numbers included).
//
// Admins are allowed through before the artist lookup, deliberately: an
// admin has no `artists` row at all, so resolving one would fail with
// ErrArtistNotFound and lock admins out of the very endpoints their role
// is supposed to reach.
//
// Wrong-artist and no-artist-row both return the same generic 403 - the
// response must not reveal whether the requested artist_id exists.
func (s *Service) assertArtistAccess(ctx context.Context, artistID, requesterUserID uuid.UUID, requesterRole string) error {
	if requesterRole == RoleAdmin {
		return nil
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return apperror.Forbidden("FORBIDDEN", "You do not have permission to view this artist's data")
		}
		return fmt.Errorf("assert artist access: resolve requester: %w", err)
	}
	if requesterArtistID != artistID {
		return apperror.Forbidden("FORBIDDEN", "You do not have permission to view this artist's data")
	}
	return nil
}

func (s *Service) GetWaitlistByArtist(ctx context.Context, artistID uuid.UUID, requesterUserID uuid.UUID, requesterRole string) ([]*WaitlistEntryResponse, error) {
	if err := s.assertArtistAccess(ctx, artistID, requesterUserID, requesterRole); err != nil {
		return nil, err
	}

	entries, err := s.repo.GetWaitlistByArtist(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("get waitlist by artist: %w", err)
	}
	return entries, nil
}

// CompleteBooking marks a confirmed booking as completed, generating the
// guest review-link token and enqueueing the review-request notification.
func (s *Service) CompleteBooking(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("complete booking: get booking: %w", err)
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to complete this booking")
		}
		return nil, fmt.Errorf("complete booking: resolve artist: %w", err)
	}

	if b.ArtistID != requesterArtistID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to complete this booking")
	}

	if b.Status != StatusConfirmed {
		return nil, apperror.Conflict("BOOKING_NOT_CONFIRMED", "Only confirmed bookings can be marked as completed")
	}

	// Mirrors ApproveBooking's and MarkNoShow's own start_time guard: a
	// service can't be "completed" before it has even started - without
	// this, an artist could mark a days-away booking complete and trigger
	// a review-request WhatsApp message for a service the customer hasn't
	// received yet.
	if b.StartTime.After(time.Now()) {
		return nil, apperror.Conflict("BOOKING_NOT_STARTED", "This booking's appointment time hasn't arrived yet, so it can't be marked as completed")
	}

	reviewToken, err := s.repo.CompleteBooking(ctx, bookingID)
	if err != nil {
		return nil, fmt.Errorf("complete booking: %w", err)
	}

	b.Status = StatusCompleted
	b.ReviewToken = &reviewToken

	customerName, serviceName, ctxErr := s.repo.GetBookingNotificationContext(ctx, bookingID)
	if ctxErr == nil {
		reviewURL := fmt.Sprintf("%s/review/%s", customerPWAURL, reviewToken)
		message := fmt.Sprintf(
			"Hi %s! Thanks for booking your %s with us. We'd love to hear how it went - leave a quick review here: %s",
			customerName, serviceName, reviewURL,
		)
		s.enqueueNotification(ctx, bookingID, b.CustomerID, "review_request", message)
	}

	return toResponse(b), nil
}

// MarkNoShow marks a confirmed booking as no_show.
// Only the artist can mark a no-show.
func (s *Service) MarkNoShow(ctx context.Context, bookingID uuid.UUID, requesterUserID uuid.UUID) (*BookingResponse, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		return nil, fmt.Errorf("mark no show: get booking: %w", err)
	}

	requesterArtistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to act on this booking")
		}
		return nil, fmt.Errorf("mark no show: resolve artist: %w", err)
	}

	if b.ArtistID != requesterArtistID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to act on this booking")
	}

	if b.Status != StatusConfirmed {
		return nil, apperror.Conflict("BOOKING_NOT_CONFIRMED", "Only confirmed bookings can be marked as no-show")
	}

	// Mirrors ApproveBooking's own start_time guard: a customer can't be a
	// "no-show" for an appointment that hasn't happened yet - without this,
	// an artist could mark a booking no-show days in advance, before the
	// customer ever had the chance to show up.
	if b.StartTime.After(time.Now()) {
		return nil, apperror.Conflict("BOOKING_NOT_STARTED", "This booking's appointment time hasn't arrived yet, so it can't be marked as no-show")
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

// customerPWAURL is the base URL for the guest-facing app, used to build
// links (currently just the review link) sent to a customer over WhatsApp.
// Read once at package init rather than per-call, matching the pattern
// businessLocation uses in the earnings domain. Falls back to localhost
// harmless in development, and a wrong-but-obvious value in production if
// CUSTOMER_PWA_URL is ever forgotten, rather than a silent empty string
// that would produce a broken link with no indication why.
var customerPWAURL = func() string {
	if v := os.Getenv("CUSTOMER_PWA_URL"); v != "" {
		return v
	}
	return "http://localhost:4200"
}()

// enqueueNotification is a best-effort wrapper around
// repo.EnqueueNotification: a queuing failure must never fail the booking
// operation that already succeeded (the artist approved the booking; that's
// done regardless of whether a WhatsApp message got queued for it). Service
// has no logger yet - swallowing the error here rather than silently
// dropping it entirely still isn't ideal, but is the honest state of things
// until a logger is threaded through this layer. Tracked, not hidden.
func (s *Service) enqueueNotification(ctx context.Context, bookingID, userID uuid.UUID, templateName, message string) {
	// Still best-effort - a queuing failure must never fail the booking
	// operation that already succeeded. But it is now LOGGED: previously a
	// persistently broken notification queue was completely invisible,
	// which is how "the customer never got their WhatsApp" becomes an
	// unexplainable support ticket instead of an alert.
	if err := s.repo.EnqueueNotification(ctx, &bookingID, userID, templateName, message); err != nil {
		s.log.Error("failed to enqueue notification - booking succeeded, message will not be sent",
			zap.Error(err),
			zap.String("booking_id", bookingID.String()),
			zap.String("user_id", userID.String()),
			zap.String("template", templateName),
		)
	}
}

// notificationTimeLabel formats a booking's start time for a WhatsApp
// message in Beirut local time - matching every other customer-facing time
// display in the app. Hardcoded for the same reason as businessLocation in
// the earnings domain: every store is currently in Lebanon.
func notificationTimeLabel(startTime time.Time) string {
	loc, err := time.LoadLocation("Asia/Beirut")
	if err != nil {
		loc = time.UTC
	}
	return startTime.In(loc).Format("Mon, 2 Jan · 3:04 PM")
}

// isEarlyBirdSlot reports whether startTime falls before the store's
// early-bird cutoff on its own calendar day. Uses the exact same comparison
// GetAvailableSlots uses to flag a slot with the early-bird badge for the
// picker UI - the two call sites must agree, or a slot shown with the badge
// could be booked without the surcharge actually being charged.
//
// The cutoff is a wall-clock LOCAL time in the store's zone ("09:00:00" means
// 9am where the salon physically is). The calendar day is therefore also the
// store's local day, not the UTC day - at 23:00 UTC it is already tomorrow in
// Beirut, and comparing against the wrong day's cutoff is off by 24 hours.
func isEarlyBirdSlot(store *Store, startTime time.Time) bool {
	if store == nil || store.EarlyBirdCutoff == nil {
		return false
	}
	loc := storeLocation(store)
	local := startTime.In(loc)
	date := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)

	cutoff, err := parseStoreTimeIn(date, *store.EarlyBirdCutoff, loc)
	if err != nil {
		return false
	}
	return startTime.Before(cutoff)
}

// storeLocation resolves a store's IANA timezone to a *time.Location,
// tolerating a nil store.
//
// A thin adapter over openinghours.Location, which owns the fallback
// behaviour and documents it.
func storeLocation(store *Store) *time.Location {
	if store == nil {
		return time.UTC
	}
	return openinghours.Location(store.Timezone)
}

// parseStoreTimeIn parses a PostgreSQL TIME string (e.g. "09:00:00") and
// combines it with a date to produce an instant in the given location.
//
// A thin adapter over openinghours.ParseTimeIn, kept so the two remaining
// early-bird call sites read the same as they did before the extraction.
func parseStoreTimeIn(date time.Time, timeStr string, loc *time.Location) (time.Time, error) {
	return openinghours.ParseTimeIn(date, timeStr, loc)
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
