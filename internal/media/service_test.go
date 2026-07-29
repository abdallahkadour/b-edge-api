// Package media implements the media/portfolio domain for B-Edge,
// providing portfolio photo management for artist profiles.
package media

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock repository ───────────────────────────────────────────────────────────

type mockRepo struct {
	artistID    uuid.UUID
	artistErr   error
	items       []*MediaItem
	count       int
	countErr    error
	getByIDItem *MediaItem
	getByIDErr  error
	createErr   error
	deleteErr   error
	setCoverErr error
	reorderErr  error
}

func (m *mockRepo) GetArtistIDByUserID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return m.artistID, m.artistErr
}

func (m *mockRepo) ListByArtist(_ context.Context, _ uuid.UUID) ([]*MediaItem, error) {
	return m.items, nil
}

func (m *mockRepo) CountByArtist(_ context.Context, _ uuid.UUID) (int, error) {
	return m.count, m.countErr
}

func (m *mockRepo) GetByID(_ context.Context, _ uuid.UUID) (*MediaItem, error) {
	return m.getByIDItem, m.getByIDErr
}

func (m *mockRepo) Create(_ context.Context, item *MediaItem) error {
	item.CreatedAt = time.Now()
	return m.createErr
}

func (m *mockRepo) Delete(_ context.Context, _ uuid.UUID) error {
	return m.deleteErr
}

func (m *mockRepo) SetCover(_ context.Context, _, _ uuid.UUID) error {
	return m.setCoverErr
}

