package product

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolationCode is the PostgreSQL error code for unique constraint violations.
const uniqueViolationCode = "23505"

// Repository defines all database operations for the product domain.
type Repository interface {
	// ── Products ──────────────────────────────────────────────────────────
	CreateProduct(ctx context.Context, p *Product) error
	GetProductByID(ctx context.Context, id uuid.UUID) (*Product, error)
	// GetProductsBySalon returns a salon's products. activeOnly=true is the
	// public/customer-facing view; false is the artist's own management
	// view, which needs to see deactivated products too (to reactivate them).
	GetProductsBySalon(ctx context.Context, salonID uuid.UUID, activeOnly bool) ([]*Product, error)
	UpdateProduct(ctx context.Context, id uuid.UUID, req UpdateProductRequest) error

	// ── Orders ────────────────────────────────────────────────────────────
	// CreateOrder inserts the order and all its line items in a single
	// transaction - an order with some items persisted and others lost to
	// a mid-write failure would be a genuinely bad state to leave behind.
	CreateOrder(ctx context.Context, o *Order, items []*OrderItem) error
	GetOrderByID(ctx context.Context, id uuid.UUID) (*Order, []*OrderItem, error)
	GetOrdersBySalon(ctx context.Context, salonID uuid.UUID, status string) ([]*Order, error)

	// GetEnrichedOrdersBySalon is what the artist-facing order queue
	// screen actually uses - an order with no customer name attached is
	// barely usable for whoever has to fulfil it. Includes items (each
	// order is fetched via GetOrderItems internally) since an order
	// listing with no line items is equally unusable.
	GetEnrichedOrdersBySalon(ctx context.Context, salonID uuid.UUID, status string) ([]*EnrichedOrderResponse, error)
	GetOrdersByCustomer(ctx context.Context, customerID uuid.UUID) ([]*Order, error)
	// UpdateOrderStatus performs one state-machine transition, stamping the
	// matching timestamp column (confirmed_at/shipped_at/etc.) in the same
	// statement. paymentReference is only meaningful on the confirm
	// transition; nil elsewhere.
	UpdateOrderStatus(ctx context.Context, id uuid.UUID, fromStatus, toStatus string, paymentReference, cancellationReason *string) error
	// GetOrderItems is used to attach line items to orders fetched via the
	// list endpoints (GetOrdersBySalon/GetOrdersByCustomer don't join them
	// inline - see the service layer for why).
	GetOrderItems(ctx context.Context, orderID uuid.UUID) ([]*OrderItem, error)

	// FindOrCreateCustomerByPhone resolves identity by phone - the same
	// "one phone number, one account" rule already established (migration
	// 014, booking's CreateGuestUser, customerauth's own version). A small,
	// deliberate duplication across domains rather than a cross-package
	// dependency - consistent with the choice already made for customerauth.
	FindOrCreateCustomerByPhone(ctx context.Context, name, phone string) (uuid.UUID, error)
}

type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository constructs the Postgres-backed Repository implementation.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

// ── Products ────────────────────────────────────────────────────────────

