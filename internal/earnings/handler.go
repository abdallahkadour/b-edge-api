// Package earnings implements the earnings domain for B-Edge,
// providing revenue aggregation and breakdown for the artist dashboard.
package earnings

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

// Handler handles all HTTP requests for the earnings domain.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a new earnings Handler.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{
		svc: svc,
		log: log.With(zap.String("module", "earnings")),
	}
}

// RegisterRoutes attaches all earnings routes to the Fiber app.
//
// Protected routes (artist Bearer):
//
//	GET /api/v1/earnings/summary  - earnings summary + breakdown
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	e := app.Group("/api/v1/earnings", middleware.RequireAuth(), middleware.RequireRole("artist", "admin"))
	e.Get("/summary", handler.GetSummary)
}

// GetSummary godoc
// @Summary      Get earnings summary for the authenticated artist
// @Description  Returns total revenue, today/week/month stats, 7-day daily breakdown,
// @Description  and per-service breakdown. Revenue includes completed + no_show bookings
// @Description  (deposit is kept on no-show). Default period is the current calendar month.
// @Tags         earnings
// @Security     BearerAuth
// @Produce      json
// @Param        from  query  string  false  "Period start date YYYY-MM-DD (requires 'to')"
// @Param        to    query  string  false  "Period end date YYYY-MM-DD (requires 'from')"
// @Success      200   {object}  response.Body{data=EarningsSummaryResponse}
// @Failure      400   {object}  response.ErrorBody
// @Failure      404   {object}  response.ErrorBody  "ARTIST_NOT_FOUND"
// @Router       /earnings/summary [get]
func (h *Handler) GetSummary(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	req := GetSummaryRequest{
		From: c.Query("from"),
		To:   c.Query("to"),
	}

	summary, err := h.svc.GetSummary(c.Context(), userID, req)
	if err != nil {
		return err
	}

	return response.OK(c, summary)
}
