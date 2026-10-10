//go:build dbtest

package booking

// The admin booking queries run on a real clone: the cross-salon list (with
// its salon name, status and salon filters, newest first) and the summary,
// whose sum of deposits owed back is SQL a mock would simply agree with.
//
// Run with: make test-db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func TestAdminBookings_ListsAcrossSalons_WithFilters(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)

	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)
	owed := f.booking(f.ArtistID, start, StatusRefundDue)
	owed.DepositAmount = decimal.NewFromInt(30)
	require.NoError(t, repo.CreateBooking(ctx, owed, nil))
	live := f.booking(f.ArtistID, start.Add(2*time.Hour), StatusConfirmed)
	require.NoError(t, repo.CreateBooking(ctx, live, nil))

	all, err := repo.ListBookingsForAdmin(ctx, "", nil, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "DB Test Salon", all[0].SalonName)
	assert.Equal(t, live.ID, all[0].ID, "newest first")

	onlyOwed, err := repo.ListBookingsForAdmin(ctx, StatusRefundDue, nil, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	require.Len(t, onlyOwed, 1)
	assert.Equal(t, owed.ID, onlyOwed[0].ID)

	other := uuid.New()
	none, err := repo.ListBookingsForAdmin(ctx, "", &other, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	assert.Empty(t, none, "a salon filter that matches nothing returns nothing, not everything")

	mine, err := repo.ListBookingsForAdmin(ctx, "", &f.SalonID, time.Now().Add(time.Hour), 10)
	require.NoError(t, err)
	assert.Len(t, mine, 2)
}

func TestAdminBookingSummary_CountsAndTheMoneyOwedBack(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	f := newFixture(t, pool)

	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)
	for i, st := range []string{StatusRefundDue, StatusRefundDue, StatusPending, StatusApproved, StatusDepositPaid, StatusConfirmed} {
		b := f.booking(f.ArtistID, start.Add(time.Duration(i)*2*time.Hour), st)
		if st == StatusRefundDue {
			b.DepositAmount = decimal.RequireFromString("25.50")
		}
		require.NoError(t, repo.CreateBooking(ctx, b, nil))
	}

	s, err := repo.AdminBookingSummary(ctx)
	require.NoError(t, err)

	assert.Equal(t, 2, s.RefundsOwed)
	assert.True(t, s.RefundsOwedAmount.Equal(decimal.RequireFromString("51.00")), "got %s", s.RefundsOwedAmount)
	assert.Equal(t, 1, s.AwaitingApproval)
	assert.Equal(t, 2, s.AwaitingDeposit, "approved (deposit not yet checked) and deposit_paid (received, not confirmed)")
}
