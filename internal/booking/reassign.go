package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

// SalonArtist is one artist of a salon, as the reassignment needs her.
type SalonArtist struct {
	ArtistID uuid.UUID
	UserID   uuid.UUID
	Name     string
}

// ReassignRequest moves a booking to another artist of the same salon.
type ReassignRequest struct {
	ArtistID string `json:"artist_id" validate:"required,uuid"`
}

// Why an artist cannot take a booking - shown to the owner, never hidden.
const (
	ReasonNotOffered   = "NOT_OFFERED"   // she does not offer this service
	ReasonNotAtStore   = "NOT_AT_STORE"  // she does not work at this location
	ReasonNotAccepting = "NOT_ACCEPTING" // not approved, or her subscription stops new bookings
	ReasonNotFree      = "NOT_FREE"      // off, busy, or could not travel there in time
)

// ReassignCandidate is one artist the owner could move a booking to.
type ReassignCandidate struct {
	ArtistID uuid.UUID `json:"artist_id"`
	Name     string    `json:"name"`
	Eligible bool      `json:"eligible"`
	// Reason is one of the Reason* constants when Eligible is false.
	Reason string `json:"reason,omitempty"`
	// Price is her price for this service, so the owner sees a difference;
	// the client keeps what she agreed either way. Zero when she does not
	// offer it.
	Price decimal.Decimal `json:"price"`
}

// reassignableStatuses are the bookings still to happen that a person is
// committed to. A held slot is a client mid-checkout, not a booking to hand
// over; finished ones are history.
var reassignableStatuses = map[string]bool{
	StatusPending: true, StatusApproved: true, StatusDepositPaid: true, StatusConfirmed: true,
}

// ReassignCandidates is every other artist of the booking's salon, each
// either able to take it or with the reason she cannot. Owner only
// (salonrole.BookingsAnyWrite): anyone else is told the booking does not
// exist, as for every booking action.
func (s *Service) ReassignCandidates(ctx context.Context, bookingID, requesterUserID uuid.UUID) ([]ReassignCandidate, error) {
	b, err := s.reassignableBooking(ctx, bookingID, requesterUserID)
	if err != nil {
		return nil, err
	}
	roster, err := s.repo.SalonArtists(ctx, b.SalonID)
	if err != nil {
		return nil, fmt.Errorf("reassign candidates: %w", err)
	}

	out := make([]ReassignCandidate, 0, len(roster))
	for _, a := range roster {
		if a.ArtistID == b.ArtistID {
			continue
		}
		price, _, reason, err := s.canTake(ctx, b, a.ArtistID)
		if err != nil {
			return nil, fmt.Errorf("reassign candidates: %w", err)
		}
		out = append(out, ReassignCandidate{
			ArtistID: a.ArtistID, Name: a.Name, Eligible: reason == "", Reason: reason, Price: price,
		})
	}
	return out, nil
}

// ReassignBooking hands a booking to another artist of the salon.
//
// The client keeps the price she agreed, whatever the new artist charges
// (founder decision, 2026-10-10); the deposit stays where it was paid, the
// salon's account. What changes is who does the work and whose earnings the
// booking counts under - recorded in the activity log with both names, and
// told to the client and to both artists (not to the owner, who did it).
func (s *Service) ReassignBooking(ctx context.Context, bookingID, requesterUserID uuid.UUID, req ReassignRequest) (*BookingResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}
	to, err := uuid.Parse(req.ArtistID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_ARTIST_ID", "artist_id must be a UUID")
	}

	b, err := s.reassignableBooking(ctx, bookingID, requesterUserID)
	if err != nil {
		return nil, err
	}
	if to == b.ArtistID {
		return nil, apperror.BadRequest("SAME_ARTIST", "The booking is already with this artist")
	}

	roster, err := s.repo.SalonArtists(ctx, b.SalonID)
	if err != nil {
		return nil, fmt.Errorf("reassign: roster: %w", err)
	}
	names := make(map[uuid.UUID]SalonArtist, len(roster))
	for _, a := range roster {
		names[a.ArtistID] = a
	}
	target, inSalon := names[to]
	if !inSalon {
		return nil, errNotEligible(ReasonNotOffered)
	}

	_, store, reason, err := s.canTake(ctx, b, to)
	if err != nil {
		return nil, fmt.Errorf("reassign: %w", err)
	}
	if reason != "" {
		return nil, errNotEligible(reason)
	}
	travel, err := s.travelFromStore(ctx, to, store, b.StartTime)
	if err != nil {
		return nil, fmt.Errorf("reassign: %w", err)
	}

	from := b.ArtistID
	rows, err := s.repo.ReassignBooking(ctx, b.ID, from, to, travel)
	if errors.Is(err, ErrSlotUnavailable) {
		return nil, errNotEligible(ReasonNotFree)
	}
	if err != nil {
		return nil, fmt.Errorf("reassign: %w", err)
	}
	if rows == 0 {
		// Between the checks and the write the booking moved, finished or
		// went to someone else. Nothing was changed.
		return nil, errNotReassignable()
	}
	b.ArtistID = to

	salon := b.SalonID
	facts := bookingFacts(b)
	facts["artist_name"] = target.Name
	audit.Record(ctx, s.activity, audit.Event{
		SalonID: &salon, EntityType: audit.EntityBooking, EntityID: b.ID, Action: audit.ActionBookingReassign,
		OldValues: map[string]any{"artist_id": from, "artist_name": names[from].Name},
		NewValues: facts,
	})
	s.announceReassignment(ctx, b, names[from], target, requesterUserID)
	return toResponse(b), nil
}

