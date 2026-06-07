// Package review implements the review domain for B-Edge.
package review

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository defines all database operations for the review domain.
type Repository interface {
	// CreateReview inserts a new review.
	// Returns ErrAlreadyReviewed if the booking already has a review.
	CreateReview(ctx context.Context, r *Review) error

	// GetReviewByBookingID returns the review for a specific booking.
	// Returns ErrReviewNotFound if none exists.
	GetReviewByBookingID(ctx context.Context, bookingID uuid.UUID) (*Review, error)

	// GetReviewByID returns a review by primary key.
	// Returns ErrReviewNotFound if not found.
	GetReviewByID(ctx context.Context, reviewID uuid.UUID) (*Review, error)

	// GetReviewsByArtist returns all visible reviews for an artist.
	GetReviewsByArtist(ctx context.Context, artistID uuid.UUID) ([]*Review, error)

	// DeleteReview permanently removes a review.
	// Used by admins and review owners only.
	DeleteReview(ctx context.Context, reviewID uuid.UUID) error

	// SetVisibility shows or hides a review.
	// Used by artists to hide reviews from their public profile.
	SetVisibility(ctx context.Context, reviewID uuid.UUID, visible bool) error

	// GetBookingStatus returns the status of a booking.
	// Used to verify the booking is completed before allowing a review.
	GetBookingStatus(ctx context.Context, bookingID uuid.UUID) (string, uuid.UUID, uuid.UUID, error)
}

// pgRepo is the PostgreSQL implementation of Repository.
type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates a review repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

// CreateReview inserts a new review row.
// The UNIQUE constraint on booking_id ensures one review per booking.
func (r *pgRepo) CreateReview(ctx context.Context, rev *Review) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO reviews (id, booking_id, customer_id, artist_id, rating, comment, is_visible)
		VALUES ($1, $2, $3, $4, $5, $6, TRUE)
		RETURNING created_at`,
		rev.ID, rev.BookingID, rev.CustomerID, rev.ArtistID, rev.Rating, rev.Comment,
	).Scan(&rev.CreatedAt)
	if err != nil {
		// Unique constraint on booking_id — already reviewed
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == "23505" {
			return ErrAlreadyReviewed
		}
		return fmt.Errorf("create review: %w", err)
	}
	return nil
}

// GetReviewByBookingID returns the review for a specific booking.
func (r *pgRepo) GetReviewByBookingID(ctx context.Context, bookingID uuid.UUID) (*Review, error) {
	rev := &Review{}
	err := r.db.QueryRow(ctx, `
		SELECT id, booking_id, customer_id, artist_id, rating, comment, is_visible, created_at
		FROM reviews
		WHERE booking_id = $1`,
		bookingID,
	).Scan(
		&rev.ID, &rev.BookingID, &rev.CustomerID, &rev.ArtistID,
		&rev.Rating, &rev.Comment, &rev.IsVisible, &rev.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReviewNotFound
		}
		return nil, fmt.Errorf("get review by booking id: %w", err)
	}
	return rev, nil
}

// GetReviewByID returns a review by primary key.
func (r *pgRepo) GetReviewByID(ctx context.Context, reviewID uuid.UUID) (*Review, error) {
	rev := &Review{}
	err := r.db.QueryRow(ctx, `
		SELECT id, booking_id, customer_id, artist_id, rating, comment, is_visible, created_at
		FROM reviews
		WHERE id = $1`,
		reviewID,
	).Scan(
		&rev.ID, &rev.BookingID, &rev.CustomerID, &rev.ArtistID,
		&rev.Rating, &rev.Comment, &rev.IsVisible, &rev.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrReviewNotFound
		}
		return nil, fmt.Errorf("get review by id: %w", err)
	}
	return rev, nil
}

// GetReviewsByArtist returns all visible reviews for an artist ordered by newest first.
func (r *pgRepo) GetReviewsByArtist(ctx context.Context, artistID uuid.UUID) ([]*Review, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, booking_id, customer_id, artist_id, rating, comment, is_visible, created_at
		FROM reviews
		WHERE artist_id = $1
		AND is_visible = TRUE
		ORDER BY created_at DESC`,
		artistID,
	)
	if err != nil {
		return nil, fmt.Errorf("get reviews by artist: %w", err)
	}
	defer rows.Close()

	var result []*Review
	for rows.Next() {
		rev := &Review{}
		if err := rows.Scan(
			&rev.ID, &rev.BookingID, &rev.CustomerID, &rev.ArtistID,
			&rev.Rating, &rev.Comment, &rev.IsVisible, &rev.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan review: %w", err)
		}
		result = append(result, rev)
	}
	return result, rows.Err()
}

// DeleteReview permanently removes a review.
func (r *pgRepo) DeleteReview(ctx context.Context, reviewID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM reviews WHERE id = $1`, reviewID)
	if err != nil {
		return fmt.Errorf("delete review: %w", err)
	}
	return nil
}

// SetVisibility shows or hides a review.
func (r *pgRepo) SetVisibility(ctx context.Context, reviewID uuid.UUID, visible bool) error {
	_, err := r.db.Exec(ctx, `
		UPDATE reviews SET is_visible = $1 WHERE id = $2`,
		visible, reviewID,
	)
	if err != nil {
		return fmt.Errorf("set review visibility: %w", err)
	}
	return nil
}

// GetBookingStatus returns the status, customer_id, and artist_id of a booking.
// Used to verify conditions before allowing a review.
func (r *pgRepo) GetBookingStatus(ctx context.Context, bookingID uuid.UUID) (string, uuid.UUID, uuid.UUID, error) {
	var status string
	var customerID, artistID uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT status, customer_id, artist_id
		FROM bookings
		WHERE id = $1
		AND deleted_at IS NULL`,
		bookingID,
	).Scan(&status, &customerID, &artistID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", uuid.Nil, uuid.Nil, fmt.Errorf("booking not found")
		}
		return "", uuid.Nil, uuid.Nil, fmt.Errorf("get booking status: %w", err)
	}
	return status, customerID, artistID, nil
}
