package promo

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// Handler exposes discount-code management.
type Handler struct{ svc *Service }

// NewHandler creates a promo handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes wires the discount routes.
//
// Everything here is artist-only and salon-scoped. There is deliberately no
// public "list the codes for this salon" endpoint: a promo code is worth
// something precisely because not everyone has it, and an endpoint that
// enumerates them hands every code to anyone who asks.
//
// Customers meet a code exactly one way - by typing one they were given, at
// checkout, against a specific booking. That lives on the booking routes,
// where the price being discounted actually is.
//
//	GET    /api/v1/artists/salon/discounts      - own codes, active first
//	POST   /api/v1/artists/salon/discounts      - create
//	PATCH  /api/v1/artists/salon/discounts/:id  - edit or deactivate
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool) {
	handler := NewHandler(NewService(NewRepository(pool)))

	auth := middleware.RequireAuth()
	artistOnly := middleware.RequireRole("artist", "admin")

	// The "/salon" segment is load-bearing for the same reason products
	// documents: the artist domain registers GET /artists/:id and it runs
	// first, so a single-segment path would be swallowed and "discounts"
	// treated as an artist handle.
	g := app.Group("/api/v1/artists/salon", auth, artistOnly)
	g.Get("/discounts", handler.ListDiscounts)
	g.Post("/discounts", handler.CreateDiscount)
	g.Patch("/discounts/:id", handler.UpdateDiscount)
}

// ListDiscounts godoc
// @Summary      List the calling artist's salon discount codes
// @Tags         discounts
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]DiscountResponse}
// @Router       /artists/salon/discounts [get]
func (h *Handler) ListDiscounts(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	out, err := h.svc.ListBySalon(c.Context(), *salonID)
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// CreateDiscount godoc
// @Summary      Create a discount code
// @Tags         discounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body CreateDiscountRequest true "Discount"
// @Success      201 {object} response.Body{data=DiscountResponse}
// @Router       /artists/salon/discounts [post]
func (h *Handler) CreateDiscount(c *fiber.Ctx) error {
	var req CreateDiscountRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	out, err := h.svc.Create(c.Context(), *salonID, req)
	if err != nil {
		return err
	}
	return response.Created(c, out)
}

// UpdateDiscount godoc
// @Summary      Edit or deactivate a discount code
// @Tags         discounts
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path string true "Discount UUID"
// @Param        body body UpdateDiscountRequest true "Fields to change"
// @Success      200 {object} response.Body{data=DiscountResponse}
// @Router       /artists/salon/discounts/{id} [patch]
func (h *Handler) UpdateDiscount(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		// Same answer as a code belonging to another salon - a malformed id
		// and a foreign one must not be distinguishable. See CLAUDE.md.
		return errDiscountNotFound()
	}

	var req UpdateDiscountRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	out, err := h.svc.Update(c.Context(), id, *salonID, req)
	if err != nil {
		return err
	}
	return response.OK(c, out)
}
