package review

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// ── TestMain ──────────────────────────────────────────────────────────────────

func TestMain(m *testing.M) {
	os.Setenv("JWT_SECRET", "test-secret-that-is-long-enough-for-testing-purposes-only")
	os.Setenv("JWT_REFRESH_SECRET", "test-refresh-secret-that-is-long-enough-for-testing-only")
	os.Setenv("APP_ENV", "test")
	os.Exit(m.Run())
}

// ── Mock repository ───────────────────────────────────────────────────────────

type mockRepo struct {
	createReviewErr         error
	getReviewByBookingIDRev *Review
	getReviewByBookingIDErr error
	getReviewByIDRev        *Review
	getReviewByIDErr        error
	getReviewsByArtistRevs  []*Review
	getReviewsByArtistErr   error
	deleteReviewErr         error
	setVisibilityErr        error
	bookingStatus           string
	bookingCustomerID       uuid.UUID
	bookingArtistID         uuid.UUID
	bookingStatusErr        error
}

func (m *mockRepo) CreateReview(_ context.Context, r *Review) error {
	r.CreatedAt = time.Now()
	return m.createReviewErr
}
func (m *mockRepo) GetReviewByBookingID(_ context.Context, _ uuid.UUID) (*Review, error) {
	return m.getReviewByBookingIDRev, m.getReviewByBookingIDErr
}
func (m *mockRepo) GetReviewByID(_ context.Context, _ uuid.UUID) (*Review, error) {
	return m.getReviewByIDRev, m.getReviewByIDErr
}
func (m *mockRepo) GetReviewsByArtist(_ context.Context, _ uuid.UUID) ([]*Review, error) {
	return m.getReviewsByArtistRevs, m.getReviewsByArtistErr
}
func (m *mockRepo) DeleteReview(_ context.Context, _ uuid.UUID) error {
	return m.deleteReviewErr
}
func (m *mockRepo) SetVisibility(_ context.Context, _ uuid.UUID, _ bool) error {
	return m.setVisibilityErr
}
func (m *mockRepo) GetBookingStatus(_ context.Context, _ uuid.UUID) (string, uuid.UUID, uuid.UUID, error) {
	return m.bookingStatus, m.bookingCustomerID, m.bookingArtistID, m.bookingStatusErr
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func newTestService(repo Repository) *Service {
	return NewService(repo)
}

func validCreateReviewReq(bookingID uuid.UUID) CreateReviewRequest {
	comment := "Amazing work, highly recommend!"
	return CreateReviewRequest{
		BookingID: bookingID.String(),
		Rating:    5,
		Comment:   &comment,
	}
}

func defaultReview(customerID, artistID uuid.UUID) *Review {
	comment := "Great service"
	return &Review{
		ID:         uuid.New(),
		BookingID:  uuid.New(),
		CustomerID: customerID,
		ArtistID:   artistID,
		Rating:     5,
		Comment:    &comment,
		IsVisible:  true,
		CreatedAt:  time.Now(),
	}
}

func isAppErr(err error, code string) bool {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.Code == code
	}
	return false
}

// ── CreateReview tests ────────────────────────────────────────────────────────

func TestCreateReview_Success(t *testing.T) {
	customerID := uuid.New()
	artistID := uuid.New()
	bookingID := uuid.New()

	repo := &mockRepo{
		bookingStatus:           "completed",
		bookingCustomerID:       customerID,
		bookingArtistID:         artistID,
		getReviewByBookingIDErr: ErrReviewNotFound, // not yet reviewed
		createReviewErr:         nil,
	}
	svc := newTestService(repo)

	result, err := svc.CreateReview(context.Background(), validCreateReviewReq(bookingID), customerID)

	require.NoError(t, err)
	assert.Equal(t, 5, result.Rating)
	assert.Equal(t, customerID, result.CustomerID)
	assert.Equal(t, artistID, result.ArtistID)
}

func TestCreateReview_BookingNotCompleted(t *testing.T) {
	customerID := uuid.New()
	bookingID := uuid.New()

	repo := &mockRepo{
		bookingStatus:     "confirmed", // not completed
		bookingCustomerID: customerID,
		bookingArtistID:   uuid.New(),
	}
	svc := newTestService(repo)

	result, err := svc.CreateReview(context.Background(), validCreateReviewReq(bookingID), customerID)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, isAppErr(err, "BOOKING_NOT_COMPLETED"))
}

func TestCreateReview_NotBookingOwner(t *testing.T) {
	bookingID := uuid.New()

	repo := &mockRepo{
		bookingStatus:     "completed",
		bookingCustomerID: uuid.New(), // different customer
		bookingArtistID:   uuid.New(),
	}
	svc := newTestService(repo)

	// Different customer trying to review someone else's booking
	result, err := svc.CreateReview(context.Background(), validCreateReviewReq(bookingID), uuid.New())

	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, isAppErr(err, "NOT_BOOKING_OWNER"))
}

