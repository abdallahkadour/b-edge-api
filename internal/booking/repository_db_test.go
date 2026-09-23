//go:build dbtest

package booking

// Repository tests against a real PostgreSQL database.
//
// These exist for exactly one reason: THERE ARE THINGS A MOCK CANNOT TEST, and
// until this file the whole repository layer sat at 0.0% coverage while the
// most important guarantee in the system lived there.
//
// The GIST exclusion constraint is that guarantee. Migration 001 calls it
// "the final atomic guard - no application-level check can replace it", and
// every other test of it in this project goes through HTTP, where a passing
// result cannot distinguish "the constraint held" from "the two requests
// happened not to overlap". Here the overlap is constructed deliberately and
// the database is asked directly.
//
// Four properties are checked, and three of them are about when the constraint
// must NOT fire. A constraint that rejects everything would pass a naive
// overlap test while making the product unusable:
//
//   - overlapping bookings for one artist are refused
//   - adjacent bookings are allowed ('[)' bounds - one ending exactly as the
//     next starts is not an overlap)
//   - two artists may hold the same time
//   - a cancelled booking does not block its old slot (the constraint's WHERE)
//
// Run with: make test-db

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

// fixture is the minimum set of rows a booking needs to exist.
type fixture struct {
	SalonID   uuid.UUID
	StoreID   uuid.UUID
	ArtistID  uuid.UUID
	Artist2ID uuid.UUID
	ServiceID uuid.UUID
	Customer  uuid.UUID
}

func newFixture(t *testing.T, pool *pgxpool.Pool) fixture {
	t.Helper()
	ctx := context.Background()

	var f fixture
	owner := insertUser(t, pool, "owner@dbtest.local", "artist")
	f.Customer = insertUser(t, pool, "customer@dbtest.local", "customer")
	second := insertUser(t, pool, "second@dbtest.local", "artist")

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO salons (owner_id, name) VALUES ($1,'DB Test Salon') RETURNING id`,
		owner).Scan(&f.SalonID))

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO stores (salon_id, name, city) VALUES ($1,'Main','Beirut') RETURNING id`,
		f.SalonID).Scan(&f.StoreID))

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO artists (user_id, salon_id) VALUES ($1,$2) RETURNING id`,
		owner, f.SalonID).Scan(&f.ArtistID))

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO artists (user_id, salon_id) VALUES ($1,$2) RETURNING id`,
		second, f.SalonID).Scan(&f.Artist2ID))

	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO services (salon_id, name, duration_min, price)
		 VALUES ($1,'Makeup',60,100.00) RETURNING id`,
		f.SalonID).Scan(&f.ServiceID))

	return f
}

func insertUser(t *testing.T, pool *pgxpool.Pool, email, role string) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ($1,$2,'x',$3) RETURNING id`,
		"DB Test "+role, email, role).Scan(&id))
	return id
}

// booking builds a Booking the repository will accept, with an explicit
// artist, start time and status so each test states its own conditions.
func (f fixture) booking(artistID uuid.UUID, start time.Time, status string) *Booking {
	end := start.Add(60 * time.Minute)
	return &Booking{
		ID:             uuid.New(),
		SalonID:        f.SalonID,
		StoreID:        f.StoreID,
		ArtistID:       artistID,
		CustomerID:     f.Customer,
		ServiceID:      f.ServiceID,
		StartTime:      start,
		EndTime:        end,
		BlockedUntil:   end,
		BufferMin:      0,
		Status:         status,
		OriginalPrice:  decimal.NewFromInt(100),
		DiscountAmount: decimal.Zero,
		FinalPrice:     decimal.NewFromInt(100),
		DepositAmount:  decimal.Zero,
		Channel:        ChannelCustomerPWA,
	}
}

// ── the exclusion constraint ───────────────────────────────────────────────

func TestGISTConstraint_OverlappingBookingsForOneArtist_Refused(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newFixture(t, pool)
	ctx := context.Background()

	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)

	// Establish the condition rather than assuming it: the first booking must
	// actually be in the table before the second proves anything.
	first := f.booking(f.ArtistID, start, StatusConfirmed)
	require.NoError(t, repo.CreateBooking(ctx, first, nil),
		"the first booking must succeed, or the second proves nothing")

	// Starts 30 minutes in, while the first is still running.
	second := f.booking(f.ArtistID, start.Add(30*time.Minute), StatusConfirmed)
	err := repo.CreateBooking(ctx, second, nil)

	require.Error(t, err, "an overlapping booking must be refused")
	assert.True(t, errors.Is(err, ErrSlotUnavailable),
		"the overlap must surface as ErrSlotUnavailable, got %v", err)

	assert.Equal(t, 1, countBookings(t, pool), "only the first booking may exist")
}

func TestGISTConstraint_AdjacentBookings_Allowed(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newFixture(t, pool)
	ctx := context.Background()

	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)

	first := f.booking(f.ArtistID, start, StatusConfirmed)
	require.NoError(t, repo.CreateBooking(ctx, first, nil))

	// Starts exactly when the first one's blocked span ends. The range is
	// '[)' - half open - so this is not an overlap. If the bounds were ever
	// changed to '[]' this test is what catches it, and the symptom in
	// production would be an artist unable to book back-to-back appointments.
	second := f.booking(f.ArtistID, first.BlockedUntil, StatusConfirmed)

	assert.NoError(t, repo.CreateBooking(ctx, second, nil),
		"back-to-back bookings must be allowed")
	assert.Equal(t, 2, countBookings(t, pool))
}

func TestGISTConstraint_DifferentArtistsSameTime_Allowed(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newFixture(t, pool)
	ctx := context.Background()

	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)

	require.NoError(t, repo.CreateBooking(ctx, f.booking(f.ArtistID, start, StatusConfirmed), nil))

	// The constraint keys on artist_id. Two artists in one salon working the
	// same hour is the entire point of multi-artist support - if this ever
	// fails, the salon feature is broken at the database level.
	err := repo.CreateBooking(ctx, f.booking(f.Artist2ID, start, StatusConfirmed), nil)

	assert.NoError(t, err, "two artists must be able to work the same hour")
	assert.Equal(t, 2, countBookings(t, pool))
}

func TestGISTConstraint_CancelledBooking_DoesNotBlockItsOldSlot(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	f := newFixture(t, pool)
	ctx := context.Background()

	start := time.Now().UTC().Add(48 * time.Hour).Truncate(time.Hour)

	// This is the constraint's WHERE clause, and it is the half that matters
	// commercially: a cancellation must return the slot to sale. If the
	// predicate were dropped, every cancelled booking would permanently
	// sterilise its own hour and nobody would see an error - just an artist
	// whose calendar slowly fills with time she cannot sell.
	cancelled := f.booking(f.ArtistID, start, StatusCancelled)
	require.NoError(t, repo.CreateBooking(ctx, cancelled, nil))

	err := repo.CreateBooking(ctx, f.booking(f.ArtistID, start, StatusConfirmed), nil)

	assert.NoError(t, err, "a cancelled booking must not block its old slot")
	assert.Equal(t, 2, countBookings(t, pool))
}

func countBookings(t *testing.T, pool *pgxpool.Pool) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT count(*) FROM bookings`).Scan(&n))
	return n
}
