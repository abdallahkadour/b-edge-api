// Package discovery implements the public customer-facing artist discovery surface.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/billing"
)

// subscriptionVisibleCond is a SQL EXISTS clause mirroring
// billing.DeriveStatus's Active/Grace/Trialing/Comped/Cancelled branches -
// deliberately NOT PastDue or Suspended, which is exactly what this excludes
// from Discover per B-Edge-Monetization-Implementation-Spec-v1.md section
// 6.1's enforcement table. An artist with no subscriptions row at all (should
// not happen after admin.Service.Approve creates one on approval, but this
// stays correct even so) fails every branch below and is correctly hidden -
// matching DeriveStatus's own CurrentPeriodEnd==nil => PastDue case.
//
// cancelled_at IS NOT NULL is treated as visible here, NOT hidden - the
// spec's own enforcement table only names past_due/suspended as hidden and
// says nothing about cancelled, so this does not invent scope beyond what
// was actually specified. Revisit if that gap gets an explicit answer.
//
// Uses billing.GraceDays (not a locally duplicated literal) so this can
// never silently drift from the exact boundary DeriveStatus itself uses for
// Active/Grace vs PastDue.
var subscriptionVisibleCond = fmt.Sprintf(`EXISTS (
	SELECT 1 FROM subscriptions sub
	WHERE sub.artist_id = a.id
	AND (
		sub.cancelled_at IS NOT NULL
		OR sub.plan_code = 'comped'
		OR (sub.trial_ends_at IS NOT NULL AND NOW() < sub.trial_ends_at)
		OR (sub.current_period_end IS NOT NULL AND NOW() < sub.current_period_end + INTERVAL '%d days')
	)
)`, billing.GraceDays)

// Repository defines all database operations for the discovery domain.
type Repository interface {
	// ListArtistCards returns discovery cards, one row per (artist, city), so an
	// artist with stores in several cities appears under each. Optional filters:
	// city (exact store city), category (exact), q (case-insensitive match on
	// name OR city - NOT service name; see the WHERE-building code for why).
	// Ordered verified-first then by rating; capped at Limit (no cursor - the
	// browse screen loads a bounded top-N, not an infinite scroll).
	ListArtistCards(ctx context.Context, f ListArtistCardsParams) ([]*ArtistCardRow, error)

	// GetArtistProfile returns the core artist row for the public profile.
	// Returns ErrArtistNotFound if the artist does not exist.
	GetArtistProfile(ctx context.Context, artistID uuid.UUID) (*ArtistProfileRow, error)

	// GetArtistStores returns the active stores an artist works at.
	GetArtistStores(ctx context.Context, artistID uuid.UUID) ([]*StoreRow, error)

	// GetSalonServices returns the active services for a salon (an artist's
	// service menu derives from their salon).
	GetSalonServices(ctx context.Context, salonID uuid.UUID) ([]*ServiceRow, error)

	// GetStoreHours returns every configured weekday row for the given
	// stores, in one query rather than one per store.
	//
	// All seven days are fetched rather than just "today" because which day
	// today IS depends on each store's own timezone, which this layer does
	// not resolve - the service does, per store. Seven rows per store is
	// small enough that filtering server-side would trade correctness for
	// nothing.
	GetStoreHours(ctx context.Context, storeIDs []uuid.UUID) ([]*DayHoursRow, error)

	// GetStoreExceptions returns dated trading overrides for the given
	// stores between from and to inclusive.
	//
	// Callers pass a window spanning at least the day before and after the
	// instant of interest, because a store's local date can differ from the
	// server's by one in either direction.
	GetStoreExceptions(ctx context.Context, storeIDs []uuid.UUID, from, to time.Time) ([]*ExceptionRow, error)
}

// ListArtistCardsParams carries the filters and page cap for the list query.
// Empty string filters mean "no filter". Limit caps the number of rows returned.
type ListArtistCardsParams struct {
	City     string
	Category string
	Query    string
	Limit    int
}

