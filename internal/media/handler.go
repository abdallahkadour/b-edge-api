// Package media implements the media/portfolio domain for B-Edge,
// providing portfolio photo management for artist profiles.
package media

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

// Handler handles all HTTP requests for the media domain.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a new media Handler.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{
		svc: svc,
		log: log.With(zap.String("module", "media")),
	}
}

// RegisterRoutes attaches all media routes to the Fiber app.
//
// Public routes (no auth):
//
//	GET  /api/v1/media/portfolio/:artist_id  — public portfolio for a given artist
//
// Protected routes (artist Bearer):
//
//	GET    /api/v1/media/my                  — own portfolio
//	POST   /api/v1/media                     — upload a photo
//	DELETE /api/v1/media/:id                 — delete a photo
//	PATCH  /api/v1/media/:id/cover           — set cover photo
//	PATCH  /api/v1/media/reorder             — reorder all photos
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	// ── Public routes ─────────────────────────────────────────────────────────
	app.Get("/api/v1/media/portfolio/:artist_id", handler.GetPortfolio)

	// ── Protected routes ──────────────────────────────────────────────────────
	m := app.Group("/api/v1/media", middleware.RequireAuth(), middleware.RequireRole("artist", "admin"))
	m.Get("/my", handler.GetMyPortfolio)
	m.Post("/", handler.AddPhoto)
	m.Patch("/reorder", handler.Reorder) // must be before /:id to avoid route conflict
	m.Delete("/:id", handler.DeletePhoto)
	m.Patch("/:id/cover", handler.SetCover)
}

// GetPortfolio godoc
// @Summary      Get public portfolio for an artist
// @Description  Returns all photos for the artist ordered by display_order.
// @Description  No authentication required — used by the customer discovery screen.
// @Tags         media
// @Produce      json
// @Param        artist_id path string true "Artist UUID"
// @Success      200 {object} response.Body{data=PortfolioResponse}
// @Failure      400 {object} response.ErrorBody
// @Router       /media/portfolio/{artist_id} [get]
func (h *Handler) GetPortfolio(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("artist_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	portfolio, err := h.svc.GetPortfolio(c.Context(), artistID)
	if err != nil {
		return err
	}

	return response.OK(c, portfolio)
}

// GetMyPortfolio godoc
// @Summary      Get the authenticated artist's own portfolio
// @Tags         media
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=PortfolioResponse}
// @Failure      404 {object} response.ErrorBody "ARTIST_NOT_FOUND"
// @Router       /media/my [get]
func (h *Handler) GetMyPortfolio(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	portfolio, err := h.svc.GetMyPortfolio(c.Context(), userID)
	if err != nil {
		return err
	}

	return response.OK(c, portfolio)
}

// AddPhoto godoc
// @Summary      Add a photo to the artist's portfolio
// @Description  Appends a new photo at the end of the portfolio.
// @Description  Returns PORTFOLIO_FULL (409) if the artist already has 20 photos.
// @Description  The URL should be a publicly accessible Cloudinary URL.
// @Tags         media
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body AddMediaRequest true "Photo URL and optional Cloudinary ID"
// @Success      201 {object} response.Body{data=MediaResponse}
// @Failure      400 {object} response.ErrorBody
// @Failure      409 {object} response.ErrorBody "PORTFOLIO_FULL"
// @Router       /media [post]
func (h *Handler) AddPhoto(c *fiber.Ctx) error {
	var req AddMediaRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	userID := middleware.UserIDFromContext(c)

	photo, err := h.svc.AddPhoto(c.Context(), userID, req)
	if err != nil {
		return err
	}

	return response.Created(c, photo)
}

// DeletePhoto godoc
// @Summary      Delete a photo from the portfolio
// @Description  Permanently removes the photo. Only the owning artist can delete.
// @Tags         media
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Media UUID"
// @Success      204
// @Failure      403 {object} response.ErrorBody "FORBIDDEN"
// @Failure      404 {object} response.ErrorBody "MEDIA_NOT_FOUND"
// @Router       /media/{id} [delete]
func (h *Handler) DeletePhoto(c *fiber.Ctx) error {
	mediaID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid media ID")
	}

	userID := middleware.UserIDFromContext(c)

	if err := h.svc.DeletePhoto(c.Context(), userID, mediaID); err != nil {
		return err
	}

	return response.NoContent(c)
}

// SetCover godoc
// @Summary      Set a photo as the cover (first in portfolio)
// @Description  Moves the photo to display_order=0 and shifts all others up by 1.
// @Description  Only the owning artist can set the cover.
// @Tags         media
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Media UUID"
// @Success      204
// @Failure      403 {object} response.ErrorBody "FORBIDDEN"
// @Failure      404 {object} response.ErrorBody "MEDIA_NOT_FOUND"
// @Router       /media/{id}/cover [patch]
func (h *Handler) SetCover(c *fiber.Ctx) error {
	mediaID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid media ID")
	}

	userID := middleware.UserIDFromContext(c)

	if err := h.svc.SetCover(c.Context(), userID, mediaID); err != nil {
		return err
	}

	return response.NoContent(c)
}

// Reorder godoc
// @Summary      Reorder all photos in the portfolio
// @Description  Accepts the full ordered list of media IDs. Must include all photos.
// @Tags         media
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body ReorderRequest true "Ordered list of all media IDs"
// @Success      204
// @Failure      400 {object} response.ErrorBody "INVALID_REORDER"
// @Router       /media/reorder [patch]
func (h *Handler) Reorder(c *fiber.Ctx) error {
	var req ReorderRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	userID := middleware.UserIDFromContext(c)

	if err := h.svc.Reorder(c.Context(), userID, req); err != nil {
		return err
	}

	return response.NoContent(c)
}
