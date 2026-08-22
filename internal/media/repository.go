// Package media implements the media/portfolio domain for B-Edge,
// providing portfolio photo management for artist profiles.
package media

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository defines all database operations for the media domain.
type Repository interface {
	// GetArtistIDByUserID resolves a users.id to an artists.id.
	GetArtistIDByUserID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)

	// ListByArtist returns all media items for an artist ordered by display_order ASC.
	ListByArtist(ctx context.Context, artistID uuid.UUID) ([]*MediaItem, error)

	// CountByArtist returns the number of media items for an artist.
	CountByArtist(ctx context.Context, artistID uuid.UUID) (int, error)

	// GetByID returns a single media item by ID.
	GetByID(ctx context.Context, id uuid.UUID) (*MediaItem, error)

	// Create inserts a new media item.
	Create(ctx context.Context, m *MediaItem) error

	// Delete removes a media item by ID.
	Delete(ctx context.Context, id uuid.UUID) error

	// SetCover moves a media item to display_order=0 and shifts all others up by 1.
	SetCover(ctx context.Context, mediaID uuid.UUID, artistID uuid.UUID) error

	// Reorder updates the display_order of all media items for an artist.
	// ids is the desired order: ids[0] gets display_order=0, ids[1] gets 1, etc.
	Reorder(ctx context.Context, artistID uuid.UUID, ids []uuid.UUID) error

	// ── Products ──────────────────────────────────────────────────────────
	// Deliberately separate methods rather than generalising the ones above
	// to take an ownerType parameter - the artist portfolio path (implicit
	// single owner per caller, 20-photo cap) and the product gallery path
	// (explicit product ID per call since one salon owns many products,
	// 8-photo cap, no "cover" concept since image_url already is one) are
	// different enough shapes that forcing them through one generic method
	// would obscure both. Small duplication, same trade-off already made
	// for FindOrCreateCustomerByPhone across domains.

	// ListByProduct returns all gallery photos for a product ordered by
	// display_order ASC.
	ListByProduct(ctx context.Context, productID uuid.UUID) ([]*MediaItem, error)

	// CountByProduct returns the number of gallery photos for a product.
	CountByProduct(ctx context.Context, productID uuid.UUID) (int, error)

	// ReorderProduct updates the display_order of all gallery photos for a
	// product. ids is the desired order.
	ReorderProduct(ctx context.Context, productID uuid.UUID, ids []uuid.UUID) error

	// GetProductSalonID resolves a product's owning salon - used to
	// authorise product-photo mutations without importing the product
	// package. media and product are independent domains that both need
	// this one fact; a raw, minimal query here is preferred over a Go
	// dependency between them.
	GetProductSalonID(ctx context.Context, productID uuid.UUID) (uuid.UUID, error)
}

// pgRepo is the PostgreSQL implementation of Repository.
type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates a new PostgreSQL-backed media Repository.
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
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrArtistNotFound
		}
		return uuid.Nil, fmt.Errorf("get artist id by user id: %w", err)
	}
	return artistID, nil
}

// ListByArtist returns all media items for an artist ordered by display_order.
func (r *pgRepo) ListByArtist(ctx context.Context, artistID uuid.UUID) ([]*MediaItem, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, owner_type, owner_id, url, cloudinary_id, type, display_order, created_at
		FROM media
		WHERE owner_type = $1
		  AND owner_id   = $2
		ORDER BY display_order ASC
	`, OwnerTypeArtist, artistID)
	if err != nil {
		return nil, fmt.Errorf("list media by artist: %w", err)
	}
	defer rows.Close()

	return scanMediaRows(rows)
}

// CountByArtist returns the count of media items for an artist.
func (r *pgRepo) CountByArtist(ctx context.Context, artistID uuid.UUID) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM media
		WHERE owner_type = $1 AND owner_id = $2
	`, OwnerTypeArtist, artistID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count media by artist: %w", err)
	}
	return count, nil
}

// GetByID returns a single media item.
func (r *pgRepo) GetByID(ctx context.Context, id uuid.UUID) (*MediaItem, error) {
	m := &MediaItem{}
	err := r.db.QueryRow(ctx, `
		SELECT id, owner_type, owner_id, url, cloudinary_id, type, display_order, created_at
		FROM media
		WHERE id = $1
	`, id).Scan(
		&m.ID, &m.OwnerType, &m.OwnerID, &m.URL,
		&m.CloudinaryID, &m.Type, &m.DisplayOrder, &m.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrMediaNotFound
		}
		return nil, fmt.Errorf("get media by id: %w", err)
	}
	return m, nil
}

