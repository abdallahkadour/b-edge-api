package inbox

import (
	"strconv"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

// Handler serves the notification centre.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates an inbox Handler.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes mounts the notification centre.
//
// Authenticated but NOT role-restricted: the feed is keyed to the caller's
// own user_id, so an artist, an admin or a signed-in customer each simply
// see their own. Adding RequireRole("artist") would have to be undone the
// first time a customer needs one.
//
// Deliberately NOT behind RequireActiveSubscription: an artist whose
// account is suspended still needs to read the notification telling them
// why, and how to fix it.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	h := NewHandler(NewService(NewRepository(pool)), log)

	g := app.Group("/api/v1/notifications", middleware.RequireAuth())
	g.Get("/", h.GetFeed)
	// Registered before /:id would matter only on a collision; "unread-count"
	// is a literal segment and the others are all UUID-shaped, but keeping
	// the specific route first matches the ordering convention used in
	// internal/media after a real route-shadowing bug there.
	g.Get("/unread-count", h.GetUnreadCount)
	g.Post("/read-all", h.MarkAllRead)
	g.Patch("/:id/read", h.MarkRead)
	g.Patch("/:id/archive", h.Archive)
}

// GetFeed godoc
// @Summary      List the caller's notifications
// @Description  Newest first, excluding archived. Returns the unread count alongside so the badge and the panel cannot disagree.
// @Tags         notifications
// @Security     BearerAuth
// @Produce      json
// @Param        unread query bool false "Only unread"
// @Param        limit  query int  false "Max 50, default 20"
// @Success      200 {object} response.Body{data=FeedResponse}
// @Router       /notifications [get]
func (h *Handler) GetFeed(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)
	unreadOnly := c.Query("unread") == "true"
	limit, _ := strconv.Atoi(c.Query("limit"))

	feed, err := h.svc.GetFeed(c.Context(), userID, unreadOnly, limit)
	if err != nil {
		return err
	}
	return response.OK(c, feed)
}

// GetUnreadCount godoc
// @Summary      Unread notification count
// @Description  Cheap endpoint for polling the bell badge.
// @Tags         notifications
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=UnreadCountResponse}
// @Router       /notifications/unread-count [get]
func (h *Handler) GetUnreadCount(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)
	count, err := h.svc.GetUnreadCount(c.Context(), userID)
	if err != nil {
		return err
	}
	return response.OK(c, count)
}

// MarkRead godoc
// @Summary      Mark one notification read
// @Tags         notifications
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Notification ID"
// @Success      204
// @Failure      404 {object} response.ErrorBody "NOTIFICATION_NOT_FOUND"
// @Router       /notifications/{id}/read [patch]
func (h *Handler) MarkRead(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid notification ID")
	}
	if err := h.svc.MarkRead(c.Context(), middleware.UserIDFromContext(c), id); err != nil {
		return err
	}
	return response.NoContent(c)
}

// MarkAllRead godoc
// @Summary      Mark every notification read
// @Tags         notifications
// @Security     BearerAuth
// @Produce      json
// @Success      204
// @Router       /notifications/read-all [post]
func (h *Handler) MarkAllRead(c *fiber.Ctx) error {
	if err := h.svc.MarkAllRead(c.Context(), middleware.UserIDFromContext(c)); err != nil {
		return err
	}
	return response.NoContent(c)
}

// Archive godoc
// @Summary      Dismiss a notification from the feed
// @Tags         notifications
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Notification ID"
// @Success      204
// @Failure      404 {object} response.ErrorBody "NOTIFICATION_NOT_FOUND"
// @Router       /notifications/{id}/archive [patch]
func (h *Handler) Archive(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid notification ID")
	}
	if err := h.svc.Archive(c.Context(), middleware.UserIDFromContext(c), id); err != nil {
		return err
	}
	return response.NoContent(c)
}