func (r *pgRepo) CreateProduct(ctx context.Context, p *Product) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO products (id, salon_id, name, description, category, price, image_url, is_active)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE)
		RETURNING created_at, updated_at`,
		p.ID, p.SalonID, p.Name, p.Description, p.Category, p.Price, p.ImageURL,
	).Scan(&p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create product: %w", err)
	}
	return nil
}

func (r *pgRepo) GetProductByID(ctx context.Context, id uuid.UUID) (*Product, error) {
	p := &Product{}
	err := r.db.QueryRow(ctx, `
		SELECT id, salon_id, name, description, category, price, image_url, is_active, created_at, updated_at
		FROM products
		WHERE id = $1`,
		id,
	).Scan(&p.ID, &p.SalonID, &p.Name, &p.Description, &p.Category, &p.Price, &p.ImageURL, &p.IsActive, &p.CreatedAt, &p.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrProductNotFound
		}
		return nil, fmt.Errorf("get product by id: %w", err)
	}
	return p, nil
}

func (r *pgRepo) GetProductsBySalon(ctx context.Context, salonID uuid.UUID, activeOnly bool) ([]*Product, error) {
	q := `
		SELECT id, salon_id, name, description, category, price, image_url, is_active, created_at, updated_at
		FROM products
		WHERE salon_id = $1`
	if activeOnly {
		q += ` AND is_active = TRUE`
	}
	q += ` ORDER BY created_at DESC`

	rows, err := r.db.Query(ctx, q, salonID)
	if err != nil {
		return nil, fmt.Errorf("get products by salon: %w", err)
	}
	defer rows.Close()

	var result []*Product
	for rows.Next() {
		p := &Product{}
		if err := rows.Scan(&p.ID, &p.SalonID, &p.Name, &p.Description, &p.Category, &p.Price, &p.ImageURL, &p.IsActive, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan product: %w", err)
		}
		result = append(result, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get products by salon: rows: %w", err)
	}
	return result, nil
}

func (r *pgRepo) UpdateProduct(ctx context.Context, id uuid.UUID, req UpdateProductRequest) error {
	_, err := r.db.Exec(ctx, `
		UPDATE products
		SET name        = COALESCE($1, name),
		    description = COALESCE($2, description),
		    category    = COALESCE($3, category),
		    price       = COALESCE($4, price),
		    image_url   = COALESCE($5, image_url),
		    is_active   = COALESCE($6, is_active),
		    updated_at  = NOW()
		WHERE id = $7`,
		req.Name, req.Description, req.Category, req.Price, req.ImageURL, req.IsActive, id,
	)
	if err != nil {
		return fmt.Errorf("update product: %w", err)
	}
	return nil
}

// ── Orders ──────────────────────────────────────────────────────────────

func (r *pgRepo) CreateOrder(ctx context.Context, o *Order, items []*OrderItem) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("create order: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit

	err = tx.QueryRow(ctx, `
		INSERT INTO orders (id, salon_id, customer_id, status, total_amount, delivery_notes)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING created_at, updated_at`,
		o.ID, o.SalonID, o.CustomerID, OrderStatusPlaced, o.TotalAmount, o.DeliveryNotes,
	).Scan(&o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create order: insert order: %w", err)
	}
	o.Status = OrderStatusPlaced

	for _, item := range items {
		item.OrderID = o.ID
		_, err = tx.Exec(ctx, `
			INSERT INTO order_items (id, order_id, product_id, product_name, unit_price, quantity, subtotal)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			item.ID, item.OrderID, item.ProductID, item.ProductName, item.UnitPrice, item.Quantity, item.Subtotal,
		)
		if err != nil {
			return fmt.Errorf("create order: insert item: %w", err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("create order: commit: %w", err)
	}
	return nil
}

func (r *pgRepo) GetOrderByID(ctx context.Context, id uuid.UUID) (*Order, []*OrderItem, error) {
	o := &Order{}
	err := r.db.QueryRow(ctx, `
		SELECT id, salon_id, customer_id, status, total_amount, payment_reference,
		       delivery_notes, cancellation_reason,
		       confirmed_at, shipped_at, delivered_at, cancelled_at, created_at, updated_at
		FROM orders
		WHERE id = $1
		AND deleted_at IS NULL`,
		id,
	).Scan(
		&o.ID, &o.SalonID, &o.CustomerID, &o.Status, &o.TotalAmount, &o.PaymentReference,
		&o.DeliveryNotes, &o.CancellationReason,
		&o.ConfirmedAt, &o.ShippedAt, &o.DeliveredAt, &o.CancelledAt, &o.CreatedAt, &o.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, ErrOrderNotFound
		}
		return nil, nil, fmt.Errorf("get order by id: %w", err)
	}

	items, err := r.GetOrderItems(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	return o, items, nil
}