// pgRepo is the PostgreSQL implementation of Repository.
type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates a discovery repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

// ListArtistCards returns discovery cards joined across artists, users (name),
// artist_stores, and stores (city). One row per (artist, city). Verified artists
// are surfaced first, then by rating, then name - a stable, sensible default order
// for a browse screen.
func (r *pgRepo) ListArtistCards(ctx context.Context, f ListArtistCardsParams) ([]*ArtistCardRow, error) {
	// Build dynamic WHERE conditions. Arguments are positional and appended in
	// lockstep with their placeholders.
	conds := []string{"s.is_active = TRUE", "u.deleted_at IS NULL", "a.status = 'active'", subscriptionVisibleCond}
	args := []any{}
	n := 0

	if f.City != "" {
		n++
		conds = append(conds, fmt.Sprintf("s.city = $%d", n))
		args = append(args, f.City)
	}
	if f.Category != "" {
		n++
		conds = append(conds, fmt.Sprintf("a.category = $%d", n))
		args = append(args, f.Category)
	}
	if f.Query != "" {
		n++
		// Matches name OR city - the placeholder text on the discover screen
		// promises "artist, service, or city", and a customer typing a city
		// name into the one search box is a completely normal thing to do.
		// Service-name matching is NOT included here: it would need a join
		// to the salon's services table, which this query doesn't otherwise
		// touch, and risks row duplication (one artist, multiple matching
		// services) that would need a DISTINCT to handle correctly. Tracked
		// separately rather than folded into this fix.
		conds = append(conds, fmt.Sprintf("(u.name ILIKE $%d OR s.city ILIKE $%d)", n, n))
		args = append(args, "%"+f.Query+"%")
	}

	where := ""
	for i, c := range conds {
		if i == 0 {
			where = "WHERE " + c
		} else {
			where += " AND " + c
		}
	}

	// LIMIT is the final positional arg.
	n++
	limitPos := n
	args = append(args, f.Limit)

	q := fmt.Sprintf(`
		SELECT a.id, a.handle, u.name, a.category, a.rating, a.review_count,
		       s.city, a.is_verified, a.created_at
		FROM artists a
		JOIN users u         ON u.id  = a.user_id
		JOIN artist_stores ast ON ast.artist_id = a.id
		JOIN stores s        ON s.id  = ast.store_id
		%s
		ORDER BY a.is_verified DESC, a.rating DESC, u.name ASC, s.city ASC
		LIMIT $%d`, where, limitPos)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list artist cards: %w", err)
	}
	defer rows.Close()

	var result []*ArtistCardRow
	for rows.Next() {
		c := &ArtistCardRow{}
		if err := rows.Scan(
			&c.ID, &c.Handle, &c.Name, &c.Category, &c.Rating, &c.ReviewCount,
			&c.City, &c.IsVerified, &c.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan artist card: %w", err)
		}
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list artist cards rows: %w", err)
	}
	return result, nil
}

// GetArtistProfile fetches the core artist row joined with the user's name.
func (r *pgRepo) GetArtistProfile(ctx context.Context, artistID uuid.UUID) (*ArtistProfileRow, error) {
	p := &ArtistProfileRow{}
	err := r.db.QueryRow(ctx, `
		SELECT a.id, u.name, a.bio, a.instagram, a.category,
		       a.rating, a.review_count, a.is_verified, a.salon_id
		FROM artists a
		JOIN users u ON u.id = a.user_id
		WHERE a.id = $1
		AND a.status = 'active'
		AND u.deleted_at IS NULL
		AND `+subscriptionVisibleCond,
		artistID,
	).Scan(
		&p.ID, &p.Name, &p.Bio, &p.Instagram, &p.Category,
		&p.Rating, &p.ReviewCount, &p.IsVerified, &p.SalonID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArtistNotFound
		}
		return nil, fmt.Errorf("get artist profile: %w", err)
	}
	return p, nil
}

