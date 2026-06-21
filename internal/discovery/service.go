// Package discovery implements the public customer-facing artist discovery surface.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// defaultPageSize is the number of artist cards returned per discovery page.
const defaultPageSize = 20

// maxPageSize caps the page size a client can request.
const maxPageSize = 50

// Service handles all discovery business logic.
type Service struct {
	repo Repository
	// now is injectable so the New-badge logic is testable with a fixed clock.
	now func() time.Time
}

// NewService creates a new discovery Service.
func NewService(repo Repository) *Service {
	return &Service{
		repo: repo,
		now:  time.Now,
	}
}

// ListArtistsParams is the validated input to the discovery list.
type ListArtistsParams struct {
	City     string
	Category string
	Query    string
	Limit    int
}

// ListArtists returns discovery cards for the browse screen. City and Query are
// free-form; Category, if set, must be one of the fixed five. An artist with
// stores in multiple cities appears once per city.
func (s *Service) ListArtists(ctx context.Context, p ListArtistsParams) ([]*ArtistCard, error) {
	if p.Category != "" && !ValidCategories[p.Category] {
		return nil, apperror.BadRequest("INVALID_CATEGORY", "Unknown artist category")
	}

	limit := p.Limit
	if limit <= 0 || limit > maxPageSize {
		limit = defaultPageSize
	}

	rows, err := s.repo.ListArtistCards(ctx, ListArtistCardsParams{
		City:     p.City,
		Category: p.Category,
		Query:    p.Query,
		Limit:    limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list artists: %w", err)
	}

	now := s.now().UTC()
	result := make([]*ArtistCard, 0, len(rows))
	for _, r := range rows {
		result = append(result, toArtistCard(r, now))
	}
	return result, nil
}

// GetArtistProfile returns the public profile aggregate: the artist plus their
// stores and their salon's services, in one response. Returns NOT_FOUND if the
// artist does not exist. An artist with no salon yet returns an empty services
// list rather than an error.
func (s *Service) GetArtistProfile(ctx context.Context, artistID uuid.UUID) (*PublicArtistProfile, error) {
	profile, err := s.repo.GetArtistProfile(ctx, artistID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist not found")
		}
		return nil, fmt.Errorf("get artist profile: %w", err)
	}

	storeRows, err := s.repo.GetArtistStores(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("get artist profile: stores: %w", err)
	}

	stores := make([]StoreCard, 0, len(storeRows))
	for _, sr := range storeRows {
		stores = append(stores, toStoreCard(sr))
	}

	// Services derive from the artist's salon. No salon → empty menu.
	services := make([]ServiceCard, 0)
	if profile.SalonID != nil {
		serviceRows, err := s.repo.GetSalonServices(ctx, *profile.SalonID)
		if err != nil {
			return nil, fmt.Errorf("get artist profile: services: %w", err)
		}
		for _, sr := range serviceRows {
			services = append(services, toServiceCard(sr))
		}
	}

	return &PublicArtistProfile{
		ID:          profile.ID,
		Name:        profile.Name,
		Bio:         profile.Bio,
		Instagram:   profile.Instagram,
		Category:    profile.Category,
		Rating:      profile.Rating,
		ReviewCount: profile.ReviewCount,
		IsVerified:  profile.IsVerified,
		Stores:      stores,
		Services:    services,
	}, nil
}
