package billing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// Service handles all billing business logic: the plan catalogue,
// subscriptions, and invoices. It knows nothing about HTTP and nothing
// about SQL; all DB access goes through Repository.
type Service struct {
	repo     Repository
	validate *validator.Validate
	audit    audit.Repository
}

// NewService creates a new billing Service. auditRepo may be nil, in which
// case audit events are silently discarded - matching the pattern
// established in internal/admin, so tests can construct a Service without
// needing a real audit table.
func NewService(repo Repository, auditRepo audit.Repository) *Service {
	a := audit.Repository(audit.NopRepository{})
	if auditRepo != nil {
		a = auditRepo
	}
	return &Service{repo: repo, validate: validator.New(), audit: a}
}

// ListPublicPlans returns the plans shown on the public pricing page.
func (s *Service) ListPublicPlans(ctx context.Context) ([]*Plan, error) {
	plans, err := s.repo.ListPublicPlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list public plans: %w", err)
	}
	return plans, nil
}

// ListAllPlans returns every plan for the admin Plans tab, including
// non-public tiers like 'comped'.
func (s *Service) ListAllPlans(ctx context.Context) ([]*Plan, error) {
	plans, err := s.repo.ListAllPlans(ctx)
	if err != nil {
		return nil, fmt.Errorf("list all plans: %w", err)
	}
	return plans, nil
}

// CreatePlan validates and inserts a new plan.
func (s *Service) CreatePlan(ctx context.Context, req CreatePlanRequest) (*Plan, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	code := strings.ToLower(strings.TrimSpace(req.Code))
	if code == "" {
		return nil, apperror.BadRequest("INVALID_CODE", "Plan code is required")
	}

	monthlyPrice, err := parseNonNegativeDecimal(req.MonthlyPrice, "monthly_price")
	if err != nil {
		return nil, err
	}

	seatPrice := decimal.Zero
	if req.SeatPrice != "" {
		seatPrice, err = parseNonNegativeDecimal(req.SeatPrice, "seat_price")
		if err != nil {
			return nil, err
		}
	}

	// USD only - decided 2026-08-30 and enforced at the database by
	// migration 026's CHECK constraints. Rejecting here as well turns a
	// non-USD plan into a 400 with a usable message rather than a
	// constraint violation surfacing as a 500.
	currency := strings.ToUpper(req.Currency)
	if currency == "" {
		currency = CurrencyUSD
	}
	if currency != CurrencyUSD {
		return nil, apperror.BadRequest("UNSUPPORTED_CURRENCY",
			"Only USD is supported. See migration 026 for why this is fixed rather than configurable.")
	}

	includedSeats := req.IncludedSeats
	if includedSeats == 0 {
		includedSeats = 1
	}

	isPublic := true
	if req.IsPublic != nil {
		isPublic = *req.IsPublic
	}

	features := req.Features
	if features == nil {
		features = []string{}
	}

	p := &Plan{
		Code:          code,
		Name:          req.Name,
		MonthlyPrice:  monthlyPrice,
		Currency:      currency,
		SeatPrice:     seatPrice,
		IncludedSeats: includedSeats,
		Description:   req.Description,
		Features:      features,
		IsPublic:      isPublic,
		SortOrder:     req.SortOrder,
	}

	if err := s.repo.CreatePlan(ctx, p); err != nil {
		if errors.Is(err, ErrPlanCodeExists) {
			return nil, apperror.Conflict("PLAN_CODE_EXISTS", "A plan with this code already exists")
		}
		return nil, fmt.Errorf("create plan: %w", err)
	}

	return s.repo.GetPlanByCode(ctx, code)
}

