// Package discovery implements the public customer-facing artist discovery surface.
package discovery

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

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
	log *zap.Logger
}

// NewService creates a new discovery Service.
//
// The logger is variadic so existing callers passing only a repository keep
// working - matching internal/booking's NewService, which takes its logger
// the same way. Omitting it yields a no-op logger rather than a nil one, so
// call sites never need a nil check.
func NewService(repo Repository, logger ...*zap.Logger) *Service {
	log := zap.NewNop()
	if len(logger) > 0 && logger[0] != nil {
		log = logger[0]
	}
	return &Service{
		repo: repo,
		now:  time.Now,
		log:  log,
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
// exceptionLookaheadDays is how far either side of now to fetch dated
// trading overrides.
//
// One day in each direction, not zero: a store's local calendar date can be
// ahead of or behind the server's, so "today" for a Beirut salon may be
// tomorrow's row from a UTC server's point of view. Fetching a three-day
// window and letting deriveOpenStatus pick the store-local date is cheaper
// and less error-prone than computing each store's date up here just to
// narrow a query that returns a handful of rows anyway.
const exceptionLookaheadDays = 1

// buildStoreCards turns store rows into client cards with their current
// trading state attached.
//
// Hours and exceptions are fetched once for every store together rather than
// per store - a profile with three branches would otherwise issue six extra
// queries to render three badges.
//
// A failure to read hours is NOT fatal: the profile still renders, with
// every store reporting ReasonUnknown. A customer being unable to see a
// salon's page because its opening hours could not be loaded would be a far
// worse outcome than a missing badge.
func (s *Service) buildStoreCards(ctx context.Context, rows []*StoreRow, now time.Time) ([]StoreCard, error) {
	cards := make([]StoreCard, 0, len(rows))
	if len(rows) == 0 {
		return cards, nil
	}

	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}

	hours, err := s.repo.GetStoreHours(ctx, ids)
	if err != nil {
		s.log.Warn("discovery: could not load store hours; omitting open status",
			zap.Error(err))
		hours = nil
	}
	excs, err := s.repo.GetStoreExceptions(ctx, ids,
		now.AddDate(0, 0, -exceptionLookaheadDays),
		now.AddDate(0, 0, exceptionLookaheadDays))
	if err != nil {
		s.log.Warn("discovery: could not load store exceptions; ignoring holiday overrides",
			zap.Error(err))
		excs = nil
	}

	byStoreHours := make(map[uuid.UUID][]*DayHoursRow, len(rows))
	for _, h := range hours {
		byStoreHours[h.StoreID] = append(byStoreHours[h.StoreID], h)
	}
	byStoreExcs := make(map[uuid.UUID][]*ExceptionRow, len(rows))
	for _, e := range excs {
		byStoreExcs[e.StoreID] = append(byStoreExcs[e.StoreID], e)
	}

	for _, r := range rows {
		cards = append(cards, toStoreCard(r, byStoreHours[r.ID], byStoreExcs[r.ID], now))
	}
	return cards, nil
}

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

	stores, err := s.buildStoreCards(ctx, storeRows, s.now())
	if err != nil {
		return nil, fmt.Errorf("get artist profile: %w", err)
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
