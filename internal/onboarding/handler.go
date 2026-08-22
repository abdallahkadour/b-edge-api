// Package onboarding implements the self-service artist signup flow.
package onboarding

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

type Handler struct {
	svc *Service
	log *zap.Logger
}

func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes attaches onboarding routes to the Fiber app.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	auth := middleware.RequireAuth()
	// Deliberately "artist" only, not the artistOnly-including-admin
	// pattern used elsewhere in this codebase. Onboarding creates a
	// profile tied to the CALLER's own user_id - an admin submitting it
	// would create an artist profile for the admin's own account, which
	// makes no sense. An admin's job here is reviewing what an artist
	// submitted, not submitting on their behalf.
	artistOnly := middleware.RequireRole("artist")

	g := app.Group("/api/v1/onboarding", auth, artistOnly)
	g.Post("/complete", handler.Complete)
	g.Get("/status", handler.GetStatus)
}

// Complete godoc
// @Summary      Submit the self-service onboarding form
// @Description  Creates a salon, artist profile (pending review), one
// @Description  store, and one service in a single transaction. Not
// @Description  shown on Discover and not bookable until an admin
// @Description  approves it.
// @Tags         onboarding
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request body CompleteOnboardingRequest true "Onboarding submission"
// @Success      201 {object} response.Body{data=CompleteOnboardingResponse}
// @Failure      409 {object} response.Body "already onboarded, or handle taken"
// @Failure      422 {object} response.Body
// @Router       /onboarding/complete [post]
func (h *Handler) Complete(c *fiber.Ctx) error {
	var req CompleteOnboardingRequest
	if err := c.BodyParser(&req); err != nil {
		return err
	}

	userID := middleware.UserIDFromContext(c)

	result, err := h.svc.Complete(c.Context(), userID, req)
	if err != nil {
		return err
	}

	return response.Created(c, result)
}

// GetStatus godoc
// @Summary      Get this artist's onboarding status
// @Tags         onboarding
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=OnboardingStatus}
// @Failure      404 {object} response.Body "onboarding not started"
// @Router       /onboarding/status [get]
func (h *Handler) GetStatus(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	status, err := h.svc.GetStatus(c.Context(), userID)
	if err != nil {
		return err
	}

	return response.OK(c, status)
}
