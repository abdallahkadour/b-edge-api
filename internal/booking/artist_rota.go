package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/schedule"
)

// artist_rota.go: reading an artist's own working hours for slot generation.
//
// business_hours says when the STORE is open. These say when one particular
// artist works inside that window, and internal/pkg/schedule intersects the
// two. Both reads return nil when the artist has declared nothing, which is
// the state every artist on the platform is in today and which
// schedule.ResolveDay treats as "no personal restriction".

// GetArtistRotaDay returns the artist's declared hours for one weekday at
// one store, or nil when they have declared none.
func (r *pgRepo) GetArtistRotaDay(ctx context.Context, artistID, storeID uuid.UUID,
	dayOfWeek int) (*schedule.DayRota, error) {

	var startMin, endMin int
	var working bool
	err := r.db.QueryRow(ctx, `
		SELECT EXTRACT(HOUR FROM start_time) * 60 + EXTRACT(MINUTE FROM start_time),
		       EXTRACT(HOUR FROM end_time)   * 60 + EXTRACT(MINUTE FROM end_time),
		       is_working
		  FROM artist_schedules
		 WHERE artist_id = $1 AND store_id = $2 AND day_of_week = $3`,
		artistID, storeID, dayOfWeek,
	).Scan(&startMin, &endMin, &working)

	if errors.Is(err, pgx.ErrNoRows) {
		// No row is not an error and not unavailability. It means this
		// artist has set no personal hours for this weekday, so the store's
		// window applies unchanged.
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get artist rota day: %w", err)
	}
	return &schedule.DayRota{
		StartMinutes: startMin, EndMinutes: endMin, IsWorking: working,
	}, nil
}

// GetArtistRotaException returns a personal override for one date, or nil.
//
// A row with store_id NULL applies to every store - "I am away on the 3rd"
// rather than "away from this branch on the 3rd". A store-specific row wins
// over a global one, which is why the ORDER BY puts non-null first.
func (r *pgRepo) GetArtistRotaException(ctx context.Context, artistID, storeID uuid.UUID,
	date time.Time) (*schedule.DayException, error) {

	var unavailable bool
	var startMin, endMin *int
	err := r.db.QueryRow(ctx, `
		SELECT is_unavailable,
		       CASE WHEN start_time IS NULL THEN NULL ELSE
		            EXTRACT(HOUR FROM start_time) * 60 + EXTRACT(MINUTE FROM start_time) END,
		       CASE WHEN end_time IS NULL THEN NULL ELSE
		            EXTRACT(HOUR FROM end_time) * 60 + EXTRACT(MINUTE FROM end_time) END
		  FROM artist_schedule_exceptions
		 WHERE artist_id = $1
		   AND exception_date = $2::date
		   AND (store_id = $3 OR store_id IS NULL)
		 ORDER BY store_id NULLS LAST
		 LIMIT 1`,
		artistID, date.Format("2006-01-02"), storeID,
	).Scan(&unavailable, &startMin, &endMin)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get artist rota exception: %w", err)
	}

	exc := &schedule.DayException{IsUnavailable: unavailable}
	if startMin != nil && endMin != nil {
		exc.StartMinutes, exc.EndMinutes = *startMin, *endMin
	}
	return exc, nil
}
