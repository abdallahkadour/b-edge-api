package onboarding

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgUniqueViolation = "23505"

// Repository defines the onboarding domain's database operations.
type Repository interface {
	// Complete creates the salon, artist profile (status='pending'), first
	// store, and first service in a single transaction. Returns
	// ErrAlreadyOnboarded if this user already has an artist row, and
	// ErrHandleTaken if the requested handle collides with an existing one.
	Complete(ctx context.Context, userID uuid.UUID, req CompleteOnboardingRequest) (uuid.UUID, error)

	// GetStatus returns the artist status for a user, or ErrNotOnboarded
	// if no artist row exists yet.
	GetStatus(ctx context.Context, userID uuid.UUID) (*OnboardingStatus, error)
}

type pgRepo struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

func (r *pgRepo) Complete(ctx context.Context, userID uuid.UUID, req CompleteOnboardingRequest) (uuid.UUID, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("complete onboarding: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit

	// Idempotency check INSIDE the transaction, not before it starts -
	// checking outside would leave a race window between the check and
	// the insert. This is defence in depth on top of the
	// artists_user_id_unique constraint (migration 019), not a
	// replacement for it: the constraint is what actually prevents a
	// double-submit under real concurrency; this check exists so the
	// second submission fails with a clean, specific error instead of a
	// raw unique-violation surfacing from Postgres.
	var existingArtistID uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM artists WHERE user_id = $1`, userID).Scan(&existingArtistID)
	if err == nil {
		return uuid.Nil, ErrAlreadyOnboarded
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("complete onboarding: check existing: %w", err)
	}

	var salonID uuid.UUID
	err = tx.QueryRow(ctx,
		`INSERT INTO salons (owner_id, name) VALUES ($1, $2) RETURNING id`,
		userID, req.SalonName,
	).Scan(&salonID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("complete onboarding: create salon: %w", err)
	}

	var artistID uuid.UUID
	err = tx.QueryRow(ctx, `
		INSERT INTO artists (user_id, salon_id, handle, bio, instagram, category, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending')
		RETURNING id`,
		userID, salonID, req.Handle, req.Bio, req.Instagram, req.Category,
	).Scan(&artistID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return uuid.Nil, ErrHandleTaken
		}
		return uuid.Nil, fmt.Errorf("complete onboarding: create artist: %w", err)
	}

	var storeID uuid.UUID
	err = tx.QueryRow(ctx,
		`INSERT INTO stores (salon_id, name, city, address) VALUES ($1, $2, $3, $4) RETURNING id`,
		salonID, req.StoreName, req.City, req.Address,
	).Scan(&storeID)
	if err != nil {
		return uuid.Nil, fmt.Errorf("complete onboarding: create store: %w", err)
	}

	// Without this, the new artist is a ghost: approved and 'active', but
	// invisible on Discover (internal/discovery/repository.go's
	// ListArtistCards INNER JOINs artist_stores) and unbookable (the
	// booking funnel's own store picker, GetStoresByArtist, reads the same
	// table). Every other store-creation path (artist/repository.go's
	// CreateStore, used by the dashboard's "Add store") already does this
	// insert - onboarding's first store was the one path that didn't.
	if _, err = tx.Exec(ctx,
		`INSERT INTO artist_stores (artist_id, store_id) VALUES ($1, $2)`,
		artistID, storeID,
	); err != nil {
		return uuid.Nil, fmt.Errorf("complete onboarding: link artist to store: %w", err)
	}

	// deposit_amount and deposit_deadline_hours are both omitted
	// deliberately - the database defaults (0.00, 48h; migration 001)
	// apply, so a brand-new artist isn't asked to reason about deposit
	// policy before they've taken a single booking. Adjustable afterward
	// from the Services screen.
	if _, err = tx.Exec(ctx,
		`INSERT INTO services (salon_id, name, duration_min, price) VALUES ($1, $2, $3, $4)`,
		salonID, req.ServiceName, req.ServiceDurationMin, req.ServicePrice,
	); err != nil {
		return uuid.Nil, fmt.Errorf("complete onboarding: create service: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("complete onboarding: commit: %w", err)
	}

	return artistID, nil
}

func (r *pgRepo) GetStatus(ctx context.Context, userID uuid.UUID) (*OnboardingStatus, error) {
	status := &OnboardingStatus{}
	err := r.db.QueryRow(ctx,
		`SELECT status, created_at FROM artists WHERE user_id = $1`, userID,
	).Scan(&status.Status, &status.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotOnboarded
		}
		return nil, fmt.Errorf("get onboarding status: %w", err)
	}
	return status, nil
}
