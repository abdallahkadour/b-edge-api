package booking

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/discount"
	"github.com/abdallahkadour/b-edge-api/internal/promo"
)

// fixedDiscount is a DiscountResolver that always applies one code: the
// final price and discount amount it was built with, whatever it is given.
type fixedDiscount struct {
	final, amount decimal.Decimal
	code          string
}

func (f *fixedDiscount) Resolve(_ context.Context, _, _ uuid.UUID, _ string,
	base, _, deposit decimal.Decimal) (*promo.Result, error) {
	id := uuid.New()
	return &promo.Result{DiscountID: &id, Breakdown: discount.Breakdown{
		Base: base, Subtotal: base, DiscountTotal: f.amount, CodeAmount: f.amount,
		Final: f.final, Deposit: deposit, AppliedCode: f.code,
	}}, nil
}
func (f *fixedDiscount) ReleaseForBooking(context.Context, uuid.UUID) error { return nil }
func (f *fixedDiscount) Preview(context.Context, uuid.UUID, uuid.UUID, string,
	decimal.Decimal, decimal.Decimal, decimal.Decimal) (*promo.PreviewResponse, error) {
	return &promo.PreviewResponse{}, nil
}

// TestSubmitGuestBooking_DiscountApplied_ResponseMatchesStoredPrice - the
// code applies at submit, AttachGuestAndSubmit stores the DISCOUNTED final
// price, and the response must say the same. It used to discard
// applyDiscount's final price and answer with the hold's pre-discount
// final_price and a zero discount_amount - the API contradicting the row it
// had just written.
func TestSubmitGuestBooking_DiscountApplied_ResponseMatchesStoredPrice(t *testing.T) {
	heldUntil := time.Now().UTC().Add(5 * time.Minute)
	booking := &Booking{
		ID: uuid.New(), SalonID: uuid.New(), CustomerID: SystemGuestPlaceholderID,
		Status: StatusHeld, HeldUntil: &heldUntil,
		OriginalPrice: dec("150.00"), FinalPrice: dec("150.00"), DepositAmount: dec("30.00"),
	}
	repo := &mockRepo{getBookingByIDBooking: booking}
	svc := newTestService(repo).WithDiscounts(&fixedDiscount{
		final: dec("135.00"), amount: dec("15.00"), code: "SAVE10"})
	code := "SAVE10"

	res, err := svc.SubmitGuestBooking(context.Background(), booking.ID,
		SubmitGuestBookingRequest{Name: "Maya Test", Phone: "+96170123456", DiscountCode: &code})

	require.NoError(t, err)
	require.NotNil(t, repo.lastApplied, "precondition: the code applied and was handed to the store")
	assert.True(t, repo.lastApplied.FinalPrice.Equal(dec("135.00")), "precondition: stored %s", repo.lastApplied.FinalPrice)
	assert.True(t, res.FinalPrice.Equal(repo.lastApplied.FinalPrice),
		"response final_price %s must equal the stored %s", res.FinalPrice, repo.lastApplied.FinalPrice)
	assert.True(t, res.DiscountAmount.Equal(repo.lastApplied.Amount),
		"response discount_amount %s must equal the stored %s", res.DiscountAmount, repo.lastApplied.Amount)
	assert.True(t, res.OriginalPrice.Equal(dec("150.00")), "the original price is untouched")
}

// TestSubmitGuestBooking_NoCode_ResponseKeepsHoldPrice - the counterweight:
// without a code nothing is re-priced, and the hold's price is the answer.
func TestSubmitGuestBooking_NoCode_ResponseKeepsHoldPrice(t *testing.T) {
	heldUntil := time.Now().UTC().Add(5 * time.Minute)
	booking := &Booking{
		ID: uuid.New(), SalonID: uuid.New(), CustomerID: SystemGuestPlaceholderID,
		Status: StatusHeld, HeldUntil: &heldUntil,
		OriginalPrice: dec("150.00"), FinalPrice: dec("165.00"), DepositAmount: dec("30.00"),
	}
	repo := &mockRepo{getBookingByIDBooking: booking}
	svc := newTestService(repo).WithDiscounts(&fixedDiscount{
		final: dec("1.00"), amount: dec("164.00"), code: "NEVER"})

	res, err := svc.SubmitGuestBooking(context.Background(), booking.ID,
		SubmitGuestBookingRequest{Name: "Maya Test", Phone: "+96170123456"})

	require.NoError(t, err)
	assert.Nil(t, repo.lastApplied)
	assert.True(t, res.FinalPrice.Equal(dec("165.00")), "got %s", res.FinalPrice)
	assert.True(t, res.DiscountAmount.IsZero(), "got %s", res.DiscountAmount)
}
