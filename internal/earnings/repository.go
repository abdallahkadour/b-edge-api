// Package earnings implements the earnings domain for B-Edge,
// providing revenue aggregation and breakdown for the artist dashboard.
package earnings

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// Repository defines all database operations required by the earnings domain.
type Repository interface {
	// GetArtistIDByUserID resolves a users.id to an artists.id.
	GetArtistIDByUserID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)

	// GetPeriodSummary returns total revenue, bookings count, and deposit total
	// for completed + no_show bookings in the given date range.
	GetPeriodSummary(ctx context.Context, artistID uuid.UUID, from, to time.Time) (decimal.Decimal, int, decimal.Decimal, error)

	// GetDailyBreakdown returns per-day revenue for completed + no_show bookings
	// in the given date range, ordered by day ascending.
	GetDailyBreakdown(ctx context.Context, artistID uuid.UUID, from, to time.Time) ([]dailyEarningsRow, error)

	// GetServiceBreakdown returns revenue grouped by service name for
	// completed + no_show bookings in the given date range.
	GetServiceBreakdown(ctx context.Context, artistID uuid.UUID, from, to time.Time) ([]serviceEarningsRow, error)
}

// pgRepo is the PostgreSQL implementation of Repository.
type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates a new PostgreSQL-backed earnings Repository.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

// GetArtistIDByUserID resolves a users.id to an artists.id.
func (r *pgRepo) GetArtistIDByUserID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var artistID uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT id FROM artists WHERE user_id = $1
	`, userID).Scan(&artistID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("get artist id by user id: %w", err)
	}
	return artistID, nil
}

// GetPeriodSummary returns aggregate revenue stats for the given period.
// Revenue = SUM(final_price) on completed + no_show bookings.
// Deposits = SUM(deposit_amount) on completed + no_show bookings.
func (r *pgRepo) GetPeriodSummary(ctx context.Context, artistID uuid.UUID, from, to time.Time) (decimal.Decimal, int, decimal.Decimal, error) {
	var totalRevenue decimal.Decimal
	var totalBookings int
	var totalDeposits decimal.Decimal

	err := r.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(final_price), 0)    AS total_revenue,
			COUNT(*)                          AS total_bookings,
			COALESCE(SUM(deposit_amount), 0) AS total_deposits
		FROM bookings
		WHERE artist_id = $1
		  AND status IN ('completed', 'no_show')
		  AND start_time >= $2
		  AND start_time < $3
	`, artistID, from, to).Scan(&totalRevenue, &totalBookings, &totalDeposits)
	if err != nil {
		return decimal.Zero, 0, decimal.Zero, fmt.Errorf("get period summary: %w", err)
	}

	return totalRevenue, totalBookings, totalDeposits, nil
}

// GetDailyBreakdown returns per-day revenue for the given period, ordered by day asc.
//
// Buckets by the business's reporting calendar (Asia/Beirut), not UTC - a
// booking at 22:30 UTC is already the next day in Beirut, and grouping by
// the raw UTC day would file it under the wrong bar in the chart. Matches
// beirutNow/businessLocation used for the "today"/"this month" boundaries
// elsewhere in this file; hardcoded for the same reason (every store is
// currently in Lebanon) and with the same future trigger (a per-salon
// reporting timezone once that stops being true).
//
// The AT TIME ZONE appears twice, which is easy to misread as redundant
// it isn't. `start_time AT TIME ZONE 'Asia/Beirut'` converts the timestamptz
// to a naive Beirut wall-clock timestamp so DATE_TRUNC('day', ...) cuts at
// Beirut midnight rather than UTC midnight. But DATE_TRUNC's result is then
// itself a naive timestamp with no zone attached, and pgx's default scan
// behaviour for a naive timestamp is to relabel it as UTC without shifting
// the clock - silently turning "Beirut midnight" into "the same digits,
// tagged UTC", off by the zone's offset. The second `AT TIME ZONE
// 'Asia/Beirut'` converts that naive value back to a real timestamptz by
// telling Postgres the naive digits ARE Beirut time, producing the correct
// UTC instant. Dropping either cast reintroduces a several-hour error.
func (r *pgRepo) GetDailyBreakdown(ctx context.Context, artistID uuid.UUID, from, to time.Time) ([]dailyEarningsRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			(DATE_TRUNC('day', start_time AT TIME ZONE 'Asia/Beirut')
				AT TIME ZONE 'Asia/Beirut')          AS day,
			COALESCE(SUM(final_price), 0)            AS revenue
		FROM bookings
		WHERE artist_id = $1
		  AND status IN ('completed', 'no_show')
		  AND start_time >= $2
		  AND start_time < $3
		GROUP BY day
		ORDER BY day ASC
	`, artistID, from, to)
	if err != nil {
		return nil, fmt.Errorf("get daily breakdown: %w", err)
	}
	defer rows.Close()

	var result []dailyEarningsRow
	for rows.Next() {
		var row dailyEarningsRow
		if err := rows.Scan(&row.Day, &row.Revenue); err != nil {
			return nil, fmt.Errorf("get daily breakdown: scan: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get daily breakdown: rows: %w", err)
	}

	return result, nil
}

// GetServiceBreakdown returns revenue grouped by service, ordered by revenue desc.
func (r *pgRepo) GetServiceBreakdown(ctx context.Context, artistID uuid.UUID, from, to time.Time) ([]serviceEarningsRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			b.service_id,
			s.name                         AS service_name,
			COUNT(*)                        AS bookings_count,
			COALESCE(SUM(b.final_price), 0) AS revenue
		FROM bookings b
		JOIN services s ON s.id = b.service_id
		WHERE b.artist_id = $1
		  AND b.status IN ('completed', 'no_show')
		  AND b.start_time >= $2
		  AND b.start_time < $3
		GROUP BY b.service_id, s.name
		ORDER BY revenue DESC
	`, artistID, from, to)
	if err != nil {
		return nil, fmt.Errorf("get service breakdown: %w", err)
	}
	defer rows.Close()

	var result []serviceEarningsRow
	for rows.Next() {
		var row serviceEarningsRow
		if err := rows.Scan(&row.ServiceID, &row.ServiceName, &row.BookingsCount, &row.Revenue); err != nil {
			return nil, fmt.Errorf("get service breakdown: scan: %w", err)
		}
		result = append(result, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get service breakdown: rows: %w", err)
	}

	return result, nil
}
