// Package product tests. Uses an in-memory mockRepo - no database needed,
// matching every other domain in this codebase.
//
// This domain had ZERO test coverage until the August 2026 security audit
// flagged it: the newest domain, handling money and a six-state machine,
// entirely unverified. These tests prioritise, in order: the money path
// (can a customer control what they're charged?), the state machine (can a
// transition be skipped?), and cross-tenant authorization.
package product

import (
	"context"
	"errors"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// ── Mock repository ──────────────────────────────────────────────────────────

type mockRepo struct {
	// products, keyed by ID so PlaceOrder's per-item lookups can return
	// genuinely different products (and differing salons/prices) in one test.
	products      map[uuid.UUID]*Product
	productErr    error
	productsList  []*Product
	listErr       error
	createProdErr error
	updateProdErr error

	// orders
	order          *Order
	orderItems     []*OrderItem
	orderErr       error
	createOrderErr error
	updateStatusErr error
	ordersByCust   []*Order
	ordersByCustErr error
	enrichedOrders []*EnrichedOrderResponse
	enrichedErr    error

	customerID  uuid.UUID
	customerErr error

	// captured for assertions
	createdOrder      *Order
	createdItems      []*OrderItem
	lastFromStatus    string
	lastToStatus      string
	lastPaymentRef    *string
	lastCancelReason  *string
	getProductCallIDs []uuid.UUID
}

func (m *mockRepo) CreateProduct(_ context.Context, _ *Product) error { return m.createProdErr }

func (m *mockRepo) GetProductByID(_ context.Context, id uuid.UUID) (*Product, error) {
	m.getProductCallIDs = append(m.getProductCallIDs, id)
	if m.productErr != nil {
		return nil, m.productErr
	}
	p, ok := m.products[id]
	if !ok {
		return nil, ErrProductNotFound
	}
	return p, nil
}

func (m *mockRepo) GetProductsBySalon(_ context.Context, _ uuid.UUID, _ bool) ([]*Product, error) {
	return m.productsList, m.listErr
}

func (m *mockRepo) UpdateProduct(_ context.Context, _ uuid.UUID, _ UpdateProductRequest) error {
	return m.updateProdErr
}

func (m *mockRepo) CreateOrder(_ context.Context, o *Order, items []*OrderItem) error {
	if m.createOrderErr != nil {
		return m.createOrderErr
	}
	// Mirrors the real repository, which stamps the status after its INSERT
	// (repository.go: `o.Status = OrderStatusPlaced`) rather than the
	// service setting it beforehand. A mock that skipped this would make
	// TestPlaceOrder_StartsInPlacedStatus fail against correct code - which
	// is exactly what happened on the first run of this suite.
	o.Status = OrderStatusPlaced
	m.createdOrder = o
	m.createdItems = items
	return nil
}

func (m *mockRepo) GetOrderByID(_ context.Context, _ uuid.UUID) (*Order, []*OrderItem, error) {
	if m.orderErr != nil {
		return nil, nil, m.orderErr
	}
	return m.order, m.orderItems, nil
}

func (m *mockRepo) GetOrdersBySalon(_ context.Context, _ uuid.UUID, _ string) ([]*Order, error) {
	return nil, nil
}

func (m *mockRepo) GetEnrichedOrdersBySalon(_ context.Context, _ uuid.UUID, _ string) ([]*EnrichedOrderResponse, error) {
	return m.enrichedOrders, m.enrichedErr
}

func (m *mockRepo) GetOrdersByCustomer(_ context.Context, _ uuid.UUID) ([]*Order, error) {
	return m.ordersByCust, m.ordersByCustErr
}

func (m *mockRepo) UpdateOrderStatus(_ context.Context, _ uuid.UUID, fromStatus, toStatus string, paymentRef, cancelReason *string) error {
	m.lastFromStatus = fromStatus
	m.lastToStatus = toStatus
	m.lastPaymentRef = paymentRef
	m.lastCancelReason = cancelReason
	return m.updateStatusErr
}

func (m *mockRepo) GetOrderItems(_ context.Context, _ uuid.UUID) ([]*OrderItem, error) {
	return m.orderItems, nil
}

func (m *mockRepo) FindOrCreateCustomerByPhone(_ context.Context, _, _ string) (uuid.UUID, error) {
	if m.customerErr != nil {
		return uuid.Nil, m.customerErr
	}
	if m.customerID != uuid.Nil {
		return m.customerID, nil
	}
	return uuid.New(), nil
}

func newTestService(repo Repository) *Service { return NewService(repo) }

// Valid pin-drop coordinates (Beirut) - DeliveryLat/DeliveryLng are
// required on CreateOrderRequest, so every test placing an order needs a
// real value here regardless of what else that test is exercising.
const validLat = 33.8938
const validLng = 35.5018

// activeProduct is a convenience builder for a sellable product.
func activeProduct(salonID uuid.UUID, price string) *Product {
	return &Product{
		ID:       uuid.New(),
		SalonID:  salonID,
		Name:     "Lip Liner",
		Price:    decimal.RequireFromString(price),
		IsActive: true,
	}
}

// ── Money path ───────────────────────────────────────────────────────────────
//
// The highest-value tests in this file. An order's total must be derived
// server-side from the CURRENT product rows - never from anything the
// customer sends. A client-controllable price is the classic e-commerce
// vulnerability and the one worth guarding hardest.

func TestPlaceOrder_TotalComputedFromServerSidePrices(t *testing.T) {
	salonID := uuid.New()
	p1 := activeProduct(salonID, "12.50")
	p2 := activeProduct(salonID, "4.00")

	repo := &mockRepo{products: map[uuid.UUID]*Product{p1.ID: p1, p2.ID: p2}}
	svc := newTestService(repo)

	res, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID:     salonID.String(),
		Name:        "Sarah",
		Phone:       "70123456",
		DeliveryLat: validLat,
		DeliveryLng: validLng,
		Items: []OrderItemRequest{
			{ProductID: p1.ID.String(), Quantity: 2}, // 25.00
			{ProductID: p2.ID.String(), Quantity: 3}, // 12.00
		},
	})

	require.NoError(t, err)
	assert.True(t, decimal.RequireFromString("37.00").Equal(res.TotalAmount),
		"total must be (12.50×2)+(4.00×3)=37.00, got %s", res.TotalAmount)
}

