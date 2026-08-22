package product

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

// Handler handles all HTTP requests for the product store.
type Handler struct {
	svc *Service
}

// RegisterRoutes attaches product-store routes to the Fiber app.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := &Handler{svc: svc}

	auth := middleware.RequireAuth()
	artistOnly := middleware.RequireRole("artist", "admin")

	// ── Public - no account needed, matching guest booking everywhere else ──
	pub := app.Group("/api/v1")
	pub.Get("/salons/:salon_id/products", handler.ListPublicProducts)
	pub.Post("/orders", handler.PlaceOrder)

	// ── Bearer - either party on an order (customer or artist) ──────────────
	//
	// Prefix MUST be scoped to /api/v1/orders, not the bare /api/v1 this used
	// to be. A Group's middleware is registered as an app-wide Use() bound to
	// its prefix, and Fiber freezes each route's middleware chain against
	// whatever is in that global stack AT REGISTRATION TIME - so a Group
	// prefixed at the API root applies auth to every route registered in any
	// domain's RegisterRoutes() called afterward in main.go, not just this
	// domain's own routes. That silently 401'd media's public portfolio
	// endpoint (registered right after this one) until this was scoped down;
	// see CLAUDE-v6.md for the specific bug this caused and how it was found.
	authed := app.Group("/api/v1/orders", auth)
	authed.Get("/me", handler.ListMyOrders)
	authed.Get("/:id", handler.GetOrder)
	authed.Patch("/:id/cancel", handler.CancelOrder)

	// ── Bearer, artist-only - catalog + order management ─────────────────────
	// NOTE the "/salon" segment - it is load-bearing, not decoration.
	// The artist domain registers `GET /artists/:id` (and it runs first),
	// so a single-segment path like /artists/products gets swallowed by
	// that route, which then treats "products" as an artist handle and
	// returns ARTIST_NOT_FOUND. Two segments can't collide with /:id.
	// This mirrors the existing /artists/salon/services convention exactly.
	artist := app.Group("/api/v1/artists/salon", auth, artistOnly)
	artist.Post("/products", handler.CreateProduct)
	artist.Patch("/products/:id", handler.UpdateProduct)
	artist.Get("/products", handler.ListMyProducts)
	artist.Get("/orders", handler.ListSalonOrders)
	artist.Patch("/orders/:id/confirm-payment", handler.ConfirmOrderPayment)
	artist.Patch("/orders/:id/ship", handler.ShipOrder)
	artist.Patch("/orders/:id/deliver", handler.DeliverOrder)
}

// ── Products ──────────────────────────────────────────────────────────────

// ListPublicProducts godoc
// @Summary      Browse a salon's active product catalog (public)
// @Tags         products
// @Produce      json
// @Param        salon_id path string true "Salon UUID"
// @Success      200 {object} response.Body{data=[]ProductResponse}
// @Router       /salons/{salon_id}/products [get]
func (h *Handler) ListPublicProducts(c *fiber.Ctx) error {
	salonID, err := uuid.Parse(c.Params("salon_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid salon ID")
	}

	products, err := h.svc.ListProductsBySalon(c.Context(), salonID, true)
	if err != nil {
		return err
	}
	return response.OK(c, products)
}

// CreateProduct godoc
// @Summary      Add a product to the artist's catalog
// @Tags         products
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body CreateProductRequest true "Product"
// @Success      201 {object} response.Body{data=ProductResponse}
// @Router       /artists/salon/products [post]
func (h *Handler) CreateProduct(c *fiber.Ctx) error {
	var req CreateProductRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	product, err := h.svc.CreateProduct(c.Context(), *salonID, req)
	if err != nil {
		return err
	}
	return response.Created(c, product)
}

// UpdateProduct godoc
// @Summary      Update a product (partial)
// @Tags         products
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path string true "Product UUID"
// @Param        body body UpdateProductRequest true "Fields to change"
// @Success      200 {object} response.Body{data=ProductResponse}
// @Router       /artists/salon/products/{id} [patch]
func (h *Handler) UpdateProduct(c *fiber.Ctx) error {
	productID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid product ID")
	}

	var req UpdateProductRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	product, err := h.svc.UpdateProduct(c.Context(), productID, *salonID, req)
	if err != nil {
		return err
	}
	return response.OK(c, product)
}

// ListMyProducts godoc
// @Summary      List the artist's own products, including deactivated ones
// @Tags         products
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]ProductResponse}
// @Router       /artists/salon/products [get]
func (h *Handler) ListMyProducts(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	products, err := h.svc.ListProductsBySalon(c.Context(), *salonID, false)
	if err != nil {
		return err
	}
	return response.OK(c, products)
}

// ── Orders ────────────────────────────────────────────────────────────────

