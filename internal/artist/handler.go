// Package artist implements the artist domain for B-Edge.
package artist

import (
	"errors"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

// Handler handles all HTTP requests for the artist domain.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a new artist Handler.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log.With(zap.String("module", "artist"))}
}

// RegisterRoutes attaches all artist routes to the Fiber app.
//
// Two ordering rules govern this function, and they interact:
//
//  1. Fiber matches routes in registration order, so every literal path
//     ("/me", "/salon/...", "/stores/...") must be registered before any
//     "/:id" route, or Fiber parses the literal as an artist UUID.
//
//  2. Middleware attached to a prefix group - app.Group(prefix, mw) - applies
//     to the WHOLE prefix from the moment the group is created, regardless of
//     where later routes are registered. A public route under that same prefix
//     therefore still runs the group's auth middleware and 401s. This is why
//     auth is attached per-route below rather than via a group: the guest
//     booking funnel reads /:id and /:id/services with no JWT.
//
// Public routes (no auth) - the customer PWA guest funnel:
//
//	GET /api/v1/artists/:id          - public artist profile
//	GET /api/v1/artists/:id/services - active services for an artist
//
// Protected routes (RequireAuth):
//
//	GET    /api/v1/artists/me                          - own profile
//	PATCH  /api/v1/artists/:id                         - update own profile
//	GET    /api/v1/artists/:id/stores                  - stores for an artist
//	GET    /api/v1/artists/salon/stores                - stores for own salon
//	POST   /api/v1/artists/salon/stores                - add a second location, self-assigned
//	GET    /api/v1/artists/salon/services              - services for own salon
//	POST   /api/v1/artists/salon/services              - add service
//	PATCH  /api/v1/artists/salon/services/:service_id  - update service
//	DELETE /api/v1/artists/salon/services/:service_id  - deactivate service
//	... (business hours routes)
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	auth := middleware.RequireAuth()
	artistOnly := middleware.RequireRole("artist", "admin")

	const base = "/api/v1/artists"

	// ── Literal paths - must precede every /:id route ────────────────────────

	// Profile
	app.Get(base+"/me", auth, handler.GetMyProfile)

	// Stores (own salon)
	app.Get(base+"/salon/stores", auth, artistOnly, handler.GetStoresBySalon)
	app.Post(base+"/salon/stores", auth, artistOnly, handler.CreateStore)

	// Services (artist dashboard - own salon)
	app.Get(base+"/salon/services", auth, artistOnly, handler.GetServicesBySalon)
	app.Post(base+"/salon/services", auth, artistOnly, handler.CreateService)
	app.Patch(base+"/salon/services/:service_id", auth, artistOnly, handler.UpdateService)
	app.Delete(base+"/salon/services/:service_id", auth, artistOnly, handler.DeleteService)

	// Business hours
	app.Get(base+"/stores/:store_id/hours", auth, artistOnly, handler.GetBusinessHours)
	app.Post(base+"/stores/:store_id/hours", auth, artistOnly, handler.SetBusinessHours)
	app.Get(base+"/stores/:store_id/exceptions", auth, artistOnly, handler.GetExceptions)
	app.Post(base+"/stores/:store_id/exceptions", auth, artistOnly, handler.CreateException)
	app.Delete(base+"/stores/:store_id/exceptions/:date", auth, artistOnly, handler.DeleteException)

	// ── Public parametric - no JWT, read by the guest booking funnel ─────────
	app.Patch(base+"/stores/:store_id", auth, artistOnly, handler.UpdateStore)
	app.Get(base+"/:id/services", handler.GetPublicServicesByArtist)
	app.Get(base+"/:id/stores", handler.GetStoresByArtist)
	app.Get(base+"/:id", handler.GetArtistByID)

	// ── Protected parametric ─────────────────────────────────────────────────

	// artistOnly is defence in depth. UpdateProfile already verifies
	// profile.UserID == the caller's user_id, but that check alone is
	// satisfied by ANY token carrying that user_id - including a
	// customer-role token minted by the OTP flow. Requiring the artist role
	// as well means a customer session can never reach this route at all.
	app.Patch(base+"/:id", auth, artistOnly, handler.UpdateProfile)
}

// CreateStore godoc
// @Summary      Add a second physical location to the artist's own salon
// @Description  Creates a new store under the caller's salon and assigns
// @Description  the caller to work there immediately. There is no separate
// @Description  staff-assignment flow yet - the artist creating a location
// @Description  is always the one working at it.
// @Tags         artists
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body CreateStoreRequest true "New store"
// @Success      201 {object} response.Body{data=Store}
// @Failure      403 {object} response.ErrorBody "NO_SALON"
// @Router       /artists/salon/stores [post]
func (h *Handler) CreateStore(c *fiber.Ctx) error {
	var req CreateStoreRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}
	userID := middleware.UserIDFromContext(c)

	store, err := h.svc.CreateStore(c.Context(), userID, *salonID, req)
	if err != nil {
		return err
	}

	return response.Created(c, store)
}

