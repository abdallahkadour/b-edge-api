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

	// Service tags (migration 028)
	salonID           uuid.UUID
	salonErr          error
	salonServiceCount int
	salonServiceErr   error
	setServicesErr    error
	serviceIDsByMedia map[uuid.UUID][]uuid.UUID
	serviceIDsErr     error
	// captured args for assertions
	lastSetServicesMediaID uuid.UUID
	lastSetServiceIDs      []uuid.UUID
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
func (m *mockRepo) GetSalonIDByArtistID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return m.salonID, m.salonErr
}
func (m *mockRepo) CountSalonServices(_ context.Context, _ uuid.UUID, _ []uuid.UUID) (int, error) {
	return m.salonServiceCount, m.salonServiceErr
}
func (m *mockRepo) SetMediaServices(_ context.Context, mediaID uuid.UUID, serviceIDs []uuid.UUID) error {
	m.lastSetServicesMediaID = mediaID
	m.lastSetServiceIDs = serviceIDs
	return m.setServicesErr
}
func (m *mockRepo) GetServiceIDsByMedia(_ context.Context, _ []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	return m.serviceIDsByMedia, m.serviceIDsErr
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

// ── SetMediaServices (migration 028) ──────────────────────────────────────────
//
// Three ownership facts must line up: the caller is an artist, the photo is
// theirs and is artist-owned, and every service belongs to their salon.
// None of that is expressible as a database constraint, so each rule gets
// its own test.

func artistPhoto(artistID uuid.UUID) *MediaItem {
	return &MediaItem{
		ID: uuid.New(), OwnerType: OwnerTypeArtist, OwnerID: artistID,
		URL: "https://res.cloudinary.com/x/a.jpg", Type: "photo",
	}
}

func TestSetMediaServices_Success(t *testing.T) {
	artistID := uuid.New()
	photo := artistPhoto(artistID)
	svcID := uuid.New()
	repo := &mockRepo{
		artistID: artistID, getByIDItem: photo,
		salonID: uuid.New(), salonServiceCount: 1,
	}
	svc := NewService(repo)

	got, err := svc.SetMediaServices(context.Background(), uuid.New(), photo.ID,
		SetMediaServicesRequest{ServiceIDs: []string{svcID.String()}})

	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, photo.ID, repo.lastSetServicesMediaID)
	assert.Equal(t, []uuid.UUID{svcID}, repo.lastSetServiceIDs)
	assert.Equal(t, []uuid.UUID{svcID}, got.ServiceIDs)
}

// An empty list is how a photo gets untagged - it must reach the repository
// as a real (empty) replace, not be skipped as a no-op.
func TestSetMediaServices_EmptyList_ClearsTags(t *testing.T) {
	artistID := uuid.New()
	photo := artistPhoto(artistID)
	repo := &mockRepo{artistID: artistID, getByIDItem: photo}
	svc := NewService(repo)

	got, err := svc.SetMediaServices(context.Background(), uuid.New(), photo.ID,
		SetMediaServicesRequest{ServiceIDs: nil})

	require.NoError(t, err)
	assert.Equal(t, photo.ID, repo.lastSetServicesMediaID, "clearing must still hit the repo")
	assert.Empty(t, got.ServiceIDs)
}

// A service belonging to someone else's salon is rejected, and the error
// deliberately does not name which one - naming it would confirm that
// another salon's service ID exists.
func TestSetMediaServices_ForeignService_Rejected(t *testing.T) {
	artistID := uuid.New()
	photo := artistPhoto(artistID)
	repo := &mockRepo{
		artistID: artistID, getByIDItem: photo,
		salonID: uuid.New(),
		// Two requested, only one belongs to this salon.
		salonServiceCount: 1,
	}
	svc := NewService(repo)

	_, err := svc.SetMediaServices(context.Background(), uuid.New(), photo.ID,
		SetMediaServicesRequest{ServiceIDs: []string{uuid.NewString(), uuid.NewString()}})

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "INVALID_SERVICE_ID", appErr.Code)
	assert.NotContains(t, appErr.Message, "does not exist",
		"the message must not reveal whether a foreign service ID is real")
	assert.Nil(t, repo.lastSetServiceIDs, "nothing may be written when validation fails")
}

