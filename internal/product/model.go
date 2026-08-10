// Package product implements the Product Store domain for B-Edge (PRD §13)
// - an artist's physical product catalogue and the orders placed against
// it. Deliberately separate from the booking/service domains: a product
// order is not an appointment, and conflating the two would make both
// harder to reason about.
package product

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

var (
	// ErrProductNotFound is returned when no product matches the given criteria.
	ErrProductNotFound = errors.New("product not found")

	// ErrOrderNotFound is returned when no order matches the given criteria.
	ErrOrderNotFound = errors.New("order not found")

	// ErrNotProductOwner is returned when an artist tries to modify a
	// product belonging to a different salon.
	ErrNotProductOwner = errors.New("not authorised to modify this product")

	// ErrOrderNotCancellable is returned when an order's current status no
	// longer permits cancellation (already shipped/delivered/cancelled/returned).
	ErrOrderNotCancellable = errors.New("order cannot be cancelled in its current status")

	// ErrInvalidOrderTransition is returned when an order status change
	// doesn't follow the state machine (e.g. shipping an order that was
	// never confirmed).
	ErrInvalidOrderTransition = errors.New("order cannot transition to that status from its current status")

	// ErrProductInactive is returned when an order tries to include a
	// product that's been deactivated - it may have shown in a browser's
	// cart before the artist took it down, so this is a real, expected
	// case to handle gracefully, not just a theoretical one.
	ErrProductInactive = errors.New("one or more products in this order are no longer available")

	// ErrNotOrderOwner is returned when someone who isn't the order's own
	// customer, the salon that owns it, or an admin tries to access it.
	ErrNotOrderOwner = errors.New("you do not have permission to access this order")

	// ErrEmptyOrder is returned when an order has no line items at all.
	ErrEmptyOrder = errors.New("an order must contain at least one item")
)

// Order status constants - the state machine from PRD §13.2, as actually
// implemented (see migration 017 for the full transition reasoning).
const (
	OrderStatusPlaced    = "placed"
	OrderStatusConfirmed = "confirmed"
	OrderStatusShipped   = "shipped"
	OrderStatusDelivered = "delivered"
	OrderStatusCancelled = "cancelled"
	OrderStatusReturned  = "returned"
)

// cancellableOrderStatuses - an order can be cancelled any time before it
// ships. Once shipped, a physical item is already in transit; PRD §13.2
// separately lists 'returned' as its own outcome for that case, not a
// retroactive cancellation.
var cancellableOrderStatuses = map[string]bool{
	OrderStatusPlaced:    true,
	OrderStatusConfirmed: true,
}

// ── Product ──────────────────────────────────────────────────────────────

// Product mirrors a row in the products table.
type Product struct {
	ID          uuid.UUID
	SalonID     uuid.UUID
	Name        string
	Description *string
	Category    *string
	Price       decimal.Decimal
	ImageURL    *string
	IsActive    bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// ProductResponse is the public/artist-facing product representation.
type ProductResponse struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Description *string         `json:"description,omitempty"`
	Category    *string         `json:"category,omitempty"`
	Price       decimal.Decimal `json:"price"`
	ImageURL    *string         `json:"image_url,omitempty"`
	IsActive    bool            `json:"is_active"`
}

func toProductResponse(p *Product) *ProductResponse {
	return &ProductResponse{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Category:    p.Category,
		Price:       p.Price,
		ImageURL:    p.ImageURL,
		IsActive:    p.IsActive,
	}
}

// toOrderResponse maps an Order + its items to the API-facing shape. An
// order is meaningless without its line items (see OrderResponse's own doc
// comment), so this always takes both together rather than letting a
// caller accidentally build a response with items missing.
func toOrderResponse(o *Order, items []*OrderItem) *OrderResponse {
	itemResponses := make([]OrderItemResponse, 0, len(items))
	for _, it := range items {
		itemResponses = append(itemResponses, OrderItemResponse{
			ProductID:   it.ProductID,
			ProductName: it.ProductName,
			UnitPrice:   it.UnitPrice,
			Quantity:    it.Quantity,
			Subtotal:    it.Subtotal,
		})
	}
	return &OrderResponse{
		ID:                 o.ID,
		Status:             o.Status,
		TotalAmount:        o.TotalAmount,
		PaymentReference:   o.PaymentReference,
		DeliveryNotes:      o.DeliveryNotes,
		CancellationReason: o.CancellationReason,
		Items:              itemResponses,
		ConfirmedAt:        o.ConfirmedAt,
		ShippedAt:          o.ShippedAt,
		DeliveredAt:        o.DeliveredAt,
		CancelledAt:        o.CancelledAt,
		CreatedAt:          o.CreatedAt,
	}
}

// CreateProductRequest is the body for POST /products (artist-only).
type CreateProductRequest struct {
	Name        string  `json:"name"        validate:"required,min=2,max=200"`
	Description *string `json:"description" validate:"omitempty,max=1000"`
	Category    *string `json:"category"    validate:"omitempty,oneof=makeup hair nails lashes skincare"`
	Price       string  `json:"price"       validate:"required"`
	ImageURL    *string `json:"image_url"   validate:"omitempty,max=500"`
}

