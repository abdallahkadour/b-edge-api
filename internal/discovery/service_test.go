// Package discovery contains unit tests for the discovery service layer.
// These tests use a mock repository — no database required.
package discovery

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock repository ───────────────────────────────────────────────────────────

type mockRepo struct {
	listCards         []*ArtistCardRow
	listErr           error
	profile           *ArtistProfileRow
	profileErr        error
	stores            []*StoreRow
	storesErr         error
	services          []*ServiceRow
	servicesErr       error
	lastListParams    ListArtistCardsParams
	lastSalonServices uuid.UUID
}

func (m *mockRepo) ListArtistCards(_ context.Context, f ListArtistCardsParams) ([]*ArtistCardRow, error) {
	m.lastListParams = f
	return m.listCards, m.listErr
}
func (m *mockRepo) GetArtistProfile(_ context.Context, _ uuid.UUID) (*ArtistProfileRow, error) {
	return m.profile, m.profileErr
}
func (m *mockRepo) GetArtistStores(_ context.Context, _ uuid.UUID) ([]*StoreRow, error) {
	return m.stores, m.storesErr
}
func (m *mockRepo) GetSalonServices(_ context.Context, salonID uuid.UUID) ([]*ServiceRow, error) {
	m.lastSalonServices = salonID
	return m.services, m.servicesErr
}

// fixedClock returns a service whose "now" is pinned for deterministic New-badge tests.
func newTestService(repo Repository, now time.Time) *Service {
	s := NewService(repo)
	s.now = func() time.Time { return now }
	return s
}

// ── ListArtists tests ─────────────────────────────────────────────────────────

// TestListArtists_InvalidCategory — an unknown category is rejected.
func TestListArtists_InvalidCategory(t *testing.T) {
	svc := newTestService(&mockRepo{}, time.Now())

	_, err := svc.ListArtists(context.Background(), ListArtistsParams{Category: "tattoos"})

	require.Error(t, err)
}

// TestListArtists_ValidCategoryPassesThrough — a valid category reaches the repo.
func TestListArtists_ValidCategoryPassesThrough(t *testing.T) {
	repo := &mockRepo{listCards: nil}
	svc := newTestService(repo, time.Now())

	_, err := svc.ListArtists(context.Background(), ListArtistsParams{Category: "makeup", City: "Beirut"})

	require.NoError(t, err)
	assert.Equal(t, "makeup", repo.lastListParams.Category)
	assert.Equal(t, "Beirut", repo.lastListParams.City)
}

// TestListArtists_DefaultsLimit — a zero/oversized limit is normalised.
func TestListArtists_DefaultsLimit(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo, time.Now())

	_, err := svc.ListArtists(context.Background(), ListArtistsParams{Limit: 0})
	require.NoError(t, err)
	assert.Equal(t, defaultPageSize, repo.lastListParams.Limit)

	_, err = svc.ListArtists(context.Background(), ListArtistsParams{Limit: 9999})
	require.NoError(t, err)
	assert.Equal(t, defaultPageSize, repo.lastListParams.Limit)
}

// TestListArtists_NewBadge — an artist created within the window is flagged new;
// an older one is not.
func TestListArtists_NewBadge(t *testing.T) {
	now := time.Date(2026, 6, 21, 0, 0, 0, 0, time.UTC)
	recent := now.Add(-5 * 24 * time.Hour)  // 5 days ago → new
	old := now.Add(-90 * 24 * time.Hour)     // 90 days ago → not new

	repo := &mockRepo{
		listCards: []*ArtistCardRow{
			{ID: uuid.New(), Name: "Rania", City: "Beirut", Rating: decimal.NewFromFloat(4.9), CreatedAt: recent},
			{ID: uuid.New(), Name: "Hadi", City: "Beirut", Rating: decimal.NewFromFloat(4.8), CreatedAt: old},
		},
	}
	svc := newTestService(repo, now)

	cards, err := svc.ListArtists(context.Background(), ListArtistsParams{})

	require.NoError(t, err)
	require.Len(t, cards, 2)
	assert.True(t, cards[0].IsNew, "5-day-old artist should be new")
	assert.False(t, cards[1].IsNew, "90-day-old artist should not be new")
}

// ── GetArtistProfile tests ────────────────────────────────────────────────────

// TestGetArtistProfile_NotFound — missing artist surfaces ARTIST_NOT_FOUND.
func TestGetArtistProfile_NotFound(t *testing.T) {
	repo := &mockRepo{profileErr: ErrArtistNotFound}
	svc := newTestService(repo, time.Now())

	_, err := svc.GetArtistProfile(context.Background(), uuid.New())

	require.Error(t, err)
}

// TestGetArtistProfile_Aggregates — profile, stores, and services combine.
func TestGetArtistProfile_Aggregates(t *testing.T) {
	salonID := uuid.New()
	repo := &mockRepo{
		profile: &ArtistProfileRow{
			ID: uuid.New(), Name: "Rania", Rating: decimal.NewFromFloat(4.9),
			ReviewCount: 127, IsVerified: true, SalonID: &salonID,
		},
		stores: []*StoreRow{
			{ID: uuid.New(), Name: "Beirut Downtown", City: "Beirut"},
			{ID: uuid.New(), Name: "Tripoli Center", City: "Tripoli"},
		},
		services: []*ServiceRow{
			{ID: uuid.New(), Name: "Bridal Makeup", DurationMin: 90, Price: decimal.NewFromFloat(200), DepositAmount: decimal.NewFromFloat(50)},
		},
	}
	svc := newTestService(repo, time.Now())

	profile, err := svc.GetArtistProfile(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "Rania", profile.Name)
	assert.True(t, profile.IsVerified)
	require.Len(t, profile.Stores, 2)
	require.Len(t, profile.Services, 1)
	assert.Equal(t, "Bridal Makeup", profile.Services[0].Name)
	assert.Equal(t, salonID, repo.lastSalonServices, "services fetched for the artist's salon")
}

// TestGetArtistProfile_NoSalon — an artist with no salon returns an empty (non-nil)
// services list and does not call GetSalonServices.
func TestGetArtistProfile_NoSalon(t *testing.T) {
	repo := &mockRepo{
		profile: &ArtistProfileRow{
			ID: uuid.New(), Name: "Newbie", Rating: decimal.Zero, SalonID: nil,
		},
		stores: nil,
	}
	svc := newTestService(repo, time.Now())

	profile, err := svc.GetArtistProfile(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.NotNil(t, profile.Services)
	assert.Empty(t, profile.Services)
	assert.Empty(t, profile.Stores)
	assert.Equal(t, uuid.Nil, repo.lastSalonServices, "no salon → GetSalonServices not called")
}
