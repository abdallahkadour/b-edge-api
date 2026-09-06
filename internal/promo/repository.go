package promo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrNotFound is returned for a code that does not exist in this salon.
//
// Deliberately indistinguishable at the API boundary from a code that exists
// but is deactivated - see eligibility.Check. A caller can tell them apart
// here, where it is needed for admin screens, and must not downstream.
var ErrNotFound = errors.New("discount not found")

// ErrAlreadyRedeemed is the partial unique index firing: this customer already
// holds a consumed redemption of this code.
//
// It exists because the index is the REAL enforcement, not the eligibility
// check. Two concurrent checkouts both pass Check - neither has committed yet -
// and one of them then loses this race. That is the point of the constraint,
// and this is how the loser is told.
var ErrAlreadyRedeemed = errors.New("discount already redeemed by this customer")

const uniqueViolationCode = "23505"

// Repository is the persistence surface for codes and their redemptions.
type Repository interface {
	// GetByCode resolves a code within one salon, case-insensitively -
	// customers type these by hand.
	GetByCode(ctx context.Context, salonID uuid.UUID, code string) (*Discount, error)
	GetByID(ctx context.Context, id uuid.UUID) (*Discount, error)

	// GatherFacts collects everything eligibility.Check needs, in ONE query.
	// Three round trips for three booleans on the booking hot path would be
	// three chances to be slow for no benefit.
	GatherFacts(ctx context.Context, discountID, customerID, salonID uuid.UUID) (Facts, error)

	ListBySalon(ctx context.Context, salonID uuid.UUID) ([]*DiscountResponse, error)
	Create(ctx context.Context, d *Discount) error
	Update(ctx context.Context, id, salonID uuid.UUID, req UpdateDiscountRequest) (*Discount, error)

	// ReleaseForBooking marks a booking's redemption unconsumed (D3.5), making
	// the code usable again without deleting the record that it was offered.
	ReleaseForBooking(ctx context.Context, bookingID uuid.UUID) error
}

type pgRepo struct{ db *pgxpool.Pool }

// NewRepository creates a promo repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) Repository { return &pgRepo{db: db} }

const discountColumns = `id, salon_id, code, description, kind, value,
	starts_at, ends_at, max_redemptions, first_time_only, is_active,
	created_at, updated_at`

func scanDiscount(row pgx.Row) (*Discount, error) {
	d := &Discount{}
	err := row.Scan(&d.ID, &d.SalonID, &d.Code, &d.Description, &d.Kind, &d.Value,
		&d.StartsAt, &d.EndsAt, &d.MaxRedemptions, &d.FirstTimeOnly, &d.IsActive,
		&d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return d, nil
}

func (r *pgRepo) GetByCode(ctx context.Context, salonID uuid.UUID, code string) (*Discount, error) {
	// UPPER on both sides, matching idx_discounts_salon_code exactly so the
	// index is used rather than a sequential scan.
	d, err := scanDiscount(r.db.QueryRow(ctx,
		`SELECT `+discountColumns+` FROM discounts
		 WHERE salon_id = $1 AND UPPER(code) = UPPER($2)`, salonID, code))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("get discount by code: %w", err)
	}
	return d, err
}

func (r *pgRepo) GetByID(ctx context.Context, id uuid.UUID) (*Discount, error) {
	d, err := scanDiscount(r.db.QueryRow(ctx,
		`SELECT `+discountColumns+` FROM discounts WHERE id = $1`, id))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("get discount by id: %w", err)
	}
	return d, err
}

