// holds.go — the guest hold: a slot reserved for a few minutes while someone
// finishes typing their name.
//
// Split out of service.go on 2026-09-06. Grouped with expiry rather than with
// the booking lifecycle because a hold is not a booking yet, and because
// expiry is what makes holds safe to hand out — abandoned holds self-heal
// lazily on read rather than needing a scheduler.
package booking

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

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
	// The span actually reserved on the calendar: the appointment plus any
	// cleanup. end_time stays the customer-facing finish; blocked_until is
	// what the exclusion constraint ranges over. Both the buffer and the
	// span are snapshotted, so changing the service's buffer later cannot
	// re-plan a booking already agreed. See migration 033.
	blockedUntil := endTime.Add(time.Duration(service.BufferMin) * time.Minute)
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
		BlockedUntil:   blockedUntil.UTC(),
		BufferMin:      service.BufferMin,
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
			return nil, errBookingNotFound()
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

// ReleaseExpiredHolds releases all held bookings whose 10-minute window
// has passed. Called by the background job every minute.
func (s *Service) ReleaseExpiredHolds(ctx context.Context) (int64, error) {
	freed, err := s.repo.ReleaseExpiredHolds(ctx)
	if err != nil {
		return 0, fmt.Errorf("release expired holds: %w", err)
	}
	s.cascadeFreedSlots(ctx, freed, "hold_expired")
	return int64(len(freed)), nil
}

// ExpireDeadlineBookings expires all approved bookings whose deposit
// deadline has passed. Called by the background job every minute.
func (s *Service) ExpireDeadlineBookings(ctx context.Context) (int64, error) {
	freed, err := s.repo.ExpireDeadlineBookings(ctx)
	if err != nil {
		return 0, fmt.Errorf("expire deadline bookings: %w", err)
	}
	s.cascadeFreedSlots(ctx, freed, "deposit_deadline_lapsed")
	return int64(len(freed)), nil
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

// apiPublicURL is where THIS server is reachable from a customer's phone.
//
// Distinct from customerPWAURL on purpose: the calendar link is served by
// the API itself (internal/calendar, GET /c/:token), not by the PWA, so
// reusing the PWA base would produce a link to a route the frontend does
// not have. Same fallback reasoning - a visibly wrong localhost beats a
// silent empty string that yields "/c/abc" with no host.
var apiPublicURL = func() string {
	if v := os.Getenv("API_PUBLIC_URL"); v != "" {
		return strings.TrimSuffix(v, "/")
	}
	if port := os.Getenv("PORT"); port != "" {
		return "http://localhost:" + port
	}
	return "http://localhost:3000"
}()
