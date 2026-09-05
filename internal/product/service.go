package product

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/money"
)

// Service handles all product-store business logic.
type Service struct {
	repo     Repository
	validate *validator.Validate
}

// NewService constructs a Service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo, validate: validator.New()}
}

// ── Products ──────────────────────────────────────────────────────────────

// CreateProduct adds a product to a salon's catalog. Bearer - salonID comes
// from the artist's own JWT (middleware.SalonIDFromContext), never from the
// request body, matching the multi-tenant isolation rule already
// established for services.
// errProductNotFound is the single answer to "you may not have this
// object", whether it does not exist or is not yours. A foreign object and a
// nonexistent one must be indistinguishable, or the status code becomes an
// oracle for enumerating real IDs (security test AUTH-02, 2026-09-05). One
// constructor shared by both branches is what stops them drifting apart; see
// the longer note on booking.errBookingNotFound.
func errProductNotFound() error {
	return apperror.NotFound("PRODUCT_NOT_FOUND", "Product not found")
}

// errOrderNotFound is the single answer to "you may not have this
// object", whether it does not exist or is not yours. A foreign object and a
// nonexistent one must be indistinguishable, or the status code becomes an
// oracle for enumerating real IDs (security test AUTH-02, 2026-09-05). One
// constructor shared by both branches is what stops them drifting apart; see
// the longer note on booking.errBookingNotFound.
func errOrderNotFound() error {
	return apperror.NotFound("ORDER_NOT_FOUND", "Order not found")
}

func (s *Service) CreateProduct(ctx context.Context, salonID uuid.UUID, req CreateProductRequest) (*ProductResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	price, err := money.Parse(req.Price, "price")
	if err != nil {
		return nil, err
	}

	p := &Product{
		ID:            uuid.New(),
		SalonID:       salonID,
		Name:          req.Name,
		Description:   req.Description,
		Category:      req.Category,
		Price:         price,
		ImageURL:      req.ImageURL,
		StockQuantity: req.StockQuantity,
		IsActive:      true,
	}

	if err := s.repo.CreateProduct(ctx, p); err != nil {
		return nil, fmt.Errorf("create product: %w", err)
	}
	return toProductResponse(p), nil
}

// UpdateProduct changes a product - partial update, only supplied fields
// change (COALESCE at the repository layer). Verifies the product actually
// belongs to the calling salon before touching it.
func (s *Service) UpdateProduct(ctx context.Context, productID, salonID uuid.UUID, req UpdateProductRequest) (*ProductResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	existing, err := s.repo.GetProductByID(ctx, productID)
	if err != nil {
		if errors.Is(err, ErrProductNotFound) {
			return nil, errProductNotFound()
		}
		return nil, fmt.Errorf("update product: get: %w", err)
	}
	if existing.SalonID != salonID {
		return nil, errProductNotFound()
	}

	// Parsed for validation only - UpdateProduct binds req.Price's string
	// directly, so this is the gate that stops a non-money value reaching
	// the NUMERIC column.
	if _, err := money.ParseOptional(req.Price, "price"); err != nil {
		return nil, err
	}

	if err := s.repo.UpdateProduct(ctx, productID, req); err != nil {
		return nil, fmt.Errorf("update product: %w", err)
	}

	updated, err := s.repo.GetProductByID(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("update product: reload: %w", err)
	}
	return toProductResponse(updated), nil
}

// ListProductsBySalon: activeOnly=true for the public/customer-facing
// catalog view, false for the artist's own management screen (which needs
// to see deactivated products too, to be able to reactivate them).
func (s *Service) ListProductsBySalon(ctx context.Context, salonID uuid.UUID, activeOnly bool) ([]*ProductResponse, error) {
	products, err := s.repo.GetProductsBySalon(ctx, salonID, activeOnly)
	if err != nil {
		return nil, fmt.Errorf("list products by salon: %w", err)
	}
	result := make([]*ProductResponse, 0, len(products))
	for _, p := range products {
		result = append(result, toProductResponse(p))
	}
	return result, nil
}

// ── Orders ────────────────────────────────────────────────────────────────

