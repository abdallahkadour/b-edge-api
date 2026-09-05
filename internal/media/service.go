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
// It knows nothing about HTTP - no fiber.Ctx, no status codes.
// It knows nothing about SQL - all DB access goes through Repository.
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
// No authentication required - used by the customer PWA discovery screen.
// errMediaNotFound is the single answer to "you may not have this
// object", whether it does not exist or is not yours. A foreign object and a
// nonexistent one must be indistinguishable, or the status code becomes an
// oracle for enumerating real IDs (security test AUTH-02, 2026-09-05). One
// constructor shared by both branches is what stops them drifting apart; see
// the longer note on booking.errBookingNotFound.
func errMediaNotFound() error {
	return apperror.NotFound("MEDIA_NOT_FOUND", "Photo not found")
}

func (s *Service) GetPortfolio(ctx context.Context, artistID uuid.UUID) (*PortfolioResponse, error) {
	items, err := s.repo.ListByArtist(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("get portfolio: %w", err)
	}

	photos, err := s.toMediaResponsesWithTags(ctx, items)
	if err != nil {
		return nil, fmt.Errorf("get portfolio: %w", err)
	}

	return &PortfolioResponse{
		ArtistID:   artistID,
		Photos:     photos,
		TotalCount: len(photos),
		MaxAllowed: MaxPortfolioPhotos,
	}, nil
}

// toMediaResponsesWithTags converts media items and attaches each one's
// tagged service IDs.
//
// Tags are fetched for the whole batch in one query rather than per photo -
// a 20-photo portfolio would otherwise issue 20 extra round trips to render
// one gallery.
//
// ServiceIDs is always a non-nil slice, so JSON carries [] rather than
// null and clients can filter without a nil check.
func (s *Service) toMediaResponsesWithTags(ctx context.Context, items []*MediaItem) ([]MediaResponse, error) {
	photos := make([]MediaResponse, 0, len(items))
	if len(items) == 0 {
		return photos, nil
	}

	ids := make([]uuid.UUID, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.ID)
	}

	tags, err := s.repo.GetServiceIDsByMedia(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("load service tags: %w", err)
	}

	for _, item := range items {
		r := toMediaResponse(item)
		if svc, ok := tags[item.ID]; ok {
			r.ServiceIDs = svc
		} else {
			r.ServiceIDs = []uuid.UUID{}
		}
		photos = append(photos, r)
	}
	return photos, nil
}

