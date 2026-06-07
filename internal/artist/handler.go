// Package artist implements the artist domain for B-Edge.
package artist

import (
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
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes attaches all artist routes to the Fiber app.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	// Public routes — no auth required
	app.Get("/api/v1/artists/:id", handler.GetArtistByID)

	// Protected routes
	a := app.Group("/api/v1/artists", middleware.RequireAuth())

	// Profile
	a.Get("/me", handler.GetMyProfile)
	a.Patch("/:id", handler.UpdateProfile)

	// Stores
	a.Get("/:id/stores", handler.GetStoresByArtist)
	a.Get("/salon/stores", middleware.RequireRole("artist", "admin"), handler.GetStoresBySalon)

	// Services
	a.Get("/salon/services", middleware.RequireRole("artist", "admin"), handler.GetServicesBySalon)
	a.Post("/salon/services", middleware.RequireRole("artist", "admin"), handler.CreateService)
	a.Patch("/salon/services/:service_id", middleware.RequireRole("artist", "admin"), handler.UpdateService)
	a.Delete("/salon/services/:service_id", middleware.RequireRole("artist", "admin"), handler.DeleteService)

	// Business hours
	a.Get("/stores/:store_id/hours", middleware.RequireRole("artist", "admin"), handler.GetBusinessHours)
	a.Post("/stores/:store_id/hours", middleware.RequireRole("artist", "admin"), handler.SetBusinessHours)
	a.Get("/stores/:store_id/exceptions", middleware.RequireRole("artist", "admin"), handler.GetExceptions)
	a.Post("/stores/:store_id/exceptions", middleware.RequireRole("artist", "admin"), handler.CreateException)
	a.Delete("/stores/:store_id/exceptions/:date", middleware.RequireRole("artist", "admin"), handler.DeleteException)
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
	artistID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	artist, err := h.svc.GetArtistByID(c.Context(), artistID)
	if err != nil {
		return err
	}

	return response.OK(c, artist)
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
	artistID, err := uuid.Parse(c.Params("id"))
	if err != nil {
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
// @Summary      Get all services for the salon
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

	hours, err := h.svc.GetBusinessHours(c.Context(), storeID)
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

	if err := h.svc.SetBusinessHours(c.Context(), storeID, req); err != nil {
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

	exceptions, err := h.svc.GetExceptions(c.Context(), storeID)
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

	if err := h.svc.CreateException(c.Context(), storeID, req); err != nil {
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

	if err := h.svc.DeleteException(c.Context(), storeID, date); err != nil {
		return err
	}

	return response.NoContent(c)
}