// PlaceOrder creates an order - public, guest-friendly, matching the exact
// philosophy already established everywhere else in B-Edge: identity is
// resolved by phone, no account required to buy. Prices are taken from the
// CURRENT product rows at order time (never trusted from the request body
// a customer-supplied price would be a real, exploitable gap), then
// snapshotted into order_items so they never silently change afterward.
func (s *Service) PlaceOrder(ctx context.Context, req CreateOrderRequest) (*OrderResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	salonID, err := uuid.Parse(req.SalonID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_SALON_ID", "Invalid salon ID")
	}

	items := make([]*OrderItem, 0, len(req.Items))
	total := decimal.Zero

	for _, reqItem := range req.Items {
		productID, err := uuid.Parse(reqItem.ProductID)
		if err != nil {
			return nil, apperror.BadRequest("INVALID_PRODUCT_ID", "Invalid product ID")
		}

		product, err := s.repo.GetProductByID(ctx, productID)
		if err != nil {
			if errors.Is(err, ErrProductNotFound) {
				return nil, apperror.NotFound("PRODUCT_NOT_FOUND", "One or more products in this order were not found")
			}
			return nil, fmt.Errorf("place order: get product: %w", err)
		}
		if product.SalonID != salonID {
			// A product from a different salon snuck into this order
			// treat it the same as "not found" rather than leaking which
			// salon it actually belongs to.
			return nil, apperror.NotFound("PRODUCT_NOT_FOUND", "One or more products in this order were not found")
		}
		if !product.IsActive {
			return nil, apperror.BadRequest("PRODUCT_INACTIVE", ErrProductInactive.Error())
		}
		// Advisory only - a fast, friendly rejection for the common case.
		// This read isn't locked, so two concurrent checkouts for the last
		// unit could both pass it; the atomic decrement inside
		// repo.CreateOrder below is what actually prevents overselling,
		// and its rejection is mapped to the same error after the loop.
		if product.StockQuantity != nil && *product.StockQuantity < reqItem.Quantity {
			return nil, apperror.Conflict("PRODUCT_OUT_OF_STOCK",
				fmt.Sprintf("%s is out of stock in the quantity you requested", product.Name))
		}

		quantity := decimal.NewFromInt(int64(reqItem.Quantity))
		subtotal := product.Price.Mul(quantity)
		total = total.Add(subtotal)

		items = append(items, &OrderItem{
			ID:          uuid.New(),
			ProductID:   product.ID,
			ProductName: product.Name,
			UnitPrice:   product.Price,
			Quantity:    reqItem.Quantity,
			Subtotal:    subtotal,
		})
	}

	customerID, err := s.repo.FindOrCreateCustomerByPhone(ctx, req.Name, req.Phone)
	if err != nil {
		return nil, fmt.Errorf("place order: resolve customer: %w", err)
	}

	order := &Order{
		ID:            uuid.New(),
		SalonID:       salonID,
		CustomerID:    customerID,
		TotalAmount:   total,
		DeliveryNotes: req.DeliveryNotes,
		DeliveryLat:   &req.DeliveryLat,
		DeliveryLng:   &req.DeliveryLng,
	}

	if err := s.repo.CreateOrder(ctx, order, items); err != nil {
		if errors.Is(err, ErrInsufficientStock) {
			return nil, apperror.Conflict("PRODUCT_OUT_OF_STOCK",
				"Sorry, one or more items in your order just sold out. Please update your cart and try again.")
		}
		return nil, fmt.Errorf("place order: %w", err)
	}
	return toOrderResponse(order, items), nil
}

// GetOrderByID: the order's own customer, the salon that owns it, or an
// admin may view it - enforced here, not left to the caller.
func (s *Service) GetOrderByID(ctx context.Context, orderID, requesterID uuid.UUID, requesterRole string, requesterSalonID *uuid.UUID) (*OrderResponse, error) {
	order, items, err := s.repo.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, ErrOrderNotFound) {
			return nil, errOrderNotFound()
		}
		return nil, fmt.Errorf("get order by id: %w", err)
	}

	isCustomer := order.CustomerID == requesterID
	isAdmin := requesterRole == "admin"
	isSalonOwner := requesterSalonID != nil && *requesterSalonID == order.SalonID

	if !isCustomer && !isAdmin && !isSalonOwner {
		return nil, errOrderNotFound()
	}

	return toOrderResponse(order, items), nil
}

// ListOrdersByCustomer: a customer's own order history - "My Orders",
// same role this plays for products that My Bookings plays for
// appointments.
func (s *Service) ListOrdersByCustomer(ctx context.Context, customerID uuid.UUID) ([]*OrderResponse, error) {
	orders, err := s.repo.GetOrdersByCustomer(ctx, customerID)
	if err != nil {
		return nil, fmt.Errorf("list orders by customer: %w", err)
	}
	result := make([]*OrderResponse, 0, len(orders))
	for _, o := range orders {
		items, err := s.repo.GetOrderItems(ctx, o.ID)
		if err != nil {
			return nil, fmt.Errorf("list orders by customer: items for %s: %w", o.ID, err)
		}
		result = append(result, toOrderResponse(o, items))
	}
	return result, nil
}

// ListOrdersBySalon is the artist-facing order queue - enriched with
// customer name/phone, see GetEnrichedOrdersBySalon's doc comment for why.
func (s *Service) ListOrdersBySalon(ctx context.Context, salonID uuid.UUID, status string) ([]*EnrichedOrderResponse, error) {
	orders, err := s.repo.GetEnrichedOrdersBySalon(ctx, salonID, status)
	if err != nil {
		return nil, fmt.Errorf("list orders by salon: %w", err)
	}
	return orders, nil
}

// ConfirmOrderPayment: 'placed' → 'confirmed', the manual "I checked my
// Wish/OMT account, the money's there" action - mirrors
// ConfirmDepositReceived's shape exactly, for a full amount instead of a
// deposit. Bearer, salon-scoped.
func (s *Service) ConfirmOrderPayment(ctx context.Context, orderID, salonID uuid.UUID, req ConfirmOrderPaymentRequest) (*OrderResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}
	return s.transitionOrder(ctx, orderID, salonID, OrderStatusPlaced, OrderStatusConfirmed, req.Reference, nil)
}

