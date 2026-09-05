package billing

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// Handler handles all HTTP requests for the billing domain.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a new billing Handler.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{
		svc: svc,
		log: log.With(zap.String("module", "billing")),
	}
}

// RegisterRoutes attaches all billing routes to the Fiber app.
//
// Public (no auth) - the pricing page needs to be readable by someone who
// hasn't signed up yet:
//
//	GET /api/v1/billing/plans  - public plan catalogue
//
// Artist (Bearer) - "my own" subscription and invoices only. Uses
// RequireRole("artist", "admin") matching the earnings domain's pattern
// even though an admin caller always 404s here (an admin account has no
// artist profile behind it, per login.component.ts's own comment on the
// frontend) - kept for consistency with the rest of this codebase rather
// than introducing a new artist-only convention for just this domain:
//
//	GET  /api/v1/billing/subscription        - my plan, seats, derived status
//	GET  /api/v1/billing/invoices             - my invoice history
//	POST /api/v1/billing/invoices/:id/submit  - submit an OMT/Whish reference
//
// Admin-only (extends the existing /api/v1/admin group established in
// internal/admin - see that package's RegisterRoutes for why every route
// under this prefix requires role="admin" specifically, not artist-or-admin):
//
//	GET   /api/v1/admin/plans                          - full plan catalogue
//	POST  /api/v1/admin/plans                          - create a plan
//	PATCH /api/v1/admin/plans/:code                     - edit a plan (new signups only)
//	GET   /api/v1/admin/billing/overview                - every artist × plan × status × owed
//	GET   /api/v1/admin/billing/invoices                - confirmation queue (?status=submitted)
//	POST  /api/v1/admin/billing/invoices/:id/confirm    - mark paid, extend period
//	POST  /api/v1/admin/billing/invoices/:id/void       - write off / correct
//	PATCH /api/v1/admin/billing/subscriptions/:id       - change plan, seats, comp, cancel
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	auditRepo := audit.NewRepository(pool)
	svc := NewService(repo, auditRepo)
	handler := NewHandler(svc, log)

	public := app.Group("/api/v1/billing")
	public.Get("/plans", handler.ListPublicPlans)

	myBilling := app.Group("/api/v1/billing", middleware.RequireAuth(), middleware.RequireRole("artist", "admin"))
	myBilling.Get("/subscription", handler.GetMySubscription)
	myBilling.Get("/invoices", handler.GetMyInvoices)
	myBilling.Post("/invoices/:id/submit", handler.SubmitInvoicePayment)

	adminPlans := app.Group("/api/v1/admin/plans", middleware.RequireAuth(), middleware.RequireRole("admin"))
	adminPlans.Get("/", handler.ListAllPlans)
	adminPlans.Post("/", handler.CreatePlan)
	adminPlans.Patch("/:code", handler.UpdatePlan)

	adminBilling := app.Group("/api/v1/admin/billing", middleware.RequireAuth(), middleware.RequireRole("admin"))
	adminBilling.Get("/overview", handler.AdminListOverview)
	adminBilling.Get("/invoices", handler.AdminListInvoices)
	adminBilling.Post("/invoices/:id/confirm", handler.AdminConfirmInvoice)
	adminBilling.Post("/invoices/:id/void", handler.AdminVoidInvoice)
	adminBilling.Patch("/subscriptions/:id", handler.AdminUpdateSubscription)
}

// ListPublicPlans godoc
// @Summary      List public subscription plans
// @Description  Returns the plan catalogue shown on the public pricing page - is_public=TRUE
// @Description  plans only (e.g. the 'comped' tier is hidden), ordered by sort_order.
// @Tags         billing
// @Produce      json
// @Success      200 {object} response.Body{data=[]Plan}
// @Router       /billing/plans [get]
func (h *Handler) ListPublicPlans(c *fiber.Ctx) error {
	plans, err := h.svc.ListPublicPlans(c.Context())
	if err != nil {
		return err
	}
	return response.OK(c, plans)
}

