package payout

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/httpcache"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// Handler exposes salon payment destinations.
type Handler struct{ svc *Service }

// NewHandler creates a payout handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes wires the payment-destination routes.
//
// The public read is salon-scoped and deliberately NOT part of the artist
// profile response. Two reasons, both from the trust & safety review:
//
//   - The profile is fetched on discovery, so putting payment destinations on
//     it would publish every artist's account number to anything that
//     paginates the marketplace. This is read only when someone is actually
//     about to pay.
//
//   - artist.ArtistResponse is documented as deliberately narrow. Widening it
//     for payment data would erode the rule that keeps it narrow.
//
//     GET    /api/v1/salons/:salon_id/payment-methods   - public, active only
//     GET    /api/v1/artists/salon/payment-methods      - own, including retired
//     PUT    /api/v1/artists/salon/payment-methods      - add or change one
//     PATCH  /api/v1/artists/salon/payment-methods/:id  - retire or restore
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool) {
	handler := NewHandler(NewService(NewRepository(pool)))

	app.Get("/api/v1/salons/:salon_id/payment-methods", handler.ListPublic)

	g := app.Group("/api/v1/artists/salon",
		middleware.RequireAuth(), middleware.RequireRole("artist", "admin"))
	g.Get("/payment-methods", handler.List)
	g.Put("/payment-methods", handler.Upsert)
	g.Patch("/payment-methods/:id", handler.SetActive)
}

// ListPublic godoc
// @Summary      Where to send payment for this salon
// @Tags         payments
// @Produce      json
// @Param        salon_id path string true "Salon UUID"
// @Success      200 {object} response.Body{data=[]PublicPaymentMethodResponse}
// @Router       /salons/{salon_id}/payment-methods [get]
func (h *Handler) ListPublic(c *fiber.Ctx) error {
	salonID, err := uuid.Parse(c.Params("salon_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_SALON_ID", "Invalid salon ID")
	}

	// Short window, not the Catalogue one. A destination that has been retired
	// because an account was compromised must stop being served quickly, and
	// this is the response that tells someone where to send money.
	httpcache.Public(c, httpcache.Discovery)

	out, err := h.svc.ListPublic(c.UserContext(), salonID)
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// List godoc
// @Summary      List the calling artist's payment destinations
// @Tags         payments
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]PaymentMethodResponse}
// @Router       /artists/salon/payment-methods [get]
func (h *Handler) List(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}
	out, err := h.svc.ListForSalon(c.UserContext(), *salonID)
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// Upsert godoc
// @Summary      Add or change a payment destination
// @Tags         payments
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body UpsertPaymentMethodRequest true "Destination"
// @Success      200 {object} response.Body{data=PaymentMethodResponse}
// @Router       /artists/salon/payment-methods [put]
func (h *Handler) Upsert(c *fiber.Ctx) error {
	var req UpsertPaymentMethodRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}
	out, err := h.svc.Upsert(c.UserContext(), *salonID, req)
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// SetActiveRequest toggles a destination.
type SetActiveRequest struct {
	IsActive *bool `json:"is_active" validate:"required"`
}

// SetActive godoc
// @Summary      Retire or restore a payment destination
// @Tags         payments
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id   path string true "Payment method UUID"
// @Param        body body SetActiveRequest true "New state"
// @Success      200 {object} response.Body{data=PaymentMethodResponse}
// @Router       /artists/salon/payment-methods/{id} [patch]
func (h *Handler) SetActive(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		// Same answer as a destination belonging to another salon - a
		// malformed id and a foreign one must not be distinguishable.
		return errNotFound()
	}
	var req SetActiveRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	if req.IsActive == nil {
		return apperror.BadRequest("IS_ACTIVE_REQUIRED", "is_active is required")
	}
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}
	out, err := h.svc.SetActive(c.UserContext(), id, *salonID, *req.IsActive)
	if err != nil {
		return err
	}
	return response.OK(c, out)
}