// SetMediaServices replaces which services a portfolio photo is tagged to.
//
// Three ownership facts have to line up, and all three are checked here
// rather than in SQL:
//  1. The caller is an artist.
//  2. The photo is artist-owned media belonging to THAT artist - a product
//     photo, or another artist's photo, is not taggable.
//  3. Every service belongs to the caller's salon.
//
// (3) crosses a scope boundary: services are salon-scoped while portfolio
// media is artist-scoped, so the artist must be resolved to their salon
// first. A constraint in migration 028 could not express this.
func (s *Service) SetMediaServices(ctx context.Context, userID, mediaID uuid.UUID, req SetMediaServicesRequest) (*MediaResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	artistID, err := s.resolveArtist(ctx, userID)
	if err != nil {
		return nil, err
	}

	item, err := s.repo.GetByID(ctx, mediaID)
	if err != nil {
		if errors.Is(err, ErrMediaNotFound) {
			return nil, errMediaNotFound()
		}
		return nil, fmt.Errorf("set media services: %w", err)
	}

	// Same 404 for "not yours" as for "doesn't exist", so a caller cannot
	// enumerate other artists' media IDs by watching the status code -
	// matching the ownership posture used on billing invoices.
	if item.OwnerType != OwnerTypeArtist || item.OwnerID != artistID {
		return nil, errMediaNotFound()
	}

	serviceIDs := make([]uuid.UUID, 0, len(req.ServiceIDs))
	for _, raw := range req.ServiceIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, apperror.BadRequest("INVALID_SERVICE_ID", "service_ids must be valid UUIDs")
		}
		serviceIDs = append(serviceIDs, id)
	}

	if len(serviceIDs) > 0 {
		salonID, err := s.repo.GetSalonIDByArtistID(ctx, artistID)
		if err != nil {
			if errors.Is(err, ErrArtistNotFound) {
				return nil, apperror.UnprocessableEntity("NO_SALON", nil)
			}
			return nil, fmt.Errorf("set media services: %w", err)
		}

		n, err := s.repo.CountSalonServices(ctx, salonID, serviceIDs)
		if err != nil {
			return nil, fmt.Errorf("set media services: %w", err)
		}
		// Deliberately does not say WHICH service failed: naming it would
		// confirm the existence of another salon's service ID.
		if n != len(serviceIDs) {
			return nil, apperror.BadRequest("INVALID_SERVICE_ID",
				"One or more services do not belong to your salon")
		}
	}

	if err := s.repo.SetMediaServices(ctx, mediaID, serviceIDs); err != nil {
		return nil, fmt.Errorf("set media services: %w", err)
	}

	resp := toMediaResponse(item)
	resp.ServiceIDs = serviceIDs
	return &resp, nil
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
		return nil, apperror.Conflict("PORTFOLIO_FULL", "Portfolio is full - maximum 20 photos allowed")
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
			return errMediaNotFound()
		}
		return fmt.Errorf("delete photo: get item: %w", err)
	}

	// Ownership check - artist can only delete their own photos
	if item.OwnerID != artistID {
		return errMediaNotFound()
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
			return errMediaNotFound()
		}
		return fmt.Errorf("set cover: get item: %w", err)
	}

	if item.OwnerID != artistID {
		return errMediaNotFound()
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

// ── Products ────────────────────────────────────────────────────────────────
//
// A product's own image_url is untouched by any of this - it stays the
// first/primary photo shown everywhere it already is (shop grid, artist
// products table). These methods manage the ADDITIONAL gallery photos shown
// on the customer product-detail page. There is no "set cover" concept
// here, deliberately: image_url already is the cover.

// GetProductPhotos returns a product's gallery. Public - no authentication
// required, mirrors GetPortfolio's own public/no-auth shape.
func (s *Service) GetProductPhotos(ctx context.Context, productID uuid.UUID) (*ProductGalleryResponse, error) {
	items, err := s.repo.ListByProduct(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("get product photos: %w", err)
	}

	// No service tags here: a product photo shows merchandise, not a
	// service being performed, so media_services is not consulted for
	// product-owned media. ServiceIDs stays an empty slice.
	photos := make([]MediaResponse, 0, len(items))
	for _, item := range items {
		photos = append(photos, toMediaResponse(item))
	}

	return &ProductGalleryResponse{
		ProductID:  productID,
		Photos:     photos,
		TotalCount: len(photos),
		MaxAllowed: MaxProductPhotos,
	}, nil
}

// AddProductPhoto appends a photo to a product's gallery, verifying the
// calling artist's salon actually owns this product first.
func (s *Service) AddProductPhoto(ctx context.Context, productID, salonID uuid.UUID, req AddMediaRequest) (*MediaResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	if err := s.verifyProductOwnership(ctx, productID, salonID); err != nil {
		return nil, err
	}

	count, err := s.repo.CountByProduct(ctx, productID)
	if err != nil {
		return nil, fmt.Errorf("add product photo: count: %w", err)
	}
	if count >= MaxProductPhotos {
		return nil, apperror.Conflict("PRODUCT_GALLERY_FULL",
			fmt.Sprintf("A product can have at most %d additional photos", MaxProductPhotos))
	}

	item := &MediaItem{
		ID:           uuid.New(),
		OwnerType:    OwnerTypeProduct,
		OwnerID:      productID,
		URL:          req.URL,
		CloudinaryID: req.CloudinaryID,
		Type:         MediaTypePhoto,
		DisplayOrder: count, // append at the end
	}

	if err := s.repo.Create(ctx, item); err != nil {
		return nil, fmt.Errorf("add product photo: create: %w", err)
	}

	res := toMediaResponse(item)
	return &res, nil
}

// DeleteProductPhoto removes a photo from a product's gallery. Ownership is
// resolved via the media item's own product, not a caller-supplied product
// ID, so a request can't claim to be deleting "its own" photo from a
// product it doesn't actually own.
func (s *Service) DeleteProductPhoto(ctx context.Context, mediaID, salonID uuid.UUID) error {
	item, err := s.repo.GetByID(ctx, mediaID)
	if err != nil {
		if errors.Is(err, ErrMediaNotFound) {
			return errMediaNotFound()
		}
		return fmt.Errorf("delete product photo: get item: %w", err)
	}
	if item.OwnerType != OwnerTypeProduct {
		return errMediaNotFound()
	}

	if err := s.verifyProductOwnership(ctx, item.OwnerID, salonID); err != nil {
		return err
	}

	if err := s.repo.Delete(ctx, mediaID); err != nil {
		return fmt.Errorf("delete product photo: %w", err)
	}
	return nil
}

// ReorderProductPhotos updates the display_order of all photos in a
// product's gallery. The IDs slice must contain all of the gallery's photo
// IDs in the desired order.
func (s *Service) ReorderProductPhotos(ctx context.Context, productID, salonID uuid.UUID, req ReorderRequest) error {
	if err := s.validate.Struct(req); err != nil {
		return mapValidationError(err)
	}

	if err := s.verifyProductOwnership(ctx, productID, salonID); err != nil {
		return err
	}

	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, rawID := range req.IDs {
		id, err := uuid.Parse(rawID)
		if err != nil {
			return apperror.BadRequest("INVALID_ID", "One or more media IDs are invalid")
		}
		ids = append(ids, id)
	}

	count, err := s.repo.CountByProduct(ctx, productID)
	if err != nil {
		return fmt.Errorf("reorder product photos: count: %w", err)
	}
	if len(ids) != count {
		return apperror.BadRequest("INVALID_REORDER", "IDs list must contain all of this product's photos")
	}

	return s.repo.ReorderProduct(ctx, productID, ids)
}

// verifyProductOwnership confirms the given salon actually owns productID -
// the same cross-tenant guard already established throughout this codebase
// (never trust that "authenticated as an artist" implies "owns this
// specific resource").
func (s *Service) verifyProductOwnership(ctx context.Context, productID, salonID uuid.UUID) error {
	actualSalonID, err := s.repo.GetProductSalonID(ctx, productID)
	if err != nil {
		if errors.Is(err, ErrProductNotFound) {
			return apperror.NotFound("PRODUCT_NOT_FOUND", "Product not found")
		}
		return fmt.Errorf("verify product ownership: %w", err)
	}
	if actualSalonID != salonID {
		return apperror.Forbidden("FORBIDDEN", "You do not have permission to modify this product's photos")
	}
	return nil
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