// UpdatePlan applies a partial update to an existing plan.
//
// Reads the current row, merges in only the fields the caller supplied,
// and writes the merged result back - so a caller changing only the price
// doesn't accidentally blank out the description or feature list.
//
// This ONLY ever touches the plans row itself. It has no knowledge of
// subscriptions and cannot affect what an existing subscriber is currently
// being charged - see the package doc comment and Plan's doc comment for
// why that separation matters.
func (s *Service) UpdatePlan(ctx context.Context, code string, req UpdatePlanRequest) (*Plan, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	existing, err := s.repo.GetPlanByCode(ctx, code)
	if err != nil {
		if errors.Is(err, ErrPlanNotFound) {
			return nil, apperror.NotFound("PLAN_NOT_FOUND", "Plan not found")
		}
		return nil, fmt.Errorf("update plan: get existing: %w", err)
	}

	if req.Name != nil {
		existing.Name = *req.Name
	}
	if req.MonthlyPrice != nil {
		price, err := parseNonNegativeDecimal(*req.MonthlyPrice, "monthly_price")
		if err != nil {
			return nil, err
		}
		existing.MonthlyPrice = price
	}
	if req.SeatPrice != nil {
		price, err := parseNonNegativeDecimal(*req.SeatPrice, "seat_price")
		if err != nil {
			return nil, err
		}
		existing.SeatPrice = price
	}
	if req.IncludedSeats != nil {
		existing.IncludedSeats = *req.IncludedSeats
	}
	if req.Description != nil {
		existing.Description = *req.Description
	}
	if req.Features != nil {
		existing.Features = *req.Features
	}
	if req.IsPublic != nil {
		existing.IsPublic = *req.IsPublic
	}
	if req.SortOrder != nil {
		existing.SortOrder = *req.SortOrder
	}

	if err := s.repo.UpdatePlan(ctx, existing); err != nil {
		if errors.Is(err, ErrPlanNotFound) {
			return nil, apperror.NotFound("PLAN_NOT_FOUND", "Plan not found")
		}
		return nil, fmt.Errorf("update plan: %w", err)
	}

	return s.repo.GetPlanByCode(ctx, code)
}

// ── Subscriptions & invoices (artist-facing) ────────────────────────────────────

// maxInvoiceGenerationIterations caps ensureInvoicesUpTo's loop - a
// defensive bound, not an expected one. In practice the loop only ever
// runs 0 or 1 times per call (see that function's doc comment for why
// invoices never stack), so this only matters if a subscription's dates
// are ever corrupted; it exists to turn that into a silently-truncated
// catch-up rather than a hang.
const maxInvoiceGenerationIterations = 36

// GetMySubscription returns the authenticated artist's subscription with
// its derived status and plan name, running lazy invoice generation first
// so the outstanding invoice (if any) is already up to date.
func (s *Service) GetMySubscription(ctx context.Context, userID uuid.UUID) (*SubscriptionResponse, error) {
	artistID, err := s.repo.GetArtistIDByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist profile not found")
		}
		return nil, fmt.Errorf("get my subscription: %w", err)
	}

	sub, err := s.repo.GetSubscriptionByArtistID(ctx, artistID)
	if err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			return nil, apperror.NotFound("SUBSCRIPTION_NOT_FOUND", "No subscription found for this account yet")
		}
		return nil, fmt.Errorf("get my subscription: %w", err)
	}

	now := time.Now()
	if err := s.ensureInvoicesUpTo(ctx, sub, now); err != nil {
		return nil, fmt.Errorf("get my subscription: %w", err)
	}

	return s.toSubscriptionResponse(ctx, sub, now), nil
}

// GetMyInvoices returns the authenticated artist's invoice history, running
// lazy invoice generation first. An artist with no subscription yet (see
// GetMySubscription) simply has no invoices - not an error.
func (s *Service) GetMyInvoices(ctx context.Context, userID uuid.UUID) ([]*Invoice, error) {
	artistID, err := s.repo.GetArtistIDByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist profile not found")
		}
		return nil, fmt.Errorf("get my invoices: %w", err)
	}

	sub, err := s.repo.GetSubscriptionByArtistID(ctx, artistID)
	if err != nil && !errors.Is(err, ErrSubscriptionNotFound) {
		return nil, fmt.Errorf("get my invoices: %w", err)
	}
	if sub != nil {
		if err := s.ensureInvoicesUpTo(ctx, sub, time.Now()); err != nil {
			return nil, fmt.Errorf("get my invoices: %w", err)
		}
	}

	invoices, err := s.repo.ListInvoicesByArtist(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("get my invoices: %w", err)
	}
	return invoices, nil
}

