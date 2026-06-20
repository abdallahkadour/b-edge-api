// Package artist implements the artist domain for B-Edge.
package artist

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// Service handles all artist business logic.
type Service struct {
	repo     Repository
	validate *validator.Validate
}

// NewService creates a new artist Service.
func NewService(repo Repository) *Service {
	return &Service{
		repo:     repo,
		validate: validator.New(),
	}
}

// ── Artist profile ────────────────────────────────────────────────────────────

// GetArtistByID returns the public profile for an artist.
func (s *Service) GetArtistByID(ctx context.Context, artistID uuid.UUID) (*ArtistResponse, error) {
	profile, err := s.repo.GetArtistByID(ctx, artistID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist not found")
		}
		return nil, fmt.Errorf("get artist by id: %w", err)
	}
	return toArtistResponse(profile), nil
}

// GetMyProfile returns the artist profile for the authenticated user.
func (s *Service) GetMyProfile(ctx context.Context, userID uuid.UUID) (*ArtistProfile, error) {
	profile, err := s.repo.GetArtistByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist profile not found")
		}
		return nil, fmt.Errorf("get my profile: %w", err)
	}
	return profile, nil
}

// UpdateProfile updates an artist's bio and instagram.
// Only the artist who owns the profile can update it.
func (s *Service) UpdateProfile(ctx context.Context, artistID uuid.UUID, userID uuid.UUID, req UpdateProfileRequest) (*ArtistResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	// Fetch profile to verify ownership
	profile, err := s.repo.GetArtistByID(ctx, artistID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist not found")
		}
		return nil, fmt.Errorf("update profile: get artist: %w", err)
	}

	if profile.UserID != userID {
		return nil, apperror.Forbidden("NOT_ARTIST_OWNER", "You do not have permission to update this profile")
	}

	if err := s.repo.UpdateArtistProfile(ctx, artistID, req); err != nil {
		return nil, fmt.Errorf("update profile: %w", err)
	}

	// Return updated profile
	updated, err := s.repo.GetArtistByID(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("update profile: get updated: %w", err)
	}
	return toArtistResponse(updated), nil
}

// ── Stores ────────────────────────────────────────────────────────────────────

// GetStoresByArtist returns all stores an artist works at.
func (s *Service) GetStoresByArtist(ctx context.Context, artistID uuid.UUID) ([]*Store, error) {
	stores, err := s.repo.GetStoresByArtist(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("get stores by artist: %w", err)
	}
	return stores, nil
}

// GetStoresBySalon returns all active stores for a salon.
func (s *Service) GetStoresBySalon(ctx context.Context, salonID uuid.UUID) ([]*Store, error) {
	stores, err := s.repo.GetStoresBySalon(ctx, salonID)
	if err != nil {
		return nil, fmt.Errorf("get stores by salon: %w", err)
	}
	return stores, nil
}

// ── Services ──────────────────────────────────────────────────────────────────

// GetServicesBySalon returns all active services for a salon.
func (s *Service) GetServicesBySalon(ctx context.Context, salonID uuid.UUID) ([]*ServiceResponse, error) {
	services, err := s.repo.GetServicesBySalon(ctx, salonID)
	if err != nil {
		return nil, fmt.Errorf("get services by salon: %w", err)
	}

	var result []*ServiceResponse
	for _, svc := range services {
		result = append(result, toServiceResponse(svc))
	}
	return result, nil
}

// Add this method to internal/artist/service.go, after GetServicesBySalon.

// GetPublicServicesByArtist returns active services for an artist's salon.
// Public endpoint — no authentication required. Used by the customer PWA
// to display services on an artist's profile page.
func (s *Service) GetPublicServicesByArtist(ctx context.Context, artistID uuid.UUID) ([]*ServiceResponse, error) {
	// Fetch the artist profile to get their salon_id.
	profile, err := s.repo.GetArtistByID(ctx, artistID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist not found")
		}
		return nil, fmt.Errorf("get public services by artist: %w", err)
	}

	// Artists with no salon yet return an empty list.
	if profile.SalonID == nil {
		return []*ServiceResponse{}, nil
	}

	// Reuse the existing salon services query.
	return s.GetServicesBySalon(ctx, *profile.SalonID)
}

// CreateService adds a new service to a salon's catalogue.
// Only artists belonging to the salon can add services.
func (s *Service) CreateService(ctx context.Context, salonID uuid.UUID, req CreateServiceRequest) (*ServiceResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	price, err := decimal.NewFromString(req.Price)
	if err != nil || price.IsNegative() {
		return nil, apperror.BadRequest("INVALID_PRICE", "Price must be a valid positive number")
	}

	deposit, err := decimal.NewFromString(req.DepositAmount)
	if err != nil || deposit.IsNegative() {
		return nil, apperror.BadRequest("INVALID_DEPOSIT", "Deposit amount must be a valid positive number")
	}

	var categoryID *uuid.UUID
	if req.CategoryID != nil {
		id, err := uuid.Parse(*req.CategoryID)
		if err != nil {
			return nil, apperror.BadRequest("INVALID_CATEGORY_ID", "Invalid category ID")
		}
		categoryID = &id
	}

	svc := &SalonServiceRecord{
		ID:                   uuid.New(),
		SalonID:              salonID,
		CategoryID:           categoryID,
		Name:                 req.Name,
		NameAr:               req.NameAr,
		Description:          req.Description,
		DurationMin:          req.DurationMin,
		ActiveDurationMin:    req.ActiveDurationMin,
		Price:                price,
		DepositAmount:        deposit,
		DepositDeadlineHours: req.DepositDeadlineHours,
		IsActive:             true,
		IsCustom:             true,
	}

	if err := s.repo.CreateService(ctx, svc); err != nil {
		return nil, fmt.Errorf("create service: %w", err)
	}

	return toServiceResponse(svc), nil
}