func TestPlaceOrder_SnapshotsNameAndPriceAtOrderTime(t *testing.T) {
	// order_items deliberately snapshots rather than live-joining, so a
	// later catalog edit can never rewrite what someone already paid.
	salonID := uuid.New()
	p := activeProduct(salonID, "9.99")

	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 1}},
	})

	require.NoError(t, err)
	require.Len(t, repo.createdItems, 1)
	assert.Equal(t, "Lip Liner", repo.createdItems[0].ProductName,
		"the product NAME must be snapshotted onto the order line")
	assert.True(t, decimal.RequireFromString("9.99").Equal(repo.createdItems[0].UnitPrice),
		"the unit PRICE must be snapshotted onto the order line")
}

func TestPlaceOrder_ProductFromAnotherSalon_Rejected(t *testing.T) {
	orderSalonID := uuid.New()
	otherSalonID := uuid.New()
	foreign := activeProduct(otherSalonID, "5.00") // belongs to a DIFFERENT salon

	repo := &mockRepo{products: map[uuid.UUID]*Product{foreign.ID: foreign}}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: orderSalonID.String(), Name: "Mallory", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: foreign.ID.String(), Quantity: 1}},
	})

	require.Error(t, err, "a product from another salon must not be orderable through this salon")
}

func TestPlaceOrder_InactiveProduct_Rejected(t *testing.T) {
	// Real case, not theoretical: a product can sit in someone's cart and
	// then be deactivated by the artist before checkout.
	salonID := uuid.New()
	p := activeProduct(salonID, "5.00")
	p.IsActive = false

	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 1}},
	})

	require.Error(t, err)
}

func TestPlaceOrder_ZeroQuantity_Rejected(t *testing.T) {
	salonID := uuid.New()
	p := activeProduct(salonID, "5.00")
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 0}},
	})

	require.Error(t, err)
}

