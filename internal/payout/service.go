package payout

import (
	"context"
	"errors"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// ErrNotFound is returned when a destination does not exist for this salon.
var ErrNotFound = errors.New("payment method not found")

// Service applies the rules around payment destinations.
type Service struct {
	repo     Repository
	validate *validator.Validate
	// activity is the salon activity log; nil records nothing.
	activity audit.Logger
}

// WithAudit records every change to the salon's payment accounts and its
// no-show setting in the activity log, with the old values beside the new.
// A redirected payment account sends customers' deposits to someone else;
// the owner has to be able to see who changed it, and from what.
func (s *Service) WithAudit(l audit.Logger) *Service {
	s.activity = l
	return s
}

// accountFacts is what the activity row keeps about a payment account.
func accountFacts(p *PaymentMethod) map[string]any {
	return map[string]any{
		"method": string(p.Method), "account_name": p.AccountName,
		"account_ref": p.AccountRef, "is_active": p.IsActive,
	}
}

// NewService creates a payout service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo, validate: validation.New()}
}

func errNotFound() error {
	return apperror.NotFound("PAYMENT_METHOD_NOT_FOUND", "Payment method not found")
}

// ListForSalon returns the destinations an artist manages, active or not.
func (s *Service) ListForSalon(ctx context.Context, salonID uuid.UUID) ([]PaymentMethodResponse, error) {
	rows, err := s.repo.ListBySalon(ctx, salonID, false)
	if err != nil {
		return nil, err
	}
	out := make([]PaymentMethodResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, toResponse(p))
	}
	return out, nil
}

// ListPublic returns the ACTIVE destinations a paying client should use.
//
// Active-only is the security-relevant half. A retired number must stop being
// offered the moment it is retired - an artist who has lost control of an
// account needs switching it off to take effect immediately, not to leave a
// stale destination on the deposit screen.
func (s *Service) ListPublic(ctx context.Context, salonID uuid.UUID) ([]PublicPaymentMethodResponse, error) {
	rows, err := s.repo.ListBySalon(ctx, salonID, true)
	if err != nil {
		return nil, err
	}
	out := make([]PublicPaymentMethodResponse, 0, len(rows))
	for _, p := range rows {
		out = append(out, toPublicResponse(p))
	}
	return out, nil
}

// Upsert sets the salon's account for one method.
func (s *Service) Upsert(ctx context.Context, salonID uuid.UUID, req UpsertPaymentMethodRequest) (*PaymentMethodResponse, error) {
	req.AccountName = strings.TrimSpace(req.AccountName)
	// Whitespace is stripped from the reference entirely, not just trimmed.
	// People paste phone numbers as "71 900 001", and a client comparing what
	// the app shows against what their banking app shows should not have to
	// reason about spacing.
	req.AccountRef = strings.Join(strings.Fields(req.AccountRef), "")

	if err := s.validate.Struct(req); err != nil {
		return nil, validation.MapError(err)
	}
	if !Method(req.Method).Valid() {
		return nil, apperror.BadRequest("INVALID_METHOD", "Choose either Whish or OMT")
	}

	// What it was, for the activity row. Best-effort: a failed read must not
	// stop the owner changing her own account, it only leaves the row
	// without its "before".
	var before map[string]any
	if s.activity != nil {
		if rows, err := s.repo.ListBySalon(ctx, salonID, false); err == nil {
			for _, r := range rows {
				if string(r.Method) == req.Method {
					before = accountFacts(r)
				}
			}
		}
	}

	p, err := s.repo.Upsert(ctx, salonID, req)
	if err != nil {
		return nil, err
	}
	ev := audit.Event{SalonID: &salonID, EntityType: audit.EntityPaymentMethod, EntityID: p.ID,
		Action: audit.ActionPaymentMethodSave, NewValues: accountFacts(p)}
	if before != nil {
		ev.OldValues = before
	}
	// Saving the account exactly as it was is not a change.
	if _, changed := audit.Changed(before, accountFacts(p)); before == nil || changed != nil {
		audit.Record(ctx, s.activity, ev)
	}
	out := toResponse(p)
	return &out, nil
}

// SetActive retires or restores a destination.
func (s *Service) SetActive(ctx context.Context, id, salonID uuid.UUID, active bool) (*PaymentMethodResponse, error) {
	p, err := s.repo.SetActive(ctx, id, salonID, active)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, errNotFound()
		}
		return nil, err
	}
	action := audit.ActionPaymentMethodRetire
	if active {
		action = audit.ActionPaymentMethodRestore
	}
	audit.Record(ctx, s.activity, audit.Event{SalonID: &salonID, EntityType: audit.EntityPaymentMethod,
		EntityID: p.ID, Action: action, NewValues: accountFacts(p)})
	out := toResponse(p)
	return &out, nil
}

// NoShowPolicy reads the salon's no-show deposit setting (D29).
func (s *Service) NoShowPolicy(ctx context.Context, salonID uuid.UUID) (*NoShowPolicy, error) {
	after, err := s.repo.GetNoShowPolicy(ctx, salonID)
	if errors.Is(err, ErrNotFound) {
		return nil, apperror.Forbidden("NO_SALON", "You are not associated with a salon")
	}
	if err != nil {
		return nil, err
	}
	return &NoShowPolicy{After: after}, nil
}

// SetNoShowPolicy changes it: 0 to 10, where 0 turns the rule off.
func (s *Service) SetNoShowPolicy(ctx context.Context, salonID uuid.UUID, req SetNoShowPolicyRequest) (*NoShowPolicy, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, validation.MapError(err)
	}
	before, beforeErr := 0, errors.New("not read")
	if s.activity != nil {
		before, beforeErr = s.repo.GetNoShowPolicy(ctx, salonID)
	}
	if err := s.repo.SetNoShowPolicy(ctx, salonID, *req.After); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperror.Forbidden("NO_SALON", "You are not associated with a salon")
		}
		return nil, err
	}
	ev := audit.Event{SalonID: &salonID, EntityType: audit.EntitySalon, EntityID: salonID,
		Action: audit.ActionSalonNoShowPolicy, NewValues: map[string]any{"after": *req.After}}
	if beforeErr == nil {
		ev.OldValues = map[string]any{"after": before}
	}
	if beforeErr != nil || before != *req.After {
		audit.Record(ctx, s.activity, ev)
	}
	return &NoShowPolicy{After: *req.After}, nil
}
