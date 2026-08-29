package billing

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// uniqueViolationCode is the Postgres SQLSTATE for a unique constraint
// violation - matches the constant already duplicated per-domain across
// artist, booking, customerauth, onboarding, product, and review.
const uniqueViolationCode = "23505"

// Repository defines all database operations required by the billing domain.
type Repository interface {
	// ListPublicPlans returns is_public plans ordered by sort_order - what
	// the public pricing page shows.
	ListPublicPlans(ctx context.Context) ([]*Plan, error)

	// ListAllPlans returns every plan including non-public ones (e.g.
	// 'comped') - for the admin Plans tab only.
	ListAllPlans(ctx context.Context) ([]*Plan, error)

	// GetPlanByCode returns a single plan, or ErrPlanNotFound.
	GetPlanByCode(ctx context.Context, code string) (*Plan, error)

	// CreatePlan inserts a new plan. Returns ErrPlanCodeExists if the code
	// is already taken.
	CreatePlan(ctx context.Context, p *Plan) error

	// UpdatePlan overwrites the given plan's editable fields in place.
	// Callers pass the fully-resolved Plan (existing row merged with the
	// caller's partial update) - see Service.UpdatePlan.
	UpdatePlan(ctx context.Context, p *Plan) error

	// GetArtistIDByUserID resolves a users.id to an artists.id - the same
	// query as earnings.Repository's method of the same name, duplicated
	// rather than shared because each domain's Repository interface is
	// self-contained (matches the existing pattern across this codebase).
	GetArtistIDByUserID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)

	// GetSubscriptionByArtistID returns ErrSubscriptionNotFound if the
	// artist has none yet (true for any artist onboarded after Phase 1 but
	// before Phase 3's plan-selection-at-signup ships).
	GetSubscriptionByArtistID(ctx context.Context, artistID uuid.UUID) (*Subscription, error)

	// GetSubscriptionByID is used by the admin subscription-edit endpoint,
	// which addresses a subscription by its own ID, not by artist ID.
	GetSubscriptionByID(ctx context.Context, id uuid.UUID) (*Subscription, error)

	// ListAllSubscriptions returns every subscription with no join - used
	// to drive lazy invoice generation across all artists before the admin
	// overview query (which DOES join) runs, so that query's outstanding
	// amounts reflect any invoice just generated. See
	// Service.ListSubscriptionsOverview for why this is two passes.
	ListAllSubscriptions(ctx context.Context) ([]*Subscription, error)

	// UpdateSubscription overwrites the given subscription's editable
	// fields. Callers pass the fully-resolved Subscription (existing row
	// merged with the caller's partial update) - see Service.UpdateSubscription.
	UpdateSubscription(ctx context.Context, s *Subscription) error

	// ListSubscriptionsOverview returns the admin billing overview: every
	// artist, their plan, and their current outstanding amount (the sum of
	// unpaid invoice amounts). Callers should run lazy invoice generation
	// via ListAllSubscriptions FIRST so this reflects up-to-date invoices.
	ListSubscriptionsOverview(ctx context.Context) ([]*SubscriptionOverviewRow, error)

	// CreateInvoiceIfMissing inserts an invoice for the given period unless
	// one already exists for that (subscription_id, period_start) pair -
	// see migration 025's unique index. Returns created=false on a no-op,
	// never an error, so this is always safe to call speculatively from
	// any read path without first checking whether the period is covered.
	CreateInvoiceIfMissing(ctx context.Context, inv *Invoice) (created bool, err error)

	// GetInvoiceByID returns ErrInvoiceNotFound if no invoice matches.
	GetInvoiceByID(ctx context.Context, id uuid.UUID) (*Invoice, error)

	// ListInvoicesByArtist returns an artist's own invoice history, newest
	// period first.
	ListInvoicesByArtist(ctx context.Context, artistID uuid.UUID) ([]*Invoice, error)

	// ListInvoicesByStatus returns every invoice with the given status, or
	// every invoice if status is empty. Backs the admin confirmation queue
	// (status="submitted") and a full history view (status="").
	ListInvoicesByStatus(ctx context.Context, status string) ([]*Invoice, error)

	// SubmitInvoice transitions an invoice issued→submitted, recording the
	// artist's payment reference. Returns ErrInvoiceWrongStatus if the
	// invoice isn't currently 'issued'.
	SubmitInvoice(ctx context.Context, id uuid.UUID, paymentReference string) error

	// ConfirmInvoice transitions an invoice submitted→paid AND extends its
	// subscription's current_period_end to the invoice's period_end, in a
	// single transaction - the two updates must never happen independently,
	// or a crash between them could mark money as received without
	// actually extending service, or vice versa. Returns
	// ErrInvoiceWrongStatus if the invoice isn't currently 'submitted'.
	ConfirmInvoice(ctx context.Context, id, confirmedBy uuid.UUID) (*Invoice, error)

	// VoidInvoice transitions an invoice to 'void' with a reason. Returns
	// ErrInvoiceWrongStatus if the invoice is already 'paid' or 'void' -
	// both are terminal states a void must never overwrite.
	VoidInvoice(ctx context.Context, id uuid.UUID, reason string) error
}