func TestPlaceOrder_NegativeQuantity_Rejected(t *testing.T) {
	// A negative quantity would produce a negative subtotal - i.e. an order
	// that reduces the total, the classic "get paid to shop" bug.
	salonID := uuid.New()
	p := activeProduct(salonID, "5.00")
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: -5}},
	})

	require.Error(t, err, "a negative quantity must never reach the total calculation")
}

// ── Delivery location ────────────────────────────────────────────────────────
//
// A courier needs somewhere real to go - Lebanese addresses don't reliably
// geocode from text, so the pin-dropped coordinates are required, not
// optional, on every order.

func TestPlaceOrder_MissingDeliveryLocation_Rejected(t *testing.T) {
	salonID := uuid.New()
	p := activeProduct(salonID, "5.00")
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456",
		// DeliveryLat/DeliveryLng both left at zero - never a real customer
		// location, so `required` must reject this the same as if omitted.
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 1}},
	})

	require.Error(t, err, "an order with no delivery location must be rejected")
	assert.Nil(t, repo.createdOrder)
}

func TestPlaceOrder_OutOfRangeLatitude_Rejected(t *testing.T) {
	salonID := uuid.New()
	p := activeProduct(salonID, "5.00")
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456",
		DeliveryLat: 200, // not a real latitude
		DeliveryLng: validLng,
		Items:       []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 1}},
	})

	require.Error(t, err)
}

func TestPlaceOrder_ValidDeliveryLocation_PersistedOnOrder(t *testing.T) {
	salonID := uuid.New()
	p := activeProduct(salonID, "5.00")
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456",
		DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 1}},
	})

	require.NoError(t, err)
	require.NotNil(t, repo.createdOrder.DeliveryLat)
	require.NotNil(t, repo.createdOrder.DeliveryLng)
	assert.Equal(t, validLat, *repo.createdOrder.DeliveryLat)
	assert.Equal(t, validLng, *repo.createdOrder.DeliveryLng)
}

func TestPlaceOrder_EmptyItems_Rejected(t *testing.T) {
	svc := newTestService(&mockRepo{})

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: uuid.New().String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{},
	})

	require.Error(t, err)
}

func TestPlaceOrder_UnknownProduct_Rejected(t *testing.T) {
	svc := newTestService(&mockRepo{products: map[uuid.UUID]*Product{}})

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: uuid.New().String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: uuid.New().String(), Quantity: 1}},
	})

	require.Error(t, err)
}

func TestPlaceOrder_StartsInPlacedStatus(t *testing.T) {
	salonID := uuid.New()
	p := activeProduct(salonID, "5.00")
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	res, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 1}},
	})

	require.NoError(t, err)
	assert.Equal(t, OrderStatusPlaced, res.Status,
		"a new order must never start anywhere but 'placed' - certainly not 'confirmed'")
}

// ── State machine ────────────────────────────────────────────────────────────
//
// Each transition must be guarded on the CURRENT status. The real guard is
// the WHERE status = $fromStatus clause in UpdateOrderStatus (which is also
// what makes concurrent double-transitions impossible); these tests assert
// the service passes the correct from/to pair down to it.

func orderInStatus(salonID uuid.UUID, status string) *mockRepo {
	return &mockRepo{
		order: &Order{
			ID:          uuid.New(),
			SalonID:     salonID,
			CustomerID:  uuid.New(),
			Status:      status,
			TotalAmount: decimal.RequireFromString("20.00"),
		},
	}
}

func TestConfirmOrderPayment_TransitionsPlacedToConfirmed(t *testing.T) {
	salonID := uuid.New()
	repo := orderInStatus(salonID, OrderStatusPlaced)
	svc := newTestService(repo)

	ref := "Whish #12345"
	_, err := svc.ConfirmOrderPayment(context.Background(), repo.order.ID, salonID,
		ConfirmOrderPaymentRequest{Reference: &ref})

	require.NoError(t, err)
	assert.Equal(t, OrderStatusPlaced, repo.lastFromStatus)
	assert.Equal(t, OrderStatusConfirmed, repo.lastToStatus)
	require.NotNil(t, repo.lastPaymentRef)
	assert.Equal(t, ref, *repo.lastPaymentRef, "the payment reference must reach the repository")
}