// UpdateStore godoc
// @Summary      Update a store's settings (artist only)
// @Description  Partial update. Omitted fields are left unchanged. Send an
// @Description  empty string for early_bird_cutoff to clear it.
// @Tags         artists
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        store_id path string true "Store UUID"
// @Param        body body UpdateStoreRequest true "Fields to update"
// @Success      200 {object} response.Body{data=Store}
// @Failure      403 {object} response.ErrorBody
// @Failure      404 {object} response.ErrorBody
// @Router       /artists/stores/{store_id} [patch]
func (h *Handler) UpdateStore(c *fiber.Ctx) error {
	storeID, err := uuid.Parse(c.Params("store_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid store ID")
	}

	var req UpdateStoreRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	store, err := h.svc.UpdateStore(c.Context(), storeID, *salonID, req)
	if err != nil {
		return err
	}

	return response.OK(c, store)
}

// GetArtistByID godoc
// @Summary      Get artist public profile
// @Tags         artists
// @Produce      json
// @Param        id path string true "Artist UUID"
// @Success      200 {object} response.Body{data=ArtistResponse}
// @Failure      404 {object} response.ErrorBody
// @Router       /artists/{id} [get]
func (h *Handler) GetArtistByID(c *fiber.Ctx) error {
	// The :id param accepts either a real UUID or a public handle (e.g.
	// "rania") - ResolveArtistID tries UUID first, falls back to a handle
	// lookup. This keeps every existing UUID-based link working exactly as
	// before while letting new links use the shorter, human-readable form.
	artistID, err := h.svc.ResolveArtistID(c.Context(), c.Params("id"))
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return apperror.NotFound("ARTIST_NOT_FOUND", "Artist not found")
		}
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	artist, err := h.svc.GetArtistByID(c.Context(), artistID)
	if err != nil {
		return err
	}

	return response.OK(c, artist)
}

// GetPublicServicesByArtist godoc
// @Summary      Get active services for an artist (public)
// @Description  Returns all active services for the artist's salon.
//
//	Used by the customer PWA to display available services.
//	No authentication required.
//
// @Tags         artists
// @Produce      json
// @Param        id path string true "Artist UUID"
// @Success      200 {object} response.Body{data=[]ServiceResponse}
// @Failure      404 {object} response.ErrorBody
// @Router       /artists/{id}/services [get]
func (h *Handler) GetPublicServicesByArtist(c *fiber.Ctx) error {
	artistID, err := h.svc.ResolveArtistID(c.Context(), c.Params("id"))
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return apperror.NotFound("ARTIST_NOT_FOUND", "Artist not found")
		}
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	services, err := h.svc.GetPublicServicesByArtist(c.Context(), artistID)
	if err != nil {
		return err
	}

	return response.OK(c, services)
}

// GetMyProfile godoc
// @Summary      Get authenticated artist's own profile
// @Tags         artists
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=ArtistProfile}
// @Failure      404 {object} response.ErrorBody
// @Router       /artists/me [get]
func (h *Handler) GetMyProfile(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	profile, err := h.svc.GetMyProfile(c.Context(), userID)
	if err != nil {
		return err
	}

	return response.OK(c, profile)
}

// UpdateProfile godoc
// @Summary      Update artist profile
// @Tags         artists
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id   path string true "Artist UUID"
// @Param        body body UpdateProfileRequest true "Profile fields to update"
// @Success      200 {object} response.Body{data=ArtistResponse}
// @Failure      403 {object} response.ErrorBody
// @Router       /artists/{id} [patch]
func (h *Handler) UpdateProfile(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	var req UpdateProfileRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	userID := middleware.UserIDFromContext(c)

	artist, err := h.svc.UpdateProfile(c.Context(), artistID, userID, req)
	if err != nil {
		return err
	}

	return response.OK(c, artist)
}

// GetStoresByArtist godoc
// @Summary      Get stores for an artist
// @Tags         artists
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Artist UUID"
// @Success      200 {object} response.Body{data=[]Store}
// @Router       /artists/{id}/stores [get]
func (h *Handler) GetStoresByArtist(c *fiber.Ctx) error {
	artistID, err := h.svc.ResolveArtistID(c.Context(), c.Params("id"))
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return apperror.NotFound("ARTIST_NOT_FOUND", "Artist not found")
		}
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	stores, err := h.svc.GetStoresByArtist(c.Context(), artistID)
	if err != nil {
		return err
	}

	return response.OK(c, stores)
}

// GetStoresBySalon godoc
// @Summary      Get all stores for the authenticated artist's salon
// @Tags         artists
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]Store}
// @Router       /artists/salon/stores [get]
func (h *Handler) GetStoresBySalon(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	stores, err := h.svc.GetStoresBySalon(c.Context(), *salonID)
	if err != nil {
		return err
	}

	return response.OK(c, stores)
}

// GetServicesBySalon godoc
// @Summary      Get all services for the authenticated artist's salon (dashboard)
// @Tags         artists
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]ServiceResponse}
// @Router       /artists/salon/services [get]
func (h *Handler) GetServicesBySalon(c *fiber.Ctx) error {
	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	services, err := h.svc.GetServicesBySalon(c.Context(), *salonID)
	if err != nil {
		return err
	}

	return response.OK(c, services)
}