// GetArtistStores returns the active stores an artist works at, ordered by city.
func (r *pgRepo) GetArtistStores(ctx context.Context, artistID uuid.UUID) ([]*StoreRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.id, s.name, s.city, s.address, s.phone, s.timezone, s.latitude, s.longitude
		FROM stores s
		JOIN artist_stores ast ON ast.store_id = s.id
		WHERE ast.artist_id = $1
		AND s.is_active = TRUE
		ORDER BY s.city ASC, s.name ASC`,
		artistID,
	)
	if err != nil {
		return nil, fmt.Errorf("get artist stores: %w", err)
	}
	defer rows.Close()

	var result []*StoreRow
	for rows.Next() {
		s := &StoreRow{}
		if err := rows.Scan(
			&s.ID, &s.Name, &s.City, &s.Address,
			&s.Phone, &s.Timezone, &s.Latitude, &s.Longitude,
		); err != nil {
			return nil, fmt.Errorf("scan store row: %w", err)
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get artist stores rows: %w", err)
	}
	return result, nil
}

// GetSalonServices returns the active services for a salon, cheapest first.
// GetStoreHours returns every configured weekday row for the given stores.
func (r *pgRepo) GetStoreHours(ctx context.Context, storeIDs []uuid.UUID) ([]*DayHoursRow, error) {
	if len(storeIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT store_id, day_of_week, is_open, open_time, close_time
		FROM business_hours
		WHERE store_id = ANY($1)`,
		storeIDs,
	)
	if err != nil {
		return nil, fmt.Errorf("get store hours: %w", err)
	}
	defer rows.Close()

	var result []*DayHoursRow
	for rows.Next() {
		h := &DayHoursRow{}
		if err := rows.Scan(&h.StoreID, &h.DayOfWeek, &h.IsOpen, &h.OpenTime, &h.CloseTime); err != nil {
			return nil, fmt.Errorf("scan store hours row: %w", err)
		}
		result = append(result, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get store hours rows: %w", err)
	}
	return result, nil
}

// GetStoreExceptions returns dated trading overrides in [from, to].
func (r *pgRepo) GetStoreExceptions(ctx context.Context, storeIDs []uuid.UUID, from, to time.Time) ([]*ExceptionRow, error) {
	if len(storeIDs) == 0 {
		return nil, nil
	}
	rows, err := r.db.Query(ctx, `
		SELECT store_id, exception_date, is_closed, open_time, close_time
		FROM business_hours_exceptions
		WHERE store_id = ANY($1)
		AND exception_date BETWEEN $2 AND $3`,
		storeIDs, from, to,
	)
	if err != nil {
		return nil, fmt.Errorf("get store exceptions: %w", err)
	}
	defer rows.Close()

	var result []*ExceptionRow
	for rows.Next() {
		e := &ExceptionRow{}
		if err := rows.Scan(&e.StoreID, &e.ExceptionDate, &e.IsClosed, &e.OpenTime, &e.CloseTime); err != nil {
			return nil, fmt.Errorf("scan store exception row: %w", err)
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get store exceptions rows: %w", err)
	}
	return result, nil
}

func (r *pgRepo) GetSalonServices(ctx context.Context, salonID uuid.UUID) ([]*ServiceRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name, duration_min, price, deposit_amount
		FROM services
		WHERE salon_id = $1
		AND is_active = TRUE
		ORDER BY price ASC, name ASC`,
		salonID,
	)
	if err != nil {
		return nil, fmt.Errorf("get salon services: %w", err)
	}
	defer rows.Close()

	var result []*ServiceRow
	for rows.Next() {
		s := &ServiceRow{}
		if err := rows.Scan(&s.ID, &s.Name, &s.DurationMin, &s.Price, &s.DepositAmount); err != nil {
			return nil, fmt.Errorf("scan service row: %w", err)
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get salon services rows: %w", err)
	}
	return result, nil
}
