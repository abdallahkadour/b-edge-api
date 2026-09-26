package product

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// A checkout whose reply is lost on a bad connection is retried by the cart
// with the same request_id (migration 054). The retry must answer with the
// order already placed, and must not create a customer, take stock, redeem
// a code or place anything a second time.

func retryReq(salonID uuid.UUID, p *Product, requestID string) CreateOrderRequest {
	return CreateOrderRequest{SalonID: salonID.String(), Name: "Maya", Phone: "70123456",
		DeliveryLat: validLat, DeliveryLng: validLng, RequestID: &requestID,
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 1}}}
}

func placedOrder(salonID uuid.UUID, requestID uuid.UUID) (*Order, []*OrderItem) {
	o := &Order{ID: uuid.New(), SalonID: salonID, CustomerID: uuid.New(), Status: OrderStatusPlaced,
		TotalAmount: decimal.RequireFromString("20"), RequestID: &requestID}
	return o, []*OrderItem{{ID: uuid.New(), OrderID: o.ID, ProductName: "Serum",
		UnitPrice: decimal.RequireFromString("20"), Quantity: 1, Subtotal: decimal.RequireFromString("20")}}
}

func TestPlaceOrder_RetryOfAPlacedOrder_ReturnsItWithoutPlacingAgain(t *testing.T) {
	salonID, rid := uuid.New(), uuid.New()
	p := activeProduct(salonID, "20")
	existing, items := placedOrder(salonID, rid)
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}, requestOrder: existing, requestItems: items}

	res, err := newTestService(repo).PlaceOrder(context.Background(), retryReq(salonID, p, rid.String()))

	require.NoError(t, err)
	assert.Equal(t, existing.ID, res.ID, "the retry answers with the order she already placed")
	assert.Len(t, res.Items, 1)
	assert.Equal(t, []uuid.UUID{rid}, repo.requestLookups)
	assert.Nil(t, repo.createdOrder, "nothing is placed a second time")
	assert.Zero(t, repo.customerCalls, "no customer is created or looked up")
	assert.Empty(t, repo.getProductCallIDs,
		"answered before the stock check - the first order may have taken the last unit")
}

func TestPlaceOrder_RetryRacedTheOriginal_ReturnsTheWinner(t *testing.T) {
	// Both requests passed the lookup before either committed; the unique
	// key refused the second. It answers with the first, not an error.
	salonID, rid := uuid.New(), uuid.New()
	p := activeProduct(salonID, "20")
	existing, items := placedOrder(salonID, rid)
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}, createOrderErr: ErrDuplicateOrderRequest,
		requestOrder: existing, requestItems: items, requestMisses: 1}

	res, err := newTestService(repo).PlaceOrder(context.Background(), retryReq(salonID, p, rid.String()))

	require.NoError(t, err)
	assert.Equal(t, existing.ID, res.ID)
	assert.Len(t, repo.requestLookups, 2, "looked up before placing, and again after losing the race")
}

func TestPlaceOrder_FirstAttempt_StoresTheRequestID(t *testing.T) {
	salonID, rid := uuid.New(), uuid.New()
	p := activeProduct(salonID, "20")
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}

	_, err := newTestService(repo).PlaceOrder(context.Background(), retryReq(salonID, p, rid.String()))

	require.NoError(t, err)
	require.NotNil(t, repo.createdOrder)
	require.NotNil(t, repo.createdOrder.RequestID, "the id is written down so a retry can find it")
	assert.Equal(t, rid, *repo.createdOrder.RequestID)
}

func TestPlaceOrder_NoRequestID_NeverLooksOneUp(t *testing.T) {
	// Older carts send none: an order behaves exactly as before.
	salonID := uuid.New()
	p := activeProduct(salonID, "20")
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	req := retryReq(salonID, p, "")
	req.RequestID = nil

	_, err := newTestService(repo).PlaceOrder(context.Background(), req)

	require.NoError(t, err)
	assert.Empty(t, repo.requestLookups)
	require.NotNil(t, repo.createdOrder)
	assert.Nil(t, repo.createdOrder.RequestID)
}

func TestPlaceOrder_RequestIDOfAnotherSalonsOrder_Conflict(t *testing.T) {
	// The same id with a different salon is not a retry - it is a client
	// bug. Placing a second order would break "one id, one order";
	// answering with the other salon's order would be wrong outright.
	salonID, rid := uuid.New(), uuid.New()
	p := activeProduct(salonID, "20")
	existing, items := placedOrder(uuid.New(), rid)
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}, requestOrder: existing, requestItems: items}

	_, err := newTestService(repo).PlaceOrder(context.Background(), retryReq(salonID, p, rid.String()))

	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "REQUEST_ID_REUSED", appErr.Code)
	assert.Equal(t, 409, appErr.HTTPStatus)
	assert.Nil(t, repo.createdOrder)
}

func TestPlaceOrder_MalformedRequestID_Rejected(t *testing.T) {
	salonID := uuid.New()
	p := activeProduct(salonID, "20")
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}

	_, err := newTestService(repo).PlaceOrder(context.Background(), retryReq(salonID, p, "not-a-uuid"))

	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 422, appErr.HTTPStatus)
	assert.Nil(t, repo.createdOrder)
}

func TestPlaceOrder_RequestLookupFails_IsNotPlacedBlind(t *testing.T) {
	// A database error on the lookup must not fall through to placing the
	// order: if the lookup cannot run, neither can the guard.
	salonID := uuid.New()
	p := activeProduct(salonID, "20")
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}, requestErr: errors.New("connection reset")}

	_, err := newTestService(repo).PlaceOrder(context.Background(), retryReq(salonID, p, uuid.NewString()))

	require.Error(t, err)
	var appErr *apperror.AppError
	assert.False(t, errors.As(err, &appErr), "a database error surfaces as an internal error, got %v", err)
	assert.Nil(t, repo.createdOrder)
}