// pgRepo is the PostgreSQL implementation of Repository.
type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates a new PostgreSQL-backed billing Repository.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

const planColumns = `
	code, name, monthly_price, currency, seat_price, included_seats,
	description, features, is_public, sort_order, updated_at
`

// ListPublicPlans returns is_public=TRUE plans ordered by sort_order.
func (r *pgRepo) ListPublicPlans(ctx context.Context) ([]*Plan, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+planColumns+`
		FROM plans
		WHERE is_public = TRUE
		ORDER BY sort_order ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list public plans: %w", err)
	}
	defer rows.Close()
	return scanPlans(rows)
}

// ListAllPlans returns every plan, public and non-public, ordered by sort_order.
func (r *pgRepo) ListAllPlans(ctx context.Context) ([]*Plan, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+planColumns+`
		FROM plans
		ORDER BY sort_order ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list all plans: %w", err)
	}
	defer rows.Close()
	return scanPlans(rows)
}

// GetPlanByCode returns a single plan by its code.
func (r *pgRepo) GetPlanByCode(ctx context.Context, code string) (*Plan, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+planColumns+`
		FROM plans
		WHERE code = $1
	`, code)

	p, err := scanPlan(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPlanNotFound
		}
		return nil, fmt.Errorf("get plan by code: %w", err)
	}
	return p, nil
}

// CreatePlan inserts a new plan row.
func (r *pgRepo) CreatePlan(ctx context.Context, p *Plan) error {
	featuresJSON, err := json.Marshal(p.Features)
	if err != nil {
		return fmt.Errorf("create plan: marshal features: %w", err)
	}

	_, err = r.db.Exec(ctx, `
		INSERT INTO plans (code, name, monthly_price, currency, seat_price,
			included_seats, description, features, is_public, sort_order)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`, p.Code, p.Name, p.MonthlyPrice, p.Currency, p.SeatPrice,
		p.IncludedSeats, p.Description, featuresJSON, p.IsPublic, p.SortOrder)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrPlanCodeExists
		}
		return fmt.Errorf("create plan: %w", err)
	}
	return nil
}

// UpdatePlan overwrites every editable column for the given plan code.
// updated_at is bumped to NOW() unconditionally - any call to this method
// is, by definition, an edit.
func (r *pgRepo) UpdatePlan(ctx context.Context, p *Plan) error {
	featuresJSON, err := json.Marshal(p.Features)
	if err != nil {
		return fmt.Errorf("update plan: marshal features: %w", err)
	}

	tag, err := r.db.Exec(ctx, `
		UPDATE plans SET
			name = $2,
			monthly_price = $3,
			seat_price = $4,
			included_seats = $5,
			description = $6,
			features = $7,
			is_public = $8,
			sort_order = $9,
			updated_at = NOW()
		WHERE code = $1
	`, p.Code, p.Name, p.MonthlyPrice, p.SeatPrice, p.IncludedSeats,
		p.Description, featuresJSON, p.IsPublic, p.SortOrder)
	if err != nil {
		return fmt.Errorf("update plan: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPlanNotFound
	}
	return nil
}

// ── Subscriptions ─────────────────────────────────────────────────────────────

const subscriptionColumns = `
	id, artist_id, plan_code, seats, monthly_price, currency,
	trial_ends_at, current_period_end, cancelled_at, created_at, updated_at
`

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

func (r *pgRepo) GetSubscriptionByArtistID(ctx context.Context, artistID uuid.UUID) (*Subscription, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+subscriptionColumns+`
		FROM subscriptions
		WHERE artist_id = $1
	`, artistID)
	return scanSubscription(row)
}