func (r *pgRepo) GetOrderItems(ctx context.Context, orderID uuid.UUID) ([]*OrderItem, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, order_id, product_id, product_name, unit_price, quantity, subtotal
		FROM order_items
		WHERE order_id = $1
		ORDER BY created_at ASC`,
		orderID,
	)
	if err != nil {
		return nil, fmt.Errorf("get order items: %w", err)
	}
	defer rows.Close()

	var result []*OrderItem
	for rows.Next() {
		item := &OrderItem{}
		if err := rows.Scan(&item.ID, &item.OrderID, &item.ProductID, &item.ProductName, &item.UnitPrice, &item.Quantity, &item.Subtotal); err != nil {
			return nil, fmt.Errorf("scan order item: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get order items: rows: %w", err)
	}
	return result, nil
}

func (r *pgRepo) GetOrdersBySalon(ctx context.Context, salonID uuid.UUID, status string) ([]*Order, error) {
	q := `
		SELECT id, salon_id, customer_id, status, total_amount, payment_reference,
		       delivery_notes, cancellation_reason,
		       confirmed_at, shipped_at, delivered_at, cancelled_at, created_at, updated_at
		FROM orders
		WHERE salon_id = $1
		AND deleted_at IS NULL`
	args := []interface{}{salonID}
	if status != "" {
		q += ` AND status = $2`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC`

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("get orders by salon: %w", err)
	}
	defer rows.Close()
	return scanOrders(rows)
}

// GetEnrichedOrdersBySalon joins to users for the customer's name/phone,
// then attaches each order's line items - see the Repository interface
// doc comment for why both matter for this specific, artist-facing view.
func (r *pgRepo) GetEnrichedOrdersBySalon(ctx context.Context, salonID uuid.UUID, status string) ([]*EnrichedOrderResponse, error) {
	q := `
		SELECT o.id, o.status, o.total_amount, o.payment_reference,
		       o.delivery_notes, o.cancellation_reason,
		       o.confirmed_at, o.shipped_at, o.delivered_at, o.cancelled_at, o.created_at,
		       u.name, u.phone
		FROM orders o
		JOIN users u ON u.id = o.customer_id
		WHERE o.salon_id = $1
		AND o.deleted_at IS NULL`
	args := []interface{}{salonID}
	if status != "" {
		q += ` AND o.status = $2`
		args = append(args, status)
	}
	q += ` ORDER BY o.created_at DESC`

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("get enriched orders by salon: %w", err)
	}
	defer rows.Close()

	var result []*EnrichedOrderResponse
	for rows.Next() {
		e := &EnrichedOrderResponse{}
		var orderID uuid.UUID
		if err := rows.Scan(
			&orderID, &e.Status, &e.TotalAmount, &e.PaymentReference,
			&e.DeliveryNotes, &e.CancellationReason,
			&e.ConfirmedAt, &e.ShippedAt, &e.DeliveredAt, &e.CancelledAt, &e.CreatedAt,
			&e.CustomerName, &e.CustomerPhone,
		); err != nil {
			return nil, fmt.Errorf("scan enriched order: %w", err)
		}
		e.ID = orderID
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get enriched orders by salon: rows: %w", err)
	}

	// Attach items - one query per order rather than a second JOIN, same
	// trade-off already accepted by GetOrderByID for the same reason: an
	// order's item count is small (a handful at most), so N+1 here is
	// negligible, and keeping the JOIN itself simple avoids a much messier
	// GROUP BY / json_agg query for what's a genuinely small win.
	for _, e := range result {
		items, err := r.GetOrderItems(ctx, e.ID)
		if err != nil {
			return nil, fmt.Errorf("get enriched orders by salon: items for %s: %w", e.ID, err)
		}
		for _, it := range items {
			e.Items = append(e.Items, OrderItemResponse{
				ProductID:   it.ProductID,
				ProductName: it.ProductName,
				UnitPrice:   it.UnitPrice,
				Quantity:    it.Quantity,
				Subtotal:    it.Subtotal,
			})
		}
	}

	return result, nil
}

