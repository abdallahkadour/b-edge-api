package onboarding

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/money"
)

type Service struct {
	repo     Repository
	validate *validator.Validate
}

func NewService(repo Repository) *Service {
	return &Service{repo: repo, validate: validator.New()}
}

func (s *Service) Complete(ctx context.Context, userID uuid.UUID, req CompleteOnboardingRequest) (*CompleteOnboardingResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}
	if err := req.Validate(); err != nil {
		return nil, apperror.BadRequest("VALIDATION_ERROR", err.Error())
	}

	// The repository INSERTs service_price as a bare string into a
	// NUMERIC(10,2) column, so this is the only thing standing between a
	// typed-in value and the row. Postgres accepts 'NaN'::numeric, which
	// would leave a brand-new artist's very first service unreadable from
	// the moment they finished onboarding. See internal/pkg/money, INJ-04.
	if _, err := money.Parse(req.ServicePrice, "service_price"); err != nil {
		return nil, err
	}

	artistID, err := s.repo.Complete(ctx, userID, req)
	if err != nil {
		switch {
		case errors.Is(err, ErrAlreadyOnboarded):
			return nil, apperror.Conflict("ALREADY_ONBOARDED", "This account has already submitted an application")
		case errors.Is(err, ErrHandleTaken):
			return nil, apperror.Conflict("HANDLE_TAKEN", "This handle is already taken - please choose another")
		default:
			return nil, fmt.Errorf("complete onboarding: %w", err)
		}
	}

	return &CompleteOnboardingResponse{ArtistID: artistID, Status: "pending"}, nil
}

func (s *Service) GetStatus(ctx context.Context, userID uuid.UUID) (*OnboardingStatus, error) {
	status, err := s.repo.GetStatus(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotOnboarded) {
			return nil, apperror.NotFound("NOT_ONBOARDED", "Onboarding has not been started")
		}
		return nil, fmt.Errorf("get onboarding status: %w", err)
	}
	return status, nil
}

// mapValidationError and fieldMessage mirror internal/artist/service.go's
// implementation exactly, byte for byte - the field-level error shape is
// meant to be identical across every domain, and my first draft here
// quietly wasn't (a generic "%s failed validation: %s" instead of the
// established human-readable messages). Caught by checking the existing
// pattern rather than assuming my own version was equivalent.
func mapValidationError(err error) error {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return apperror.BadRequest("VALIDATION_ERROR", err.Error())
	}
	details := make([]apperror.FieldError, 0, len(ve))
	for _, fe := range ve {
		details = append(details, apperror.FieldError{
			Field:   fe.Field(),
			Message: fieldMessage(fe),
		})
	}
	return apperror.UnprocessableEntity("VALIDATION_ERROR", details)
}

func fieldMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + " is required"
	case "min":
		return fe.Field() + " must be at least " + fe.Param()
	case "max":
		return fe.Field() + " must be at most " + fe.Param() + " characters"
	case "uuid":
		return fe.Field() + " must be a valid UUID"
	default:
		return fe.Field() + " is invalid"
	}
}
