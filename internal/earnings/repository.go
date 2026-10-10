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

	// GetSalonTotals is the salon-wide figures for bookings starting, and
	// shop orders delivered, in [from, to).
	GetSalonTotals(ctx context.Context, salonID uuid.UUID, from, to time.Time) (SalonTotals, error)

	// GetSalonWaiting is what is waiting on someone in the salon now.
	GetSalonWaiting(ctx context.Context, salonID uuid.UUID) (SalonWaiting, error)

	// GetSalonArtists is one row per current member, plus any artist who
	// has left but had bookings here in [from, to), highest earned first.
	GetSalonArtists(ctx context.Context, salonID uuid.UUID, from, to time.Time) ([]ArtistOverview, error)
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
		WHERE `+ArtistScopeCond("", 1)+`
		  AND `+EarnedCond("")+`
		  AND `+PeriodCond("", 2, 3)+`
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
		WHERE `+ArtistScopeCond("", 1)+`
		  AND `+EarnedCond("")+`
		  AND `+PeriodCond("", 2, 3)+`
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
		WHERE `+ArtistScopeCond("b", 1)+`
		  AND `+EarnedCond("b")+`
		  AND `+PeriodCond("b", 2, 3)+`
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

// GetSalonTotals - see the interface. The earned figures use EarnedCond and
// PeriodCond exactly as GetPeriodSummary does, with the artist scope widened
// to the salon, so the salon total is the sum of every artist's own page.
func (r *pgRepo) GetSalonTotals(ctx context.Context, salonID uuid.UUID, from, to time.Time) (SalonTotals, error) {
	var t SalonTotals
	err := r.db.QueryRow(ctx, `
		SELECT
			COALESCE(SUM(final_price)    FILTER (WHERE `+EarnedCond("")+`), 0),
			COUNT(*)                     FILTER (WHERE `+EarnedCond("")+`),
			COALESCE(SUM(deposit_amount) FILTER (WHERE `+EarnedCond("")+`), 0),
			COUNT(*) FILTER (WHERE status = 'completed'),
			COUNT(*) FILTER (WHERE status = 'no_show'),
			COUNT(*) FILTER (WHERE `+CancelledCond("")+`),
			(SELECT COUNT(*) FROM orders o
			  WHERE o.salon_id = $1 AND o.status = 'delivered' AND o.deleted_at IS NULL
			    AND o.delivered_at >= $2 AND o.delivered_at < $3),
			(SELECT COALESCE(SUM(o.total_amount), 0) FROM orders o
			  WHERE o.salon_id = $1 AND o.status = 'delivered' AND o.deleted_at IS NULL
			    AND o.delivered_at >= $2 AND o.delivered_at < $3)
		FROM bookings
		WHERE `+SalonScopeCond("", 1)+`
		  AND `+PeriodCond("", 2, 3)+`
	`, salonID, from, to).Scan(&t.Earned, &t.EarnedBookings, &t.Deposits,
		&t.Completed, &t.NoShows, &t.Cancelled, &t.OrdersDelivered, &t.OrdersValue)
	if err != nil {
		return SalonTotals{}, fmt.Errorf("get salon totals: %w", err)
	}
	return t, nil
}

// GetSalonWaiting - see the interface. The same buckets as the admin's
// platform-wide summary, for one salon.
func (r *pgRepo) GetSalonWaiting(ctx context.Context, salonID uuid.UUID) (SalonWaiting, error) {
	var w SalonWaiting
	err := r.db.QueryRow(ctx, `
		SELECT count(*) FILTER (WHERE status = 'pending'),
		       count(*) FILTER (WHERE status IN ('approved', 'deposit_paid')),
		       count(*) FILTER (WHERE status = 'refund_due'),
		       COALESCE(sum(deposit_amount) FILTER (WHERE status = 'refund_due'), 0)
		  FROM bookings
		 WHERE `+SalonScopeCond("", 1)+` AND deleted_at IS NULL`,
		salonID).Scan(&w.AwaitingApproval, &w.DepositsToCheck, &w.RefundsOwed, &w.RefundsOwedAmount)
	if err != nil {
		return SalonWaiting{}, fmt.Errorf("get salon waiting: %w", err)
	}
	return w, nil
}

// GetSalonArtists - see the interface.
//
// The people CTE is what keeps a quiet member on the list (she has no
// bookings to group by) and an artist who left on it while her bookings
// here are in the window (she is no longer in artists.salon_id).
func (r *pgRepo) GetSalonArtists(ctx context.Context, salonID uuid.UUID, from, to time.Time) ([]ArtistOverview, error) {
	rows, err := r.db.Query(ctx, `
		WITH people AS (
			SELECT a.id FROM artists a WHERE a.salon_id = $1
			UNION
			SELECT b.artist_id FROM bookings b
			 WHERE `+SalonScopeCond("b", 1)+` AND `+PeriodCond("b", 2, 3)+`
		)
		SELECT a.id, u.name, a.user_id, (a.salon_id IS NOT DISTINCT FROM $1) AS is_member,
		       COALESCE(SUM(b.final_price) FILTER (WHERE `+EarnedCond("b")+`), 0) AS earned,
		       COUNT(b.id) FILTER (WHERE `+EarnedCond("b")+`),
		       COUNT(b.id) FILTER (WHERE b.status = 'completed'),
		       COUNT(b.id) FILTER (WHERE b.status = 'no_show'),
		       COUNT(b.id) FILTER (WHERE `+CancelledCond("b")+`),
		       a.rating, a.review_count
		  FROM people p
		  JOIN artists a ON a.id = p.id
		  JOIN users   u ON u.id = a.user_id
		  LEFT JOIN bookings b ON b.artist_id = a.id
		                      AND `+SalonScopeCond("b", 1)+`
		                      AND `+PeriodCond("b", 2, 3)+`
		 GROUP BY a.id, u.name, a.user_id, a.salon_id, a.rating, a.review_count
		 ORDER BY earned DESC, u.name`, salonID, from, to)
	if err != nil {
		return nil, fmt.Errorf("get salon artists: %w", err)
	}
	defer rows.Close()

	out := make([]ArtistOverview, 0)
	for rows.Next() {
		var a ArtistOverview
		if err := rows.Scan(&a.ArtistID, &a.Name, &a.userID, &a.IsMember,
			&a.Earned, &a.EarnedBookings, &a.Completed, &a.NoShows, &a.Cancelled,
			&a.Rating, &a.ReviewCount); err != nil {
			return nil, fmt.Errorf("get salon artists: scan: %w", err)
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
