//go:build dbtest

package booking

// Decision D29's two queries on a real clone: whose no-shows count (this
// customer, this salon, this window, status no_show only) and the guarded
// update that raises a deposit only where there was none.
//
// Run with: make test-db

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func TestNoShowHistory_CountsHerMissesAtThisSalonInTheWindow(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)

	base := time.Now().UTC().Add(-30 * 24 * time.Hour).Truncate(time.Hour)
	for i, st := range []string{StatusNoShow, StatusNoShow, StatusCompleted, StatusCancelled} {
		require.NoError(t, repo.CreateBooking(ctx, f.booking(f.ArtistID, base.Add(time.Duration(i)*3*time.Hour), st), nil))
	}
	old := f.booking(f.ArtistID, time.Now().UTC().AddDate(-2, 0, 0).Truncate(time.Hour), StatusNoShow)
	require.NoError(t, repo.CreateBooking(ctx, old, nil))
	other := insertUser(t, pool, "someone.else@noshow.local", "customer")
	theirs := f.booking(f.ArtistID, base.Add(20*time.Hour), StatusNoShow)
	theirs.CustomerID = other
	require.NoError(t, repo.CreateBooking(ctx, theirs, nil))

	after, count, err := repo.NoShowHistory(ctx, f.SalonID, f.Customer, time.Now().AddDate(-1, 0, 0))
	require.NoError(t, err)
	assert.Equal(t, 2, after, "the salon's setting, default 2")
	assert.Equal(t, 2, count, "two misses this year; the completed, the cancelled, the one from two years ago and another customer's do not count")

	_, err = pool.Exec(ctx, `UPDATE salons SET no_show_deposit_after = 0 WHERE id = $1`, f.SalonID)
	require.NoError(t, err)
	after, _, err = repo.NoShowHistory(ctx, f.SalonID, f.Customer, time.Now().AddDate(-1, 0, 0))
	require.NoError(t, err)
	assert.Equal(t, 0, after, "the owner turned it off")
}

func TestRequireNoShowDeposit_OnlyWhereThereWasNone(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)
	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)

	free := f.booking(f.ArtistID, start, StatusPending)
	require.NoError(t, repo.CreateBooking(ctx, free, nil))
	paid := f.booking(f.ArtistID, start.Add(2*time.Hour), StatusPending)
	paid.DepositAmount = decimal.NewFromInt(30)
	require.NoError(t, repo.CreateBooking(ctx, paid, nil))

	n, err := repo.RequireNoShowDeposit(ctx, free.ID, decimal.NewFromInt(50))
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	n, err = repo.RequireNoShowDeposit(ctx, paid.ID, decimal.NewFromInt(50))
	require.NoError(t, err)
	assert.EqualValues(t, 0, n, "a deposit already asked for is never replaced")

	got, err := repo.GetBookingByID(ctx, free.ID)
	require.NoError(t, err)
	assert.True(t, got.DepositAmount.Equal(decimal.NewFromInt(50)))
	assert.True(t, got.NoShowDeposit, "the booking records why it asks for a deposit")
	kept, err := repo.GetBookingByID(ctx, paid.ID)
	require.NoError(t, err)
	assert.True(t, kept.DepositAmount.Equal(decimal.NewFromInt(30)))
	assert.False(t, kept.NoShowDeposit)
}
