package booking

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Decision D29 (2026-10-09): a customer with repeat no-shows at a salon pays
// a deposit - half the price - to book a service that otherwise takes none.
// See migration 055 for why each part of the rule is what it is.

var d = decimal.RequireFromString

func TestNoShowDeposit_BelowTheThreshold_NoDeposit(t *testing.T) {
	amount, applies := noShowDeposit(d("150"), decimal.Zero, 1, 2)
	assert.False(t, applies, "one miss is an accident")
	assert.True(t, amount.IsZero())
}

func TestNoShowDeposit_AtTheThreshold_HalfThePrice(t *testing.T) {
	amount, applies := noShowDeposit(d("150"), decimal.Zero, 2, 2)
	assert.True(t, applies)
	assert.True(t, amount.Equal(d("75")), "got %s", amount)
}

func TestNoShowDeposit_RoundsToCents(t *testing.T) {
	amount, _ := noShowDeposit(d("45.55"), decimal.Zero, 3, 2)
	assert.True(t, amount.Equal(d("22.78")), "got %s", amount)
}

func TestNoShowDeposit_AServiceThatAlreadyTakesADeposit_IsLeftAlone(t *testing.T) {
	amount, applies := noShowDeposit(d("150"), d("30"), 5, 2)
	assert.False(t, applies)
	assert.True(t, amount.Equal(d("30")))
}

func TestNoShowDeposit_Off_NeverApplies(t *testing.T) {
	_, applies := noShowDeposit(d("150"), decimal.Zero, 9, 0)
	assert.False(t, applies)
}

func TestNoShowDeposit_AFreeService_AsksForNothing(t *testing.T) {
	_, applies := noShowDeposit(decimal.Zero, decimal.Zero, 9, 2)
	assert.False(t, applies, "half of nothing is nothing - no zero-dollar deposit")
}

func heldForSubmit(price string) *Booking {
	heldUntil := time.Now().UTC().Add(5 * time.Minute)
	return &Booking{ID: uuid.New(), SalonID: uuid.New(), CustomerID: SystemGuestPlaceholderID, Status: StatusHeld,
		HeldUntil: &heldUntil, FinalPrice: d(price), OriginalPrice: d(price), DepositAmount: decimal.Zero}
}

func TestSubmitGuestBooking_RepeatNoShows_AsksForADeposit(t *testing.T) {
	b := heldForSubmit("150")
	repo := &mockRepo{getBookingByIDBooking: b, noShowAfter: 2, noShowCount: 2, requireNoShowRows: 1}
	svc := newTestService(repo)

	res, err := svc.SubmitGuestBooking(context.Background(), b.ID,
		SubmitGuestBookingRequest{Name: "Maya Test", Phone: "+96170123456"})

	require.NoError(t, err)
	assert.True(t, res.DepositAmount.Equal(d("75")), "got %s", res.DepositAmount)
	assert.True(t, res.NoShowDeposit)
	assert.True(t, repo.requiredNoShowAmount.Equal(d("75")))
	assert.Equal(t, b.SalonID, repo.noShowSalon, "her history at THIS salon")
	assert.WithinDuration(t, time.Now().AddDate(-1, 0, 0), repo.noShowSince, time.Minute, "the last 12 months")
}

func TestSubmitGuestBooking_FewerNoShows_NoDeposit(t *testing.T) {
	b := heldForSubmit("150")
	repo := &mockRepo{getBookingByIDBooking: b, noShowAfter: 2, noShowCount: 1}
	svc := newTestService(repo)

	res, err := svc.SubmitGuestBooking(context.Background(), b.ID,
		SubmitGuestBookingRequest{Name: "Maya Test", Phone: "+96170123456"})

	require.NoError(t, err)
	assert.True(t, res.DepositAmount.IsZero())
	assert.False(t, res.NoShowDeposit)
	assert.Zero(t, repo.requireNoShowCalls)
}

func TestSubmitBooking_LoggedIn_RepeatNoShows_AsksForADeposit(t *testing.T) {
	b := heldForSubmit("100")
	b.CustomerID = uuid.New()
	repo := &mockRepo{getBookingByIDBooking: b, noShowAfter: 2, noShowCount: 3, requireNoShowRows: 1}
	svc := newTestService(repo)

	res, err := svc.SubmitBooking(context.Background(), b.ID, b.CustomerID)

	require.NoError(t, err)
	assert.True(t, res.DepositAmount.Equal(d("50")), "got %s", res.DepositAmount)
	assert.True(t, res.NoShowDeposit)
	assert.Equal(t, b.CustomerID, repo.noShowCustomer)
}
