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
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
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

	// Auth is applied per-route below, not via app.Group("/api/v1/reviews",
	// RequireAuth()). A Group's middleware is registered as an app-wide
	// Use() bound to its prefix, and Fiber freezes each route's middleware
	// chain against whatever's in that global stack at the moment the route
	// is registered - a route registered afterward under the SAME prefix,
	// even directly on `app` and even with a comment saying "deliberately
	// outside the group," still inherits it. That silently 401'd the guest
	// review-by-token routes below (they share the /api/v1/reviews prefix)
	// until this was found and fixed; see CLAUDE-v6.md and the identical
	// bug this exact pattern caused in product/handler.go.
	r := app.Group("/api/v1/reviews")
	auth := middleware.RequireAuth()

	r.Post("/", auth, handler.CreateReview)
	r.Get("/artist/:artist_id", auth, handler.GetReviewsByArtist)
	r.Delete("/:id", auth, handler.DeleteReview)
	r.Patch("/:id/hide", auth, middleware.RequireRole("artist", "admin"), handler.HideReview)
	r.Patch("/:id/show", auth, middleware.RequireRole("artist", "admin"), handler.ShowReview)

	// Guest review-link flow — no auth. A guest who booked never receives a
	// JWT; the token in the URL is the only credential these two routes
	// accept, in place of a Bearer header.
	r.Get("/by-token/:token", handler.GetBookingContextByToken)
	r.Post("/by-token/:token", handler.CreateReviewByToken)

	// Public review list - deliberately a DIFFERENT path
	// (/public/reviews/... not /reviews/...) rather than moving the
	// existing /reviews/artist/:id route. The old authed one stays exactly
	// as it was in case anything already depends on its exact shape;
	// this is a genuinely new, additive endpoint, not a behavior change
	// to an existing one.
	app.Get("/api/v1/public/reviews/artist/:artist_id", handler.GetPublicReviewsByArtist)
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
		return validation.MapBodyError(err)
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
// @Description  artist, store, time, and price — for rendering the
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
// @Description  The token in the URL is the only credential — there is no
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
		return validation.MapBodyError(err)
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

	requesterUserID := middleware.UserIDFromContext(c)

	reviews, err := h.svc.GetReviewsByArtist(c.Context(), artistID, requesterUserID)
	if err != nil {
		return err
	}

	return response.OK(c, reviews)
}

// GetPublicReviewsByArtist godoc
// @Summary      Get an artist's visible reviews (public, no account needed)
// @Description  A prospective customer deciding whether to book has no
// @Description  reason to be logged in yet - reviews must be readable
// @Description  before that decision, not after.
// @Tags         reviews
// @Produce      json
// @Param        artist_id path string true "Artist UUID"
// @Success      200 {object} response.Body{data=[]EnrichedReviewResponse}
// @Router       /public/reviews/artist/{artist_id} [get]
func (h *Handler) GetPublicReviewsByArtist(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("artist_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}

	reviews, err := h.svc.GetPublicReviewsByArtist(c.Context(), artistID)
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
