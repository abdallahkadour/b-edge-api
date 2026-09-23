//go:build dbtest

package billing

// Repository tests against a real PostgreSQL database.
//
// Two things are proved here, and the second is the more important.
//
// 1. CreateInvoiceIfMissing is idempotent, and SubmitInvoice / ConfirmInvoice
//    refuse to act on the wrong status AT THE SQL LEVEL. Invoice state is the
//    only place B-Edge records that money changed hands - there is no gateway
//    to reconcile against, so a double-confirm is unrecoverable by any means
//    other than someone remembering.
//
// 2. NUMERIC(10,2) SILENTLY ROUNDS, and a CHECK constraint cannot stop it.
//    This is a characterization test of PostgreSQL itself, and it exists
//    because CLAUDE.md asserts it as the load-bearing reason internal/pkg/money
//    must be a whitelist:
//
//        "scale can never be defended at the database level (the column
//         coerces before any CHECK runs), which makes this package the only
//         defence rather than defence-in-depth"
//
//    That claim is the entire justification for an input whitelist. Nothing
//    verified it. The project has already had one documentation claim -
//    "mutation-tested" - repeated as evidence for weeks while being false, so
//    an architectural premise nobody has executed is worth executing.
//
// Run with: make test-db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

type billingFixture struct {
	ArtistID       uuid.UUID
	SubscriptionID uuid.UUID
}

func newBillingFixture(t *testing.T, pool *pgxpool.Pool) billingFixture {
	t.Helper()
	ctx := context.Background()

	var userID, salonID uuid.UUID
	var f billingFixture

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ('DB Test','billing@dbtest.local','x','artist') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO salons (owner_id, name) VALUES ($1,'DB Test Salon') RETURNING id`,
		userID).Scan(&salonID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO artists (user_id, salon_id) VALUES ($1,$2) RETURNING id`,
		userID, salonID).Scan(&f.ArtistID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO subscriptions (artist_id, plan_code, monthly_price)
		 VALUES ($1,'solo',45.00) RETURNING id`,
		f.ArtistID).Scan(&f.SubscriptionID))

	return f
}

func (f billingFixture) invoice(period time.Time) *Invoice {
	return &Invoice{
		ID:             uuid.New(),
		SubscriptionID: f.SubscriptionID,
		ArtistID:       f.ArtistID,
		PeriodStart:    period,
		PeriodEnd:      period.AddDate(0, 1, 0),
		DueDate:        period,
		Amount:         decimal.NewFromFloat(45.00),
		SeatsBilled:    1,
		PlanCode:       "solo",
		// Required: migration 051 added CHECK (currency = 'USD'), so a
		// zero-valued Currency is rejected by the database rather than
		// silently stored as ''. The constraint doing its job is why this
		// line exists.
		Currency: "USD",
		// 'issued' is the column default AND what SubmitInvoice's WHERE
		// clause requires. A zero-valued Status inserts '' and every
		// transition below then refuses - which is the status guard working,
		// not a bug.
		Status: "issued",
	}
}

// invoiceIDFor looks the created invoice's real ID out of the database.
//
// NOT inv.ID. CreateInvoiceIfMissing's INSERT uses gen_random_uuid() for the
// primary key and never reads the ID on the struct it was handed, nor writes
// the generated one back. So a caller holding the *Invoice it just passed in
// has an ID that addresses nothing - every subsequent call with it silently
// hits zero rows and surfaces as "not in the required status", which reads
// like a state-machine rejection rather than a wrong identifier.
//
// Nothing in the service layer trips over this today because it re-reads
// invoices by period rather than by the struct it built. It is recorded here
// rather than changed, because changing a primary-key source is not something
// to do as a side effect of writing a test.
func invoiceIDFor(t *testing.T, pool *pgxpool.Pool, subID uuid.UUID, period time.Time) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT id FROM invoices WHERE subscription_id=$1 AND period_start=$2`,
		subID, period).Scan(&id))
	return id
}

// ── invoice lifecycle ──────────────────────────────────────────────────────

func TestCreateInvoiceIfMissing_SamePeriodTwice_CreatesOne(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newBillingFixture(t, pool)
	ctx := context.Background()

	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)

	created, err := repo.CreateInvoiceIfMissing(ctx, f.invoice(period))
	require.NoError(t, err)
	require.True(t, created, "the first call must create the invoice, or the second proves nothing")

	// The same period again - a retry, a restarted worker, a double request.
	// Idempotency here is what stops an artist being billed twice for one
	// month, and there is no gateway to reverse a duplicate charge.
	createdAgain, err := repo.CreateInvoiceIfMissing(ctx, f.invoice(period))
	require.NoError(t, err)

	assert.False(t, createdAgain, "the second call must not create a second invoice")
	assert.Equal(t, 1, countInvoices(t, pool), "exactly one invoice may exist for the period")
}