// ShipOrder: 'confirmed' → 'shipped'.
func (s *Service) ShipOrder(ctx context.Context, orderID, salonID uuid.UUID) (*OrderResponse, error) {
	return s.transitionOrder(ctx, orderID, salonID, OrderStatusConfirmed, OrderStatusShipped, nil, nil)
}

// DeliverOrder: 'shipped' → 'delivered', the final happy-path state.
func (s *Service) DeliverOrder(ctx context.Context, orderID, salonID uuid.UUID) (*OrderResponse, error) {
	return s.transitionOrder(ctx, orderID, salonID, OrderStatusShipped, OrderStatusDelivered, nil, nil)
}

// CancelOrder works for either the order's own customer or the salon that
// owns it - matching CancelBooking's exact dual-role shape in the booking
// domain. Only permitted from 'placed' or 'confirmed' - once shipped, a
// physical item is already in transit; PRD §13.2 has 'returned' as its own
// separate outcome for that case.
func (s *Service) CancelOrder(ctx context.Context, orderID, requesterID uuid.UUID, requesterRole string, requesterSalonID *uuid.UUID, req CancelOrderRequest) (*OrderResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	order, _, err := s.repo.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, ErrOrderNotFound) {
			return nil, errOrderNotFound()
		}
		return nil, fmt.Errorf("cancel order: get: %w", err)
	}

	isCustomer := order.CustomerID == requesterID
	isAdmin := requesterRole == "admin"
	isSalonOwner := requesterSalonID != nil && *requesterSalonID == order.SalonID
	if !isCustomer && !isAdmin && !isSalonOwner {
		return nil, errOrderNotFound()
	}

	if !cancellableOrderStatuses[order.Status] {
		return nil, apperror.BadRequest("ORDER_NOT_CANCELLABLE", ErrOrderNotCancellable.Error())
	}

	if err := s.repo.UpdateOrderStatus(ctx, orderID, order.Status, OrderStatusCancelled, nil, req.Reason); err != nil {
		if errors.Is(err, ErrInvalidOrderTransition) {
			return nil, apperror.BadRequest("ORDER_NOT_CANCELLABLE", ErrOrderNotCancellable.Error())
		}
		return nil, fmt.Errorf("cancel order: %w", err)
	}

	updated, items, err := s.repo.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("cancel order: reload: %w", err)
	}
	return toOrderResponse(updated, items), nil
}

// transitionOrder is the shared guarded-transition helper behind
// ConfirmOrderPayment/ShipOrder/DeliverOrder - verifies salon ownership,
// then delegates the actual state check to UpdateOrderStatus's own
// WHERE-status-matches guard (see repository.go), which is what actually
// prevents a double-transition race, not this layer.
func (s *Service) transitionOrder(ctx context.Context, orderID, salonID uuid.UUID, fromStatus, toStatus string, paymentReference, cancellationReason *string) (*OrderResponse, error) {
	order, _, err := s.repo.GetOrderByID(ctx, orderID)
	if err != nil {
		if errors.Is(err, ErrOrderNotFound) {
			return nil, errOrderNotFound()
		}
		return nil, fmt.Errorf("transition order: get: %w", err)
	}
	if order.SalonID != salonID {
		return nil, errOrderNotFound()
	}

	if err := s.repo.UpdateOrderStatus(ctx, orderID, fromStatus, toStatus, paymentReference, cancellationReason); err != nil {
		if errors.Is(err, ErrInvalidOrderTransition) {
			return nil, apperror.BadRequest("INVALID_TRANSITION", ErrInvalidOrderTransition.Error())
		}
		return nil, fmt.Errorf("transition order: %w", err)
	}

	updated, items, err := s.repo.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, fmt.Errorf("transition order: reload: %w", err)
	}
	return toOrderResponse(updated, items), nil
}

// mapValidationError converts a validator error into a proper
// UnprocessableEntity with field-level details - matching the exact
// pattern established in review/customerauth, not a stripped-down generic
// message.
func mapValidationError(err error) error {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return apperror.BadRequest("VALIDATION_ERROR", err.Error())
	}
	details := make([]apperror.FieldError, 0, len(ve))
	for _, fe := range ve {
		details = append(details, apperror.FieldError{
			Field:   fe.Field(),
			Message: validationMessage(fe),
		})
	}
	return apperror.UnprocessableEntity("VALIDATION_ERROR", details)
}

func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + " is required"
	case "min":
		return fe.Field() + " must be at least " + fe.Param()
	case "max":
		return fe.Field() + " must be at most " + fe.Param() + " characters"
	case "uuid":
		return fe.Field() + " must be a valid UUID"
	case "url":
		return fe.Field() + " must be a valid URL"
	case "oneof":
		return fe.Field() + " must be one of: " + fe.Param()
	default:
		return fe.Field() + " is invalid"
	}
}
