package booking

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// AdminListBookings is every salon's bookings for the admin, newest first,
// optionally narrowed to one status and/or one salon. Read-only by design:
// the admin sees the platform's bookings - above all the refunds owed - and
// acts through the artist, not over her.
func (s *Service) AdminListBookings(ctx context.Context, status, salonID string, cursor time.Time, limit int) ([]*AdminBookingResponse, bool, error) {
	if status != "" && !ValidBookingStatuses[status] {
		return nil, false, apperror.BadRequest("INVALID_STATUS", "Unknown booking status filter")
	}
	var salon *uuid.UUID
	if salonID != "" {
		id, err := uuid.Parse(salonID)
		if err != nil {
			return nil, false, apperror.BadRequest("INVALID_SALON_ID", "salon_id must be a UUID")
		}
		salon = &id
	}
	if limit <= 0 || limit > 100 {
		limit = defaultPageSize
	}

	rows, err := s.repo.ListBookingsForAdmin(ctx, status, salon, cursor, limit)
	if err != nil {
		return nil, false, fmt.Errorf("admin list bookings: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	out := make([]*AdminBookingResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, &AdminBookingResponse{EnrichedBookingResponse: toEnrichedResponse(&r.EnrichedBooking), SalonName: r.SalonName})
	}
	return out, hasMore, nil
}

// AdminBookingSummary is the platform-wide count of what is waiting on
// someone: refunds owed (and how much), requests to approve, deposits to check.
func (s *Service) AdminBookingSummary(ctx context.Context) (*AdminBookingSummary, error) {
	return s.repo.AdminBookingSummary(ctx)
}