func TestSubmitInvoice_AlreadySubmitted_DoesNotOverwrite(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newBillingFixture(t, pool)
	ctx := context.Background()

	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	_, err := repo.CreateInvoiceIfMissing(ctx, f.invoice(period))
	require.NoError(t, err)
	id := invoiceIDFor(t, pool, f.SubscriptionID, period)

	require.NoError(t, repo.SubmitInvoice(ctx, id, "OMT-FIRST-REF"))

	// A second submission with a different reference. Whether this errors or
	// silently no-ops is the repository's business; what must NOT happen is
	// the original payment reference being replaced, because that reference
	// is the only record of which transfer the artist actually made.
	_ = repo.SubmitInvoice(ctx, id, "OMT-SECOND-REF")

	var ref string
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT coalesce(payment_reference,'') FROM invoices WHERE id=$1`, id).Scan(&ref))

	assert.Equal(t, "OMT-FIRST-REF", ref,
		"a second submission must not overwrite the reference of the transfer actually made")
}

func TestConfirmInvoice_Twice_ConfirmsOnce(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newBillingFixture(t, pool)
	ctx := context.Background()

	period := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	_, err := repo.CreateInvoiceIfMissing(ctx, f.invoice(period))
	require.NoError(t, err)
	id := invoiceIDFor(t, pool, f.SubscriptionID, period)
	require.NoError(t, repo.SubmitInvoice(ctx, id, "OMT-REF"))

	admin := adminUser(t, pool)

	first, err := repo.ConfirmInvoice(ctx, id, admin)
	require.NoError(t, err, "the first confirmation must succeed")
	require.NotNil(t, first)

	// Two admins clicking confirm on the same invoice. There is no payment
	// gateway to ask "was this already settled" - the row is the only record.
	_, secondErr := repo.ConfirmInvoice(ctx, id, admin)

	var status string
	var paidAt *time.Time
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT status, paid_at FROM invoices WHERE id=$1`, id).Scan(&status, &paidAt))

	assert.Equal(t, "paid", status)
	require.NotNil(t, paidAt, "the confirmation must have stamped paid_at")
	require.NotNil(t, first.PaidAt)
	assert.Equal(t, first.PaidAt.UTC(), paidAt.UTC(),
		"a second confirmation must not move the timestamp of the first")
	_ = secondErr // the guard may be an error or a no-op; the row is what matters
}

// ── the architectural premise ──────────────────────────────────────────────

func TestNumericColumn_SilentlyRoundsExcessScale(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	// CLAUDE.md states that scale "can never be defended at the database
	// level (the column coerces before any CHECK runs)". If that were
	// FALSE - if the column rejected excess scale, or a CHECK could catch it
	// - then internal/pkg/money would be belt-and-braces rather than the only
	// line of defence, and its deliberate rejection of "10.999" would be
	// unnecessary strictness.
	//
	// It is true, and this is what it looks like: 10.999 goes in, 11.00 comes
	// out, no error, no warning.
	var stored decimal.Decimal
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT $1::NUMERIC(10,2)`, "10.999").Scan(&stored))

	assert.Equal(t, "11", stored.String(),
		"NUMERIC(10,2) must be shown to round silently - the premise money's whitelist rests on")
}

func TestNumericColumn_CheckConstraintCannotSeeTheOriginalScale(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()

	// The follow-on claim: a CHECK cannot rescue it, because coercion to the
	// column type happens BEFORE the constraint is evaluated. By the time the
	// CHECK runs, 10.999 is already 11.00 and looks perfectly well-formed.
	//
	// Anyone proposing "just add a CHECK on scale" should run this first.
	_, err := pool.Exec(ctx, `
		CREATE TABLE scale_probe (
			amount NUMERIC(10,2),
			CONSTRAINT two_decimals CHECK (scale(amount) <= 2)
		)`)
	require.NoError(t, err)

	_, err = pool.Exec(ctx, `INSERT INTO scale_probe (amount) VALUES ('10.999')`)

	assert.NoError(t, err,
		"the CHECK cannot fire: the value is rounded to 11.00 before it is evaluated")

	var stored decimal.Decimal
	require.NoError(t, pool.QueryRow(ctx, `SELECT amount FROM scale_probe`).Scan(&stored))
	assert.Equal(t, "11", stored.String(),
		"the row was accepted and silently rounded, with a constraint that was supposed to stop it")
}

// ── helpers ────────────────────────────────────────────────────────────────

func adminUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ('DB Admin','admin@dbtest.local','x','admin') RETURNING id`).Scan(&id))
	return id
}

func countInvoices(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM invoices`).Scan(&n))
	return n
}
