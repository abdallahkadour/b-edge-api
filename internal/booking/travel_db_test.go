//go:build dbtest

package booking

// Travel between an artist's stores, enforced where the booking is written
// (audit 2026-10-10, T1 under concurrency).
//
// The service now refuses a time the slot list would not offer, but that is
// a read followed by a write: two holds in the same second - Beirut 10:00 and
// Tripoli 11:30 - can both read "free". The GIST exclusion constraint cannot
// help; the two appointments do not overlap, only the road between them
// does. So every write to an artist's diary takes one per-artist transaction
// lock and re-checks travel inside the transaction, after its own row is in.
//
// Run with: make test-db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

// secondStore adds Tripoli to the fixture's salon.
func secondStore(t *testing.T, pool *pgxpool.Pool, f fixture) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(),
		`INSERT INTO stores (salon_id, name, city) VALUES ($1,'Tripoli','Tripoli') RETURNING id`, f.SalonID).Scan(&id))
	return id
}

func bookedAt(f fixture, store uuid.UUID, start time.Time, travel map[uuid.UUID]int) *Booking {
	b := f.booking(f.ArtistID, start, StatusHeld)
	b.StoreID = store
	b.travel = travel
	return b
}

func TestTravel_ASecondBookingInsideTheRoad_IsRefusedByTheTransaction(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)
	tripoli := secondStore(t, pool, f)
	ten := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)

	require.NoError(t, repo.CreateBooking(ctx, bookedAt(f, f.StoreID, ten, map[uuid.UUID]int{tripoli: 150}), nil))

	// Tripoli at 11:30: no overlap for GIST to see, 30 minutes of road.
	err := repo.CreateGuestHold(ctx, bookedAt(f, tripoli, ten.Add(90*time.Minute), map[uuid.UUID]int{f.StoreID: 150}), "", 2)
	assert.ErrorIs(t, err, ErrSlotUnavailable)

	// 13:30 leaves the full 150 minutes.
	ok := bookedAt(f, tripoli, ten.Add(210*time.Minute), map[uuid.UUID]int{f.StoreID: 150})
	assert.NoError(t, repo.CreateGuestHold(ctx, ok, "", 2), "positive control: enough road is fine")
}

func TestTravel_TheSecondWriterWaitsForTheFirst_ThenSeesIt(t *testing.T) {
	// Deterministic interleaving rather than hoping a race happens: hold the
	// first booking's transaction open, start the second, prove it is
	// waiting, then commit the first and see the second refused.
	pool := testdb.New(t)
	repo := NewRepository(pool).(*pgRepo)
	ctx := context.Background()
	f := newFixture(t, pool)
	tripoli := secondStore(t, pool, f)
	ten := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)

	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck
	require.NoError(t, writeBookingTx(ctx, tx, bookedAt(f, f.StoreID, ten, map[uuid.UUID]int{tripoli: 150}), nil))

	done := make(chan error, 1)
	go func() {
		done <- repo.CreateGuestHold(ctx, bookedAt(f, tripoli, ten.Add(90*time.Minute), map[uuid.UUID]int{f.StoreID: 150}), "", 2)
	}()

	select {
	case err := <-done:
		t.Fatalf("the second hold finished (%v) while the first was still open - nothing serialised them", err)
	case <-time.After(400 * time.Millisecond):
	}

	require.NoError(t, tx.Commit(ctx))
	select {
	case err := <-done:
		assert.ErrorIs(t, err, ErrSlotUnavailable, "after waiting, the second sees the first and is refused")
	case <-time.After(5 * time.Second):
		t.Fatal("the second hold never finished")
	}
}

func TestTravel_CleanupCountsOnBothSides(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)
	tripoli := secondStore(t, pool, f)
	ten := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)

	first := bookedAt(f, f.StoreID, ten, map[uuid.UUID]int{tripoli: 150})
	first.BlockedUntil = first.EndTime.Add(30 * time.Minute) // cleaning until 11:30
	require.NoError(t, repo.CreateBooking(ctx, first, nil))

	// 13:30 is 150 minutes after the client leaves, 120 after she does.
	err := repo.CreateGuestHold(ctx, bookedAt(f, tripoli, ten.Add(210*time.Minute), map[uuid.UUID]int{f.StoreID: 150}), "", 2)
	assert.ErrorIs(t, err, ErrSlotUnavailable)
}

func TestTravel_Reschedule_IntoTheRoad_IsRefused(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)
	tripoli := secondStore(t, pool, f)
	ten := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)

	require.NoError(t, repo.CreateBooking(ctx, bookedAt(f, f.StoreID, ten, nil), nil))
	moving := bookedAt(f, tripoli, ten.Add(6*time.Hour), nil)
	moving.Status = StatusConfirmed
	require.NoError(t, repo.CreateBooking(ctx, moving, nil))

	newStart := ten.Add(90 * time.Minute)
	rows, err := repo.RescheduleBooking(ctx, moving.ID, newStart, newStart.Add(time.Hour), newStart.Add(time.Hour), maxReschedules,
		map[uuid.UUID]int{f.StoreID: 150})

	assert.ErrorIs(t, err, ErrSlotUnavailable)
	assert.Zero(t, rows)
	var start time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT start_time FROM bookings WHERE id = $1`, moving.ID).Scan(&start))
	assert.True(t, start.Equal(moving.StartTime), "the move was rolled back, not half-applied")
}

func TestTravel_ShiftingADay_IntoTheRoad_IsRefused(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)
	tripoli := secondStore(t, pool, f)
	ten := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)

	require.NoError(t, repo.CreateBooking(ctx, bookedAt(f, f.StoreID, ten, nil), nil))
	later := bookedAt(f, tripoli, ten.Add(4*time.Hour), nil) // 14:00, 3 hours of road
	later.Status = StatusConfirmed
	require.NoError(t, repo.CreateBooking(ctx, later, nil))

	err := repo.ShiftBookings(ctx, []uuid.UUID{later.ID}, -60, map[uuid.UUID]int{f.StoreID: 150}) // to 13:00: 2 hours

	assert.ErrorIs(t, err, ErrSlotUnavailable)
	var start time.Time
	require.NoError(t, pool.QueryRow(ctx, `SELECT start_time FROM bookings WHERE id = $1`, later.ID).Scan(&start))
	assert.True(t, start.Equal(later.StartTime), "all or nothing")
}

func TestGetStoreTravel_ReadsABranchTheSalonSwitchedOff(t *testing.T) {
	// GetStore refuses an inactive store, rightly - it cannot be booked. Its
	// travel figures must still be readable: bookings can remain on it, and
	// the artist can still be placed there (verify-uc1 B1, 2026-10-10).
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)
	closed := secondStore(t, pool, f)
	_, err := pool.Exec(ctx, `UPDATE stores SET is_active = false, weekday_buffer_min = 75 WHERE id = $1`, closed)
	require.NoError(t, err)

	_, err = repo.GetStore(ctx, closed)
	require.ErrorIs(t, err, ErrStoreNotFound, "precondition: the bookable read refuses it")

	st, err := repo.GetStoreTravel(ctx, closed)
	require.NoError(t, err)
	assert.Equal(t, 75, st.WeekdayBufferMin)
	assert.False(t, st.IsActive)
}
