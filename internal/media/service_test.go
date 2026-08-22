// Package media implements the media/portfolio domain for B-Edge,
// providing portfolio photo management for artist profiles.
package media

import (
	"context"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
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

	// Product gallery - separate from the artist-portfolio fields above so
	// a test can give the two paths genuinely different data.
	productItems      []*MediaItem
	productCount      int
	productCountErr   error
	productSalonID    uuid.UUID
	productSalonErr   error
	productReorderErr error
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

func (m *mockRepo) ListByProduct(_ context.Context, _ uuid.UUID) ([]*MediaItem, error) {
	return m.productItems, nil
}

func (m *mockRepo) CountByProduct(_ context.Context, _ uuid.UUID) (int, error) {
	return m.productCount, m.productCountErr
}

func (m *mockRepo) ReorderProduct(_ context.Context, _ uuid.UUID, _ []uuid.UUID) error {
	return m.productReorderErr
}

func (m *mockRepo) GetProductSalonID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return m.productSalonID, m.productSalonErr
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

// ── Cross-tenant authorization tests ─────────────────────────────────────────
//
// Portfolio photos are owned by an artist. These lock in the OwnerID checks
// so a future refactor can't drop them - without one, any artist could delete
// a competitor's portfolio photos or hijack their cover image.

func TestDeletePhoto_OtherArtistsPhoto_Denied(t *testing.T) {
	mediaID := uuid.New()
	attackerArtistID := uuid.New()
	victimArtistID := uuid.New()

	repo := &mockRepo{
		artistID:    attackerArtistID,
		getByIDItem: &MediaItem{ID: mediaID, OwnerID: victimArtistID},
	}
	svc := NewService(repo)

	err := svc.DeletePhoto(context.Background(), uuid.New(), mediaID)

	assert.Error(t, err, "an artist must not be able to delete another artist's photo")
}

func TestSetCover_OtherArtistsPhoto_Denied(t *testing.T) {
	mediaID := uuid.New()

	repo := &mockRepo{
		artistID:    uuid.New(),
		getByIDItem: &MediaItem{ID: mediaID, OwnerID: uuid.New()},
	}
	svc := NewService(repo)

	err := svc.SetCover(context.Background(), uuid.New(), mediaID)

	assert.Error(t, err, "an artist must not be able to set another artist's photo as a cover")
}

// ── Product gallery tests ───────────────────────────────────────────────────
//
// A product's own image_url is untouched by any of this - only the
// ADDITIONAL gallery photos. Ownership here is salon-based, not the
// artist-based OwnerID check above, since one salon owns many products.

func makeProductItem(productID uuid.UUID, order int) *MediaItem {
	return &MediaItem{
		ID:           uuid.New(),
		OwnerType:    OwnerTypeProduct,
		OwnerID:      productID,
		URL:          "https://res.cloudinary.com/bedge/image/upload/v1/product.jpg",
		Type:         MediaTypePhoto,
		DisplayOrder: order,
		CreatedAt:    time.Now(),
	}
}

func TestGetProductPhotos_ReturnsGallery(t *testing.T) {
	productID := uuid.New()
	items := []*MediaItem{makeProductItem(productID, 0), makeProductItem(productID, 1)}

	repo := &mockRepo{productItems: items}
	svc := NewService(repo)

	result, err := svc.GetProductPhotos(context.Background(), productID)
	require.NoError(t, err)
	assert.Equal(t, productID, result.ProductID)
	assert.Len(t, result.Photos, 2)
	assert.Equal(t, MaxProductPhotos, result.MaxAllowed)
}

func TestAddProductPhoto_OwningSalon_Succeeds(t *testing.T) {
	salonID := uuid.New()
	productID := uuid.New()

	repo := &mockRepo{productSalonID: salonID, productCount: 2}
	svc := NewService(repo)

	res, err := svc.AddProductPhoto(context.Background(), productID, salonID,
		AddMediaRequest{URL: "https://res.cloudinary.com/bedge/image/upload/v1/angle2.jpg"})

	require.NoError(t, err)
	assert.Equal(t, 2, res.DisplayOrder, "must append at the current count")
}

func TestAddProductPhoto_OtherSalonsProduct_Denied(t *testing.T) {
	repo := &mockRepo{productSalonID: uuid.New()} // owned by a DIFFERENT salon
	svc := NewService(repo)

	_, err := svc.AddProductPhoto(context.Background(), uuid.New(), uuid.New(),
		AddMediaRequest{URL: "https://res.cloudinary.com/bedge/image/upload/v1/x.jpg"})

	assert.Error(t, err, "an artist must not add photos to another salon's product")
}

func TestAddProductPhoto_GalleryFull_Returns409(t *testing.T) {
	salonID := uuid.New()
	repo := &mockRepo{productSalonID: salonID, productCount: MaxProductPhotos}
	svc := NewService(repo)

	_, err := svc.AddProductPhoto(context.Background(), uuid.New(), salonID,
		AddMediaRequest{URL: "https://res.cloudinary.com/bedge/image/upload/v1/one-too-many.jpg"})

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, fiber.StatusConflict, appErr.HTTPStatus)
}