// CreateService godoc
// @Summary      Add a new service to the salon catalogue
// @Tags         artists
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body CreateServiceRequest true "Service details"
// @Success      201 {object} response.Body{data=ServiceResponse}
// @Router       /artists/salon/services [post]
func (h *Handler) CreateService(c *fiber.Ctx) error {
	var req CreateServiceRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	svc, err := h.svc.CreateService(c.Context(), *salonID, req)
	if err != nil {
		return err
	}

	return response.Created(c, svc)
}

// UpdateService godoc
// @Summary      Update a service
// @Tags         artists
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        service_id path string true "Service UUID"
// @Param        body body UpdateServiceRequest true "Fields to update"
// @Success      200 {object} response.Body{data=ServiceResponse}
// @Router       /artists/salon/services/{service_id} [patch]
func (h *Handler) UpdateService(c *fiber.Ctx) error {
	serviceID, err := uuid.Parse(c.Params("service_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid service ID")
	}

	var req UpdateServiceRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	svc, err := h.svc.UpdateService(c.Context(), serviceID, *salonID, req)
	if err != nil {
		return err
	}

	return response.OK(c, svc)
}

// DeleteService godoc
// @Summary      Deactivate a service
// @Tags         artists
// @Security     BearerAuth
// @Produce      json
// @Param        service_id path string true "Service UUID"
// @Success      204
// @Router       /artists/salon/services/{service_id} [delete]
func (h *Handler) DeleteService(c *fiber.Ctx) error {
	serviceID, err := uuid.Parse(c.Params("service_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid service ID")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	if err := h.svc.DeleteService(c.Context(), serviceID, *salonID); err != nil {
		return err
	}

	return response.NoContent(c)
}

// GetBusinessHours godoc
// @Summary      Get business hours for a store
// @Tags         artists
// @Security     BearerAuth
// @Produce      json
// @Param        store_id path string true "Store UUID"
// @Success      200 {object} response.Body{data=[]BusinessHours}
// @Router       /artists/stores/{store_id}/hours [get]
func (h *Handler) GetBusinessHours(c *fiber.Ctx) error {
	storeID, err := uuid.Parse(c.Params("store_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid store ID")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	hours, err := h.svc.GetBusinessHours(c.Context(), storeID, *salonID)
	if err != nil {
		return err
	}

	return response.OK(c, hours)
}

// SetBusinessHours godoc
// @Summary      Set business hours for a store on a specific day
// @Tags         artists
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        store_id path string true "Store UUID"
// @Param        body body SetBusinessHoursRequest true "Hours configuration"
// @Success      204
// @Router       /artists/stores/{store_id}/hours [post]
func (h *Handler) SetBusinessHours(c *fiber.Ctx) error {
	storeID, err := uuid.Parse(c.Params("store_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid store ID")
	}

	var req SetBusinessHoursRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	if err := h.svc.SetBusinessHours(c.Context(), storeID, *salonID, req); err != nil {
		return err
	}

	return response.NoContent(c)
}

// GetExceptions godoc
// @Summary      Get business hours exceptions for a store
// @Tags         artists
// @Security     BearerAuth
// @Produce      json
// @Param        store_id path string true "Store UUID"
// @Success      200 {object} response.Body{data=[]BusinessHoursException}
// @Router       /artists/stores/{store_id}/exceptions [get]
func (h *Handler) GetExceptions(c *fiber.Ctx) error {
	storeID, err := uuid.Parse(c.Params("store_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid store ID")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	exceptions, err := h.svc.GetExceptions(c.Context(), storeID, *salonID)
	if err != nil {
		return err
	}

	return response.OK(c, exceptions)
}

// CreateException godoc
// @Summary      Add a holiday or special-hours day
// @Tags         artists
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        store_id path string true "Store UUID"
// @Param        body body CreateExceptionRequest true "Exception details"
// @Success      204
// @Router       /artists/stores/{store_id}/exceptions [post]
func (h *Handler) CreateException(c *fiber.Ctx) error {
	storeID, err := uuid.Parse(c.Params("store_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid store ID")
	}

	var req CreateExceptionRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	if err := h.svc.CreateException(c.Context(), storeID, *salonID, req); err != nil {
		return err
	}

	return response.NoContent(c)
}

// DeleteException godoc
// @Summary      Remove a business hours exception
// @Tags         artists
// @Security     BearerAuth
// @Produce      json
// @Param        store_id path string true "Store UUID"
// @Param        date     path string true "Date in YYYY-MM-DD format"
// @Success      204
// @Router       /artists/stores/{store_id}/exceptions/{date} [delete]
func (h *Handler) DeleteException(c *fiber.Ctx) error {
	storeID, err := uuid.Parse(c.Params("store_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid store ID")
	}

	date := c.Params("date")
	if date == "" {
		return apperror.BadRequest("INVALID_DATE", "Date is required")
	}

	salonID := middleware.SalonIDFromContext(c)
	if salonID == nil {
		return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}

	if err := h.svc.DeleteException(c.Context(), storeID, *salonID, date); err != nil {
		return err
	}

	return response.NoContent(c)
}