func TestShipOrder_RequiresConfirmedNotPlaced(t *testing.T) {
	// The guard that stops an order shipping before payment was confirmed.
	salonID := uuid.New()
	repo := orderInStatus(salonID, OrderStatusPlaced)
	svc := newTestService(repo)

	_, err := svc.ShipOrder(context.Background(), repo.order.ID, salonID)

	require.NoError(t, err) // the mock doesn't enforce; assert the guard passed down
	assert.Equal(t, OrderStatusConfirmed, repo.lastFromStatus,
		"ship must be guarded on 'confirmed' - shipping straight from 'placed' would mean shipping unpaid goods")
	assert.Equal(t, OrderStatusShipped, repo.lastToStatus)
}

func TestDeliverOrder_RequiresShipped(t *testing.T) {
	salonID := uuid.New()
	repo := orderInStatus(salonID, OrderStatusShipped)
	svc := newTestService(repo)

	_, err := svc.DeliverOrder(context.Background(), repo.order.ID, salonID)

	require.NoError(t, err)
	assert.Equal(t, OrderStatusShipped, repo.lastFromStatus)
	assert.Equal(t, OrderStatusDelivered, repo.lastToStatus)
}

func TestTransition_RepositoryRejection_SurfacesAsError(t *testing.T) {
	// UpdateOrderStatus returns ErrInvalidOrderTransition when its guarded
	// WHERE matches no row - i.e. the order wasn't in the expected status.
	// That must surface as a clean error, not a success.
	salonID := uuid.New()
	repo := orderInStatus(salonID, OrderStatusPlaced)
	repo.updateStatusErr = ErrInvalidOrderTransition
	svc := newTestService(repo)

	_, err := svc.ShipOrder(context.Background(), repo.order.ID, salonID)

	require.Error(t, err, "a rejected state transition must surface, never be swallowed")
}

func TestCancelOrder_AfterShipped_Rejected(t *testing.T) {
	// Once shipped, a physical item is in transit. PRD §13.2 makes
	// 'returned' the outcome for that case - not a retroactive cancel.
	salonID := uuid.New()
	repo := orderInStatus(salonID, OrderStatusShipped)
	svc := newTestService(repo)

	_, err := svc.CancelOrder(context.Background(), repo.order.ID,
		repo.order.CustomerID, "customer", nil, CancelOrderRequest{})

	require.Error(t, err, "a shipped order must not be cancellable")
}

func TestCancelOrder_WhenPlaced_AllowedForCustomer(t *testing.T) {
	salonID := uuid.New()
	repo := orderInStatus(salonID, OrderStatusPlaced)
	svc := newTestService(repo)

	_, err := svc.CancelOrder(context.Background(), repo.order.ID,
		repo.order.CustomerID, "customer", nil, CancelOrderRequest{})

	require.NoError(t, err, "a customer must be able to cancel their own not-yet-shipped order")
	assert.Equal(t, OrderStatusCancelled, repo.lastToStatus)
}

// ── Authorization ────────────────────────────────────────────────────────────

func TestGetOrderByID_UnrelatedUser_Denied(t *testing.T) {
	repo := orderInStatus(uuid.New(), OrderStatusPlaced)
	svc := newTestService(repo)

	_, err := svc.GetOrderByID(context.Background(), repo.order.ID,
		uuid.New(), "customer", nil) // neither the order's customer nor its salon

	require.Error(t, err, "a stranger must not be able to read someone else's order")
}

func TestGetOrderByID_OwningCustomer_Allowed(t *testing.T) {
	repo := orderInStatus(uuid.New(), OrderStatusPlaced)
	svc := newTestService(repo)

	_, err := svc.GetOrderByID(context.Background(), repo.order.ID,
		repo.order.CustomerID, "customer", nil)

	require.NoError(t, err)
}

