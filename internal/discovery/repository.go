// Package discovery implements the public customer-facing artist discovery surface.
package discovery

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// (no time import needed here; the New-badge computation lives in the service.)

// Repository defines all database operations for the discovery domain.
type Repository interface {
	// ListArtistCards returns discovery cards, one row per (artist, city), so an
	// artist with stores in several cities appears under each. Optional filters:
	// city (exact store city), category (exact), q (case-insensitive name match).
	// Ordered verified-first then by rating; capped at Limit (no cursor — the
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
// are surfaced first, then by rating, then name — a stable, sensible default order
// for a browse screen.
func (r *pgRepo) ListArtistCards(ctx context.Context, f ListArtistCardsParams) ([]*ArtistCardRow, error) {
	// Build dynamic WHERE conditions. Arguments are positional and appended in
	// lockstep with their placeholders.
	conds := []string{"s.is_active = TRUE", "u.deleted_at IS NULL"}
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
		conds = append(conds, fmt.Sprintf("u.name ILIKE $%d", n))
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
		SELECT a.id, u.name, a.category, a.rating, a.review_count,
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
			&c.ID, &c.Name, &c.Category, &c.Rating, &c.ReviewCount,
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
		AND u.deleted_at IS NULL`,
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
		SELECT s.id, s.name, s.city, s.address
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
		if err := rows.Scan(&s.ID, &s.Name, &s.City, &s.Address); err != nil {
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
func (r *pgRepo) GetSalonServices(ctx context.Context, salonID uuid.UUID) ([]*ServiceRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, name, duration_min, price, deposit_amount
		FROM services
		WHERE salon_id = $1
		AND is_active = TRUE
		AND deleted_at IS NULL
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
