package payout

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository reads and writes salon payment destinations.
type Repository interface {
	ListBySalon(ctx context.Context, salonID uuid.UUID, activeOnly bool) ([]*PaymentMethod, error)
	Upsert(ctx context.Context, salonID uuid.UUID, req UpsertPaymentMethodRequest) (*PaymentMethod, error)
	SetActive(ctx context.Context, id, salonID uuid.UUID, active bool) (*PaymentMethod, error)
}

type repository struct{ db *pgxpool.Pool }

// NewRepository creates a payout repository.
func NewRepository(db *pgxpool.Pool) Repository { return &repository{db: db} }

const paymentMethodColumns = `
	id, salon_id, method, account_name, account_ref, is_active, created_at, updated_at`

func scanPaymentMethod(row pgx.Row) (*PaymentMethod, error) {
	var p PaymentMethod
	err := row.Scan(&p.ID, &p.SalonID, &p.Method, &p.AccountName,
		&p.AccountRef, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func (r *repository) ListBySalon(ctx context.Context, salonID uuid.UUID, activeOnly bool) ([]*PaymentMethod, error) {
	// The filter is a bound parameter rather than two query strings, so the
	// public and artist-facing reads cannot drift apart.
	rows, err := r.db.Query(ctx, `
		SELECT `+paymentMethodColumns+`
		FROM salon_payment_methods
		WHERE salon_id = $1
		  AND ($2::boolean IS FALSE OR is_active = TRUE)
		ORDER BY method`, salonID, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("list payment methods: %w", err)
	}
	defer rows.Close()

	// Non-nil, so an empty result serialises as [] and not null - the same
	// defect ApiService.getArray had to paper over elsewhere.
	out := make([]*PaymentMethod, 0)
	for rows.Next() {
		p, err := scanPaymentMethod(rows)
		if err != nil {
			return nil, fmt.Errorf("scan payment method: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Upsert creates the salon's account for a method, or replaces it.
//
// ON CONFLICT on the (salon_id, method) unique constraint, so "add" and
// "change" are one atomic statement. Doing it as SELECT-then-INSERT-or-UPDATE
// would race two concurrent saves into a constraint violation.
func (r *repository) Upsert(ctx context.Context, salonID uuid.UUID, req UpsertPaymentMethodRequest) (*PaymentMethod, error) {
	row := r.db.QueryRow(ctx, `
		INSERT INTO salon_payment_methods (salon_id, method, account_name, account_ref)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (salon_id, method) DO UPDATE
		   SET account_name = EXCLUDED.account_name,
		       account_ref  = EXCLUDED.account_ref,
		       is_active    = TRUE,
		       updated_at   = NOW()
		RETURNING `+paymentMethodColumns,
		salonID, req.Method, req.AccountName, req.AccountRef)

	p, err := scanPaymentMethod(row)
	if err != nil {
		return nil, fmt.Errorf("upsert payment method: %w", err)
	}
	return p, nil
}

// SetActive retires or restores a destination.
//
// Scoped by salon_id in the WHERE clause, not checked afterwards: a foreign id
// then affects zero rows and returns ErrNoRows, which the service maps to the
// same 404 a nonexistent id gets. See CLAUDE.md on not letting a status code
// confirm that an id is real.
func (r *repository) SetActive(ctx context.Context, id, salonID uuid.UUID, active bool) (*PaymentMethod, error) {
	row := r.db.QueryRow(ctx, `
		UPDATE salon_payment_methods
		   SET is_active = $3, updated_at = NOW()
		 WHERE id = $1 AND salon_id = $2
		RETURNING `+paymentMethodColumns, id, salonID, active)

	p, err := scanPaymentMethod(row)
	if err != nil {
		return nil, err
	}
	return p, nil
}