// SubmitInvoicePayment records the artist's OMT/Whish reference and moves
// the invoice issued→submitted. This is a CLAIM, not proof of payment -
// only Service.ConfirmInvoice (admin-only) treats money as real. See spec
// section 8.
func (s *Service) SubmitInvoicePayment(ctx context.Context, userID, invoiceID uuid.UUID, req SubmitInvoicePaymentRequest) (*Invoice, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	artistID, err := s.repo.GetArtistIDByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist profile not found")
		}
		return nil, fmt.Errorf("submit invoice payment: %w", err)
	}

	inv, err := s.repo.GetInvoiceByID(ctx, invoiceID)
	if err != nil {
		if errors.Is(err, ErrInvoiceNotFound) {
			return nil, apperror.NotFound("INVOICE_NOT_FOUND", "Invoice not found")
		}
		return nil, fmt.Errorf("submit invoice payment: %w", err)
	}

	// Same 404 as a genuinely missing invoice, not 403 - a caller probing
	// invoice IDs that aren't theirs learns nothing about whether the ID
	// is real. This is a billing endpoint handling money, so the ownership
	// check that E2E-TEST-PLAN.md notes is still missing on the artist
	// review-list endpoint is not optional here.
	if inv.ArtistID != artistID {
		return nil, apperror.NotFound("INVOICE_NOT_FOUND", "Invoice not found")
	}

	if err := s.repo.SubmitInvoice(ctx, invoiceID, req.PaymentReference); err != nil {
		if errors.Is(err, ErrInvoiceWrongStatus) {
			return nil, apperror.Conflict("INVOICE_NOT_ISSUED", "Only an issued invoice can be submitted for payment review")
		}
		return nil, fmt.Errorf("submit invoice payment: %w", err)
	}

	updated, err := s.repo.GetInvoiceByID(ctx, invoiceID)
	if err != nil {
		return nil, fmt.Errorf("submit invoice payment: reload: %w", err)
	}
	return updated, nil
}

// ensureInvoicesUpTo lazily generates any invoice the subscription is due
// for as of now, replacing a scheduled job - matching the
// lazy-expiry-from-read-paths pattern already established elsewhere in
// this codebase (RELEASE-CHECKLIST.md) rather than introducing the first
// cron this project would have.
//
// By construction this never generates more than one OUTSTANDING invoice
// at a time: current_period_end only advances when ConfirmInvoice runs, so
// an artist who hasn't paid in three months still has exactly one
// (increasingly overdue) invoice, not three stacked ones. That is a
// deliberate simplification for a subscription this simple to reason
// about, not an oversight - see the doc comment on Invoice.
func (s *Service) ensureInvoicesUpTo(ctx context.Context, sub *Subscription, now time.Time) error {
	if sub.PlanCode == "comped" || sub.CancelledAt != nil {
		return nil
	}
	if DeriveStatus(sub, now) == StatusTrialing {
		return nil
	}

	periodStart := sub.CreatedAt
	if sub.TrialEndsAt != nil {
		periodStart = *sub.TrialEndsAt
	}
	if sub.CurrentPeriodEnd != nil {
		periodStart = *sub.CurrentPeriodEnd
	}

	for range maxInvoiceGenerationIterations {
		if periodStart.After(now) {
			return nil
		}
		periodEnd := periodStart.AddDate(0, 1, 0)

		// Seat overage (seats beyond a plan's included_seats) is not
		// billed here yet - subscriptions doesn't currently carry
		// included_seats alongside its price snapshot, so there is
		// nothing to compare seats against. Every invoice is currently
		// just MonthlyPrice. Deferred to Phase 3, matching the spec's
		// original scope for full seat-based billing - flagged here
		// rather than silently under-billing without a trace of why.
		inv := &Invoice{
			SubscriptionID: sub.ID,
			ArtistID:       sub.ArtistID,
			PeriodStart:    periodStart,
			PeriodEnd:      periodEnd,
			DueDate:        periodStart,
			Amount:         sub.MonthlyPrice,
			Currency:       sub.Currency,
			SeatsBilled:    sub.Seats,
			PlanCode:       sub.PlanCode,
		}
		if _, err := s.repo.CreateInvoiceIfMissing(ctx, inv); err != nil {
			return fmt.Errorf("ensure invoices up to: %w", err)
		}

		periodStart = periodEnd
	}
	return nil
}

func (s *Service) toSubscriptionResponse(ctx context.Context, sub *Subscription, now time.Time) *SubscriptionResponse {
	planName := sub.PlanCode
	if plan, err := s.repo.GetPlanByCode(ctx, sub.PlanCode); err == nil {
		planName = plan.Name
	}
	return &SubscriptionResponse{
		Subscription: *sub,
		Status:       DeriveStatus(sub, now),
		PlanName:     planName,
	}
}

