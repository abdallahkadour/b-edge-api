package admin

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

type Handler struct {
	svc Service
	log *zap.Logger
}

func NewHandler(svc Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes attaches admin review routes to the Fiber app.
//
// Every route here requires role="admin" specifically - not the
// artistOnly-including-admin pattern used elsewhere. There is no
// "artist-or-admin" case for approving artists; this whole group is
// admin-only, full stop. Reaching an admin role in the first place
// requires being one of at most two seeded accounts (see
// cmd/seedadmin) - nothing in this codebase self-registers one.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	auditRepo := audit.NewRepository(pool)
	svc := NewService(repo, auditRepo)
	handler := NewHandler(svc, log)

	g := app.Group("/api/v1/admin", middleware.RequireAuth(), middleware.RequireRole("admin"))
	g.Get("/artists/pending", handler.ListPending)
	g.Post("/artists/:id/approve", handler.Approve)
	g.Post("/artists/:id/reject", handler.Reject)
}

// ListPending godoc
// @Summary      List artists awaiting review
// @Tags         admin
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]PendingArtist}
// @Router       /admin/artists/pending [get]
func (h *Handler) ListPending(c *fiber.Ctx) error {
	artists, err := h.svc.ListPending(c.Context())
	if err != nil {
		return err
	}
	return response.OK(c, artists)
}

// Approve godoc
// @Summary      Approve a pending artist - goes live on Discover immediately
// @Tags         admin
// @Security     BearerAuth
// @Param        id path string true "Artist UUID"
// @Success      204
// @Failure      409 {object} response.Body "not awaiting review"
// @Router       /admin/artists/{id}/approve [post]
func (h *Handler) Approve(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}
	adminID := middleware.UserIDFromContext(c)

	if err := h.svc.Approve(c.Context(), artistID, adminID, c.IP()); err != nil {
		return err
	}
	return response.NoContent(c)
}

// Reject godoc
// @Summary      Reject a pending artist
// @Tags         admin
// @Security     BearerAuth
// @Param        id path string true "Artist UUID"
// @Param        request body DecisionRequest false "Rejection reason"
// @Success      204
// @Failure      409 {object} response.Body "not awaiting review"
// @Router       /admin/artists/{id}/reject [post]
func (h *Handler) Reject(c *fiber.Ctx) error {
	artistID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid artist ID")
	}
	adminID := middleware.UserIDFromContext(c)

	var req DecisionRequest
	if err := c.BodyParser(&req); err != nil {
		// A rejection reason is optional - an empty/absent body is fine,
		// only a genuinely malformed one is an error.
		if len(c.Body()) > 0 {
			return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
		}
	}

	if err := h.svc.Reject(c.Context(), artistID, adminID, req, c.IP()); err != nil {
		return err
	}
	return response.NoContent(c)
}