func (r *pgRepo) GetSubscriptionByID(ctx context.Context, id uuid.UUID) (*Subscription, error) {
	row := r.db.QueryRow(ctx, `
		SELECT `+subscriptionColumns+`
		FROM subscriptions
		WHERE id = $1
	`, id)
	return scanSubscription(row)
}

func (r *pgRepo) ListAllSubscriptions(ctx context.Context) ([]*Subscription, error) {
	rows, err := r.db.Query(ctx, `SELECT `+subscriptionColumns+` FROM subscriptions`)
	if err != nil {
		return nil, fmt.Errorf("list all subscriptions: %w", err)
	}
	defer rows.Close()

	var result []*Subscription
	for rows.Next() {
		s, err := scanSubscription(rows)
		if err != nil {
			return nil, fmt.Errorf("list all subscriptions: scan: %w", err)
		}
		result = append(result, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list all subscriptions: rows: %w", err)
	}
	return result, nil
}

func (r *pgRepo) UpdateSubscription(ctx context.Context, s *Subscription) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE subscriptions SET
			plan_code = $2,
			seats = $3,
			monthly_price = $4,
			currency = $5,
			trial_ends_at = $6,
			current_period_end = $7,
			cancelled_at = $8,
			updated_at = NOW()
		WHERE id = $1
	`, s.ID, s.PlanCode, s.Seats, s.MonthlyPrice, s.Currency,
		s.TrialEndsAt, s.CurrentPeriodEnd, s.CancelledAt)
	if err != nil {
		return fmt.Errorf("update subscription: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrSubscriptionNotFound
	}
	return nil
}

// ListSubscriptionsOverview returns every artist's subscription joined with
// their name and plan name, plus the sum of their unpaid (issued or
// submitted) invoice amounts. Callers should run lazy invoice generation
// via ListAllSubscriptions first - see the Repository interface doc comment.
func (r *pgRepo) ListSubscriptionsOverview(ctx context.Context) ([]*SubscriptionOverviewRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT
			s.artist_id, u.name, s.id, s.plan_code, p.name,
			s.monthly_price, s.currency, s.current_period_end,
			s.seats, s.trial_ends_at, s.cancelled_at,
			COALESCE((
				SELECT SUM(i.amount) FROM invoices i
				WHERE i.subscription_id = s.id AND i.status IN ('issued', 'submitted')
			), 0) AS outstanding_amount
		FROM subscriptions s
		JOIN artists a ON a.id = s.artist_id
		JOIN users u ON u.id = a.user_id
		JOIN plans p ON p.code = s.plan_code
		ORDER BY u.name ASC
	`)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions overview: %w", err)
	}
	defer rows.Close()

	var result []*SubscriptionOverviewRow
	for rows.Next() {
		var row SubscriptionOverviewRow
		if err := rows.Scan(
			&row.ArtistID, &row.ArtistName, &row.SubscriptionID, &row.PlanCode, &row.PlanName,
			&row.MonthlyPrice, &row.Currency, &row.CurrentPeriodEnd,
			&row.Seats, &row.TrialEndsAt, &row.CancelledAt,
			&row.OutstandingAmount,
		); err != nil {
			return nil, fmt.Errorf("list subscriptions overview: scan: %w", err)
		}
		// Status is derived, not a DB column - DeriveStatus takes a
		// Subscription, so assemble the minimal one it actually reads from
		// the fields just scanned above rather than a second query.
		row.Status = DeriveStatus(&Subscription{
			PlanCode:         row.PlanCode,
			TrialEndsAt:      row.TrialEndsAt,
			CurrentPeriodEnd: row.CurrentPeriodEnd,
			CancelledAt:      row.CancelledAt,
		}, time.Now())
		result = append(result, &row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list subscriptions overview: rows: %w", err)
	}
	return result, nil
}

