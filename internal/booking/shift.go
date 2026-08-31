package booking

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// This file holds the pure evaluation half of bulk schedule shifting: given
// a day's bookings and the constraints around them, decide what would move
// and what would break.
//
// Kept free of the repository and the clock on purpose. Every rule here is
// a judgement about someone's working day - "this would run past closing",
// "you would not make it between branches" - and each deserves a test that
// does not need a database to express.

// shiftInput is everything evaluateShift needs, gathered by the caller.
type shiftInput struct {
	// SameStore are the artist's bookings at the store being shifted.
	SameStore []*EnrichedBooking
	// CrossStore are the artist's bookings at OTHER stores that day. They
	// do not move, but they constrain how far these ones can.
	CrossStore []*EnrichedBooking
	// OpenAt/CloseAt bound the trading day. StoreOpen false means the store
	// does not trade that date at all.
	OpenAt    time.Time
	CloseAt   time.Time
	StoreOpen bool
	// BufferFor returns the required travel minutes between a booking at
	// otherStoreID and one at the store being shifted. Injected as a
	// function because the real lookup is per-pair and hits the database.
	BufferFor func(otherStoreID uuid.UUID) int
	// ShiftMinutes may be negative.
	ShiftMinutes int
	// Now is passed rather than read so tests are deterministic.
	Now time.Time
}

// evaluateShift performs the dry run.
//
// Returns every booking that would move, everything deliberately skipped,
// and every reason the shift cannot proceed. It never stops at the first
// blocker: an artist adjusting a schedule needs the whole list, not one
// problem at a time.
func evaluateShift(in shiftInput) *ShiftPreviewResponse {
	delta := time.Duration(in.ShiftMinutes) * time.Minute

	resp := &ShiftPreviewResponse{
		ShiftMinutes: in.ShiftMinutes,
		Movable:      []ShiftPreviewItem{},
		Skipped:      []ShiftSkipped{},
		Blockers:     []ShiftBlocker{},
	}

	// A store that does not trade that day blocks outright. Shifting
	// bookings around inside a closed day is not a meaningful operation,
	// and the times to validate against do not exist.
	if !in.StoreOpen {
		resp.Blockers = append(resp.Blockers, ShiftBlocker{
			Reason: BlockerStoreClosed,
			Detail: "The store is not open on this date, so there are no opening hours to shift within.",
		})
		return resp
	}

	var moving []*EnrichedBooking
	for _, b := range in.SameStore {
		if !isMovable(b.Status) {
			resp.Skipped = append(resp.Skipped, ShiftSkipped{
				BookingID: b.ID, Reason: SkipTerminalStatus, Status: b.Status,
			})
			continue
		}
		// A booking already under way is out of scope regardless of the
		// direction of the shift.
		if !b.StartTime.After(in.Now) {
			resp.Skipped = append(resp.Skipped, ShiftSkipped{
				BookingID: b.ID, Reason: SkipInProgress, Status: b.Status,
			})
			continue
		}
		moving = append(moving, b)
	}

	for _, b := range moving {
		newStart := b.StartTime.Add(delta)
		newEnd := b.EndTime.Add(delta)

		if newEnd.After(in.CloseAt) {
			id := b.ID
			resp.Blockers = append(resp.Blockers, ShiftBlocker{
				Reason: BlockerPastClosing, BookingID: &id,
				Detail: fmt.Sprintf("%s would finish at %s, after the store closes at %s.",
					serviceLabel(b), localHM(newEnd, in.CloseAt), localHM(in.CloseAt, in.CloseAt)),
			})
		}
		if newStart.Before(in.OpenAt) {
			id := b.ID
			resp.Blockers = append(resp.Blockers, ShiftBlocker{
				Reason: BlockerBeforeOpening, BookingID: &id,
				Detail: fmt.Sprintf("%s would start at %s, before the store opens at %s.",
					serviceLabel(b), localHM(newStart, in.OpenAt), localHM(in.OpenAt, in.OpenAt)),
			})
		}
		// Only meaningful for a negative shift, but checked unconditionally
		// so a clock skew or a stale request cannot slip a booking into the
		// past unnoticed.
		if !newStart.After(in.Now) {
			id := b.ID
			resp.Blockers = append(resp.Blockers, ShiftBlocker{
				Reason: BlockerIntoPast, BookingID: &id,
				Detail: fmt.Sprintf("%s would move to a time that has already passed.", serviceLabel(b)),
			})
		}

		// Travel buffers. The artist must be able to physically get between
		// branches; a shift that erases that time produces a schedule that
		// looks fine and cannot be worked.
		for _, other := range in.CrossStore {
			if !isMovable(other.Status) {
				continue
			}
			need := time.Duration(in.BufferFor(other.StoreID)) * time.Minute
			if gap, ok := gapBetween(newStart, newEnd, other.StartTime, other.EndTime); ok && gap < need {
				id := b.ID
				resp.Blockers = append(resp.Blockers, ShiftBlocker{
					Reason: BlockerTravelBuffer, BookingID: &id,
					Detail: fmt.Sprintf(
						"%s would leave %d minutes to travel to your other branch, but %d are needed.",
						serviceLabel(b), int(gap.Minutes()), int(need.Minutes())),
				})
			}
		}

		resp.Movable = append(resp.Movable, ShiftPreviewItem{
			BookingID:    b.ID,
			CustomerName: customerLabel(b),
			ServiceName:  serviceLabel(b),
			CurrentStart: b.StartTime,
			NewStart:     newStart,
			NewEnd:       newEnd,
			Status:       b.Status,
			HasPhone:     hasPhone(b),
		})
		if hasPhone(b) {
			resp.NotifiableCount++
		}
	}

	resp.CanApply = len(resp.Blockers) == 0 && len(resp.Movable) > 0
	return resp
}

// gapBetween returns the free time between two intervals, and whether they
// are disjoint at all.
//
// ok is false when the two overlap, which is a harder failure than a short
// buffer - but one the overlap constraint already owns, so this reports it
// as a zero gap rather than duplicating that check.
func gapBetween(aStart, aEnd, bStart, bEnd time.Time) (time.Duration, bool) {
	switch {
	case !aEnd.After(bStart): // a finishes before b starts
		return bStart.Sub(aEnd), true
	case !bEnd.After(aStart): // b finishes before a starts
		return aStart.Sub(bEnd), true
	default:
		return 0, true // overlapping: zero travel time available
	}
}

// localHM formats an instant as HH:MM in the same location as ref.
//
// Times are shown to an artist standing in their own salon, so they must
// read in the store's zone, never the server's.
func localHM(t, ref time.Time) string {
	return t.In(ref.Location()).Format("15:04")
}

// hasPhone reports whether this customer can be notified at all. False
// means someone who has to be telephoned by hand after a bulk change.
func hasPhone(b *EnrichedBooking) bool {
	return b.CustomerPhone != nil && *b.CustomerPhone != ""
}

// customerLabel and serviceLabel degrade to a generic word rather than an
// empty string. A preview line reading "Appointment would finish after
// closing" is still actionable; a blank one is not.
func customerLabel(b *EnrichedBooking) string {
	if b.CustomerName == "" {
		return "Customer"
	}
	return b.CustomerName
}

func serviceLabel(b *EnrichedBooking) string {
	if b.ServiceName == "" {
		return "Appointment"
	}
	return b.ServiceName
}