// GatherFacts answers all three situational questions in one round trip.
//
// CustomerIsNew is "no COMPLETED booking at this salon" rather than "no
// booking at all": a held-then-abandoned slot, or a cancellation, must not
// burn a first-time offer the customer never got the benefit of.
func (r *pgRepo) GatherFacts(ctx context.Context, discountID, customerID, salonID uuid.UUID) (Facts, error) {
	var f Facts
	err := r.db.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM discount_redemptions
			  WHERE discount_id = $1 AND consumed),
			EXISTS (SELECT 1 FROM discount_redemptions
			         WHERE discount_id = $1 AND customer_id = $2 AND consumed),
			NOT EXISTS (SELECT 1 FROM bookings
			             WHERE customer_id = $2 AND salon_id = $3
			               AND status = 'completed' AND deleted_at IS NULL)`,
		discountID, customerID, salonID,
	).Scan(&f.ConsumedRedemptions, &f.CustomerHasUsed, &f.CustomerIsNew)
	if err != nil {
		return Facts{}, fmt.Errorf("gather discount facts: %w", err)
	}
	f.Now = time.Now().UTC()
	return f, nil
}

func (r *pgRepo) ListBySalon(ctx context.Context, salonID uuid.UUID) ([]*DiscountResponse, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+discountColumns+`,
		       (SELECT count(*) FROM discount_redemptions dr
		         WHERE dr.discount_id = d.id AND dr.consumed)
		FROM discounts d
		WHERE salon_id = $1
		ORDER BY is_active DESC, created_at DESC`, salonID)
	if err != nil {
		return nil, fmt.Errorf("list discounts: %w", err)
	}
	defer rows.Close()

	out := make([]*DiscountResponse, 0)
	for rows.Next() {
		d := &Discount{}
		var count int
		if err := rows.Scan(&d.ID, &d.SalonID, &d.Code, &d.Description, &d.Kind, &d.Value,
			&d.StartsAt, &d.EndsAt, &d.MaxRedemptions, &d.FirstTimeOnly, &d.IsActive,
			&d.CreatedAt, &d.UpdatedAt, &count); err != nil {
			return nil, fmt.Errorf("scan discount: %w", err)
		}
		out = append(out, toDiscountResponse(d, count))
	}
	return out, rows.Err()
}

func (r *pgRepo) Create(ctx context.Context, d *Discount) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO discounts (salon_id, code, description, kind, value,
		                       starts_at, ends_at, max_redemptions, first_time_only)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		RETURNING id, is_active, created_at, updated_at`,
		d.SalonID, d.Code, d.Description, d.Kind, d.Value,
		d.StartsAt, d.EndsAt, d.MaxRedemptions, d.FirstTimeOnly,
	).Scan(&d.ID, &d.IsActive, &d.CreatedAt, &d.UpdatedAt)
	if err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == uniqueViolationCode {
			return ErrDuplicateCode
		}
		return fmt.Errorf("create discount: %w", err)
	}
	return nil
}

// ErrDuplicateCode is the per-salon uniqueness index firing.
var ErrDuplicateCode = errors.New("a discount with that code already exists")

func (r *pgRepo) Update(ctx context.Context, id, salonID uuid.UUID, req UpdateDiscountRequest) (*Discount, error) {
	// COALESCE for every field: a PATCH omitting a field leaves it alone. The
	// code itself is absent on purpose - see UpdateDiscountRequest.
	d, err := scanDiscount(r.db.QueryRow(ctx, `
		UPDATE discounts SET
			description     = COALESCE($3, description),
			value           = COALESCE($4, value),
			starts_at       = COALESCE($5, starts_at),
			ends_at         = COALESCE($6, ends_at),
			max_redemptions = COALESCE($7, max_redemptions),
			is_active       = COALESCE($8, is_active),
			updated_at      = NOW()
		WHERE id = $1 AND salon_id = $2
		RETURNING `+discountColumns,
		id, salonID, req.Description, req.Value,
		req.StartsAt, req.EndsAt, req.MaxRedemptions, req.IsActive))
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("update discount: %w", err)
	}
	return d, err
}

func (r *pgRepo) ReleaseForBooking(ctx context.Context, bookingID uuid.UUID) error {
	// No error when nothing matches: most bookings carry no discount, and
	// cancelling one of those is not a failure.
	if _, err := r.db.Exec(ctx,
		`UPDATE discount_redemptions SET consumed = FALSE WHERE booking_id = $1`,
		bookingID); err != nil {
		return fmt.Errorf("release discount redemption: %w", err)
	}
	return nil
}
