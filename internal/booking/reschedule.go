package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// maxReschedules caps how many times one booking may be moved.
//
// Unlimited rescheduling is its own abuse: a slot held and moved repeatedly is
// a slot nobody else can book, with no deposit ever at risk. Two is enough for
// the honest case - something came up, then something came up again - and
// short of a pattern. A client who needs a third can cancel, which is the
// existing behaviour they are no longer forced into.
const maxReschedules = 2

// RescheduleBookingRequest moves an existing booking.
//
// Only the time changes. Service, store and artist are deliberately immovable:
// changing any of them is a different appointment at a different price, and
// letting it happen under "reschedule" would quietly bypass the deposit that
// was agreed for the original.
type RescheduleBookingRequest struct {
	StartTime string `json:"start_time" validate:"required"`
}

// RescheduleBooking moves a client's appointment to a new time.
//
// # WHY THIS EXISTS
//
// A client who needed a different time had one option: cancel and rebook. That
// loses the slot to whoever books it first and, inside 24 hours, forfeits the
// deposit - so the product punished someone for the ordinary act of having
// something come up.
//
// # WHAT IT DELIBERATELY DOES NOT DO
//
// It does not reset the status to pending. The artist already accepted this
// client for this service; only the time moved, and the new time came from the
// artist's own published availability. Sending it back for re-approval would
// strand a paid deposit in limbo over a change the artist had already agreed
// to in principle.
//
// It does not re-price. The deposit and the agreed price travel with the
// booking. A reschedule that silently re-ran the early-bird surcharge would
// change what someone owes for moving an appointment, which nobody expects.
func (s *Service) RescheduleBooking(ctx context.Context, bookingID, requesterUserID uuid.UUID, req RescheduleBookingRequest) (*BookingResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	newStart, err := time.Parse(time.RFC3339, req.StartTime)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_START_TIME", "Start time must be RFC3339")
	}
	newStart = newStart.UTC()

	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("reschedule: get booking: %w", err)
	}

	// A booking belonging to someone else is reported exactly as a missing
	// one. Anything else lets a caller confirm a booking id is real.
	if b.CustomerID != requesterUserID {
		return nil, errBookingNotFound()
	}

	if !isMovableStatus(b.Status) {
		return nil, apperror.Conflict("NOT_MOVABLE",
			"This booking can no longer be moved")
	}
	if b.RescheduleCount >= maxReschedules {
		return nil, apperror.Conflict("RESCHEDULE_LIMIT",
			"This booking has already been moved twice. Please cancel and book again.")
	}
	if !newStart.After(time.Now().UTC()) {
		return nil, apperror.BadRequest("START_IN_PAST", "Choose a time in the future")
	}

	// The new time must be one the artist is genuinely offering. Rather than
	// re-deriving opening hours, notice periods, buffers and travel time here -
	// a second implementation free to disagree with the first - this asks the
	// slot generator, excluding THIS booking from the artist's occupancy so it
	// cannot block its own move.
	slots, err := s.GetAvailableSlots(ctx, GetAvailableSlotsRequest{
		ArtistID:         b.ArtistID.String(),
		StoreID:          b.StoreID.String(),
		ServiceID:        b.ServiceID.String(),
		Date:             newStart.Format("2006-01-02"),
		excludeBookingID: &b.ID,
	})
	if err != nil {
		return nil, err
	}
	if !slotOffered(slots, newStart) {
		return nil, apperror.Conflict("SLOT_UNAVAILABLE",
			"That time is no longer available. Please pick another.")
	}

	// Durations travel with the booking: the service has not changed, so the
	// appointment is exactly as long as it was and blocks exactly as much
	// afterwards.
	duration := b.EndTime.Sub(b.StartTime)
	tail := b.BlockedUntil.Sub(b.EndTime)
	newEnd := newStart.Add(duration)

	rows, err := s.repo.RescheduleBooking(ctx, b.ID, newStart, newEnd, newEnd.Add(tail), maxReschedules)
	if err != nil {
		return nil, err
	}
	if rows == 0 {
		// The guards in the UPDATE refused. Either the booking stopped being
		// movable or the count was exhausted between the checks above and the
		// write - the race the single statement exists to lose safely.
		return nil, apperror.Conflict("NOT_MOVABLE", "This booking can no longer be moved")
	}

	// Best-effort, matching every other notification in this service: failing
	// to tell the artist must not undo a move that already committed.
	// Addressed to the ARTIST, resolved from the booking. The client already
	// knows - they just did it.
	if artistUserID, uErr := s.repo.GetArtistUserID(ctx, b.ArtistID); uErr == nil {
		_ = s.repo.EnqueueNotification(ctx, &b.ID, artistUserID, "booking_rescheduled",
			fmt.Sprintf("A client moved their appointment to %s.",
				newStart.Format("Mon 2 Jan at 3:04pm")))
	}

	updated, err := s.repo.GetBookingByID(ctx, b.ID)
	if err != nil {
		return nil, fmt.Errorf("reschedule: reload: %w", err)
	}
	return toResponse(updated), nil
}

// isMovableStatus reports whether a booking is still something to move.
//
// Narrower than BlockingStatuses: a HELD booking is an unconfirmed guest hold
// that expires on its own, and moving one would extend a reservation nobody
// has committed to.
func isMovableStatus(status string) bool {
	switch status {
	case StatusPending, StatusApproved, StatusDepositPaid, StatusConfirmed:
		return true
	}
	return false
}

// slotOffered reports whether the generator offered exactly this start time.
//
// Exact equality, not a window. The generator emits slots on a fixed grid, so
// anything else is a time the artist was never offering - and accepting "close
// enough" is how an appointment ends up starting at 10:03.
func slotOffered(slots []*TimeSlot, start time.Time) bool {
	for _, sl := range slots {
		if sl.StartTime.UTC().Equal(start) {
			return true
		}
	}
	return false
}
