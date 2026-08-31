package share

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/billing"
)

// subscriptionVisibleCond mirrors discovery's and artist's conditions of the
// same name, for the same reason and from the same constant: a link preview
// must not render for an artist whose profile is itself hidden from Discover.
// A rich card for a suspended artist would advertise a page the customer
// then cannot reach.
//
// Uses billing.GraceDays so this can never drift from DeriveStatus's own
// grace/past_due boundary.
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

// Repository reads the one row a preview card needs.
type Repository interface {
	// GetPreviewByHandleOrID resolves either a public handle (migration
	// 012) or a UUID string. Accepting both means a link keeps working
	// whether it was generated before or after the artist set a handle.
	GetPreviewByHandleOrID(ctx context.Context, handleOrID string) (*ArtistPreview, error)
}

type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates a PostgreSQL-backed share Repository.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

// GetPreviewByHandleOrID fetches the preview row in a single query.
//
// The cover photo and city are correlated subqueries rather than joins:
// both are "any one of these" rather than a real relationship, and a join
// would multiply the row out and need a DISTINCT to collapse again.
func (r *pgRepo) GetPreviewByHandleOrID(ctx context.Context, handleOrID string) (*ArtistPreview, error) {
	p := &ArtistPreview{}

	// A UUID is matched on a.id; anything else is matched on a.handle.
	// Parsing here rather than casting in SQL avoids an invalid-UUID cast
	// error for a handle like "rania".
	var byID *uuid.UUID
	if parsed, err := uuid.Parse(handleOrID); err == nil {
		byID = &parsed
	}

	err := r.db.QueryRow(ctx, `
		SELECT a.id, a.handle, u.name, a.bio, a.category, a.rating, a.review_count,
		       (SELECT m.url FROM media m
		        WHERE m.owner_type = 'artist' AND m.owner_id = a.id
		        ORDER BY m.display_order ASC LIMIT 1) AS cover_url,
		       (SELECT s.city FROM artist_stores ast
		        JOIN stores s ON s.id = ast.store_id
		        WHERE ast.artist_id = a.id AND s.is_active = TRUE
		        ORDER BY s.name ASC LIMIT 1) AS city
		FROM artists a
		JOIN users u ON u.id = a.user_id
		WHERE ($1::uuid IS NOT NULL AND a.id = $1::uuid OR $1::uuid IS NULL AND a.handle = $2)
		AND a.status = 'active'
		AND u.deleted_at IS NULL
		AND `+subscriptionVisibleCond,
		byID, handleOrID,
	).Scan(
		&p.ID, &p.Handle, &p.Name, &p.Bio, &p.Category,
		&p.Rating, &p.ReviewCount, &p.CoverURL, &p.City,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArtistNotFound
		}
		return nil, fmt.Errorf("get preview by handle or id: %w", err)
	}
	return p, nil
}
