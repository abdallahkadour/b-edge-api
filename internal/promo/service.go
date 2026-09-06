package promo

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/discount"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/money"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// Service resolves codes and manages them.
type Service struct {
	repo     Repository
	validate *validator.Validate
}

// NewService creates a promo service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo, validate: validation.New()}
}

// Result is the outcome of applying a code to a price.
//
// A refused code is NOT an error. "That code has expired" is an ordinary thing
// for a customer to be told at checkout, and forcing the caller into an error
// branch for it would mean either failing the whole booking or inspecting
// error strings to decide whether to continue.
type Result struct {
	Breakdown discount.Breakdown
	// DiscountID is set only when a code actually reduced the price - so the
	// caller writes a redemption exactly when there is something to redeem.
	DiscountID *uuid.UUID
	// Reason is empty when the code applied, or when no code was supplied.
	Reason string
}

// Applied reports whether a code reduced the price.
func (r Result) Applied() bool { return r.DiscountID != nil }

// Resolve prices a booking or order, with or without a code.
//
// base, surcharge and deposit come from the caller because they belong to the
// service or product being bought, and promo has no business reading either
// table. This keeps the domain boundary one-directional: booking knows about
// promo, promo knows nothing about booking.
//
// An empty code is the common path and costs no queries at all.
func (s *Service) Resolve(
	ctx context.Context,
	salonID, customerID uuid.UUID,
	code string,
	base, surcharge, deposit decimal.Decimal,
) (*Result, error) {
	in := discount.Input{Base: base, Surcharge: surcharge, Deposit: deposit}

	code = strings.TrimSpace(code)
	if code == "" {
		return &Result{Breakdown: discount.Resolve(in)}, nil
	}

	d, err := s.repo.GetByCode(ctx, salonID, code)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			// Same wording as a deactivated code - see eligibility.Check.
			return &Result{Breakdown: discount.Resolve(in), Reason: "That code isn't valid."}, nil
		}
		return nil, err
	}

	facts, err := s.repo.GatherFacts(ctx, d.ID, customerID, salonID)
	if err != nil {
		return nil, err
	}

	if e := Check(d, facts); !e.OK() {
		return &Result{Breakdown: discount.Resolve(in), Reason: e.Reason}, nil
	}

	rule, ok := ToRule(d)
	if !ok {
		// A kind the resolver does not understand. Refuse rather than apply
		// nothing silently - see ToRule.
		return &Result{Breakdown: discount.Resolve(in), Reason: "That code isn't valid."}, nil
	}

	in.Code = &rule
	breakdown := discount.Resolve(in)

	// The resolver caps a discount at the deposit, so a code CAN resolve to
	// zero. Reporting it as applied would write a redemption - burning the
	// customer's one use - for a discount they did not receive.
	if breakdown.CodeAmount.IsZero() {
		return &Result{
			Breakdown: breakdown,
			Reason:    "That code doesn't reduce this booking any further.",
		}, nil
	}

	id := d.ID
	return &Result{Breakdown: breakdown, DiscountID: &id}, nil
}

// Preview answers "what would this code do", without committing anything.
func (s *Service) Preview(
	ctx context.Context,
	salonID, customerID uuid.UUID,
	code string,
	base, surcharge, deposit decimal.Decimal,
) (*PreviewResponse, error) {
	res, err := s.Resolve(ctx, salonID, customerID, code, base, surcharge, deposit)
	if err != nil {
		return nil, err
	}

	out := &PreviewResponse{Code: strings.ToUpper(strings.TrimSpace(code)), Valid: res.Applied()}
	if !res.Applied() {
		out.Reason = res.Reason
		return out, nil
	}

	b := res.Breakdown
	out.Subtotal = b.Subtotal.StringFixed(2)
	out.DiscountTotal = b.DiscountTotal.StringFixed(2)
	out.Final = b.Final.StringFixed(2)
	out.Deposit = b.Deposit.StringFixed(2)
	out.Balance = b.Balance.StringFixed(2)
	return out, nil
}

// ── Management ───────────────────────────────────────────────────────────────

// ListBySalon returns every code a salon owns, active first.
func (s *Service) ListBySalon(ctx context.Context, salonID uuid.UUID) ([]*DiscountResponse, error) {
	return s.repo.ListBySalon(ctx, salonID)
}

// Create adds a code to a salon.
func (s *Service) Create(ctx context.Context, salonID uuid.UUID, req CreateDiscountRequest) (*DiscountResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, validation.MapError(err)
	}

	// Through money.Parse like every other amount in this API: a percentage is
	// still a 2-decimal number, and "NaN" must not reach the column. The 0-100
	// bound for percentages is the database's CHECK, which is where it belongs
	// since it depends on kind.
	value, err := money.Parse(req.Value, "value")
	if err != nil {
		return nil, err
	}

	d := &Discount{
		SalonID:        salonID,
		Code:           strings.ToUpper(strings.TrimSpace(req.Code)),
		Description:    req.Description,
		Kind:           req.Kind,
		Value:          value,
		StartsAt:       req.StartsAt,
		EndsAt:         req.EndsAt,
		MaxRedemptions: req.MaxRedemptions,
		FirstTimeOnly:  req.FirstTimeOnly,
	}

	if err := s.repo.Create(ctx, d); err != nil {
		if errors.Is(err, ErrDuplicateCode) {
			return nil, apperror.Conflict("DUPLICATE_CODE", "You already have a code with that name.")
		}
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23514" {
			// A CHECK constraint - a percentage over 100, or a value of zero.
			return nil, apperror.BadRequest("INVALID_DISCOUNT",
				"A percentage must be between 0 and 100, and any amount must be above zero.")
		}
		return nil, fmt.Errorf("create discount: %w", err)
	}

	return toDiscountResponse(d, 0), nil
}

// Update patches a code. Ownership failures are 404, matching the rest of the
// API - see CLAUDE.md.
func (s *Service) Update(ctx context.Context, id, salonID uuid.UUID, req UpdateDiscountRequest) (*DiscountResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, validation.MapError(err)
	}
	if req.Value != nil {
		if _, err := money.Parse(*req.Value, "value"); err != nil {
			return nil, err
		}
	}

	d, err := s.repo.Update(ctx, id, salonID, req)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, errDiscountNotFound()
		}
		return nil, err
	}
	return toDiscountResponse(d, 0), nil
}

// ReleaseForBooking implements D3.5: a customer who did not break the booking
// keeps their code.
func (s *Service) ReleaseForBooking(ctx context.Context, bookingID uuid.UUID) error {
	return s.repo.ReleaseForBooking(ctx, bookingID)
}

// errDiscountNotFound is the single answer to "you may not have this code",
// whether it does not exist or belongs to another salon. Same shape and same
// reasoning as booking.errBookingNotFound.
func errDiscountNotFound() error {
	return apperror.NotFound("DISCOUNT_NOT_FOUND", "Discount not found")
}
