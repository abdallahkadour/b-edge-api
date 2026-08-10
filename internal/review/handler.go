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
	r.Patch("/:id/show", middleware.RequireRole("artist", "admin"), handler.ShowReview)

	// Guest review-link flow - deliberately OUTSIDE the RequireAuth() group.
	// A guest who booked never receives a JWT; the token in the URL is the
	// only credential these two routes accept, in place of a Bearer header.
	app.Get("/api/v1/reviews/by-token/:token", handler.GetBookingContextByToken)
	app.Post("/api/v1/reviews/by-token/:token", handler.CreateReviewByToken)
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

// GetBookingContextByToken godoc
// @Summary      Get booking summary for a review link (public, no auth)
// @Description  Resolves a review-link token to the booking's service,
// @Description  artist, store, time, and price - for rendering the
// @Description  confirmation card before the customer submits a review.
// @Tags         reviews
// @Produce      json
// @Param        token path string true "Review link token"
// @Success      200 {object} response.Body{data=ReviewBookingContext}
// @Router       /reviews/by-token/{token} [get]
func (h *Handler) GetBookingContextByToken(c *fiber.Ctx) error {
	ctxRow, err := h.svc.GetBookingContextByToken(c.Context(), c.Params("token"))
	if err != nil {
		return err
	}
	return response.OK(c, ctxRow)
}

// CreateReviewByToken godoc
// @Summary      Submit a review via a guest review link (public, no auth)
// @Description  The token in the URL is the only credential - there is no
// @Description  Bearer header, since a guest customer never receives a
// @Description  login session. Every existing review rule (must be
// @Description  completed, one review per booking) still applies.
// @Tags         reviews
// @Accept       json
// @Produce      json
// @Param        token path string true "Review link token"
// @Param        body body SubmitReviewByTokenRequest true "Review details"
// @Success      201 {object} response.Body{data=ReviewResponse}
// @Failure      404 {object} response.ErrorBody
// @Failure      409 {object} response.ErrorBody
// @Router       /reviews/by-token/{token} [post]
func (h *Handler) CreateReviewByToken(c *fiber.Ctx) error {
	var req SubmitReviewByTokenRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	review, err := h.svc.CreateReviewByToken(c.Context(), c.Params("token"), req)
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
// @Description  The artist (resolved from the JWT) may hide reviews on their own
// @Description  profile. Hiding drops the review from the cached rating average.
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

	requesterUserID := middleware.UserIDFromContext(c)

	if err := h.svc.HideReview(c.Context(), reviewID, requesterUserID); err != nil {
		return err
	}

	return response.NoContent(c)
}

// ShowReview godoc
// @Summary      Un-hide a previously hidden review (artist only)
// @Description  Restores a hidden review to the artist's public profile and back
// @Description  into the cached rating average.
// @Tags         reviews
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Review UUID"
// @Success      204
// @Failure      403 {object} response.ErrorBody
// @Router       /reviews/{id}/show [patch]
func (h *Handler) ShowReview(c *fiber.Ctx) error {
	reviewID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid review ID")
	}

	requesterUserID := middleware.UserIDFromContext(c)

	if err := h.svc.ShowReview(c.Context(), reviewID, requesterUserID); err != nil {
		return err
	}

	return response.NoContent(c)
}
