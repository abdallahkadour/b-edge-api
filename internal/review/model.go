// Package review implements the review domain for B-Edge,
// including review submission, retrieval, and moderation.
package review

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
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

	// ErrArtistNotFound is returned when a user_id does not resolve to an artist
	// profile — used when authorising artist-only moderation actions.
	ErrArtistNotFound = errors.New("artist profile not found for user")

	// ErrInvalidReviewToken is returned when a review link's token doesn't
	// match any booking — either it was mistyped, or the booking doesn't
	// have one (not completed yet, or predates the review-link feature).
	ErrInvalidReviewToken = errors.New("invalid or expired review link")
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

	// StoreID is the venue the visit happened at, copied from the booking.
	// Nullable: reviews predating migration 035 have none.
	StoreID *uuid.UUID `db:"store_id"`

	// SalonRating rates the VENUE and is deliberately independent of Rating,
	// which rates the specialist. Optional - a review that answers only the
	// specialist question is complete, and forcing the second question is how
	// response rates fall (assessment §2.2 on survey fatigue).
	//
	// Never derive one from the other. "Salon rating = average of its
	// stylists" is the gaming vector §2.2 names: an owner inflates the venue
	// by hiring one star.
	SalonRating *int `db:"salon_rating"`
}

// BookingAttribution is what a review needs to know about the booking it is
// rating: who may submit it, which specialist it scores, and which VENUE.
//
// StoreID is a pointer because bookings.store_id is nullable in principle,
// and a review with no venue simply carries no salon rating rather than
// failing - losing a specialist rating because the room is unknown would be
// the worse outcome.
type BookingAttribution struct {
	Status     string
	CustomerID uuid.UUID
	ArtistID   uuid.UUID
	StoreID    *uuid.UUID
}

// ── Request structs ───────────────────────────────────────────────────────────

// CreateReviewRequest is the request body for POST /api/v1/reviews.
type CreateReviewRequest struct {
	BookingID string `json:"booking_id" validate:"required,uuid"`
	Rating    int    `json:"rating"     validate:"required,min=1,max=5"`
	// SalonRating is OPTIONAL - omitting it submits a specialist-only review,
	// which is a complete review. See Review.SalonRating.
	SalonRating *int    `json:"salon_rating" validate:"omitempty,min=1,max=5"`
	Comment     *string `json:"comment"      validate:"omitempty,max=1000"`
}

// SubmitReviewByTokenRequest is the request body for
// POST /api/v1/reviews/by-token/:token — the guest review-link flow.
// No booking_id: the token itself resolves to exactly one booking, so
// including it in the body would be redundant and would let a caller send
// a mismatched token/booking_id pair with no clear rule for which wins.
type SubmitReviewByTokenRequest struct {
	Rating int `json:"rating" validate:"required,min=1,max=5"`
	// Optional, as on CreateReviewRequest.
	SalonRating *int    `json:"salon_rating" validate:"omitempty,min=1,max=5"`
	Comment     *string `json:"comment"      validate:"omitempty,max=1000"`
}

// ReviewBookingContext is the booking summary shown on the review-link
// landing screen before submission — "Your Booking: Bridal Makeup with
// Rania, Mon 23 June, $200" — resolved from the token with no auth. Holds
// only what's needed to render that confirmation card; not a full booking
// representation.
type ReviewBookingContext struct {
	ServiceName string          `json:"service_name"`
	ArtistName  string          `json:"artist_name"`
	StoreName   string          `json:"store_name"`
	StartTime   time.Time       `json:"start_time"`
	FinalPrice  decimal.Decimal `json:"final_price"`
	// AlreadyReviewed lets the landing screen show its "submitted" state
	// immediately on load, rather than only after a second submit attempt
	// bounces off the 409 CreateReviewByToken already returns for a
	// duplicate. The review link isn't single-use - review_token is never
	// cleared after a successful review - so without this, reopening or
	// reloading a link you already used re-shows a blank, fully-editable
	// form with no indication anything happened.
	AlreadyReviewed bool `json:"already_reviewed"`
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
	// IsVisible: irrelevant on the public endpoint (only visible reviews are
	// ever returned there), but load-bearing on the artist's own moderation
	// view (GetReviewsByArtist) - without it, a hide/show toggle in the UI
	// has no way to know which state a review is actually in.
	IsVisible bool      `json:"is_visible"`
	CreatedAt time.Time `json:"created_at"`

	// SalonRating is omitted entirely when the reviewer did not answer the
	// venue question - so a consumer renders "not rated" rather than a zero.
	SalonRating *int       `json:"salon_rating,omitempty"`
	StoreID     *uuid.UUID `json:"store_id,omitempty"`
}

// EnrichedReviewResponse is the PUBLIC-facing shape - it adds a display
// name so a review reads as coming from a real person, not a UUID. This is
// the same enrichment lesson learned twice already elsewhere in this
// codebase (EnrichedBookingResponse needed artist_name, EnrichedOrderResponse
// needed customer_name) - a customer-facing list with no name attached is
// barely usable for whoever's reading it.
//
// ReviewerName is deliberately NOT the customer's full stored name.
// Unlike EnrichedBookingResponse (seen only by the artist who owns that
// booking) or EnrichedOrderResponse (same), this response is PUBLIC - shown
// to any anonymous visitor deciding whether to book. Showing a stranger's
// full name next to their comment is a real privacy exposure the other two
// cases don't have. Formatted server-side as "first name + last initial"
// (e.g. "Sarah K."), a standard pattern deliberately chosen over showing
// nothing (which reads as fake/generated) or the full name (which doesn't).
type EnrichedReviewResponse struct {
	ReviewResponse
	ReviewerName string `json:"reviewer_name"`
}

// (SalonRating is inherited from ReviewResponse and populated by the same
// query - the public list carries both scores so a customer can see the venue
// and the specialist rated separately, which is the whole point of collecting
// two numbers.)