func scanSubscription(row rowScanner) (*Subscription, error) {
	var s Subscription
	err := row.Scan(
		&s.ID, &s.ArtistID, &s.PlanCode, &s.Seats, &s.MonthlyPrice, &s.Currency,
		&s.TrialEndsAt, &s.CurrentPeriodEnd, &s.CancelledAt, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrSubscriptionNotFound
		}
		return nil, fmt.Errorf("scan subscription: %w", err)
	}
	return &s, nil
}

// ── Invoices ──────────────────────────────────────────────────────────────────

const invoiceColumns = `
	id, subscription_id, artist_id, invoice_number, period_start, period_end,
	due_date, amount, currency, seats_billed, plan_code, status,
	payment_reference, submitted_at, confirmed_by, paid_at, void_reason, created_at
`

func (r *pgRepo) CreateInvoiceIfMissing(ctx context.Context, inv *Invoice) (bool, error) {
	tag, err := r.db.Exec(ctx, `
		INSERT INTO invoices (
			id, subscription_id, artist_id, period_start, period_end, due_date,
			amount, currency, seats_billed, plan_code, status
		) VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6, $7, $8, $9, 'issued')
		ON CONFLICT (subscription_id, period_start) DO NOTHING
	`, inv.SubscriptionID, inv.ArtistID, inv.PeriodStart, inv.PeriodEnd, inv.DueDate,
		inv.Amount, inv.Currency, inv.SeatsBilled, inv.PlanCode)
	if err != nil {
		return false, fmt.Errorf("create invoice if missing: %w", err)
	}
	return tag.RowsAffected() > 0, nil
}

func (r *pgRepo) GetInvoiceByID(ctx context.Context, id uuid.UUID) (*Invoice, error) {
	row := r.db.QueryRow(ctx, `SELECT `+invoiceColumns+` FROM invoices WHERE id = $1`, id)
	return scanInvoice(row)
}

func (r *pgRepo) ListInvoicesByArtist(ctx context.Context, artistID uuid.UUID) ([]*Invoice, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+invoiceColumns+`
		FROM invoices
		WHERE artist_id = $1
		ORDER BY period_start DESC
	`, artistID)
	if err != nil {
		return nil, fmt.Errorf("list invoices by artist: %w", err)
	}
	defer rows.Close()
	return scanInvoices(rows)
}

func (r *pgRepo) ListInvoicesByStatus(ctx context.Context, status string) ([]*Invoice, error) {
	var rows pgx.Rows
	var err error
	if status == "" {
		rows, err = r.db.Query(ctx, `SELECT `+invoiceColumns+` FROM invoices ORDER BY created_at DESC`)
	} else {
		rows, err = r.db.Query(ctx, `
			SELECT `+invoiceColumns+`
			FROM invoices
			WHERE status = $1
			ORDER BY created_at DESC
		`, status)
	}
	if err != nil {
		return nil, fmt.Errorf("list invoices by status: %w", err)
	}
	defer rows.Close()
	return scanInvoices(rows)
}

