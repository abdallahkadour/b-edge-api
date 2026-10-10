package booking

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
)

// WithAudit gives the service the salon activity log (2026-10-10).
//
// Every change to a booking - approved, deposit confirmed, refunded,
// cancelled, completed, no-show, moved - is then written to audit_events
// under the BOOKING's salon, naming who made it (internal/pkg/caller), so
// the owner's Activity screen can answer "who did that?". Without it the
// service records nothing, which is what every unit test that does not ask
// about the log gets.
func (s *Service) WithAudit(l audit.Logger) *Service {
	s.activity = l
	return s
}

// recordTransition writes one status change. Called only after the write
// has committed, so a refused or failed change leaves no row claiming it
// happened.
func (s *Service) recordTransition(ctx context.Context, b *Booking, action, from string) {
	salon := b.SalonID
	audit.Record(ctx, s.activity, audit.Event{
		SalonID: &salon, EntityType: audit.EntityBooking, EntityID: b.ID, Action: action,
		OldValues: map[string]any{"status": from},
		NewValues: bookingFacts(b),
	})
}

// recordMove writes a reschedule: the time it was, and the booking as it is.
func (s *Service) recordMove(ctx context.Context, b *Booking, from time.Time) {
	salon := b.SalonID
	audit.Record(ctx, s.activity, audit.Event{
		SalonID: &salon, EntityType: audit.EntityBooking, EntityID: b.ID, Action: audit.ActionBookingReschedule,
		OldValues: map[string]any{"start_time": from},
		NewValues: bookingFacts(b),
	})
}

// recordShift writes one row for a whole-day shift rather than one per
// booking: it was one decision, and a feed of six identical moves would bury
// it. The salon is the caller's - an artist shifts her own day.
func (s *Service) recordShift(ctx context.Context, artistID uuid.UUID, date string, minutes, moved int) {
	audit.Record(ctx, s.activity, audit.Event{
		EntityType: audit.EntityArtist, EntityID: artistID, Action: audit.ActionBookingShiftDay,
		NewValues: map[string]any{"date": date, "minutes": minutes, "moved": moved},
	})
}

// bookingFacts is what the activity row keeps about the booking: enough to
// say whose appointment it was, when and for how much, as it stood at the
// moment of the change. Names are joined at read time.
func bookingFacts(b *Booking) map[string]any {
	return map[string]any{
		"status":         b.Status,
		"artist_id":      b.ArtistID,
		"start_time":     b.StartTime,
		"final_price":    b.FinalPrice,
		"deposit_amount": b.DepositAmount,
	}
}