// ── Subscriptions & invoices (admin-facing) ─────────────────────────────────────

// ListSubscriptionsOverview returns every artist's subscription, plan, and
// outstanding amount for the admin Billing tab.
//
// Two passes on purpose: first ensureInvoicesUpTo runs against every
// subscription with no join, THEN the joined overview query runs - so its
// outstanding-amount aggregate reflects any invoice just generated in pass
// one, rather than being one invoice stale until the next time someone
// loads this screen.
func (s *Service) ListSubscriptionsOverview(ctx context.Context) ([]*SubscriptionOverviewRow, error) {
	subs, err := s.repo.ListAllSubscriptions(ctx)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions overview: %w", err)
	}

	now := time.Now()
	for _, sub := range subs {
		if err := s.ensureInvoicesUpTo(ctx, sub, now); err != nil {
			return nil, fmt.Errorf("list subscriptions overview: %w", err)
		}
	}

	overview, err := s.repo.ListSubscriptionsOverview(ctx)
	if err != nil {
		return nil, fmt.Errorf("list subscriptions overview: %w", err)
	}
	return overview, nil
}

// ListInvoices returns every invoice with the given status (or every
// invoice if status is ""), for the admin confirmation queue.
func (s *Service) ListInvoices(ctx context.Context, status string) ([]*Invoice, error) {
	invoices, err := s.repo.ListInvoicesByStatus(ctx, status)
	if err != nil {
		return nil, fmt.Errorf("list invoices: %w", err)
	}
	return invoices, nil
}

// ConfirmInvoice is the single most consequential action in this package:
// it takes an artist's submitted payment reference as real money received
// and extends their service accordingly. Admin-only, audited, and
// idempotent by construction - confirming an invoice that isn't currently
// 'submitted' (including one already 'paid') returns a conflict rather
// than silently extending the period a second time.
func (s *Service) ConfirmInvoice(ctx context.Context, invoiceID, adminID uuid.UUID, ip string) (*Invoice, error) {
	inv, err := s.repo.ConfirmInvoice(ctx, invoiceID, adminID)
	if err != nil {
		if errors.Is(err, ErrInvoiceWrongStatus) {
			return nil, apperror.Conflict("INVOICE_NOT_SUBMITTED", "Only a submitted invoice can be confirmed as paid")
		}
		return nil, fmt.Errorf("confirm invoice: %w", err)
	}

	// Best-effort - a failure to WRITE the audit log must never undo a
	// confirmation that already committed. Matches internal/admin's
	// Approve/Reject pattern.
	_ = s.audit.Log(ctx, audit.Event{
		ActorID:    &adminID,
		ActorRole:  "admin",
		EntityType: "invoice",
		EntityID:   invoiceID,
		Action:     "confirmed_paid",
		NewValues: map[string]any{
			"artist_id": inv.ArtistID,
			"amount":    inv.Amount.String(),
			"currency":  inv.Currency,
		},
		IPAddress: ip,
	})

	return inv, nil
}

// VoidInvoice writes off or corrects an invoice - the reason is required
// because this is a correction to a financial record, not a routine action.
func (s *Service) VoidInvoice(ctx context.Context, invoiceID, adminID uuid.UUID, req VoidInvoiceRequest, ip string) error {
	if err := s.validate.Struct(req); err != nil {
		return mapValidationError(err)
	}

	if err := s.repo.VoidInvoice(ctx, invoiceID, req.Reason); err != nil {
		if errors.Is(err, ErrInvoiceWrongStatus) {
			return apperror.Conflict("INVOICE_NOT_VOIDABLE", "Only an issued or submitted invoice can be voided")
		}
		return fmt.Errorf("void invoice: %w", err)
	}

	_ = s.audit.Log(ctx, audit.Event{
		ActorID:    &adminID,
		ActorRole:  "admin",
		EntityType: "invoice",
		EntityID:   invoiceID,
		Action:     "voided",
		NewValues:  map[string]any{"reason": req.Reason},
		IPAddress:  ip,
	})

	return nil
}