// ListAllPlans godoc
// @Summary      List every plan, including non-public ones
// @Tags         billing
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]Plan}
// @Router       /admin/plans [get]
func (h *Handler) ListAllPlans(c *fiber.Ctx) error {
	plans, err := h.svc.ListAllPlans(c.Context())
	if err != nil {
		return err
	}
	return response.OK(c, plans)
}

// CreatePlan godoc
// @Summary      Create a new subscription plan
// @Tags         billing
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        request body CreatePlanRequest true "New plan"
// @Success      201 {object} response.Body{data=Plan}
// @Failure      409 {object} response.ErrorBody "PLAN_CODE_EXISTS"
// @Failure      422 {object} response.ErrorBody "VALIDATION_ERROR"
// @Router       /admin/plans [post]
func (h *Handler) CreatePlan(c *fiber.Ctx) error {
	var req CreatePlanRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	plan, err := h.svc.CreatePlan(c.Context(), req)
	if err != nil {
		return err
	}
	return response.Created(c, plan)
}

// UpdatePlan godoc
// @Summary      Edit a plan - affects new signups only, never existing subscribers
// @Tags         billing
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        code path string true "Plan code"
// @Param        request body UpdatePlanRequest true "Fields to change"
// @Success      200 {object} response.Body{data=Plan}
// @Failure      404 {object} response.ErrorBody "PLAN_NOT_FOUND"
// @Router       /admin/plans/{code} [patch]
func (h *Handler) UpdatePlan(c *fiber.Ctx) error {
	code := c.Params("code")

	var req UpdatePlanRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	plan, err := h.svc.UpdatePlan(c.Context(), code, req)
	if err != nil {
		return err
	}
	return response.OK(c, plan)
}

// ── Artist: my subscription & invoices ──────────────────────────────────────────

// GetMySubscription godoc
// @Summary      Get the authenticated artist's subscription and derived status
// @Tags         billing
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=SubscriptionResponse}
// @Failure      404 {object} response.ErrorBody "SUBSCRIPTION_NOT_FOUND"
// @Router       /billing/subscription [get]
func (h *Handler) GetMySubscription(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	sub, err := h.svc.GetMySubscription(c.Context(), userID)
	if err != nil {
		return err
	}
	return response.OK(c, sub)
}

// GetMyInvoices godoc
// @Summary      List the authenticated artist's invoice history
// @Tags         billing
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]Invoice}
// @Router       /billing/invoices [get]
func (h *Handler) GetMyInvoices(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	invoices, err := h.svc.GetMyInvoices(c.Context(), userID)
	if err != nil {
		return err
	}
	return response.OK(c, invoices)
}

// SubmitInvoicePayment godoc
// @Summary      Submit an OMT/Whish payment reference for an invoice
// @Description  A claim, not proof of payment - only an admin's confirm action extends service.
// @Tags         billing
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path string true "Invoice UUID"
// @Param        request body SubmitInvoicePaymentRequest false "Payment reference"
// @Success      200 {object} response.Body{data=Invoice}
// @Failure      404 {object} response.ErrorBody "INVOICE_NOT_FOUND"
// @Failure      409 {object} response.ErrorBody "INVOICE_NOT_ISSUED"
// @Router       /billing/invoices/{id}/submit [post]
func (h *Handler) SubmitInvoicePayment(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	invoiceID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid invoice ID")
	}

	var req SubmitInvoicePaymentRequest
	if err := c.BodyParser(&req); err != nil {
		if len(c.Body()) > 0 {
			return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
		}
	}

	inv, err := h.svc.SubmitInvoicePayment(c.Context(), userID, invoiceID, req)
	if err != nil {
		return err
	}
	return response.OK(c, inv)
}

// ── Admin: billing overview, invoices, subscriptions ─────────────────────────────