func (m *mockRepo) Reorder(_ context.Context, _ uuid.UUID, _ []uuid.UUID) error {
	return m.reorderErr
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func makeItem(artistID uuid.UUID, order int) *MediaItem {
	return &MediaItem{
		ID:           uuid.New(),
		OwnerType:    OwnerTypeArtist,
		OwnerID:      artistID,
		URL:          "https://res.cloudinary.com/bedge/image/upload/v1/test.jpg",
		Type:         MediaTypePhoto,
		DisplayOrder: order,
		CreatedAt:    time.Now(),
	}
}

// ── GetPortfolio tests ────────────────────────────────────────────────────────

func TestGetPortfolio_ReturnsPhotos(t *testing.T) {
	artistID := uuid.New()
	items := []*MediaItem{
		makeItem(artistID, 0),
		makeItem(artistID, 1),
		makeItem(artistID, 2),
	}

	repo := &mockRepo{artistID: artistID, items: items}
	svc := NewService(repo)

	result, err := svc.GetPortfolio(context.Background(), artistID)
	require.NoError(t, err)
	assert.Equal(t, artistID, result.ArtistID)
	assert.Len(t, result.Photos, 3)
	assert.Equal(t, 3, result.TotalCount)
	assert.Equal(t, MaxPortfolioPhotos, result.MaxAllowed)
}

func TestGetPortfolio_EmptyPortfolio(t *testing.T) {
	artistID := uuid.New()

	repo := &mockRepo{artistID: artistID, items: nil}
	svc := NewService(repo)

	result, err := svc.GetPortfolio(context.Background(), artistID)
	require.NoError(t, err)
	assert.Empty(t, result.Photos)
	assert.Equal(t, 0, result.TotalCount)
}

// ── AddPhoto tests ────────────────────────────────────────────────────────────

func TestAddPhoto_Success(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	repo := &mockRepo{artistID: artistID, count: 3}
	svc := NewService(repo)

	req := AddMediaRequest{
		URL: "https://res.cloudinary.com/bedge/image/upload/v1/bridal.jpg",
	}

	result, err := svc.AddPhoto(context.Background(), userID, req)
	require.NoError(t, err)
	assert.Equal(t, req.URL, result.URL)
	assert.Equal(t, 3, result.DisplayOrder) // appended at count position
	assert.Equal(t, MediaTypePhoto, result.Type)
}

func TestAddPhoto_PortfolioFull_Returns409(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	repo := &mockRepo{artistID: artistID, count: MaxPortfolioPhotos}
	svc := NewService(repo)

	req := AddMediaRequest{
		URL: "https://res.cloudinary.com/bedge/image/upload/v1/extra.jpg",
	}

	_, err := svc.AddPhoto(context.Background(), userID, req)
	assert.Error(t, err)
}

func TestAddPhoto_InvalidURL_ReturnsValidationError(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	repo := &mockRepo{artistID: artistID, count: 0}
	svc := NewService(repo)

	req := AddMediaRequest{URL: "not-a-url"}
	_, err := svc.AddPhoto(context.Background(), userID, req)
	assert.Error(t, err)
}

func TestAddPhoto_ArtistNotFound_ReturnsError(t *testing.T) {
	userID := uuid.New()

	repo := &mockRepo{artistErr: ErrArtistNotFound}
	svc := NewService(repo)

	req := AddMediaRequest{
		URL: "https://res.cloudinary.com/bedge/image/upload/v1/test.jpg",
	}

	_, err := svc.AddPhoto(context.Background(), userID, req)
	assert.Error(t, err)
}

// ── DeletePhoto tests ─────────────────────────────────────────────────────────

func TestDeletePhoto_Success(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()
	mediaID := uuid.New()

	item := &MediaItem{
		ID:      mediaID,
		OwnerID: artistID,
	}

	repo := &mockRepo{artistID: artistID, getByIDItem: item}
	svc := NewService(repo)

	err := svc.DeletePhoto(context.Background(), userID, mediaID)
	assert.NoError(t, err)
}

func TestDeletePhoto_WrongOwner_ReturnsForbidden(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()
	otherArtistID := uuid.New()
	mediaID := uuid.New()

	item := &MediaItem{
		ID:      mediaID,
		OwnerID: otherArtistID, // different artist owns this photo
	}

	repo := &mockRepo{artistID: artistID, getByIDItem: item}
	svc := NewService(repo)

	err := svc.DeletePhoto(context.Background(), userID, mediaID)
	assert.Error(t, err)
}

func TestDeletePhoto_NotFound_ReturnsError(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	repo := &mockRepo{artistID: artistID, getByIDErr: ErrMediaNotFound}
	svc := NewService(repo)

	err := svc.DeletePhoto(context.Background(), userID, uuid.New())
	assert.Error(t, err)
}

// ── SetCover tests ────────────────────────────────────────────────────────────

func TestSetCover_Success(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()
	mediaID := uuid.New()

	item := &MediaItem{ID: mediaID, OwnerID: artistID}

	repo := &mockRepo{artistID: artistID, getByIDItem: item}
	svc := NewService(repo)

	err := svc.SetCover(context.Background(), userID, mediaID)
	assert.NoError(t, err)
}

func TestSetCover_WrongOwner_ReturnsForbidden(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()
	mediaID := uuid.New()

	item := &MediaItem{ID: mediaID, OwnerID: uuid.New()} // different owner

	repo := &mockRepo{artistID: artistID, getByIDItem: item}
	svc := NewService(repo)

	err := svc.SetCover(context.Background(), userID, mediaID)
	assert.Error(t, err)
}

// ── Reorder tests ─────────────────────────────────────────────────────────────

func TestReorder_Success(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	id1, id2, id3 := uuid.New(), uuid.New(), uuid.New()

	repo := &mockRepo{artistID: artistID, count: 3}
	svc := NewService(repo)

	req := ReorderRequest{
		IDs: []string{id3.String(), id1.String(), id2.String()},
	}

	err := svc.Reorder(context.Background(), userID, req)
	assert.NoError(t, err)
}

func TestReorder_WrongCount_ReturnsError(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	repo := &mockRepo{artistID: artistID, count: 3}
	svc := NewService(repo)

	// Only 2 IDs but artist has 3 photos
	req := ReorderRequest{
		IDs: []string{uuid.New().String(), uuid.New().String()},
	}

	err := svc.Reorder(context.Background(), userID, req)
	assert.Error(t, err)
}

func TestReorder_InvalidUUID_ReturnsError(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	repo := &mockRepo{artistID: artistID, count: 1}
	svc := NewService(repo)

	req := ReorderRequest{IDs: []string{"not-a-uuid"}}

	err := svc.Reorder(context.Background(), userID, req)
	assert.Error(t, err)
}
