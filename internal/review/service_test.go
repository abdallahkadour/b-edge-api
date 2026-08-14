// Package review contains unit tests for the review service layer.
// These tests use a mock repository — no database required.
package review

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock repository ───────────────────────────────────────────────────────────

type mockRepo struct {
	createErr         error
	byBookingReview   *Review
	byBookingErr      error
	byIDReview        *Review
	byIDErr           error
	byArtistReviews   []*Review
	enrichedByArtistReviews []*EnrichedReviewResponse
	byArtistErr       error
	deleteErr         error
	setVisibilityErr  error
	bookingStatus     string
	bookingCustomerID uuid.UUID
	bookingArtistID   uuid.UUID
	bookingErr        error
	artistIDByUser    uuid.UUID
	artistIDByUserErr error
	// captured args for assertions
	lastDeleteArtistID uuid.UUID
	lastSetVisibility  *bool
	lastSetArtistID    uuid.UUID
	// review-link token flow
	tokenBookingID  uuid.UUID
	tokenCustomerID uuid.UUID
	tokenResolveErr error
	tokenContext    *ReviewBookingContext
	tokenContextErr error
}

func (m *mockRepo) CreateReview(_ context.Context, _ *Review) error { return m.createErr }
func (m *mockRepo) GetReviewByBookingID(_ context.Context, _ uuid.UUID) (*Review, error) {
	return m.byBookingReview, m.byBookingErr
}
func (m *mockRepo) GetReviewByID(_ context.Context, _ uuid.UUID) (*Review, error) {
	return m.byIDReview, m.byIDErr
}
func (m *mockRepo) GetReviewsByArtist(_ context.Context, _ uuid.UUID) ([]*Review, error) {
	return m.byArtistReviews, m.byArtistErr
}
func (m *mockRepo) GetEnrichedReviewsByArtist(_ context.Context, _ uuid.UUID) ([]*EnrichedReviewResponse, error) {
	return m.enrichedByArtistReviews, m.byArtistErr
}
func (m *mockRepo) GetBookingIDByReviewToken(_ context.Context, _ string) (uuid.UUID, uuid.UUID, error) {
	return m.tokenBookingID, m.tokenCustomerID, m.tokenResolveErr
}
func (m *mockRepo) GetBookingContextByToken(_ context.Context, _ string) (*ReviewBookingContext, error) {
	return m.tokenContext, m.tokenContextErr
}
func (m *mockRepo) DeleteReview(_ context.Context, _ uuid.UUID, artistID uuid.UUID) error {
	m.lastDeleteArtistID = artistID
	return m.deleteErr
}
func (m *mockRepo) SetVisibility(_ context.Context, _ uuid.UUID, artistID uuid.UUID, visible bool) error {
	m.lastSetArtistID = artistID
	m.lastSetVisibility = &visible
	return m.setVisibilityErr
}
func (m *mockRepo) GetBookingStatus(_ context.Context, _ uuid.UUID) (string, uuid.UUID, uuid.UUID, error) {
	return m.bookingStatus, m.bookingCustomerID, m.bookingArtistID, m.bookingErr
}
func (m *mockRepo) GetArtistIDByUserID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return m.artistIDByUser, m.artistIDByUserErr
}

func newTestService(repo Repository) *Service { return NewService(repo) }

func strptr(s string) *string { return &s }

// ── CreateReview tests ────────────────────────────────────────────────────────

// TestCreateReview_Success — completed booking owned by the customer, not yet
// reviewed → review created.
func TestCreateReview_Success(t *testing.T) {
	customerID := uuid.New()
	artistID := uuid.New()
	repo := &mockRepo{
		bookingStatus:     completedStatus,
		bookingCustomerID: customerID,
		bookingArtistID:   artistID,
		byBookingErr:      ErrReviewNotFound, // not yet reviewed
	}
	svc := newTestService(repo)

	res, err := svc.CreateReview(context.Background(), CreateReviewRequest{
		BookingID: uuid.New().String(),
		Rating:    5,
		Comment:   strptr("Amazing"),
	}, customerID)

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 5, res.Rating)
	assert.Equal(t, artistID, res.ArtistID)
}

// ── Guest review-link tests ─────────────────────────────────────────────────

