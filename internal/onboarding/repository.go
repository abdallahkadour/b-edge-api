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

	// CompleteIntoExistingSalon creates an artist profile inside a salon
	// that already exists, for someone accepting an invitation.
	//
	// Deliberately the same transaction shape as Complete, minus the three
	// steps that belong to founding a business: no salon is created, no
	// store, no first service. Those already exist and are the owner's.
	//
	// Returns ErrAlreadyOnboarded if this user already has an artist row,
	// ErrHandleTaken on a handle collision, and ErrSalonNotFound if the
	// salon was deleted between the invitation being sent and accepted.
	CompleteIntoExistingSalon(ctx context.Context, userID, salonID uuid.UUID,
		profile ArtistProfile) (uuid.UUID, error)

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

	artistID, err := insertArtist(ctx, tx, userID, salonID, req.ArtistProfile)
	if err != nil {
		return uuid.Nil, err
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

	// Seed the week from the store's default opening hours.
	//
	// Without this a brand-new artist is completely unbookable and nothing
	// says so: openinghours.Resolve finds no business_hours row for the
	// weekday, reports the store closed, and slot generation returns an
	// empty list for every date forever. They appear on Discover, a
	// customer opens the profile, and there is nothing to book. Two stores
	// on the development database reached that state before this existed.
	//
	// All seven days open, from stores.default_open_time to
	// default_close_time (09:00-18:00 by migration 049). Closing a day is
	// one tap in the bulk hours editor; discovering that you have been
	// silently closed all week is not. Being open on a day you do not work
	// costs a cancellation, being closed on a day you do work costs every
	// booking you never hear about - and the second failure is invisible,
	// which is what makes it the worse default.
	if _, err = tx.Exec(ctx, `
		INSERT INTO business_hours (store_id, day_of_week, open_time, close_time, is_open)
		SELECT $1, d, s.default_open_time, s.default_close_time, TRUE
		  FROM stores s, generate_series(0, 6) AS d
		 WHERE s.id = $1`,
		storeID,
	); err != nil {
		return uuid.Nil, fmt.Errorf("complete onboarding: seed business hours: %w", err)
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

// insertArtist writes the artist row. Shared by both onboarding paths so
// that founding a salon and joining one cannot drift apart on what an artist
// actually is - status 'pending' in particular, which is what keeps admin
// approval a gate rather than a suggestion (BR-7).
//
// Takes a pgx.Tx rather than the pool: both callers are mid-transaction and
// neither may commit an artist without the rest of its setup.
func insertArtist(ctx context.Context, tx pgx.Tx, userID, salonID uuid.UUID,
	p ArtistProfile) (uuid.UUID, error) {

	var artistID uuid.UUID
	err := tx.QueryRow(ctx, `
		INSERT INTO artists (user_id, salon_id, handle, bio, instagram, category, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending')
		RETURNING id`,
		userID, salonID, p.Handle, p.Bio, p.Instagram, p.Category,
	).Scan(&artistID)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return uuid.Nil, ErrHandleTaken
		}
		return uuid.Nil, fmt.Errorf("create artist: %w", err)
	}
	return artistID, nil
}

func (r *pgRepo) CompleteIntoExistingSalon(ctx context.Context, userID, salonID uuid.UUID,
	profile ArtistProfile) (uuid.UUID, error) {

	tx, err := r.db.Begin(ctx)
	if err != nil {
		return uuid.Nil, fmt.Errorf("join salon: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit

	// Same reasoning as Complete: inside the transaction, as defence in
	// depth on artists_user_id_unique rather than instead of it, so a
	// double-submit fails with a specific error instead of a raw unique
	// violation.
	var existing uuid.UUID
	err = tx.QueryRow(ctx, `SELECT id FROM artists WHERE user_id = $1`, userID).Scan(&existing)
	if err == nil {
		return uuid.Nil, ErrAlreadyOnboarded
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("join salon: check existing: %w", err)
	}

	// Lock the salon row for the life of the transaction. Without it, an
	// owner deleting their salon concurrently with someone accepting an
	// invitation to it would leave an artist pointing at nothing.
	var exists bool
	err = tx.QueryRow(ctx,
		`SELECT true FROM salons WHERE id = $1 AND deleted_at IS NULL FOR SHARE`,
		salonID,
	).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrSalonNotFound
	}
	if err != nil {
		return uuid.Nil, fmt.Errorf("join salon: load salon: %w", err)
	}

	artistID, err := insertArtist(ctx, tx, userID, salonID, profile)
	if err != nil {
		return uuid.Nil, err
	}

	// Link the new member to every active store the salon has.
	//
	// Without an artist_stores row an artist is a ghost: invisible on
	// Discover, which INNER JOINs artist_stores, and unbookable, because
	// the funnel's store picker reads the same table. Complete() has a long
	// comment about exactly this - onboarding's first store was once the
	// only path that forgot it.
	//
	// Every active store is the right default rather than none: the owner
	// invited this person to the salon, so being bookable at the salon's
	// locations is what they meant. Narrowing comes later, per store, from
	// the member's own schedule - and unlike an empty artist_stores, a
	// narrow schedule is visible and editable in the dashboard rather than
	// being an invisible absence.
	if _, err = tx.Exec(ctx, `
		INSERT INTO artist_stores (artist_id, store_id)
		SELECT $1, s.id FROM stores s
		 WHERE s.salon_id = $2 AND s.is_active
		ON CONFLICT DO NOTHING`,
		artistID, salonID,
	); err != nil {
		return uuid.Nil, fmt.Errorf("join salon: link artist to stores: %w", err)
	}

	// No store and no service are created here. Both belong to the salon
	// and both are the owner's to define - a member cannot write either
	// (salonrole.ServicesWrite, salonrole.StoresWrite), so creating one on
	// their behalf would hand them a row they are not allowed to edit.

	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, fmt.Errorf("join salon: commit: %w", err)
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
