// Package review implements the review domain for B-Edge.
package review

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

// Handler handles all HTTP requests for the review domain.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a new review Handler.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{
		svc: svc,
		// This attaches "module: review" to every log inside this file
		log: log.With(zap.String("module", "review")),
	}
}

// RegisterRoutes attaches all review routes to the Fiber app.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	r := app.Group("/api/v1/reviews", middleware.RequireAuth())

	r.Post("/", handler.CreateReview)
	r.Get("/artist/:artist_id", handler.GetReviewsByArtist)
	r.Delete("/:id", handler.DeleteReview)
	r.Patch("/:id/hide", middleware.RequireRole("artist", "admin"), handler.HideReview)
}

// CreateReview godoc
// @Summary      Submit a review for a completed booking
// @Tags         reviews
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body CreateReviewRequest true "Review details"
// @Success      201 {object} response.Body{data=ReviewResponse}
// @Failure      409 {object} response.ErrorBody
// @Router       /reviews [post]
func (h *Handler) CreateReview(c *fiber.Ctx) error {
	var req CreateReviewRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	customerID := middleware.UserIDFromContext(c)

	review, err := h.svc.CreateReview(c.Context(), req, customerID)
	if err != nil {
		return err
	}

	return response.Created(c, review)
}

// GetReviewsByArtist godoc
// @Summary      Get all visible reviews for an artist
// @Tags         reviews
// @Security     BearerAuth
// @Produce      json
// @Param        artist_id path string true "Artist UUID"
// @Success      200 {object} response.Body{data=[]ReviewResponse}
// @Router       /reviews/artist/{artist_id} [get]
func (h *Handler) GetReviewsByArtist(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("artist_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	reviews, err := h.svc.GetReviewsByArtist(c.Context(), artistID)
	if err != nil {
		return err
	}

	return response.OK(c, reviews)
}

// DeleteReview godoc
// @Summary      Delete a review
// @Tags         reviews
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Review UUID"
// @Success      204
// @Failure      403 {object} response.ErrorBody
// @Router       /reviews/{id} [delete]
func (h *Handler) DeleteReview(c *fiber.Ctx) error {
	reviewID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid review ID")
	}

	requesterID := middleware.UserIDFromContext(c)
	requesterRole := middleware.RoleFromContext(c)

	if err := h.svc.DeleteReview(c.Context(), reviewID, requesterID, requesterRole); err != nil {
		return err
	}

	return response.NoContent(c)
}

// HideReview godoc
// @Summary      Hide a review from public view (artist only)
// @Tags         reviews
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Review UUID"
// @Success      204
// @Failure      403 {object} response.ErrorBody
// @Router       /reviews/{id}/hide [patch]
func (h *Handler) HideReview(c *fiber.Ctx) error {
	reviewID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid review ID")
	}

	artistID := middleware.UserIDFromContext(c)

	if err := h.svc.HideReview(c.Context(), reviewID, artistID); err != nil {
		return err
	}

	return response.NoContent(c)
}