// UpdateSubscription applies a partial update to a subscription - change of
// plan, seats, trial/period dates, or cancellation. Addressed by
// subscription ID, not artist ID, matching the admin route
// (PATCH /admin/billing/subscriptions/:id).
//
// Setting PlanCode re-snapshots MonthlyPrice/Currency from that plan's
// CURRENT price - see UpdateSubscriptionRequest's doc comment for why this
// is a deliberately different code path from Service.UpdatePlan, which
// never touches a subscription.
func (s *Service) UpdateSubscription(ctx context.Context, subscriptionID, adminID uuid.UUID, req UpdateSubscriptionRequest, ip string) (*Subscription, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	existing, err := s.repo.GetSubscriptionByID(ctx, subscriptionID)
	if err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			return nil, apperror.NotFound("SUBSCRIPTION_NOT_FOUND", "Subscription not found")
		}
		return nil, fmt.Errorf("update subscription: %w", err)
	}

	if req.PlanCode != nil {
		plan, err := s.repo.GetPlanByCode(ctx, *req.PlanCode)
		if err != nil {
			if errors.Is(err, ErrPlanNotFound) {
				return nil, apperror.BadRequest("INVALID_PLAN_CODE", "Unknown plan code")
			}
			return nil, fmt.Errorf("update subscription: %w", err)
		}
		existing.PlanCode = plan.Code
		existing.MonthlyPrice = plan.MonthlyPrice
		existing.Currency = plan.Currency
	}
	if req.Seats != nil {
		existing.Seats = *req.Seats
	}
	if req.TrialEndsAt != nil {
		t, err := time.Parse(time.RFC3339, *req.TrialEndsAt)
		if err != nil {
			return nil, apperror.BadRequest("INVALID_TRIAL_ENDS_AT", "trial_ends_at must be a valid RFC3339 timestamp")
		}
		existing.TrialEndsAt = &t
	}
	if req.CurrentPeriodEnd != nil {
		t, err := time.Parse(time.RFC3339, *req.CurrentPeriodEnd)
		if err != nil {
			return nil, apperror.BadRequest("INVALID_CURRENT_PERIOD_END", "current_period_end must be a valid RFC3339 timestamp")
		}
		existing.CurrentPeriodEnd = &t
	}
	if req.Cancel != nil {
		if *req.Cancel {
			now := time.Now()
			existing.CancelledAt = &now
		} else {
			existing.CancelledAt = nil
		}
	}

	if err := s.repo.UpdateSubscription(ctx, existing); err != nil {
		if errors.Is(err, ErrSubscriptionNotFound) {
			return nil, apperror.NotFound("SUBSCRIPTION_NOT_FOUND", "Subscription not found")
		}
		return nil, fmt.Errorf("update subscription: %w", err)
	}

	_ = s.audit.Log(ctx, audit.Event{
		ActorID:    &adminID,
		ActorRole:  "admin",
		EntityType: "subscription",
		EntityID:   subscriptionID,
		Action:     "updated",
		NewValues:  req,
		IPAddress:  ip,
	})

	return s.repo.GetSubscriptionByID(ctx, subscriptionID)
}

// parseNonNegativeDecimal parses a request price field, matching the exact
// pattern established in product.CreateProduct - a bad or negative amount
// is a 400, never a panic or a silently-stored garbage value.
func parseNonNegativeDecimal(raw, field string) (decimal.Decimal, error) {
	value, err := decimal.NewFromString(raw)
	if err != nil || value.IsNegative() {
		return decimal.Zero, apperror.BadRequest(
			"INVALID_"+strings.ToUpper(field),
			field+" must be a valid non-negative amount",
		)
	}
	return value, nil
}

// mapValidationError converts a validator error into a proper
// UnprocessableEntity with field-level details - matching the exact
// pattern established in product/review/customerauth, not a stripped-down
// generic message.
func mapValidationError(err error) error {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return apperror.BadRequest("VALIDATION_ERROR", err.Error())
	}
	details := make([]apperror.FieldError, 0, len(ve))
	for _, fe := range ve {
		details = append(details, apperror.FieldError{
			Field:   fe.Field(),
			Message: validationMessage(fe),
		})
	}
	return apperror.UnprocessableEntity("VALIDATION_ERROR", details)
}

func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + " is required"
	case "min":
		return fe.Field() + " must be at least " + fe.Param()
	case "max":
		return fe.Field() + " must be at most " + fe.Param() + " characters"
	case "len":
		return fe.Field() + " must be exactly " + fe.Param() + " characters"
	default:
		return fe.Field() + " is invalid"
	}
}