// AdminListOverview godoc
// @Summary      List every artist's subscription, derived status, and amount owed
// @Tags         billing
// @Security     BearerAuth
// @Produce      json
// @Success      200 {object} response.Body{data=[]SubscriptionOverviewRow}
// @Router       /admin/billing/overview [get]
func (h *Handler) AdminListOverview(c *fiber.Ctx) error {
	overview, err := h.svc.ListSubscriptionsOverview(c.Context())
	if err != nil {
		return err
	}
	return response.OK(c, overview)
}

// AdminListInvoices godoc
// @Summary      List invoices, optionally filtered by status
// @Description  The confirmation queue is ?status=submitted. Omit status for full history.
// @Tags         billing
// @Security     BearerAuth
// @Produce      json
// @Param        status query string false "issued|submitted|paid|void"
// @Success      200 {object} response.Body{data=[]Invoice}
// @Router       /admin/billing/invoices [get]
func (h *Handler) AdminListInvoices(c *fiber.Ctx) error {
	status := c.Query("status")

	invoices, err := h.svc.ListInvoices(c.Context(), status)
	if err != nil {
		return err
	}
	return response.OK(c, invoices)
}

// AdminConfirmInvoice godoc
// @Summary      Confirm an invoice as paid - extends the subscription's period
// @Tags         billing
// @Security     BearerAuth
// @Produce      json
// @Param        id path string true "Invoice UUID"
// @Success      200 {object} response.Body{data=Invoice}
// @Failure      409 {object} response.ErrorBody "INVOICE_NOT_SUBMITTED"
// @Router       /admin/billing/invoices/{id}/confirm [post]
func (h *Handler) AdminConfirmInvoice(c *fiber.Ctx) error {
	adminID := middleware.UserIDFromContext(c)

	invoiceID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid invoice ID")
	}

	inv, err := h.svc.ConfirmInvoice(c.Context(), invoiceID, adminID, c.IP())
	if err != nil {
		return err
	}
	return response.OK(c, inv)
}

// AdminVoidInvoice godoc
// @Summary      Void an invoice - a correction, not a routine action
// @Tags         billing
// @Security     BearerAuth
// @Accept       json
// @Param        id path string true "Invoice UUID"
// @Param        request body VoidInvoiceRequest true "Reason (required)"
// @Success      204
// @Failure      409 {object} response.ErrorBody "INVOICE_NOT_VOIDABLE"
// @Router       /admin/billing/invoices/{id}/void [post]
func (h *Handler) AdminVoidInvoice(c *fiber.Ctx) error {
	adminID := middleware.UserIDFromContext(c)

	invoiceID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid invoice ID")
	}

	var req VoidInvoiceRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	if err := h.svc.VoidInvoice(c.Context(), invoiceID, adminID, req, c.IP()); err != nil {
		return err
	}
	return response.NoContent(c)
}

// AdminUpdateSubscription godoc
// @Summary      Change a subscription's plan, seats, trial/period dates, or cancellation
// @Tags         billing
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        id path string true "Subscription UUID"
// @Param        request body UpdateSubscriptionRequest true "Fields to change"
// @Success      200 {object} response.Body{data=Subscription}
// @Failure      404 {object} response.ErrorBody "SUBSCRIPTION_NOT_FOUND"
// @Router       /admin/billing/subscriptions/{id} [patch]
func (h *Handler) AdminUpdateSubscription(c *fiber.Ctx) error {
	adminID := middleware.UserIDFromContext(c)

	subscriptionID, err := uuid.Parse(c.Params("id"))
	if err != nil {
		return apperror.BadRequest("INVALID_ID", "Invalid subscription ID")
	}

	var req UpdateSubscriptionRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	sub, err := h.svc.UpdateSubscription(c.Context(), subscriptionID, adminID, req, c.IP())
	if err != nil {
		return err
	}
	return response.OK(c, sub)
}