func (r *pgRepo) GetOrdersByCustomer(ctx context.Context, customerID uuid.UUID) ([]*Order, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, salon_id, customer_id, status, total_amount, payment_reference,
		       delivery_notes, cancellation_reason,
		       confirmed_at, shipped_at, delivered_at, cancelled_at, created_at, updated_at
		FROM orders
		WHERE customer_id = $1
		AND deleted_at IS NULL
		ORDER BY created_at DESC`,
		customerID,
	)
	if err != nil {
		return nil, fmt.Errorf("get orders by customer: %w", err)
	}
	defer rows.Close()
	return scanOrders(rows)
}

func scanOrders(rows pgx.Rows) ([]*Order, error) {
	var result []*Order
	for rows.Next() {
		o := &Order{}
		if err := rows.Scan(
			&o.ID, &o.SalonID, &o.CustomerID, &o.Status, &o.TotalAmount, &o.PaymentReference,
			&o.DeliveryNotes, &o.CancellationReason,
			&o.ConfirmedAt, &o.ShippedAt, &o.DeliveredAt, &o.CancelledAt, &o.CreatedAt, &o.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		result = append(result, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan orders: rows: %w", err)
	}
	return result, nil
}

// UpdateOrderStatus performs one guarded state-machine transition - the
// WHERE clause requires the row to currently be in fromStatus, so a
// concurrent double-transition (two requests trying to ship the same order
// at once) can only ever succeed once; the loser gets RowsAffected()==0.
func (r *pgRepo) UpdateOrderStatus(ctx context.Context, id uuid.UUID, fromStatus, toStatus string, paymentReference, cancellationReason *string) error {
	var timestampCol string
	switch toStatus {
	case OrderStatusConfirmed:
		timestampCol = "confirmed_at"
	case OrderStatusShipped:
		timestampCol = "shipped_at"
	case OrderStatusDelivered:
		timestampCol = "delivered_at"
	case OrderStatusCancelled:
		timestampCol = "cancelled_at"
	default:
		timestampCol = "" // 'returned' has no dedicated timestamp column
	}

	q := fmt.Sprintf(`
		UPDATE orders
		SET status = $1,
		    payment_reference = COALESCE($2, payment_reference),
		    cancellation_reason = COALESCE($3, cancellation_reason),
		    %s
		    updated_at = NOW()
		WHERE id = $4
		AND status = $5
		AND deleted_at IS NULL`,
		timestampColAssignment(timestampCol),
	)

	result, err := r.db.Exec(ctx, q, toStatus, paymentReference, cancellationReason, id, fromStatus)
	if err != nil {
		return fmt.Errorf("update order status: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrInvalidOrderTransition
	}
	return nil
}

// timestampColAssignment builds the "col = NOW()," fragment for
// UpdateOrderStatus, or an empty string when the target status has no
// dedicated timestamp column (currently just 'returned').
func timestampColAssignment(col string) string {
	if col == "" {
		return ""
	}
	return col + " = NOW(),"
}

// FindOrCreateCustomerByPhone mirrors booking's CreateGuestUser /
// customerauth's own version - same lookup-then-insert-with-race-handling
// shape, same "one phone number, one account" guarantee (migration 014).
func (r *pgRepo) FindOrCreateCustomerByPhone(ctx context.Context, name, phone string) (uuid.UUID, error) {
	var existingID uuid.UUID
	err := r.db.QueryRow(ctx,
		`SELECT id FROM users WHERE phone = $1 AND deleted_at IS NULL`,
		phone,
	).Scan(&existingID)
	if err == nil {
		return existingID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("find or create customer: lookup: %w", err)
	}

	id := uuid.New()
	email := fmt.Sprintf("customer_%s@bedge.guest", id.String())

	_, err = r.db.Exec(ctx, `
		INSERT INTO users (id, name, email, password_hash, role, phone, status)
		VALUES ($1, $2, $3, 'GUEST_ACCOUNT_NO_PASSWORD', 'customer', $4, 'active')`,
		id, name, email, phone,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			var winnerID uuid.UUID
			lookupErr := r.db.QueryRow(ctx,
				`SELECT id FROM users WHERE phone = $1 AND deleted_at IS NULL`,
				phone,
			).Scan(&winnerID)
			if lookupErr == nil {
				return winnerID, nil
			}
		}
		return uuid.Nil, fmt.Errorf("find or create customer: insert: %w", err)
	}
	return id, nil
}