func TestGetOrderByID_OwningSalon_Allowed(t *testing.T) {
	salonID := uuid.New()
	repo := orderInStatus(salonID, OrderStatusPlaced)
	svc := newTestService(repo)

	_, err := svc.GetOrderByID(context.Background(), repo.order.ID,
		uuid.New(), "artist", &salonID)

	require.NoError(t, err, "the salon fulfilling the order must be able to read it")
}

func TestGetOrderByID_OtherSalon_Denied(t *testing.T) {
	repo := orderInStatus(uuid.New(), OrderStatusPlaced)
	attackerSalonID := uuid.New()
	svc := newTestService(repo)

	_, err := svc.GetOrderByID(context.Background(), repo.order.ID,
		uuid.New(), "artist", &attackerSalonID)

	require.Error(t, err, "an artist must not read another salon's orders")
}

func TestConfirmOrderPayment_OtherSalon_Denied(t *testing.T) {
	repo := orderInStatus(uuid.New(), OrderStatusPlaced)
	svc := newTestService(repo)

	_, err := svc.ConfirmOrderPayment(context.Background(), repo.order.ID,
		uuid.New(), ConfirmOrderPaymentRequest{}) // a DIFFERENT salon

	require.Error(t, err, "an artist must not confirm payment on another salon's order")
	assert.Empty(t, repo.lastToStatus, "the transition must not even be attempted")
}

func TestShipOrder_OtherSalon_Denied(t *testing.T) {
	repo := orderInStatus(uuid.New(), OrderStatusConfirmed)
	svc := newTestService(repo)

	_, err := svc.ShipOrder(context.Background(), repo.order.ID, uuid.New())

	require.Error(t, err)
}

func TestCancelOrder_UnrelatedUser_Denied(t *testing.T) {
	repo := orderInStatus(uuid.New(), OrderStatusPlaced)
	svc := newTestService(repo)

	_, err := svc.CancelOrder(context.Background(), repo.order.ID,
		uuid.New(), "customer", nil, CancelOrderRequest{})

	require.Error(t, err, "a stranger must not be able to cancel someone else's order")
}

func TestUpdateProduct_OtherSalonsProduct_Denied(t *testing.T) {
	victimSalonID := uuid.New()
	p := activeProduct(victimSalonID, "10.00")
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	_, err := svc.UpdateProduct(context.Background(), p.ID, uuid.New(), UpdateProductRequest{})

	require.Error(t, err, "an artist must not be able to edit another salon's product")
}

// ── Product validation ───────────────────────────────────────────────────────

func TestCreateProduct_NegativePrice_Rejected(t *testing.T) {
	svc := newTestService(&mockRepo{})

	_, err := svc.CreateProduct(context.Background(), uuid.New(), CreateProductRequest{
		Name: "Broken", Price: "-10.00",
	})

	require.Error(t, err)
}

func TestCreateProduct_NonNumericPrice_Rejected(t *testing.T) {
	svc := newTestService(&mockRepo{})

	_, err := svc.CreateProduct(context.Background(), uuid.New(), CreateProductRequest{
		Name: "Broken", Price: "ten dollars",
	})

	require.Error(t, err)
}

func TestCreateProduct_Success(t *testing.T) {
	svc := newTestService(&mockRepo{})

	res, err := svc.CreateProduct(context.Background(), uuid.New(), CreateProductRequest{
		Name: "Beauty Blender", Price: "8.50",
	})

	require.NoError(t, err)
	assert.True(t, res.IsActive, "a newly created product must be active by default")
	assert.True(t, decimal.RequireFromString("8.50").Equal(res.Price))
}

// ── Stock / availability ─────────────────────────────────────────────────────
//
// Two layers, tested separately: PlaceOrder's own pre-check (fast, friendly,
// but reads an unlocked snapshot) and the fact that a repository-level
// rejection (what the real atomic decrement returns under a genuine race)
// surfaces as the same customer-facing error rather than a raw 500.

// stockedProduct is activeProduct with a tracked stock count.
func stockedProduct(salonID uuid.UUID, price string, stock int) *Product {
	p := activeProduct(salonID, price)
	p.StockQuantity = &stock
	return p
}