func TestDeleteProductPhoto_OwningSalon_Succeeds(t *testing.T) {
	salonID := uuid.New()
	productID := uuid.New()
	mediaID := uuid.New()

	repo := &mockRepo{
		getByIDItem:    &MediaItem{ID: mediaID, OwnerType: OwnerTypeProduct, OwnerID: productID},
		productSalonID: salonID,
	}
	svc := NewService(repo)

	err := svc.DeleteProductPhoto(context.Background(), mediaID, salonID)
	assert.NoError(t, err)
}

func TestDeleteProductPhoto_OtherSalonsProduct_Denied(t *testing.T) {
	mediaID := uuid.New()
	productID := uuid.New()

	repo := &mockRepo{
		getByIDItem:    &MediaItem{ID: mediaID, OwnerType: OwnerTypeProduct, OwnerID: productID},
		productSalonID: uuid.New(), // the product's REAL salon
	}
	svc := NewService(repo)

	err := svc.DeleteProductPhoto(context.Background(), mediaID, uuid.New()) // a different, attacking salon

	assert.Error(t, err, "an artist must not delete another salon's product photo")
}

func TestDeleteProductPhoto_ArtistPortfolioPhoto_NotFound(t *testing.T) {
	// A portfolio photo's ID fed into the PRODUCT delete endpoint must not
	// be treated as a product photo just because the ID happens to exist.
	mediaID := uuid.New()
	repo := &mockRepo{
		getByIDItem: &MediaItem{ID: mediaID, OwnerType: OwnerTypeArtist, OwnerID: uuid.New()},
	}
	svc := NewService(repo)

	err := svc.DeleteProductPhoto(context.Background(), mediaID, uuid.New())

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, fiber.StatusNotFound, appErr.HTTPStatus)
}

func TestReorderProductPhotos_WrongCount_ReturnsError(t *testing.T) {
	salonID := uuid.New()
	repo := &mockRepo{productSalonID: salonID, productCount: 3}
	svc := NewService(repo)

	err := svc.ReorderProductPhotos(context.Background(), uuid.New(), salonID,
		ReorderRequest{IDs: []string{uuid.New().String()}}) // only 1, product has 3

	assert.Error(t, err)
}

func TestReorderProductPhotos_OtherSalonsProduct_Denied(t *testing.T) {
	repo := &mockRepo{productSalonID: uuid.New()}
	svc := NewService(repo)

	err := svc.ReorderProductPhotos(context.Background(), uuid.New(), uuid.New(),
		ReorderRequest{IDs: []string{uuid.New().String()}})

	assert.Error(t, err)
}
