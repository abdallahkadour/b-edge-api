//go:build dbtest

package booking

// Handing a booking to another artist, on a real clone: the statement,
// the GIST constraint seeing the new artist's diary, the travel re-check,
// and a stale reassignment losing cleanly.
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

func TestReassignBooking_MovesItAndTheCalendarEvent(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)
	b := f.booking(f.ArtistID, time.Now().UTC().Add(72*time.Hour).Truncate(time.Hour), StatusConfirmed)
	require.NoError(t, repo.CreateBooking(ctx, b, nil))

	rows, err := repo.ReassignBooking(ctx, b.ID, f.ArtistID, f.Artist2ID, nil)

	require.NoError(t, err)
	assert.EqualValues(t, 1, rows)
	var artist uuid.UUID
	var seq int
	require.NoError(t, pool.QueryRow(ctx, `SELECT artist_id, calendar_sequence FROM bookings WHERE id = $1`, b.ID).Scan(&artist, &seq))
	assert.Equal(t, f.Artist2ID, artist)
	assert.Equal(t, 1, seq, "the client's calendar event changes artist, so it must update in place, not duplicate")
}

func TestReassignBooking_IntoTheNewArtistsOwnAppointment_IsRefused(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)
	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)
	b := f.booking(f.ArtistID, start, StatusConfirmed)
	require.NoError(t, repo.CreateBooking(ctx, b, nil))
	require.NoError(t, repo.CreateBooking(ctx, f.booking(f.Artist2ID, start.Add(30*time.Minute), StatusConfirmed), nil))

	rows, err := repo.ReassignBooking(ctx, b.ID, f.ArtistID, f.Artist2ID, nil)

	assert.ErrorIs(t, err, ErrSlotUnavailable, "the GIST constraint checks the new artist's diary in the same statement")
	assert.Zero(t, rows)
}

func TestReassignBooking_IntoTheNewArtistsTravel_IsRefused(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)
	tripoli := secondStore(t, pool, f)
	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)
	b := f.booking(f.ArtistID, start, StatusConfirmed)
	require.NoError(t, repo.CreateBooking(ctx, b, nil))
	away := f.booking(f.Artist2ID, start.Add(-150*time.Minute), StatusConfirmed) // Tripoli, ending 90 min before
	away.StoreID = tripoli
	require.NoError(t, repo.CreateBooking(ctx, away, nil))

	_, err := repo.ReassignBooking(ctx, b.ID, f.ArtistID, f.Artist2ID, map[uuid.UUID]int{tripoli: 150})

	assert.ErrorIs(t, err, ErrSlotUnavailable)
	var artist uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT artist_id FROM bookings WHERE id = $1`, b.ID).Scan(&artist))
	assert.Equal(t, f.ArtistID, artist, "rolled back, not half-moved")
}

func TestReassignBooking_FromSomeoneWhoNoLongerHasIt_ChangesNothing(t *testing.T) {
	// Two owners' tabs moving the same booking: the second one's idea of who
	// has it is stale, and the guard on the old artist makes it lose.
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)
	b := f.booking(f.ArtistID, time.Now().UTC().Add(72*time.Hour).Truncate(time.Hour), StatusConfirmed)
	require.NoError(t, repo.CreateBooking(ctx, b, nil))
	_, err := repo.ReassignBooking(ctx, b.ID, f.ArtistID, f.Artist2ID, nil)
	require.NoError(t, err)

	rows, err := repo.ReassignBooking(ctx, b.ID, f.ArtistID, f.Artist2ID, nil)

	require.NoError(t, err)
	assert.Zero(t, rows)
}

func TestReassignBooking_AFinishedBooking_StaysWhereItIs(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)
	b := f.booking(f.ArtistID, time.Now().UTC().Add(72*time.Hour).Truncate(time.Hour), StatusCompleted)
	require.NoError(t, repo.CreateBooking(ctx, b, nil))

	rows, err := repo.ReassignBooking(ctx, b.ID, f.ArtistID, f.Artist2ID, nil)

	require.NoError(t, err)
	assert.Zero(t, rows)
}

func TestSalonArtists_TheSalonsArtistsWithTheirNames(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)

	got, err := repo.SalonArtists(ctx, f.SalonID)

	require.NoError(t, err)
	ids := map[uuid.UUID]string{}
	for _, a := range got {
		ids[a.ArtistID] = a.Name
		assert.NotEqual(t, uuid.Nil, a.UserID)
	}
	assert.Len(t, ids, 2)
	assert.Contains(t, ids, f.ArtistID)
	assert.Contains(t, ids, f.Artist2ID)
	none, err := repo.SalonArtists(ctx, uuid.New())
	require.NoError(t, err)
	assert.Empty(t, none)
}