// Create inserts a new media item and scans the generated created_at.
func (r *pgRepo) Create(ctx context.Context, m *MediaItem) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO media (id, owner_type, owner_id, url, cloudinary_id, type, display_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at
	`, m.ID, m.OwnerType, m.OwnerID, m.URL, m.CloudinaryID, m.Type, m.DisplayOrder,
	).Scan(&m.CreatedAt)
	if err != nil {
		return fmt.Errorf("create media: %w", err)
	}
	return nil
}

// Delete removes a media item by ID.
func (r *pgRepo) Delete(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM media WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete media: %w", err)
	}
	return nil
}

// SetCover moves the given media item to display_order=0 and shifts all others up.
// Uses a transaction so the reorder is atomic.
func (r *pgRepo) SetCover(ctx context.Context, mediaID uuid.UUID, artistID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("set cover: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Shift all existing items up by 1 to make room at position 0
	_, err = tx.Exec(ctx, `
		UPDATE media
		SET display_order = display_order + 1
		WHERE owner_type = $1 AND owner_id = $2
	`, OwnerTypeArtist, artistID)
	if err != nil {
		return fmt.Errorf("set cover: shift orders: %w", err)
	}

	// Place the target item at position 0
	_, err = tx.Exec(ctx, `
		UPDATE media
		SET display_order = 0
		WHERE id = $1
	`, mediaID)
	if err != nil {
		return fmt.Errorf("set cover: set position: %w", err)
	}

	return tx.Commit(ctx)
}

// Reorder assigns sequential display_order values based on the provided ID slice.
// ids[0] → display_order=0, ids[1] → display_order=1, etc.
// All updates run inside a single transaction.
func (r *pgRepo) Reorder(ctx context.Context, artistID uuid.UUID, ids []uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("reorder media: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for i, id := range ids {
		_, err := tx.Exec(ctx, `
			UPDATE media
			SET display_order = $1
			WHERE id = $2
			  AND owner_type = $3
			  AND owner_id   = $4
		`, i, id, OwnerTypeArtist, artistID)
		if err != nil {
			return fmt.Errorf("reorder media: update id %s: %w", id, err)
		}
	}

	return tx.Commit(ctx)
}

// ── Products ────────────────────────────────────────────────────────────────

// ListByProduct returns all gallery photos for a product ordered by display_order.
func (r *pgRepo) ListByProduct(ctx context.Context, productID uuid.UUID) ([]*MediaItem, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, owner_type, owner_id, url, cloudinary_id, type, display_order, created_at
		FROM media
		WHERE owner_type = $1
		  AND owner_id   = $2
		ORDER BY display_order ASC
	`, OwnerTypeProduct, productID)
	if err != nil {
		return nil, fmt.Errorf("list media by product: %w", err)
	}
	defer rows.Close()

	return scanMediaRows(rows)
}

// CountByProduct returns the count of gallery photos for a product.
func (r *pgRepo) CountByProduct(ctx context.Context, productID uuid.UUID) (int, error) {
	var count int
	err := r.db.QueryRow(ctx, `
		SELECT COUNT(*) FROM media
		WHERE owner_type = $1 AND owner_id = $2
	`, OwnerTypeProduct, productID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count media by product: %w", err)
	}
	return count, nil
}

// ReorderProduct assigns sequential display_order values based on the
// provided ID slice, scoped to one product's gallery.
func (r *pgRepo) ReorderProduct(ctx context.Context, productID uuid.UUID, ids []uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("reorder product media: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	for i, id := range ids {
		_, err := tx.Exec(ctx, `
			UPDATE media
			SET display_order = $1
			WHERE id = $2
			  AND owner_type = $3
			  AND owner_id   = $4
		`, i, id, OwnerTypeProduct, productID)
		if err != nil {
			return fmt.Errorf("reorder product media: update id %s: %w", id, err)
		}
	}

	return tx.Commit(ctx)
}

// GetProductSalonID resolves a product's owning salon directly against the
// products table - see the Repository interface doc comment for why this
// is a raw query rather than a cross-package call.
func (r *pgRepo) GetProductSalonID(ctx context.Context, productID uuid.UUID) (uuid.UUID, error) {
	var salonID uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT salon_id FROM products WHERE id = $1`, productID).Scan(&salonID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrProductNotFound
		}
		return uuid.Nil, fmt.Errorf("get product salon id: %w", err)
	}
	return salonID, nil
}

// ── Scan helpers ──────────────────────────────────────────────────────────────

func scanMediaRows(rows pgx.Rows) ([]*MediaItem, error) {
	var result []*MediaItem
	for rows.Next() {
		m := &MediaItem{}
		if err := rows.Scan(
			&m.ID, &m.OwnerType, &m.OwnerID, &m.URL,
			&m.CloudinaryID, &m.Type, &m.DisplayOrder, &m.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan media row: %w", err)
		}
		result = append(result, m)
	}
	return result, rows.Err()
}
