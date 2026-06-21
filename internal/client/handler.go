// Package client implements the artist-facing CRM for B-Edge.
package client

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

// Handler handles all HTTP requests for the client CRM domain.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a new client Handler.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log.With(zap.String("module", "client"))}
}

// RegisterRoutes attaches all client CRM routes to the Fiber app.
//
// All routes require an authenticated artist:
//
//	GET /api/v1/clients                       — list the artist's clients
//	GET /api/v1/clients/:customer_id          — one client's profile + history
//	PUT /api/v1/clients/:customer_id/notes    — upsert the private note
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	c := app.Group("/api/v1/clients", middleware.RequireAuth(), middleware.RequireRole("artist", "admin"))

	c.Get("/", handler.ListClients)
	c.Get("/:customer_id", handler.GetClient)
	c.Put("/:customer_id/notes", handler.UpsertNote)
}

// ListClients godoc
// @Summary      List the authenticated artist's clients
// @Description  Customers with at least one completed booking with the artist,
// @Description  aggregated with bookings count, total spent, last visit, and the
// @Description  customer's average rating of this artist. Optional ?q= searches
// @Description  name or service.
// @Tags         clients
// @Security     BearerAuth
// @Produce      json
// @Param        q query string false "Search by client name or service"
// @Success      200 {object} response.Body{data=[]ClientCard}
// @Router       /clients [get]
func (h *Handler) ListClients(c *fiber.Ctx) error {
	requesterUserID := middleware.UserIDFromContext(c)
	q := c.Query("q")

	clients, err := h.svc.ListClients(c.Context(), requesterUserID, q)
	if err != nil {
		return err
	}

	return response.OK(c, clients)
}

// GetClient godoc
// @Summary      Get one client's profile and history
// @Tags         clients
// @Security     BearerAuth
// @Produce      json
// @Param        customer_id path string true "Customer UUID"
// @Success      200 {object} response.Body{data=ClientProfile}
// @Failure      404 {object} response.ErrorBody "CLIENT_NOT_FOUND"
// @Router       /clients/{customer_id} [get]
func (h *Handler) GetClient(c *fiber.Ctx) error {
	customerID, err := uuid.Parse(c.Params("customer_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid customer ID")
	}

	requesterUserID := middleware.UserIDFromContext(c)

	profile, err := h.svc.GetClient(c.Context(), requesterUserID, customerID)
	if err != nil {
		return err
	}

	return response.OK(c, profile)
}

// UpsertNote godoc
// @Summary      Create or update the artist's private note for a client
// @Tags         clients
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        customer_id path string true "Customer UUID"
// @Param        body body UpsertNoteRequest true "Note content"
// @Success      200 {object} response.Body{data=NoteResponse}
// @Failure      404 {object} response.ErrorBody "CLIENT_NOT_FOUND"
// @Router       /clients/{customer_id}/notes [put]
func (h *Handler) UpsertNote(c *fiber.Ctx) error {
	customerID, err := uuid.Parse(c.Params("customer_id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid customer ID")
	}

	var req UpsertNoteRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	requesterUserID := middleware.UserIDFromContext(c)

	note, err := h.svc.UpsertNote(c.Context(), requesterUserID, customerID, req)
	if err != nil {
		return err
	}

	return response.OK(c, note)
}
