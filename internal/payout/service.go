package payout

import (
	"context"
	"errors"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// ErrNotFound is returned when a destination does not exist for this salon.
var ErrNotFound = errors.New("payment method not found")

// Service applies the rules around payment destinations.
type Service struct {
	repo     Repository
	validate *validator.Validate
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

	p, err := s.repo.Upsert(ctx, salonID, req)
	if err != nil {
		return nil, err
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
	out := toResponse(p)
	return &out, nil
}
