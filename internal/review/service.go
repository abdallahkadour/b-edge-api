// Package review implements the review domain for B-Edge.
package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// completedStatus is the only booking status that allows a review.
const completedStatus = "completed"

// Service handles all review business logic.
type Service struct {
	repo     Repository
	validate *validator.Validate
}

// NewService creates a new review Service.
func NewService(repo Repository) *Service {
	return &Service{
		repo:     repo,
		validate: validator.New(),
	}
}

// CreateReview submits a review for a completed booking.
// Rules enforced:
//  1. Booking must exist and be completed
//  2. Only the customer on the booking can review it
//  3. One review per booking — cannot review twice
func (s *Service) CreateReview(ctx context.Context, req CreateReviewRequest, customerID uuid.UUID) (*ReviewResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	bookingID, err := uuid.Parse(req.BookingID)
	if err != nil {
		return nil, apperror.BadRequest("INVALID_BOOKING_ID", "Invalid booking ID")
	}

	// Step 1: Verify booking exists, is completed, and belongs to this customer
	status, bookingCustomerID, artistID, err := s.repo.GetBookingStatus(ctx, bookingID)
	if err != nil {
		return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
	}

	if status != completedStatus {
		return nil, apperror.Conflict("BOOKING_NOT_COMPLETED", "You can only review completed appointments")
	}

	if bookingCustomerID != customerID {
		return nil, apperror.Forbidden("NOT_BOOKING_OWNER", "You can only review your own appointments")
	}

	// Step 2: Check not already reviewed
	_, err = s.repo.GetReviewByBookingID(ctx, bookingID)
	if err == nil {
		// Review exists — already reviewed
		return nil, apperror.Conflict("ALREADY_REVIEWED", "You have already reviewed this appointment")
	}
	if !errors.Is(err, ErrReviewNotFound) {
		return nil, fmt.Errorf("create review: check existing: %w", err)
	}

	// Step 3: Create the review
	rev := &Review{
		ID:         uuid.New(),
		BookingID:  bookingID,
		CustomerID: customerID,
		ArtistID:   artistID,
		Rating:     req.Rating,
		Comment:    req.Comment,
		IsVisible:  true,
	}

	if err := s.repo.CreateReview(ctx, rev); err != nil {
		if errors.Is(err, ErrAlreadyReviewed) {
			return nil, apperror.Conflict("ALREADY_REVIEWED", "You have already reviewed this appointment")
		}
		return nil, fmt.Errorf("create review: %w", err)
	}

	return toResponse(rev), nil
}

// GetReviewsByArtist returns all visible reviews for an artist.
func (s *Service) GetReviewsByArtist(ctx context.Context, artistID uuid.UUID) ([]*ReviewResponse, error) {
	reviews, err := s.repo.GetReviewsByArtist(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("get reviews by artist: %w", err)
	}

	var result []*ReviewResponse
	for _, r := range reviews {
		result = append(result, toResponse(r))
	}
	return result, nil
}

// DeleteReview permanently removes a review.
// Only the review owner (customer) or an admin can delete.
func (s *Service) DeleteReview(ctx context.Context, reviewID uuid.UUID, requesterID uuid.UUID, requesterRole string) error {
	rev, err := s.repo.GetReviewByID(ctx, reviewID)
	if err != nil {
		if errors.Is(err, ErrReviewNotFound) {
			return apperror.NotFound("REVIEW_NOT_FOUND", "Review not found")
		}
		return fmt.Errorf("delete review: get review: %w", err)
	}

	// Only the customer who wrote it or an admin can delete
	if requesterRole != "admin" && rev.CustomerID != requesterID {
		return apperror.Forbidden("NOT_REVIEW_OWNER", "You do not have permission to delete this review")
	}

	return s.repo.DeleteReview(ctx, reviewID)
}

// HideReview hides a review from public view.
// Artists can hide reviews on their own profile.
func (s *Service) HideReview(ctx context.Context, reviewID uuid.UUID, artistID uuid.UUID) error {
	rev, err := s.repo.GetReviewByID(ctx, reviewID)
	if err != nil {
		if errors.Is(err, ErrReviewNotFound) {
			return apperror.NotFound("REVIEW_NOT_FOUND", "Review not found")
		}
		return fmt.Errorf("hide review: get review: %w", err)
	}

	if rev.ArtistID != artistID {
		return apperror.Forbidden("FORBIDDEN", "You do not have permission to hide this review")
	}

	return s.repo.SetVisibility(ctx, reviewID, false)
}

// ── Private helpers ───────────────────────────────────────────────────────────

func toResponse(r *Review) *ReviewResponse {
	return &ReviewResponse{
		ID:         r.ID,
		BookingID:  r.BookingID,
		CustomerID: r.CustomerID,
		ArtistID:   r.ArtistID,
		Rating:     r.Rating,
		Comment:    r.Comment,
		CreatedAt:  r.CreatedAt,
	}
}

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

func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + " is required"
	case "min":
		return fe.Field() + " must be at least " + fe.Param()
	case "max":
		return fe.Field() + " must be at most " + fe.Param() + " characters"
	case "uuid":
		return fe.Field() + " must be a valid UUID"
	default:
		return fe.Field() + " is invalid"
	}
}
