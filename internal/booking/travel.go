package booking

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// travelMinutes is the time the artist needs between a booking at store a and
// one at store b on localDay (the store's own calendar date).
//
// ONE rule for slot generation, the day shift and the write-path check. Until
// 2026-10-10 it was written out in slots.go and shift.go, and both took the
// REQUESTED store's default and looked the artist's override up in one
// direction - so whether a 10:00 in Beirut and an 11:30 in Tripoli could both
// stand depended on which was booked first (audit T4, measured live).
//
// Symmetric by construction: an override for the pair in EITHER direction
// wins, the longer if both exist; otherwise the longer of the two stores'
// defaults for that day. The road is the same both ways.
func (s *Service) travelMinutes(ctx context.Context, artistID uuid.UUID, a, b *Store, localDay time.Time) (int, error) {
	weekend := !weekdays[localDay.Weekday()]
	pick := func(weekday, weekendMin int) int {
		if weekend {
			return weekendMin
		}
		return weekday
	}

	override := -1
	for _, dir := range [2][2]uuid.UUID{{a.ID, b.ID}, {b.ID, a.ID}} {
		o, err := s.repo.GetArtistStoreBuffer(ctx, artistID, dir[0], dir[1])
		if err != nil {
			return 0, fmt.Errorf("travel minutes: %w", err)
		}
		if o != nil {
			override = max(override, pick(o.WeekdayBufferMin, o.WeekendBufferMin))
		}
	}
	if override >= 0 {
		return override, nil
	}
	return max(pick(a.WeekdayBufferMin, a.WeekendBufferMin), pick(b.WeekdayBufferMin, b.WeekendBufferMin)), nil
}

// travelFromStore is the minutes needed between store and every OTHER store
// the artist works at, on the store's calendar day of start. Handed to the
// repository with a booking being written, so the transaction can refuse a
// time that leaves her too little road (lockArtistAndCheckTravel).
func (s *Service) travelFromStore(ctx context.Context, artistID uuid.UUID, store *Store, start time.Time) (map[uuid.UUID]int, error) {
	ids, err := s.repo.ArtistStoreIDs(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("travel from store: artist stores: %w", err)
	}
	local := start.In(storeLocation(store))
	day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)

	out := make(map[uuid.UUID]int, len(ids))
	for _, id := range ids {
		if id == store.ID {
			continue
		}
		other, err := s.repo.GetStoreTravel(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("travel from store: other store: %w", err)
		}
		mins, err := s.travelMinutes(ctx, artistID, store, other, day)
		if err != nil {
			return nil, err
		}
		out[id] = mins
	}
	return out, nil
}

// assertOffered refuses a start time the slot list would not offer: outside
// the store's or the artist's hours, inside same-day notice, off the
// 15-minute grid, overlapping, or inside travel to another store.
//
// The list a customer picks from IS the rule. Until 2026-10-10 the hold and
// the create path did not ask it - they checked the parties and "not in the
// past" and inserted, so the travel buffer, the opening hours and the grid
// existed only in what the app drew (audit T1-T3, measured: a hold inside
// the travel window, one at 03:00 with the store closed, one at 13:07 - all
// 201). The reschedule already worked this way; this is the same question
// asked the same way.
func (s *Service) assertOffered(ctx context.Context, artistID, storeID, serviceID uuid.UUID, start time.Time, store *Store) error {
	slots, err := s.GetAvailableSlots(ctx, GetAvailableSlotsRequest{
		ArtistID:  artistID.String(),
		StoreID:   storeID.String(),
		ServiceID: serviceID.String(),
		Date:      start.In(storeLocation(store)).Format("2006-01-02"),
	})
	if err != nil {
		return err
	}
	if !slotOffered(slots, start) {
		return apperror.Conflict("SLOT_UNAVAILABLE", "This time is no longer available. Please choose another.")
	}
	return nil
}
