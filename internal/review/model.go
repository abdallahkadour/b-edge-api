// Package review implements the review domain for B-Edge,
// including review submission, retrieval, and moderation.
package review

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ── Sentinel errors ───────────────────────────────────────────────────────────

var (
	// ErrReviewNotFound is returned when no review matches the given criteria.
	ErrReviewNotFound = errors.New("review not found")

	// ErrAlreadyReviewed is returned when a customer tries to review
	// a booking they have already reviewed.
	ErrAlreadyReviewed = errors.New("booking has already been reviewed")

	// ErrBookingNotCompleted is returned when a customer tries to review
	// a booking that has not been completed yet.
	ErrBookingNotCompleted = errors.New("booking must be completed before leaving a review")

	// ErrNotReviewOwner is returned when a user tries to delete
	// a review they did not write.
	ErrNotReviewOwner = errors.New("not authorised to delete this review")
)

// ── Core structs ──────────────────────────────────────────────────────────────

// Review represents a single customer review from the reviews table.
type Review struct {
	ID         uuid.UUID `db:"id"`
	BookingID  uuid.UUID `db:"booking_id"`
	CustomerID uuid.UUID `db:"customer_id"`
	ArtistID   uuid.UUID `db:"artist_id"`
	Rating     int       `db:"rating"`
	Comment    *string   `db:"comment"`
	IsVisible  bool      `db:"is_visible"`
	CreatedAt  time.Time `db:"created_at"`
}

// ── Request structs ───────────────────────────────────────────────────────────

// CreateReviewRequest is the request body for POST /api/v1/reviews.
type CreateReviewRequest struct {
	BookingID string  `json:"booking_id" validate:"required,uuid"`
	Rating    int     `json:"rating"     validate:"required,min=1,max=5"`
	Comment   *string `json:"comment"    validate:"omitempty,max=1000"`
}

// ── Response structs ──────────────────────────────────────────────────────────

// ReviewResponse is the safe representation of a review returned to clients.
type ReviewResponse struct {
	ID         uuid.UUID `json:"id"`
	BookingID  uuid.UUID `json:"booking_id"`
	CustomerID uuid.UUID `json:"customer_id"`
	ArtistID   uuid.UUID `json:"artist_id"`
	Rating     int       `json:"rating"`
	Comment    *string   `json:"comment,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}
