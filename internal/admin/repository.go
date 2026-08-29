package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Repository interface {
	ListPending(ctx context.Context) ([]*PendingArtist, error)

	// UpdateStatus performs a guarded transition: the WHERE clause only
	// matches a row currently in 'pending', so this can never re-decide
	// an artist who was already approved or rejected, and RowsAffected
	// tells the caller whether the guard actually matched anything.
	// Mirrors the same guarded-UPDATE pattern already used for booking
	// and order status transitions elsewhere in this codebase.
	UpdateStatus(ctx context.Context, artistID uuid.UUID, newStatus string) (rowsAffected int64, err error)

	// ApproveWithTrialSubscription approves a pending artist AND creates
	// their initial trial subscription in one transaction - see
	// Service.Approve's doc comment for why this exists (before this,
	// artists approved after Aug 29, 2026 got no subscriptions row at
	// all, which billing.DeriveStatus silently reads as permanently
	// past_due). Guarded exactly like UpdateStatus: the WHERE clause only
	// matches a currently-pending artist, so this can never re-approve an
	// already-decided artist or create a duplicate subscription for one.
	// Reads the given plan's CURRENT price inside the same transaction and
	// snapshots it onto the new subscription - matches Plan's own doc
	// comment that a price edit must only affect signups from that point
	// forward, and approval is exactly such a signup event.
	ApproveWithTrialSubscription(ctx context.Context, artistID uuid.UUID, trialPlanCode string, trialEndsAt time.Time) (rowsAffected int64, err error)
}

type pgRepo struct {
	db *pgxpool.Pool
}

func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

func (r *pgRepo) ListPending(ctx context.Context) ([]*PendingArtist, error) {
	// The JOINs to stores and services assume exactly one of each per
	// pending artist - true today because onboarding creates exactly one
	// of each, and a pending artist has no access to the screens that add
	// more (Services/Stores management requires status='active', enforced
	// by the same dashboard gate that shows the "pending review" banner
	// instead of the normal dashboard). If that invariant ever changes,
	// this becomes a cartesian product across stores x services and
	// needs a LIMIT or DISTINCT ON - flagged here rather than silently
	// trusted.
	rows, err := r.db.Query(ctx, `
		SELECT a.id, u.name, u.email, a.handle, a.category, a.bio,
		       s.name, st.name, st.city, sv.name, a.created_at
		FROM artists a
		JOIN users u ON u.id = a.user_id
		JOIN salons s ON s.id = a.salon_id
		JOIN stores st ON st.salon_id = s.id
		JOIN services sv ON sv.salon_id = s.id
		WHERE a.status = 'pending'
		ORDER BY a.created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list pending artists: %w", err)
	}
	defer rows.Close()

	var result []*PendingArtist
	for rows.Next() {
		p := &PendingArtist{}
		if err := rows.Scan(
			&p.ArtistID, &p.Name, &p.Email, &p.Handle, &p.Category, &p.Bio,
			&p.SalonName, &p.StoreName, &p.City, &p.ServiceName, &p.SubmittedAt,
		); err != nil {
			return nil, fmt.Errorf("scan pending artist: %w", err)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

func (r *pgRepo) UpdateStatus(ctx context.Context, artistID uuid.UUID, newStatus string) (int64, error) {
	tag, err := r.db.Exec(ctx,
		`UPDATE artists SET status = $1, updated_at = NOW() WHERE id = $2 AND status = 'pending'`,
		newStatus, artistID,
	)
	if err != nil {
		return 0, fmt.Errorf("update artist status: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (r *pgRepo) ApproveWithTrialSubscription(ctx context.Context, artistID uuid.UUID, trialPlanCode string, trialEndsAt time.Time) (int64, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("approve with trial subscription: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit

	tag, err := tx.Exec(ctx,
		`UPDATE artists SET status = 'active', updated_at = NOW() WHERE id = $1 AND status = 'pending'`,
		artistID,
	)
	if err != nil {
		return 0, fmt.Errorf("approve with trial subscription: update status: %w", err)
	}
	rows := tag.RowsAffected()
	if rows == 0 {
		// Not currently pending - nothing to approve. Returning here
		// leaves the transaction to the deferred Rollback; there is
		// nothing else in it to undo.
		return 0, nil
	}

	// Snapshot the plan's price AT THIS MOMENT, inside the same
	// transaction as the approval - never joined later, matching every
	// other subscription's monthly_price/currency (see Subscription's
	// doc comment).
	var monthlyPrice, currency string
	if err := tx.QueryRow(ctx,
		`SELECT monthly_price, currency FROM plans WHERE code = $1`, trialPlanCode,
	).Scan(&monthlyPrice, &currency); err != nil {
		return 0, fmt.Errorf("approve with trial subscription: read plan: %w", err)
	}

	if _, err := tx.Exec(ctx,
		`INSERT INTO subscriptions (artist_id, plan_code, seats, monthly_price, currency, trial_ends_at)
		 VALUES ($1, $2, 1, $3, $4, $5)`,
		artistID, trialPlanCode, monthlyPrice, currency, trialEndsAt,
	); err != nil {
		return 0, fmt.Errorf("approve with trial subscription: create subscription: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("approve with trial subscription: commit: %w", err)
	}
	return rows, nil
}
