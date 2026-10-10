package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/caller"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

// actsForArtist reports whether requesterUserID may act on b as its artist:
// she IS its artist, or she owns the salon b belongs to (2026-10-10, owner
// dashboard decision 1). The owner path is salonrole.BookingsAnyWrite, read
// from the request's caller (RequireAuth fills it from the database, not the
// token) - and only when that caller is the requester, so one person's
// standing never stands in for another's.
//
// A false answer is reported by the callers as BOOKING_NOT_FOUND, as before:
// a booking someone may not touch must look like one that does not exist.
func (s *Service) actsForArtist(ctx context.Context, b *Booking, requesterUserID uuid.UUID) (bool, error) {
	if salonHolds(ctx, requesterUserID, b.SalonID, salonrole.BookingsAnyWrite) {
		return true, nil
	}
	artistID, err := s.repo.GetArtistIDByUserID(ctx, requesterUserID)
	if errors.Is(err, ErrArtistNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return artistID == b.ArtistID, nil
}

// salonHolds reports whether the request's caller is userID, belongs to
// salonID, and holds cap there.
func salonHolds(ctx context.Context, userID, salonID uuid.UUID, cap salonrole.Capability) bool {
	c, ok := caller.From(ctx)
	return ok && c.UserID == userID && c.InSalon(salonID) && salonrole.Can(c.SalonRole, cap)
}

// ListSalonBookings is the owner's "whole team" bookings list: every
// booking made at salonID, newest first, optionally one artist's (artist,
// a UUID) and/or one status. The route is guarded by
// salonrole.CalendarSalonRead and salonID is the caller's own salon, so the
// scope is the salon whatever artist id is sent - another salon's artist
// simply finds nothing.
func (s *Service) ListSalonBookings(ctx context.Context, salonID uuid.UUID, artist, status string, cursor time.Time, limit int) ([]*EnrichedBookingResponse, bool, error) {
	artistID, err := optionalArtist(artist)
	if err != nil {
		return nil, false, err
	}
	if status != "" && !ValidBookingStatuses[status] {
		return nil, false, apperror.BadRequest("INVALID_STATUS", "Unknown booking status filter")
	}
	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}

	rows, err := s.repo.ListEnrichedBookingsBySalon(ctx, salonID, artistID, status, cursor, limit)
	if err != nil {
		return nil, false, fmt.Errorf("list salon bookings: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	out := make([]*EnrichedBookingResponse, 0, len(rows))
	for _, e := range rows {
		out = append(out, toEnrichedResponse(e))
	}
	return out, hasMore, nil
}

// ListSalonWeek is the owner's team calendar: the salon's committed
// appointments for the week, every artist's or one's.
func (s *Service) ListSalonWeek(ctx context.Context, salonID uuid.UUID, artist string, weekStart time.Time) ([]*EnrichedBookingResponse, error) {
	artistID, err := optionalArtist(artist)
	if err != nil {
		return nil, err
	}
	weekStart = time.Date(weekStart.Year(), weekStart.Month(), weekStart.Day(), 0, 0, 0, 0, time.UTC)

	rows, err := s.repo.ListEnrichedBookingsForSalonWeek(ctx, salonID, artistID, weekStart)
	if err != nil {
		return nil, fmt.Errorf("list salon week: %w", err)
	}
	out := make([]*EnrichedBookingResponse, 0, len(rows))
	for _, e := range rows {
		out = append(out, toEnrichedResponse(e))
	}
	return out, nil
}

func optionalArtist(raw string) (*uuid.UUID, error) {
	if raw == "" {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_ARTIST_ID", "artist_id must be a UUID")
	}
	return &id, nil
}
