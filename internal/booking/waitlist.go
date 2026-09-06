// waitlist.go — the queue for a slot that is already taken, and what happens
// when one frees up.
//
// Split out of service.go on 2026-09-06. The lazy cascade lives here; the
// background sweep that unstalls a queue nobody cascaded is in
// waitlist_worker.go, which was already separate and explains why a timer is
// unavoidable for that one case.
package booking

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

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

// cascadeFreedSlots runs the waitlist cascade for every slot an expiry
// sweep released.
//
// The sweeps run lazily on the read path of GetAvailableSlots, so this fires
// during a read - consistent with the sweeps themselves already mutating
// there, and it only does anything when rows genuinely changed. An empty
// slice is the overwhelmingly common case and costs one length check.
func (s *Service) cascadeFreedSlots(ctx context.Context, freed []FreedSlot, reason string) {
	for _, f := range freed {
		s.cascadeWaitlist(ctx, &Booking{
			ArtistID:  f.ArtistID,
			StoreID:   f.StoreID,
			ServiceID: f.ServiceID,
			StartTime: f.StartTime,
		}, reason)
	}
}

// cascadeWaitlist tells the next person in line that a slot opened.
//
// Called from every event that frees time on an artist's calendar, not just
// cancellation. Until now `cancelled` was the ONLY caller, which left three
// silent gaps - a no-show, a lapsed hold or deposit deadline, and (since
// migration 033) an appointment finishing early and handing its cleanup
// back. In every one of those a real slot opens and nobody waiting was ever
// told, so the queue simply stalled until an unrelated cancellation
// happened to occur for the same artist, store, service and date.
//
// `reason` is for the log only. It costs nothing and it is the difference
// between "the waitlist fired" and knowing which path fired it, which is
// most of the work when this misbehaves in production.
//
// Best-effort on purpose, like every other notification in this service: a
// failure here must never fail the operation that already succeeded. The
// booking was cancelled, or completed, or marked no-show - undoing that
// because a queue lookup failed would be strictly worse than a missed
// notification.
func (s *Service) cascadeWaitlist(ctx context.Context, b *Booking, reason string) {
	if b == nil {
		return
	}
	// The waitlist is keyed to a DATE, not an instant - someone waiting for
	// "a slot on the 14th" does not care which hour opened up.
	date := time.Date(b.StartTime.Year(), b.StartTime.Month(), b.StartTime.Day(),
		0, 0, 0, 0, time.UTC)

	if err := s.repo.NotifyNextWaitlistEntry(ctx, b.ArtistID, b.StoreID, b.ServiceID, date); err != nil {
		s.log.Error("failed to notify next waitlist entry - the freeing operation still succeeded",
			zap.Error(err),
			zap.String("reason", reason),
			zap.String("artist_id", b.ArtistID.String()),
			zap.String("service_id", b.ServiceID.String()),
		)
	}
}