func TestCreateProduct_WithStockQuantity_Passthrough(t *testing.T) {
	svc := newTestService(&mockRepo{})

	res, err := svc.CreateProduct(context.Background(), uuid.New(), CreateProductRequest{
		Name: "Limited Edition Palette", Price: "45.00", StockQuantity: intPtr(10),
	})

	require.NoError(t, err)
	require.NotNil(t, res.StockQuantity, "a supplied stock quantity must reach the response")
	assert.Equal(t, 10, *res.StockQuantity)
}

func TestCreateProduct_NoStockQuantity_MeansUnlimited(t *testing.T) {
	svc := newTestService(&mockRepo{})

	res, err := svc.CreateProduct(context.Background(), uuid.New(), CreateProductRequest{
		Name: "Everyday Essential", Price: "9.00",
	})

	require.NoError(t, err)
	assert.Nil(t, res.StockQuantity, "omitting stock_quantity must mean unlimited, not zero")
}

func TestCreateProduct_NegativeStock_Rejected(t *testing.T) {
	svc := newTestService(&mockRepo{})

	_, err := svc.CreateProduct(context.Background(), uuid.New(), CreateProductRequest{
		Name: "Broken", Price: "10.00", StockQuantity: intPtr(-1),
	})

	require.Error(t, err, "a negative stock quantity must never be accepted")
}

func TestPlaceOrder_EnoughStock_Succeeds(t *testing.T) {
	salonID := uuid.New()
	p := stockedProduct(salonID, "20.00", 5)
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 5}},
	})

	require.NoError(t, err, "ordering exactly the remaining stock must be allowed")
}

func TestPlaceOrder_MoreThanStock_RejectedByPreCheck(t *testing.T) {
	salonID := uuid.New()
	p := stockedProduct(salonID, "20.00", 3)
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 10}},
	})

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, fiber.StatusConflict, appErr.HTTPStatus,
		"out-of-stock must be a 409, not a generic 400/500")
	assert.Nil(t, repo.createdOrder, "no order may be persisted when the pre-check rejects it")
}

func TestPlaceOrder_UnlimitedStock_NeverBlocksOnQuantity(t *testing.T) {
	salonID := uuid.New()
	p := activeProduct(salonID, "20.00") // StockQuantity left nil - unlimited
	repo := &mockRepo{products: map[uuid.UUID]*Product{p.ID: p}}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		// 50 is OrderItemRequest's own validation ceiling (max=50) - the
		// largest quantity a request can carry at all, chosen specifically
		// so this exercises the stock check's upper edge, not the
		// unrelated request-validation ceiling.
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 50}},
	})

	require.NoError(t, err, "a product with no tracked stock must never be blocked on quantity")
}

func TestPlaceOrder_RepositoryReportsInsufficientStock_MapsToConflict(t *testing.T) {
	// Simulates losing the real race: the pre-check's unlocked read still
	// saw enough stock, but repo.CreateOrder's atomic decrement (the actual
	// guard) found someone else took the last unit first.
	salonID := uuid.New()
	p := stockedProduct(salonID, "20.00", 1)
	repo := &mockRepo{
		products:       map[uuid.UUID]*Product{p.ID: p},
		createOrderErr: ErrInsufficientStock,
	}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 1}},
	})

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, fiber.StatusConflict, appErr.HTTPStatus,
		"a race lost at the repository layer must still surface as a clean 409, not a raw 500")
}

func intPtr(n int) *int { return &n }

func TestPlaceOrder_CustomerResolutionFails_NoOrderCreated(t *testing.T) {
	salonID := uuid.New()
	p := activeProduct(salonID, "5.00")
	repo := &mockRepo{
		products:    map[uuid.UUID]*Product{p.ID: p},
		customerErr: errors.New("db down"),
	}
	svc := newTestService(repo)

	_, err := svc.PlaceOrder(context.Background(), CreateOrderRequest{
		SalonID: salonID.String(), Name: "Sarah", Phone: "70123456", DeliveryLat: validLat, DeliveryLng: validLng,
		Items: []OrderItemRequest{{ProductID: p.ID.String(), Quantity: 1}},
	})

	require.Error(t, err)
	assert.Nil(t, repo.createdOrder, "no order may be persisted if the customer couldn't be resolved")
}