func TestCreateReview_AlreadyReviewed(t *testing.T) {
	customerID := uuid.New()
	artistID := uuid.New()
	bookingID := uuid.New()

	repo := &mockRepo{
		bookingStatus:           "completed",
		bookingCustomerID:       customerID,
		bookingArtistID:         artistID,
		getReviewByBookingIDRev: defaultReview(customerID, artistID), // review exists
		getReviewByBookingIDErr: nil,
	}
	svc := newTestService(repo)

	result, err := svc.CreateReview(context.Background(), validCreateReviewReq(bookingID), customerID)

	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, isAppErr(err, "ALREADY_REVIEWED"))
}

func TestCreateReview_InvalidRating(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	req := CreateReviewRequest{
		BookingID: uuid.New().String(),
		Rating:    6, // invalid — max is 5
	}

	result, err := svc.CreateReview(context.Background(), req, uuid.New())

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestCreateReview_BookingNotFound(t *testing.T) {
	repo := &mockRepo{
		bookingStatusErr: errors.New("booking not found"),
	}
	svc := newTestService(repo)

	result, err := svc.CreateReview(context.Background(), validCreateReviewReq(uuid.New()), uuid.New())

	require.Error(t, err)
	assert.Nil(t, result)
	assert.True(t, isAppErr(err, "BOOKING_NOT_FOUND"))
}

// ── GetReviewsByArtist tests ──────────────────────────────────────────────────

func TestGetReviewsByArtist_Success(t *testing.T) {
	artistID := uuid.New()
	reviews := []*Review{
		defaultReview(uuid.New(), artistID),
		defaultReview(uuid.New(), artistID),
	}

	repo := &mockRepo{
		getReviewsByArtistRevs: reviews,
	}
	svc := newTestService(repo)

	result, err := svc.GetReviewsByArtist(context.Background(), artistID)

	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestGetReviewsByArtist_Empty(t *testing.T) {
	repo := &mockRepo{
		getReviewsByArtistRevs: nil,
	}
	svc := newTestService(repo)

	result, err := svc.GetReviewsByArtist(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Empty(t, result)
}

// ── DeleteReview tests ────────────────────────────────────────────────────────

func TestDeleteReview_OwnerSuccess(t *testing.T) {
	customerID := uuid.New()
	rev := defaultReview(customerID, uuid.New())

	repo := &mockRepo{
		getReviewByIDRev: rev,
		deleteReviewErr:  nil,
	}
	svc := newTestService(repo)

	err := svc.DeleteReview(context.Background(), rev.ID, customerID, "customer")

	require.NoError(t, err)
}

func TestDeleteReview_AdminSuccess(t *testing.T) {
	rev := defaultReview(uuid.New(), uuid.New())

	repo := &mockRepo{
		getReviewByIDRev: rev,
		deleteReviewErr:  nil,
	}
	svc := newTestService(repo)

	// Admin can delete any review
	err := svc.DeleteReview(context.Background(), rev.ID, uuid.New(), "admin")

	require.NoError(t, err)
}

func TestDeleteReview_NotOwner(t *testing.T) {
	rev := defaultReview(uuid.New(), uuid.New())

	repo := &mockRepo{
		getReviewByIDRev: rev,
	}
	svc := newTestService(repo)

	// Different customer — not the owner
	err := svc.DeleteReview(context.Background(), rev.ID, uuid.New(), "customer")

	require.Error(t, err)
	assert.True(t, isAppErr(err, "NOT_REVIEW_OWNER"))
}

func TestDeleteReview_NotFound(t *testing.T) {
	repo := &mockRepo{
		getReviewByIDErr: ErrReviewNotFound,
	}
	svc := newTestService(repo)

	err := svc.DeleteReview(context.Background(), uuid.New(), uuid.New(), "customer")

	require.Error(t, err)
	assert.True(t, isAppErr(err, "REVIEW_NOT_FOUND"))
}

// ── HideReview tests ──────────────────────────────────────────────────────────

func TestHideReview_Success(t *testing.T) {
	artistID := uuid.New()
	rev := defaultReview(uuid.New(), artistID)

	repo := &mockRepo{
		getReviewByIDRev: rev,
		setVisibilityErr: nil,
	}
	svc := newTestService(repo)

	err := svc.HideReview(context.Background(), rev.ID, artistID)

	require.NoError(t, err)
}

func TestHideReview_NotArtistOwner(t *testing.T) {
	rev := defaultReview(uuid.New(), uuid.New())

	repo := &mockRepo{
		getReviewByIDRev: rev,
	}
	svc := newTestService(repo)

	// Different artist — not the one being reviewed
	err := svc.HideReview(context.Background(), rev.ID, uuid.New())

	require.Error(t, err)
	assert.True(t, isAppErr(err, "FORBIDDEN"))
}
