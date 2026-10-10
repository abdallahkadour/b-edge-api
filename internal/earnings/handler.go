// Package earnings implements the earnings domain for B-Edge,
// providing revenue aggregation and breakdown for the artist dashboard.
package earnings

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
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
//	GET /api/v1/earnings/salon    - the owner's whole-salon overview
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	e := app.Group("/api/v1/earnings", middleware.RequireAuth(), middleware.RequireRole("artist", "admin"))
	e.Get("/summary", handler.GetSummary)
	e.Get("/salon", middleware.RequireSalonCapability(salonrole.EarningsSalonRead), handler.GetSalonOverview)
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

	summary, err := h.svc.GetSummary(c.UserContext(), userID, req)
	if err != nil {
		return err
	}

	return response.OK(c, summary)
}

// GetSalonOverview godoc
// @Summary      The salon owner's overview of the whole salon
// @Description  Owner only (salon capability earnings:salon:read). For the period: earned
// @Description  (completed + no_show at booked price, the same rule as /earnings/summary,
// @Description  across every artist), deposits on those bookings, completed / no-show /
// @Description  cancelled counts and shop orders delivered; the same for the previous period
// @Description  (cut to the same point while the period is running); what is waiting now
// @Description  (approvals, deposits to check, refunds owed); and one row per artist,
// @Description  including an artist who has left but had bookings here in the period.
// @Description  B-Edge holds no money: these are recorded figures, not cash received.
// @Description  Default period is the current calendar month, Asia/Beirut.
// @Tags         earnings
// @Security     BearerAuth
// @Produce      json
// @Param        from  query  string  false  "Period start date YYYY-MM-DD (requires 'to')"
// @Param        to    query  string  false  "Period end date YYYY-MM-DD, inclusive (requires 'from')"
// @Success      200   {object}  response.Body{data=SalonOverviewResponse}
// @Failure      400   {object}  response.ErrorBody
// @Failure      403   {object}  response.ErrorBody  "NO_SALON or SALON_ROLE_FORBIDDEN (members)"
// @Router       /earnings/salon [get]
func (h *Handler) GetSalonOverview(c *fiber.Ctx) error {
	out, err := h.svc.GetSalonOverview(c.UserContext(),
		middleware.UserIDFromContext(c), *middleware.SalonIDFromContext(c),
		GetSummaryRequest{From: c.Query("from"), To: c.Query("to")})
	if err != nil {
		return err
	}
	return response.OK(c, out)
}