// UpdateProductRequest is the body for PATCH /products/:id (artist-only).
// Every field optional - only supplied fields are changed (COALESCE at the
// repository layer, same convention as UpdateServiceRequest).
type UpdateProductRequest struct {
	Name        *string `json:"name"        validate:"omitempty,min=2,max=200"`
	Description *string `json:"description" validate:"omitempty,max=1000"`
	Category    *string `json:"category"    validate:"omitempty,oneof=makeup hair nails lashes skincare"`
	Price       *string `json:"price"`
	ImageURL    *string `json:"image_url"   validate:"omitempty,max=500"`
	IsActive    *bool   `json:"is_active"`
}

// EnrichedOrderResponse adds customer display fields an artist actually
// needs to fulfil an order - who it's for, how to reach them. Same
// enrichment principle already established for EnrichedBookingResponse in
// the booking domain, learned there the hard way (a booking list with no
// customer name attached is barely usable for the artist reading it).
type EnrichedOrderResponse struct {
	OrderResponse
	CustomerName  string  `json:"customer_name"`
	CustomerPhone *string `json:"customer_phone,omitempty"`
}

// ── Order ────────────────────────────────────────────────────────────────

// Order mirrors a row in the orders table.
type Order struct {
	ID                  uuid.UUID
	SalonID             uuid.UUID
	CustomerID          uuid.UUID
	Status              string
	TotalAmount         decimal.Decimal
	PaymentReference    *string
	DeliveryNotes       *string
	CancellationReason  *string
	ConfirmedAt         *time.Time
	ShippedAt           *time.Time
	DeliveredAt         *time.Time
	CancelledAt         *time.Time
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// OrderItem mirrors a row in order_items. ProductName/UnitPrice are
// snapshots at order time - see migration 017 for why.
type OrderItem struct {
	ID          uuid.UUID
	OrderID     uuid.UUID
	ProductID   uuid.UUID
	ProductName string
	UnitPrice   decimal.Decimal
	Quantity    int
	Subtotal    decimal.Decimal
}

// OrderItemResponse is the client-facing line item shape.
type OrderItemResponse struct {
	ProductID   uuid.UUID       `json:"product_id"`
	ProductName string          `json:"product_name"`
	UnitPrice   decimal.Decimal `json:"unit_price"`
	Quantity    int             `json:"quantity"`
	Subtotal    decimal.Decimal `json:"subtotal"`
}

// OrderResponse is the full order representation, items included - an
// order is meaningless without its line items, so callers always get both
// together rather than needing a second request.
type OrderResponse struct {
	ID                 uuid.UUID           `json:"id"`
	Status             string              `json:"status"`
	TotalAmount        decimal.Decimal     `json:"total_amount"`
	PaymentReference   *string             `json:"payment_reference,omitempty"`
	DeliveryNotes      *string             `json:"delivery_notes,omitempty"`
	CancellationReason *string             `json:"cancellation_reason,omitempty"`
	Items              []OrderItemResponse `json:"items"`
	ConfirmedAt        *time.Time          `json:"confirmed_at,omitempty"`
	ShippedAt          *time.Time          `json:"shipped_at,omitempty"`
	DeliveredAt        *time.Time          `json:"delivered_at,omitempty"`
	CancelledAt        *time.Time          `json:"cancelled_at,omitempty"`
	CreatedAt          time.Time           `json:"created_at"`
}

// OrderItemRequest is one line of a CreateOrderRequest.
type OrderItemRequest struct {
	ProductID string `json:"product_id" validate:"required,uuid"`
	Quantity  int    `json:"quantity"   validate:"required,min=1,max=50"`
}

// CreateOrderRequest is the body for POST /orders. Guest-friendly,
// matching the rest of B-Edge - identity resolved by phone, no account
// required, same as a guest booking.
type CreateOrderRequest struct {
	SalonID       string             `json:"salon_id"       validate:"required,uuid"`
	Name          string             `json:"name"           validate:"required,min=2,max=100"`
	Phone         string             `json:"phone"          validate:"required,min=7,max=20"`
	DeliveryNotes *string            `json:"delivery_notes" validate:"omitempty,max=500"`
	Items         []OrderItemRequest `json:"items"          validate:"required,min=1,dive"`
}

// ConfirmOrderPaymentRequest is the body for PATCH /artists/orders/:id/confirm-payment.
// Reference is optional, mirroring ConfirmDepositReceived's own optional
// transaction-reference field in the booking domain exactly.
type ConfirmOrderPaymentRequest struct {
	Reference *string `json:"reference" validate:"omitempty,max=255"`
}

// CancelOrderRequest is the body for PATCH /orders/:id/cancel.
type CancelOrderRequest struct {
	Reason *string `json:"reason" validate:"omitempty,max=500"`
}
