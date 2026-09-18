package report

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/clientip"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// Handler exposes reporting.
type Handler struct{ svc *Service }

// NewHandler creates a report handler.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// RegisterRoutes wires the reporting routes.
//
// Creating a report requires authentication. An unauthenticated endpoint here
// would be a spam cannon, and the alternative - proving identity with a
// booking id plus a phone number - is exactly the credential-stuffing surface
// this product does not need. A client already signs in with the phone they
// booked with in order to see the booking, so this costs them nothing.
//
//	GET    /api/v1/reports/categories   - what can be reported (public)
//	POST   /api/v1/reports              - raise a problem
//	GET    /api/v1/reports/me           - my own reports and their status
//	GET    /api/v1/admin/reports        - the queue
//	PATCH  /api/v1/admin/reports/:id    - move one along
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool) {
	handler := NewHandler(NewService(NewRepository(pool), audit.NewRepository(pool)))

	// Public so the form can render its options before anyone signs in.
	app.Get("/api/v1/reports/categories", handler.ListCategories)

	g := app.Group("/api/v1/reports", middleware.RequireAuth())
	g.Post("/", handler.Create)
	g.Get("/me", handler.ListMine)

	a := app.Group("/api/v1/admin/reports",
		middleware.RequireAuth(), middleware.RequireRole("admin"))
	a.Get("/", handler.ListQueue)
	a.Patch("/:id", handler.Resolve)
}

// ListCategories godoc
// @Summary      What can be reported
// @Tags         reports
// @Produce      json
// @Success      200 {object} response.Body
// @Router       /reports/categories [get]
func (h *Handler) ListCategories(c *fiber.Ctx) error {
	return response.OK(c, Categories())
}

// Create godoc
// @Summary      Raise a problem
// @Tags         reports
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body CreateReportRequest true "Report"
// @Success      201 {object} response.Body{data=Response}
// @Failure      409 {object} response.Body "already reported"
// @Router       /reports [post]
func (h *Handler) Create(c *fiber.Ctx) error {
	var req CreateReportRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	userID := middleware.UserIDFromContext(c)
	role := middleware.RoleFromContext(c)
	if role != "artist" && role != "admin" {
		role = "customer"
	}

	out, err := h.svc.Create(c.UserContext(), userID, role, req)
	if err != nil {
		return err
	}
	return response.Created(c, out)
}

// ListMine godoc
// @Summary      My reports and what happened to them
// @Tags         reports
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]Response}
// @Router       /reports/me [get]
func (h *Handler) ListMine(c *fiber.Ctx) error {
	out, err := h.svc.ListMine(c.UserContext(), middleware.UserIDFromContext(c))
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// ListQueue godoc
// @Summary      The report queue
// @Tags         admin
// @Security     BearerAuth
// @Produce      json
// @Param        include_resolved query bool false "Include closed reports"
// @Success      200 {object} response.Body{data=[]AdminResponse}
// @Router       /admin/reports [get]
func (h *Handler) ListQueue(c *fiber.Ctx) error {
	out, err := h.svc.ListQueue(c.UserContext(), c.QueryBool("include_resolved", false))
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// Resolve godoc
// @Summary      Move a report along
// @Tags         admin
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id   path string               true "Report UUID"
// @Param        body body ResolveReportRequest true "New status and what was done"
// @Success      204
// @Failure      409 {object} response.Body "already dealt with"
// @Router       /admin/reports/{id} [patch]
func (h *Handler) Resolve(c *fiber.Ctx) error {
	id, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.NotFound("REPORT_NOT_FOUND", "Report not found")
	}
	var req ResolveReportRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	adminID := middleware.UserIDFromContext(c)
	if err := h.svc.Resolve(c.UserContext(), id, adminID, req, clientip.From(c)); err != nil {
		return err
	}
	return response.NoContent(c)
}