// UpdateService updates a service's fields.
func (s *Service) UpdateService(ctx context.Context, serviceID uuid.UUID, salonID uuid.UUID, req UpdateServiceRequest) (*ServiceResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	// Verify service belongs to this salon
	existing, err := s.repo.GetServiceByID(ctx, serviceID)
	if err != nil {
		if errors.Is(err, ErrServiceNotFound) {
			return nil, apperror.NotFound("SERVICE_NOT_FOUND", "Service not found")
		}
		return nil, fmt.Errorf("update service: get service: %w", err)
	}

	if existing.SalonID != salonID {
		return nil, apperror.Forbidden("FORBIDDEN", "You do not have permission to update this service")
	}

	if err := s.repo.UpdateService(ctx, serviceID, req); err != nil {
		return nil, fmt.Errorf("update service: %w", err)
	}

	updated, err := s.repo.GetServiceByID(ctx, serviceID)
	if err != nil {
		return nil, fmt.Errorf("update service: get updated: %w", err)
	}
	return toServiceResponse(updated), nil
}

// DeleteService deactivates a service.
func (s *Service) DeleteService(ctx context.Context, serviceID uuid.UUID, salonID uuid.UUID) error {
	existing, err := s.repo.GetServiceByID(ctx, serviceID)
	if err != nil {
		if errors.Is(err, ErrServiceNotFound) {
			return apperror.NotFound("SERVICE_NOT_FOUND", "Service not found")
		}
		return fmt.Errorf("delete service: get service: %w", err)
	}

	if existing.SalonID != salonID {
		return apperror.Forbidden("FORBIDDEN", "You do not have permission to delete this service")
	}

	return s.repo.DeleteService(ctx, serviceID)
}

// ── Business hours ────────────────────────────────────────────────────────────

// GetBusinessHours returns all business hours for a store.
func (s *Service) GetBusinessHours(ctx context.Context, storeID uuid.UUID) ([]*BusinessHours, error) {
	hours, err := s.repo.GetBusinessHours(ctx, storeID)
	if err != nil {
		return nil, fmt.Errorf("get business hours: %w", err)
	}
	return hours, nil
}

// SetBusinessHours upserts hours for a store on a specific day.
func (s *Service) SetBusinessHours(ctx context.Context, storeID uuid.UUID, req SetBusinessHoursRequest) error {
	if err := s.validate.Struct(req); err != nil {
		return mapValidationError(err)
	}

	// Validate time format
	if _, err := time.Parse("15:04:05", req.OpenTime); err != nil {
		return apperror.BadRequest("INVALID_TIME", "open_time must be in HH:MM:SS format")
	}
	if _, err := time.Parse("15:04:05", req.CloseTime); err != nil {
		return apperror.BadRequest("INVALID_TIME", "close_time must be in HH:MM:SS format")
	}

	return s.repo.SetBusinessHours(ctx, storeID, req)
}

// GetExceptions returns all business hours exceptions for a store.
func (s *Service) GetExceptions(ctx context.Context, storeID uuid.UUID) ([]*BusinessHoursException, error) {
	exceptions, err := s.repo.GetExceptions(ctx, storeID)
	if err != nil {
		return nil, fmt.Errorf("get exceptions: %w", err)
	}
	return exceptions, nil
}

// CreateException adds a holiday or special-hours day.
func (s *Service) CreateException(ctx context.Context, storeID uuid.UUID, req CreateExceptionRequest) error {
	if err := s.validate.Struct(req); err != nil {
		return mapValidationError(err)
	}

	if _, err := time.Parse("2006-01-02", req.ExceptionDate); err != nil {
		return apperror.BadRequest("INVALID_DATE", "exception_date must be in YYYY-MM-DD format")
	}

	return s.repo.CreateException(ctx, storeID, req)
}

// DeleteException removes a business hours exception.
func (s *Service) DeleteException(ctx context.Context, storeID uuid.UUID, dateStr string) error {
	date, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		return apperror.BadRequest("INVALID_DATE", "Date must be in YYYY-MM-DD format")
	}
	return s.repo.DeleteException(ctx, storeID, date)
}

// ── Private helpers ───────────────────────────────────────────────────────────

func toArtistResponse(p *ArtistProfile) *ArtistResponse {
	return &ArtistResponse{
		ID:          p.ID,
		Name:        p.Name,
		Bio:         p.Bio,
		BioAr:       p.BioAr,
		Instagram:   p.Instagram,
		Rating:      p.Rating,
		ReviewCount: p.ReviewCount,
		IsVerified:  p.IsVerified,
	}
}

func toServiceResponse(s *SalonServiceRecord) *ServiceResponse {
	return &ServiceResponse{
		ID:                   s.ID,
		SalonID:              s.SalonID,
		Name:                 s.Name,
		NameAr:               s.NameAr,
		Description:          s.Description,
		DurationMin:          s.DurationMin,
		ActiveDurationMin:    s.ActiveDurationMin,
		Price:                s.Price,
		DepositAmount:        s.DepositAmount,
		DepositDeadlineHours: s.DepositDeadlineHours,
		IsActive:             s.IsActive,
	}
}

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