func (r *pgRepo) SubmitInvoice(ctx context.Context, id uuid.UUID, paymentReference string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE invoices SET
			status = 'submitted',
			payment_reference = NULLIF($2, ''),
			submitted_at = NOW()
		WHERE id = $1 AND status = 'issued'
	`, id, paymentReference)
	if err != nil {
		return fmt.Errorf("submit invoice: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInvoiceWrongStatus
	}
	return nil
}

// ConfirmInvoice marks an invoice paid and extends its subscription's
// current_period_end in one transaction. GREATEST guards against ever
// moving current_period_end backwards, which matters if invoices are ever
// confirmed out of chronological order.
func (r *pgRepo) ConfirmInvoice(ctx context.Context, id, confirmedBy uuid.UUID) (*Invoice, error) {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("confirm invoice: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit

	row := tx.QueryRow(ctx, `
		UPDATE invoices SET
			status = 'paid',
			confirmed_by = $2,
			paid_at = NOW()
		WHERE id = $1 AND status = 'submitted'
		RETURNING `+invoiceColumns, id, confirmedBy)

	inv, err := scanInvoice(row)
	if err != nil {
		if errors.Is(err, ErrInvoiceNotFound) {
			return nil, ErrInvoiceWrongStatus
		}
		return nil, fmt.Errorf("confirm invoice: update: %w", err)
	}

	_, err = tx.Exec(ctx, `
		UPDATE subscriptions SET
			current_period_end = GREATEST(COALESCE(current_period_end, $2::timestamptz), $2::timestamptz),
			updated_at = NOW()
		WHERE id = $1
	`, inv.SubscriptionID, inv.PeriodEnd)
	if err != nil {
		return nil, fmt.Errorf("confirm invoice: extend period: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("confirm invoice: commit: %w", err)
	}
	return inv, nil
}

func (r *pgRepo) VoidInvoice(ctx context.Context, id uuid.UUID, reason string) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE invoices SET status = 'void', void_reason = $2
		WHERE id = $1 AND status IN ('issued', 'submitted')
	`, id, reason)
	if err != nil {
		return fmt.Errorf("void invoice: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrInvoiceWrongStatus
	}
	return nil
}

func scanInvoice(row rowScanner) (*Invoice, error) {
	var inv Invoice
	err := row.Scan(
		&inv.ID, &inv.SubscriptionID, &inv.ArtistID, &inv.InvoiceNumber,
		&inv.PeriodStart, &inv.PeriodEnd, &inv.DueDate, &inv.Amount, &inv.Currency,
		&inv.SeatsBilled, &inv.PlanCode, &inv.Status, &inv.PaymentReference,
		&inv.SubmittedAt, &inv.ConfirmedBy, &inv.PaidAt, &inv.VoidReason, &inv.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvoiceNotFound
		}
		return nil, fmt.Errorf("scan invoice: %w", err)
	}
	return &inv, nil
}

func scanInvoices(rows pgx.Rows) ([]*Invoice, error) {
	var result []*Invoice
	for rows.Next() {
		inv, err := scanInvoice(rows)
		if err != nil {
			return nil, fmt.Errorf("scan invoices: %w", err)
		}
		result = append(result, inv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan invoices: rows: %w", err)
	}
	return result, nil
}

// ── Scanning helpers ───────────────────────────────────────────────────────────

// rowScanner is satisfied by both pgx.Row (QueryRow) and pgx.Rows (Query) -
// lets scanPlan back both GetPlanByCode and the list queries without
// duplicating the Scan call and its column order.
type rowScanner interface {
	Scan(dest ...any) error
}

func scanPlan(row rowScanner) (*Plan, error) {
	var p Plan
	var featuresJSON []byte
	var monthlyPrice, seatPrice decimal.Decimal

	err := row.Scan(
		&p.Code, &p.Name, &monthlyPrice, &p.Currency, &seatPrice,
		&p.IncludedSeats, &p.Description, &featuresJSON, &p.IsPublic,
		&p.SortOrder, &p.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	p.MonthlyPrice = monthlyPrice
	p.SeatPrice = seatPrice

	if len(featuresJSON) > 0 {
		if err := json.Unmarshal(featuresJSON, &p.Features); err != nil {
			return nil, fmt.Errorf("scan plan: unmarshal features: %w", err)
		}
	}
	if p.Features == nil {
		p.Features = []string{}
	}

	return &p, nil
}

func scanPlans(rows pgx.Rows) ([]*Plan, error) {
	var result []*Plan
	for rows.Next() {
		p, err := scanPlan(rows)
		if err != nil {
			return nil, fmt.Errorf("scan plans: %w", err)
		}
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan plans: rows: %w", err)
	}
	return result, nil
}

// isUniqueViolation reports whether err is a Postgres unique_violation -
// used to translate a duplicate plans.code insert into the domain-level
// ErrPlanCodeExists rather than a raw DB error leaking up.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == uniqueViolationCode
	}
	return false
}