func TestCreateReviewByToken_Success(t *testing.T) {
	customerID := uuid.New()
	artistID := uuid.New()
	bookingID := uuid.New()
	repo := &mockRepo{
		tokenBookingID:    bookingID,
		tokenCustomerID:   customerID,
		bookingStatus:     completedStatus,
		bookingCustomerID: customerID,
		bookingArtistID:   artistID,
		byBookingErr:      ErrReviewNotFound, // not yet reviewed
	}
	svc := newTestService(repo)

	res, err := svc.CreateReviewByToken(context.Background(), "a-valid-token", SubmitReviewByTokenRequest{
		Rating:  5,
		Comment: strptr("Loved it"),
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	assert.Equal(t, 5, res.Rating)
	assert.Equal(t, artistID, res.ArtistID)
}

// TestCreateReviewByToken_InvalidToken_NotFound guards the actual point of
// this feature: an unrecognised or mistyped token must fail cleanly, not
// leak whether the underlying booking exists in some other form.
func TestCreateReviewByToken_InvalidToken_NotFound(t *testing.T) {
	repo := &mockRepo{
		tokenResolveErr: ErrInvalidReviewToken,
	}
	svc := newTestService(repo)

	_, err := svc.CreateReviewByToken(context.Background(), "not-a-real-token", SubmitReviewByTokenRequest{
		Rating: 5,
	})

	assert.Error(t, err)
}

// TestCreateReviewByToken_AlreadyReviewed_Conflict — the same booking-level
// protection CreateReview already has must still apply through the token
// path, since CreateReviewByToken delegates to it rather than reimplementing
// the check.
func TestCreateReviewByToken_AlreadyReviewed_Conflict(t *testing.T) {
	customerID := uuid.New()
	repo := &mockRepo{
		tokenBookingID:    uuid.New(),
		tokenCustomerID:   customerID,
		bookingStatus:     completedStatus,
		bookingCustomerID: customerID,
		byBookingReview:   &Review{ID: uuid.New()}, // already exists
		byBookingErr:      nil,
	}
	svc := newTestService(repo)

	_, err := svc.CreateReviewByToken(context.Background(), "a-valid-token", SubmitReviewByTokenRequest{
		Rating: 4,
	})

	assert.Error(t, err)
}

// TestCreateReviewByToken_NotCompleted_Conflict — a token only exists once
// CompleteBooking generates one, so this case is defensive (a booking that
// somehow reached this path without being completed), but the check must
// still fire since CreateReviewByToken delegates to CreateReview rather than
// trusting the token's mere existence as proof of completion.
func TestCreateReviewByToken_NotCompleted_Conflict(t *testing.T) {
	customerID := uuid.New()
	repo := &mockRepo{
		tokenBookingID:    uuid.New(),
		tokenCustomerID:   customerID,
		bookingStatus:     "confirmed", // not completed
		bookingCustomerID: customerID,
	}
	svc := newTestService(repo)

	_, err := svc.CreateReviewByToken(context.Background(), "a-valid-token", SubmitReviewByTokenRequest{
		Rating: 5,
	})

	assert.Error(t, err)
}

func TestGetBookingContextByToken_Success(t *testing.T) {
	ctxRow := &ReviewBookingContext{
		ServiceName: "Bridal Makeup",
		ArtistName:  "Rania",
		StoreName:   "Beirut Downtown",
		FinalPrice:  decimal.NewFromInt(200),
	}
	repo := &mockRepo{tokenContext: ctxRow}
	svc := newTestService(repo)

	res, err := svc.GetBookingContextByToken(context.Background(), "a-valid-token")

	require.NoError(t, err)
	assert.Equal(t, "Bridal Makeup", res.ServiceName)
	assert.Equal(t, "Rania", res.ArtistName)
}

func TestGetBookingContextByToken_InvalidToken_NotFound(t *testing.T) {
	repo := &mockRepo{tokenContextErr: ErrInvalidReviewToken}
	svc := newTestService(repo)

	_, err := svc.GetBookingContextByToken(context.Background(), "not-a-real-token")

	assert.Error(t, err)
}

// TestCreateReview_NotCompleted — booking not completed → conflict.
func TestCreateReview_NotCompleted(t *testing.T) {
	customerID := uuid.New()
	repo := &mockRepo{
		bookingStatus:     "confirmed",
		bookingCustomerID: customerID,
		bookingArtistID:   uuid.New(),
	}
	svc := newTestService(repo)

	_, err := svc.CreateReview(context.Background(), CreateReviewRequest{
		BookingID: uuid.New().String(), Rating: 5,
	}, customerID)

	require.Error(t, err)
}

// TestCreateReview_NotOwner — booking belongs to another customer → forbidden.
func TestCreateReview_NotOwner(t *testing.T) {
	repo := &mockRepo{
		bookingStatus:     completedStatus,
		bookingCustomerID: uuid.New(), // someone else
		bookingArtistID:   uuid.New(),
	}
	svc := newTestService(repo)

	_, err := svc.CreateReview(context.Background(), CreateReviewRequest{
		BookingID: uuid.New().String(), Rating: 5,
	}, uuid.New())

	require.Error(t, err)
}

// TestCreateReview_AlreadyReviewed — a review already exists → conflict.
func TestCreateReview_AlreadyReviewed(t *testing.T) {
	customerID := uuid.New()
	repo := &mockRepo{
		bookingStatus:     completedStatus,
		bookingCustomerID: customerID,
		bookingArtistID:   uuid.New(),
		byBookingReview:   &Review{ID: uuid.New()}, // exists
		byBookingErr:      nil,
	}
	svc := newTestService(repo)

	_, err := svc.CreateReview(context.Background(), CreateReviewRequest{
		BookingID: uuid.New().String(), Rating: 5,
	}, customerID)

	require.Error(t, err)
}

// ── DeleteReview tests ────────────────────────────────────────────────────────

// TestDeleteReview_OwnerPassesArtistID — the owner can delete, and the artist_id
// is forwarded to the repo so the rating recompute targets the right artist.
func TestDeleteReview_OwnerPassesArtistID(t *testing.T) {
	customerID := uuid.New()
	artistID := uuid.New()
	repo := &mockRepo{
		byIDReview: &Review{ID: uuid.New(), CustomerID: customerID, ArtistID: artistID},
	}
	svc := newTestService(repo)

	err := svc.DeleteReview(context.Background(), uuid.New(), customerID, "customer")

	require.NoError(t, err)
	assert.Equal(t, artistID, repo.lastDeleteArtistID, "artist_id must be forwarded for the recompute")
}

// TestDeleteReview_NotOwner — a non-owner non-admin cannot delete.
func TestDeleteReview_NotOwner(t *testing.T) {
	repo := &mockRepo{
		byIDReview: &Review{ID: uuid.New(), CustomerID: uuid.New(), ArtistID: uuid.New()},
	}
	svc := newTestService(repo)

	err := svc.DeleteReview(context.Background(), uuid.New(), uuid.New(), "customer")

	require.Error(t, err)
}

// ── HideReview / ShowReview tests (the resolved-artist-id fix) ─────────────────

// TestHideReview_ResolvesArtistID — the requester's user_id is resolved to their
// artists.id, which matches the review's artist_id → hide succeeds and forwards
// visible=false with the correct artist_id.
func TestHideReview_ResolvesArtistID(t *testing.T) {
	artistID := uuid.New()
	userID := uuid.New()
	repo := &mockRepo{
		byIDReview:     &Review{ID: uuid.New(), ArtistID: artistID},
		artistIDByUser: artistID, // user resolves to the owning artist
	}
	svc := newTestService(repo)

	err := svc.HideReview(context.Background(), uuid.New(), userID)

	require.NoError(t, err)
	require.NotNil(t, repo.lastSetVisibility)
	assert.False(t, *repo.lastSetVisibility, "hide sets visible=false")
	assert.Equal(t, artistID, repo.lastSetArtistID)
}

// TestHideReview_WrongArtist — the requester resolves to a DIFFERENT artist than
// the review's → forbidden. (This is the case the old buggy code always hit.)
func TestHideReview_WrongArtist(t *testing.T) {
	repo := &mockRepo{
		byIDReview:     &Review{ID: uuid.New(), ArtistID: uuid.New()},
		artistIDByUser: uuid.New(), // a different artist
	}
	svc := newTestService(repo)

	err := svc.HideReview(context.Background(), uuid.New(), uuid.New())

	require.Error(t, err)
	assert.Nil(t, repo.lastSetVisibility, "visibility must not be changed on a failed auth")
}

// TestHideReview_NotAnArtist — the requester has no artist profile → forbidden.
func TestHideReview_NotAnArtist(t *testing.T) {
	repo := &mockRepo{
		byIDReview:        &Review{ID: uuid.New(), ArtistID: uuid.New()},
		artistIDByUserErr: ErrArtistNotFound,
	}
	svc := newTestService(repo)

	err := svc.HideReview(context.Background(), uuid.New(), uuid.New())

	require.Error(t, err)
}

// TestShowReview_SetsVisibleTrue — un-hide forwards visible=true.
func TestShowReview_SetsVisibleTrue(t *testing.T) {
	artistID := uuid.New()
	repo := &mockRepo{
		byIDReview:     &Review{ID: uuid.New(), ArtistID: artistID},
		artistIDByUser: artistID,
	}
	svc := newTestService(repo)

	err := svc.ShowReview(context.Background(), uuid.New(), uuid.New())

	require.NoError(t, err)
	require.NotNil(t, repo.lastSetVisibility)
	assert.True(t, *repo.lastSetVisibility, "show sets visible=true")
}

// TestGetPublicReviewsByArtist_ReturnsEnrichedData guards the actual reason
// this endpoint exists: a review with no name attached is barely usable
// for a prospective customer deciding whether to book. This is real repo
// data flowing through, not just "the method returns without error."
func TestGetPublicReviewsByArtist_ReturnsEnrichedData(t *testing.T) {
	comment := "Amazing bridal makeup, so professional"
	repo := &mockRepo{
		enrichedByArtistReviews: []*EnrichedReviewResponse{
			{
				ReviewResponse: ReviewResponse{
					ID:      uuid.New(),
					Rating:  5,
					Comment: &comment,
				},
				ReviewerName: "Sarah K.",
			},
		},
	}
	svc := newTestService(repo)

	result, err := svc.GetPublicReviewsByArtist(context.Background(), uuid.New())

	require.NoError(t, err)
	require.Len(t, result, 1)
	assert.Equal(t, "Sarah K.", result[0].ReviewerName)
	assert.Equal(t, 5, result[0].Rating)
}

func TestGetPublicReviewsByArtist_RepoError_Propagates(t *testing.T) {
	repo := &mockRepo{byArtistErr: errors.New("db down")}
	svc := newTestService(repo)

	_, err := svc.GetPublicReviewsByArtist(context.Background(), uuid.New())

	require.Error(t, err)
}
