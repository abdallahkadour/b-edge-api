package activity

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

// Handler serves the activity feed.
type Handler struct{ svc *Service }

// RegisterRoutes attaches the activity routes.
//
//	GET /api/v1/salon/activity - the salon's activity log (owner only)
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool) {
	h := &Handler{svc: NewService(NewRepository(pool))}
	app.Get("/api/v1/salon/activity", middleware.RequireAuth(), middleware.RequireRole("artist"),
		middleware.RequireSalonCapability(salonrole.ActivitySalonRead), h.List)
}

// List godoc
// @Summary      The salon's activity log
// @Description  Owner only (salon capability activity:salon:read). Who did what in the salon,
// @Description  newest first: booking approvals, deposits confirmed, refunds, cancellations,
// @Description  no-shows, moves; changes to prices, payment accounts, opening hours, discount
// @Description  codes and products; shop order movements; invitations, joins and departures.
// @Description  Each entry names the person, what it was about, and the values before and after.
// @Description  Append-only: nothing here can be edited or deleted.
// @Tags         salon
// @Security     BearerAuth
// @Produce      json
// @Param        kind    query  string  false  "bookings | payments | menu | shop | team"
// @Param        actor   query  string  false  "Only this person's changes (users.id)"
// @Param        cursor  query  string  false  "RFC3339 time of the last entry on the previous page"
// @Param        limit   query  int     false  "Page size, 1-100 (default 30)"
// @Success      200     {object}  response.Body{data=[]Entry}
// @Failure      400     {object}  response.ErrorBody  "INVALID_KIND or INVALID_ACTOR"
// @Failure      403     {object}  response.ErrorBody  "NO_SALON or SALON_ROLE_FORBIDDEN (members)"
// @Router       /salon/activity [get]
func (h *Handler) List(c *fiber.Ctx) error {
	cursor := time.Now().UTC()
	if raw := c.Query("cursor"); raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			cursor = t
		}
	}
	limit := 30
	if v := c.QueryInt("limit", 30); v > 0 && v <= 100 {
		limit = v
	}

	rows, hasMore, err := h.svc.List(c.UserContext(), middleware.UserIDFromContext(c),
		*middleware.SalonIDFromContext(c), c.Query("kind"), c.Query("actor"), cursor, limit)
	if err != nil {
		return err
	}
	var next string
	if hasMore && len(rows) > 0 {
		next = rows[len(rows)-1].At.Format(time.RFC3339Nano)
	}
	return response.List(c, rows, &response.Meta{NextCursor: next, HasMore: hasMore})
}
