package product

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/promo"
)

// The cart shows what a code does BEFORE she places the order, like the
// booking funnel. Placing an order never fails over a refused code - it goes
// through at full price - so without this preview a code that did nothing
// would pass silently.

func previewReq(salonID uuid.UUID, items ...OrderItemRequest) OrderDiscountPreviewRequest {
	return OrderDiscountPreviewRequest{SalonID: salonID.String(), Code: "SAVE10", Items: items}
}

func TestPreviewOrderDiscount_PricesTheCartFromServerSidePrices(t *testing.T) {
	// Same rule as placing: the amount discounted is computed from the
	// current product rows, never sent by the customer.
	salonID := uuid.New()
	p1, p2 := activeProduct(salonID, "12.50"), activeProduct(salonID, "4.00")
	want := &promo.PreviewResponse{Code: "SAVE10", Valid: true, Final: "27.00"}
	stub := &stubResolver{t: t, preview: want}
	repo := &mockRepo{products: map[uuid.UUID]*Product{p1.ID: p1, p2.ID: p2}}

	res, err := newTestService(repo).WithDiscounts(stub).PreviewOrderDiscount(context.Background(),
		previewReq(salonID, OrderItemRequest{ProductID: p1.ID.String(), Quantity: 2},
			OrderItemRequest{ProductID: p2.ID.String(), Quantity: 3}))

	require.NoError(t, err)
	assert.Same(t, want, res)
	assert.True(t, dec("37.00").Equal(stub.previewBase), "12.50×2 + 4.00×3, got %s", stub.previewBase)
	assert.Equal(t, "SAVE10", stub.previewCode)
}

func TestPreviewOrderDiscount_NoCustomerYet_FaceValue(t *testing.T) {
	// The customer is only known once she places the order. Keying the
	// preview on a phone number would tell anyone whether that number has
	// used the code or booked here before, so the per-customer checks run
	// for real at placing - as they do for a guest booking.
	salonID := uuid.New()
	p := activeProduct(salonID, "20.00")
	stub := &stubResolver{t: t, preview: &promo.PreviewResponse{Code: "SAVE10", Valid: true}}
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}

	_, err := newTestService(repo).WithDiscounts(stub).PreviewOrderDiscount(context.Background(),
		previewReq(salonID, OrderItemRequest{ProductID: p.ID.String(), Quantity: 1}))

	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, stub.previewCustomer)
	assert.Zero(t, repo.customerCalls, "a preview creates no customer")
	assert.Nil(t, repo.createdOrder, "a preview places nothing")
}

func TestPreviewOrderDiscount_ProductFromAnotherSalon_NotFound(t *testing.T) {
	salonID := uuid.New()
	stranger := activeProduct(uuid.New(), "20.00")
	stub := &stubResolver{t: t, mustNotBeCalled: true}
	repo := &mockRepo{products: map[uuid.UUID]*Product{stranger.ID: stranger}}

	_, err := newTestService(repo).WithDiscounts(stub).PreviewOrderDiscount(context.Background(),
		previewReq(salonID, OrderItemRequest{ProductID: stranger.ID.String(), Quantity: 1}))

	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "PRODUCT_NOT_FOUND", appErr.Code)
}

func TestPreviewOrderDiscount_MissingCode_Rejected(t *testing.T) {
	salonID := uuid.New()
	p := activeProduct(salonID, "20.00")
	stub := &stubResolver{t: t, mustNotBeCalled: true}
	req := previewReq(salonID, OrderItemRequest{ProductID: p.ID.String(), Quantity: 1})
	req.Code = ""

	_, err := newTestService(&mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}).
		WithDiscounts(stub).PreviewOrderDiscount(context.Background(), req)

	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 422, appErr.HTTPStatus)
}

func TestPreviewOrderDiscount_NoResolver_NotValid(t *testing.T) {
	// Codes switched off: say so rather than pretend.
	salonID := uuid.New()
	p := activeProduct(salonID, "20.00")

	res, err := newTestService(&mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}).
		PreviewOrderDiscount(context.Background(),
			previewReq(salonID, OrderItemRequest{ProductID: p.ID.String(), Quantity: 1}))

	require.NoError(t, err)
	assert.False(t, res.Valid)
	assert.NotEmpty(t, res.Reason)
}