// reassignableBooking loads a booking the caller may hand over: she owns its
// salon, and it is still to happen.
func (s *Service) reassignableBooking(ctx context.Context, bookingID, requesterUserID uuid.UUID) (*Booking, error) {
	b, err := s.repo.GetBookingByID(ctx, bookingID)
	if err != nil {
		if errors.Is(err, ErrBookingNotFound) {
			return nil, errBookingNotFound()
		}
		return nil, fmt.Errorf("reassign: get booking: %w", err)
	}
	if !salonHolds(ctx, requesterUserID, b.SalonID, salonrole.BookingsAnyWrite) {
		return nil, errBookingNotFound()
	}
	if !reassignableStatuses[b.Status] || !b.StartTime.After(time.Now()) {
		return nil, errNotReassignable()
	}
	return b, nil
}

// canTake asks of artistID exactly what a fresh booking would: approved and
// accepting bookings, offers the service, works at the store, and is offered
// this very time by the slot list (hours, rota, other bookings, travel).
// reason is empty when she can; price is hers for the service.
func (s *Service) canTake(ctx context.Context, b *Booking, artistID uuid.UUID) (decimal.Decimal, *Store, string, error) {
	if err := s.checkArtistAcceptsNewBookings(ctx, artistID); err != nil {
		if code := appCode(err); code == "ARTIST_NOT_ACCEPTING_BOOKINGS" {
			return decimal.Zero, nil, ReasonNotAccepting, nil
		}
		return decimal.Zero, nil, "", err
	}
	service, store, err := s.validateBookingParties(ctx, artistID, b.StoreID, b.ServiceID)
	if err != nil {
		switch appCode(err) {
		case "SERVICE_NOT_FOUND":
			return decimal.Zero, nil, ReasonNotOffered, nil
		case "STORE_NOT_FOUND":
			return decimal.Zero, nil, ReasonNotAtStore, nil
		}
		return decimal.Zero, nil, "", err
	}
	if err := s.assertOffered(ctx, artistID, b.StoreID, b.ServiceID, b.StartTime, store); err != nil {
		if appCode(err) == "SLOT_UNAVAILABLE" {
			return service.Price, store, ReasonNotFree, nil
		}
		return decimal.Zero, nil, "", err
	}
	return service.Price, store, "", nil
}

// announceReassignment tells the client and both artists. Best-effort, like
// every notification here: the move has happened whether or not a message
// could be queued. The owner who did it is not told.
func (s *Service) announceReassignment(ctx context.Context, b *Booking, from, to SalonArtist, ownerUserID uuid.UUID) {
	client, service, err := s.repo.GetBookingNotificationContext(ctx, b.ID)
	if err != nil {
		return
	}
	when := notificationTimeLabel(b.StartTime)
	s.enqueueNotification(ctx, b.ID, b.CustomerID, "booking_reassigned",
		fmt.Sprintf("Hi %s! Your %s on %s will now be with %s. Everything else stays the same.", client, service, when, to.Name))
	if to.UserID != ownerUserID {
		s.enqueueNotification(ctx, b.ID, to.UserID, "booking_reassigned_to_you",
			fmt.Sprintf("%s's %s on %s has been moved to you.", client, service, when))
	}
	if from.UserID != uuid.Nil && from.UserID != ownerUserID {
		s.enqueueNotification(ctx, b.ID, from.UserID, "booking_reassigned_away",
			fmt.Sprintf("%s's %s on %s is now with %s.", client, service, when, to.Name))
	}
}

func errNotReassignable() error {
	return apperror.Conflict("BOOKING_NOT_REASSIGNABLE", "Only an upcoming booking can be moved to another artist")
}

// errNotEligible is the refusal for a target who cannot take the booking.
// NOT_FREE is its own code, because the owner's next move differs: someone
// else, or another time.
func errNotEligible(reason string) error {
	if reason == ReasonNotFree {
		return apperror.Conflict("ARTIST_NOT_FREE", "That artist is not free at this time")
	}
	msg := map[string]string{
		ReasonNotOffered:   "That artist does not offer this service",
		ReasonNotAtStore:   "That artist does not work at this location",
		ReasonNotAccepting: "That artist is not taking bookings right now",
	}[reason]
	return apperror.Conflict("ARTIST_NOT_ELIGIBLE", msg)
}

// appCode is the AppError code of err, or "".
func appCode(err error) string {
	var ae *apperror.AppError
	if errors.As(err, &ae) {
		return ae.Code
	}
	return ""
}