// Another artist's photo is a 404, not a 403 - same posture as billing
// invoices, so IDs cannot be enumerated by watching the status code.
func TestSetMediaServices_AnotherArtistsPhoto_Returns404(t *testing.T) {
	callerArtistID := uuid.New()
	someoneElses := artistPhoto(uuid.New())
	repo := &mockRepo{artistID: callerArtistID, getByIDItem: someoneElses}
	svc := NewService(repo)

	_, err := svc.SetMediaServices(context.Background(), uuid.New(), someoneElses.ID,
		SetMediaServicesRequest{})

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "MEDIA_NOT_FOUND", appErr.Code)
	assert.Equal(t, fiber.StatusNotFound, appErr.HTTPStatus)
}

// A product photo is not taggable to a service - it shows merchandise, not
// a service being performed.
func TestSetMediaServices_ProductPhoto_Rejected(t *testing.T) {
	artistID := uuid.New()
	productPhoto := &MediaItem{
		ID: uuid.New(), OwnerType: OwnerTypeProduct, OwnerID: uuid.New(),
		URL: "https://res.cloudinary.com/x/p.jpg", Type: "photo",
	}
	repo := &mockRepo{artistID: artistID, getByIDItem: productPhoto}
	svc := NewService(repo)

	_, err := svc.SetMediaServices(context.Background(), uuid.New(), productPhoto.ID,
		SetMediaServicesRequest{})

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "MEDIA_NOT_FOUND", appErr.Code)
}

func TestSetMediaServices_MissingPhoto_Returns404(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), getByIDErr: ErrMediaNotFound}
	svc := NewService(repo)

	_, err := svc.SetMediaServices(context.Background(), uuid.New(), uuid.New(),
		SetMediaServicesRequest{})

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "MEDIA_NOT_FOUND", appErr.Code)
}

// ── Portfolio tags on read ────────────────────────────────────────────────────

// ServiceIDs must always be a non-nil slice so JSON carries [] rather than
// null - clients filter on it without a nil check.
func TestGetPortfolio_UntaggedPhoto_HasEmptyNotNullServiceIDs(t *testing.T) {
	artistID := uuid.New()
	repo := &mockRepo{items: []*MediaItem{artistPhoto(artistID)}}
	svc := NewService(repo)

	got, err := svc.GetPortfolio(context.Background(), artistID)

	require.NoError(t, err)
	require.Len(t, got.Photos, 1)
	assert.NotNil(t, got.Photos[0].ServiceIDs, "must be [] in JSON, never null")
	assert.Empty(t, got.Photos[0].ServiceIDs)
}

func TestGetPortfolio_TaggedPhoto_CarriesItsServiceIDs(t *testing.T) {
	artistID := uuid.New()
	photo := artistPhoto(artistID)
	svcID := uuid.New()
	repo := &mockRepo{
		items:             []*MediaItem{photo},
		serviceIDsByMedia: map[uuid.UUID][]uuid.UUID{photo.ID: {svcID}},
	}
	svc := NewService(repo)

	got, err := svc.GetPortfolio(context.Background(), artistID)

	require.NoError(t, err)
	require.Len(t, got.Photos, 1)
	assert.Equal(t, []uuid.UUID{svcID}, got.Photos[0].ServiceIDs)
}

// Tags for a whole portfolio are loaded in one query, not one per photo.
func TestGetPortfolio_ManyPhotos_LoadsTagsInOneCall(t *testing.T) {
	artistID := uuid.New()
	items := []*MediaItem{artistPhoto(artistID), artistPhoto(artistID), artistPhoto(artistID)}
	repo := &countingTagRepo{mockRepo: mockRepo{items: items}}
	svc := NewService(repo)

	_, err := svc.GetPortfolio(context.Background(), artistID)

	require.NoError(t, err)
	assert.Equal(t, 1, repo.tagCalls, "a 20-photo portfolio must not issue 20 tag queries")
}

// countingTagRepo counts GetServiceIDsByMedia calls to prove batching.
type countingTagRepo struct {
	mockRepo
	tagCalls int
}

func (c *countingTagRepo) GetServiceIDsByMedia(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	c.tagCalls++
	return c.mockRepo.GetServiceIDsByMedia(ctx, ids)
}