// PlaceOrder godoc
// @Summary      Place a product order (public, no account)
// @Tags         orders
// @Accept       json
// @Produce      json
// @Param        body body CreateOrderRequest true "Order"
// @Success      201 {object} response.Body{data=OrderResponse}
// @Router       /orders [post]
func (h *Handler) PlaceOrder(c *fiber.Ctx) error {
	var req CreateOrderRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	order, err := h.svc.PlaceOrder(c.Context(), req)
	if err != nil {
		return err
	}
	return response.Created(c, order)
}

// GetOrder godoc
// @Summary      Get a single order (its own customer, its own artist, or admin)
// @Tags         orders
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Order UUID"
// @Success      200 {object} response.Body{data=OrderResponse}
// @Router       /orders/{id} [get]
func (h *Handler) GetOrder(c *fiber.Ctx) error {
	orderID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid order ID")
	}

	requesterID := middleware.UserIDFromContext(c)
	requesterRole := middleware.RoleFromContext(c)
	requesterSalonID := middleware.SalonIDFromContext(c)

	order, err := h.svc.GetOrderByID(c.Context(), orderID, requesterID, requesterRole, requesterSalonID)
	if err != nil {
		return err
	}
	return response.OK(c, order)
}

// ListMyOrders godoc
// @Summary      Get the authenticated customer's own order history
// @Tags         orders
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]OrderResponse}
// @Router       /orders/me [get]
func (h *Handler) ListMyOrders(c *fiber.Ctx) error {
	customerID := middleware.UserIDFromContext(c)

	orders, err := h.svc.ListOrdersByCustomer(c.Context(), customerID)
	if err != nil {
		return err
	}
	return response.OK(c, orders)
}

// ListSalonOrders godoc
// @Summary      Get the artist's order queue, enriched with customer name/phone
// @Tags         orders
// @Security     BearerAuth
// @Produce      json
// @Param        status query string false "Filter by status"
// @Success      200 {object} response.Body{data=[]EnrichedOrderResponse}
// @Router       /artists/salon/orders [get]
func (h *Handler) ListSalonOrders(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	status := c.Query("status")
	orders, err := h.svc.ListOrdersBySalon(c.Context(), *salonID, status)
	if err != nil {
		return err
	}
	return response.OK(c, orders)
}

// ConfirmOrderPayment godoc
// @Summary      Confirm payment received for an order (artist)
// @Tags         orders
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path string true "Order UUID"
// @Param        body body ConfirmOrderPaymentRequest false "Optional payment reference"
// @Success      200 {object} response.Body{data=OrderResponse}
// @Router       /artists/salon/orders/{id}/confirm-payment [patch]
func (h *Handler) ConfirmOrderPayment(c *fiber.Ctx) error {
	orderID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid order ID")
	}

	var req ConfirmOrderPaymentRequest
	_ = c.BodyParser(&req) // optional body - a parse failure on an empty body is fine

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	order, err := h.svc.ConfirmOrderPayment(c.Context(), orderID, *salonID, req)
	if err != nil {
		return err
	}
	return response.OK(c, order)
}

// ShipOrder godoc
// @Summary      Mark an order as shipped (artist)
// @Tags         orders
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Order UUID"
// @Success      200 {object} response.Body{data=OrderResponse}
// @Router       /artists/salon/orders/{id}/ship [patch]
func (h *Handler) ShipOrder(c *fiber.Ctx) error {
	orderID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid order ID")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	order, err := h.svc.ShipOrder(c.Context(), orderID, *salonID)
	if err != nil {
		return err
	}
	return response.OK(c, order)
}

// DeliverOrder godoc
// @Summary      Mark an order as delivered (artist)
// @Tags         orders
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Order UUID"
// @Success      200 {object} response.Body{data=OrderResponse}
// @Router       /artists/salon/orders/{id}/deliver [patch]
func (h *Handler) DeliverOrder(c *fiber.Ctx) error {
	orderID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid order ID")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	order, err := h.svc.DeliverOrder(c.Context(), orderID, *salonID)
	if err != nil {
		return err
	}
	return response.OK(c, order)
}

// CancelOrder godoc
// @Summary      Cancel an order (its own customer or its own artist)
// @Tags         orders
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path string true "Order UUID"
// @Param        body body CancelOrderRequest false "Optional reason"
// @Success      200 {object} response.Body{data=OrderResponse}
// @Router       /orders/{id}/cancel [patch]
func (h *Handler) CancelOrder(c *fiber.Ctx) error {
	orderID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid order ID")
	}

	var req CancelOrderRequest
	_ = c.BodyParser(&req)

	requesterID := middleware.UserIDFromContext(c)
	requesterRole := middleware.RoleFromContext(c)
	requesterSalonID := middleware.SalonIDFromContext(c)

	order, err := h.svc.CancelOrder(c.Context(), orderID, requesterID, requesterRole, requesterSalonID, req)
	if err != nil {
		return err
	}
	return response.OK(c, order)
}
