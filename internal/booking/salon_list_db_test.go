//go:build dbtest

package booking

// The owner's salon-wide lists on a real clone: every artist's bookings at
// this salon, one artist's when asked, never another salon's.
//
// Run with: make test-db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func TestListEnrichedBookingsBySalon_EveryArtistHereAndNoOtherSalon(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)

	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)
	mine := f.booking(f.ArtistID, start, StatusPending)
	theirs := f.booking(f.Artist2ID, start.Add(2*time.Hour), StatusConfirmed)
	require.NoError(t, repo.CreateBooking(ctx, mine, nil))
	require.NoError(t, repo.CreateBooking(ctx, theirs, nil))

	// Another salon, with its own artist, store and service.
	otherOwner := insertUser(t, pool, "other-owner@dbtest.local", "artist")
	var otherSalon, otherStore, otherArtist, otherService uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id, name) VALUES ($1,'Other') RETURNING id`, otherOwner).Scan(&otherSalon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO stores (salon_id, name, city) VALUES ($1,'X','Beirut') RETURNING id`, otherSalon).Scan(&otherStore))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO artists (user_id, salon_id) VALUES ($1,$2) RETURNING id`, otherOwner, otherSalon).Scan(&otherArtist))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id, name, duration_min, price) VALUES ($1,'Y',60,100) RETURNING id`, otherSalon).Scan(&otherService))
	foreign := f.booking(otherArtist, start, StatusConfirmed)
	foreign.SalonID, foreign.StoreID, foreign.ServiceID = otherSalon, otherStore, otherService
	require.NoError(t, repo.CreateBooking(ctx, foreign, nil))

	all, err := repo.ListEnrichedBookingsBySalon(ctx, f.SalonID, nil, "", time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	ids := []uuid.UUID{}
	for _, b := range all {
		ids = append(ids, b.ID)
	}
	assert.ElementsMatch(t, []uuid.UUID{mine.ID, theirs.ID}, ids, "both artists' bookings here; never the other salon's")

	one, err := repo.ListEnrichedBookingsBySalon(ctx, f.SalonID, &f.Artist2ID, "", time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, one, 1)
	assert.Equal(t, theirs.ID, one[0].ID)

	pending, err := repo.ListEnrichedBookingsBySalon(ctx, f.SalonID, nil, StatusPending, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, mine.ID, pending[0].ID)

	smuggled, err := repo.ListEnrichedBookingsBySalon(ctx, f.SalonID, &otherArtist, "", time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	assert.Empty(t, smuggled, "another salon's artist id finds nothing here")
}

func TestListEnrichedBookingsForSalonWeek_CommittedBookingsOfEveryArtist(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)

	week := time.Now().UTC().AddDate(0, 0, 7).Truncate(24 * time.Hour)
	a := f.booking(f.ArtistID, week.Add(10*time.Hour), StatusConfirmed)
	b := f.booking(f.Artist2ID, week.Add(34*time.Hour), StatusApproved)
	req := f.booking(f.Artist2ID, week.Add(50*time.Hour), StatusPending) // a request, not on the grid
	late := f.booking(f.ArtistID, week.AddDate(0, 0, 8), StatusConfirmed)
	for _, x := range []*Booking{a, b, req, late} {
		require.NoError(t, repo.CreateBooking(ctx, x, nil))
	}

	got, err := repo.ListEnrichedBookingsForSalonWeek(ctx, f.SalonID, nil, week)
	require.NoError(t, err)

	ids := []uuid.UUID{}
	for _, x := range got {
		ids = append(ids, x.ID)
	}
	assert.Equal(t, []uuid.UUID{a.ID, b.ID}, ids, "in time order, both artists, committed only, this week only")
}
