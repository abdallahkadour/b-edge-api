// Package media implements the media/portfolio domain for B-Edge,
// providing portfolio photo management for artist profiles.
package media

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// Service handles all media business logic.
// It knows nothing about HTTP — no fiber.Ctx, no status codes.
// It knows nothing about SQL — all DB access goes through Repository.
type Service struct {
	repo     Repository
	validate *validator.Validate
}

// NewService creates a new media Service.
func NewService(repo Repository) *Service {
	return &Service{
		repo:     repo,
		validate: validator.New(),
	}
}

// GetPortfolio returns the public portfolio for any artist.
// No authentication required — used by the customer PWA discovery screen.
func (s *Service) GetPortfolio(ctx context.Context, artistID uuid.UUID) (*PortfolioResponse, error) {
	items, err := s.repo.ListByArtist(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("get portfolio: %w", err)
	}

	photos := make([]MediaResponse, 0, len(items))
	for _, item := range items {
		photos = append(photos, toMediaResponse(item))
	}

	return &PortfolioResponse{
		ArtistID:   artistID,
		Photos:     photos,
		TotalCount: len(photos),
		MaxAllowed: MaxPortfolioPhotos,
	}, nil
}

// GetMyPortfolio returns the portfolio for the authenticated artist.
func (s *Service) GetMyPortfolio(ctx context.Context, userID uuid.UUID) (*PortfolioResponse, error) {
	artistID, err := s.resolveArtist(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.GetPortfolio(ctx, artistID)
}

// AddPhoto adds a new photo to the artist's portfolio.
// Returns ErrPortfolioFull if the artist already has 20 photos.
// The new photo is appended at the end (highest display_order).
func (s *Service) AddPhoto(ctx context.Context, userID uuid.UUID, req AddMediaRequest) (*MediaResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	artistID, err := s.resolveArtist(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Enforce the 20-photo limit
	count, err := s.repo.CountByArtist(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("add photo: count: %w", err)
	}
	if count >= MaxPortfolioPhotos {
		return nil, apperror.Conflict("PORTFOLIO_FULL", "Portfolio is full — maximum 20 photos allowed")
	}

	item := &MediaItem{
		ID:           uuid.New(),
		OwnerType:    OwnerTypeArtist,
		OwnerID:      artistID,
		URL:          req.URL,
		CloudinaryID: req.CloudinaryID,
		Type:         MediaTypePhoto,
		DisplayOrder: count, // append at the end
	}

	if err := s.repo.Create(ctx, item); err != nil {
		return nil, fmt.Errorf("add photo: create: %w", err)
	}

	res := toMediaResponse(item)
	return &res, nil
}

// DeletePhoto removes a photo from the artist's portfolio.
// Only the owning artist can delete their photos.
func (s *Service) DeletePhoto(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error {
	artistID, err := s.resolveArtist(ctx, userID)
	if err != nil {
		return err
	}

	item, err := s.repo.GetByID(ctx, mediaID)
	if err != nil {
		if errors.Is(err, ErrMediaNotFound) {
			return apperror.NotFound("MEDIA_NOT_FOUND", "Photo not found")
		}
		return fmt.Errorf("delete photo: get item: %w", err)
	}

	// Ownership check — artist can only delete their own photos
	if item.OwnerID != artistID {
		return apperror.Forbidden("FORBIDDEN", "You do not have permission to delete this photo")
	}

	if err := s.repo.Delete(ctx, mediaID); err != nil {
		return fmt.Errorf("delete photo: %w", err)
	}

	return nil
}

// SetCover promotes a photo to be the cover (display_order=0).
// Only the owning artist can set their cover photo.
func (s *Service) SetCover(ctx context.Context, userID uuid.UUID, mediaID uuid.UUID) error {
	artistID, err := s.resolveArtist(ctx, userID)
	if err != nil {
		return err
	}

	item, err := s.repo.GetByID(ctx, mediaID)
	if err != nil {
		if errors.Is(err, ErrMediaNotFound) {
			return apperror.NotFound("MEDIA_NOT_FOUND", "Photo not found")
		}
		return fmt.Errorf("set cover: get item: %w", err)
	}

	if item.OwnerID != artistID {
		return apperror.Forbidden("FORBIDDEN", "You do not have permission to update this photo")
	}

	if err := s.repo.SetCover(ctx, mediaID, artistID); err != nil {
		return fmt.Errorf("set cover: %w", err)
	}

	return nil
}

// Reorder updates the display_order of all photos for the authenticated artist.
// The IDs slice must contain all of the artist's photo IDs in the desired order.
func (s *Service) Reorder(ctx context.Context, userID uuid.UUID, req ReorderRequest) error {
	if err := s.validate.Struct(req); err != nil {
		return mapValidationError(err)
	}

	artistID, err := s.resolveArtist(ctx, userID)
	if err != nil {
		return err
	}

	// Parse and validate all UUIDs before touching the DB
	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, rawID := range req.IDs {
		id, err := uuid.Parse(rawID)
		if err != nil {
			return apperror.BadRequest("INVALID_ID", "One or more media IDs are invalid")
		}
		ids = append(ids, id)
	}

	// Verify the count matches what the artist actually has
	count, err := s.repo.CountByArtist(ctx, artistID)
	if err != nil {
		return fmt.Errorf("reorder: count: %w", err)
	}
	if len(ids) != count {
		return apperror.BadRequest("INVALID_REORDER", "IDs list must contain all of your photos")
	}

	return s.repo.Reorder(ctx, artistID, ids)
}

// ── Private helpers ───────────────────────────────────────────────────────────

// resolveArtist resolves a users.id to an artists.id.
// Returns a typed apperror if the artist profile does not exist.
func (s *Service) resolveArtist(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	artistID, err := s.repo.GetArtistIDByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return uuid.Nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist profile not found")
		}
		return uuid.Nil, fmt.Errorf("resolve artist: %w", err)
	}
	return artistID, nil
}

// mapValidationError converts go-playground/validator errors to apperror types.
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

// validationMessage returns a human-readable message for a field validation failure.
func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + " is required"
	case "url":
		return fe.Field() + " must be a valid URL"
	case "max":
		return fe.Field() + " is too long"
	case "min":
		return fe.Field() + " must have at least " + fe.Param() + " items"
	default:
		return fe.Field() + " is invalid"
	}
}
