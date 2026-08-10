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

// uniqueViolationCode is the PostgreSQL error code for a unique-constraint
// violation - raised when a second review is inserted for the same booking.
const uniqueViolationCode = "23505"

// Repository defines all database operations for the review domain.
type Repository interface {
	// CreateReview inserts a new review AND recomputes the artist's cached
	// rating/review_count in a single transaction. Returns ErrAlreadyReviewed if
	// the booking already has a review.
	CreateReview(ctx context.Context, r *Review) error

	// GetReviewByBookingID returns the review for a specific booking.
	// Returns ErrReviewNotFound if none exists.
	GetReviewByBookingID(ctx context.Context, bookingID uuid.UUID) (*Review, error)

	// GetReviewByID returns a review by primary key.
	// Returns ErrReviewNotFound if not found.
	GetReviewByID(ctx context.Context, reviewID uuid.UUID) (*Review, error)

	// GetReviewsByArtist returns all visible reviews for an artist.
	GetReviewsByArtist(ctx context.Context, artistID uuid.UUID) ([]*Review, error)

	// DeleteReview permanently removes a review AND recomputes the artist's
	// cached rating in the same transaction. artistID is needed for the recompute.
	DeleteReview(ctx context.Context, reviewID uuid.UUID, artistID uuid.UUID) error

	// SetVisibility shows or hides a review AND recomputes the artist's cached
	// rating in the same transaction (the cache counts visible reviews only).
	SetVisibility(ctx context.Context, reviewID uuid.UUID, artistID uuid.UUID, visible bool) error

	// GetBookingStatus returns the status, customer_id, and artist_id of a booking.
	// Used to verify the booking is completed before allowing a review.
	GetBookingStatus(ctx context.Context, bookingID uuid.UUID) (string, uuid.UUID, uuid.UUID, error)

	// GetBookingIDByReviewToken resolves a review-link token to its booking's
	// ID and customer_id - the identity proof for the guest review flow,
	// replacing what a JWT would normally establish. Returns
	// ErrInvalidReviewToken if no booking has this token.
	GetBookingIDByReviewToken(ctx context.Context, token string) (bookingID, customerID uuid.UUID, err error)

	// GetBookingContextByToken returns the display summary for the review
	// link's landing screen - service, artist, store, time, price. Separate
	// from GetBookingIDByReviewToken because the two callers need different
	// shapes: one just needs IDs to authorise a write, the other needs
	// human-readable fields to render a confirmation card.
	GetBookingContextByToken(ctx context.Context, token string) (*ReviewBookingContext, error)

	// GetArtistIDByUserID resolves a user's UUID to their artists.id. Returns
	// ErrArtistNotFound if the user is not an artist. Used to authorise artist-only
	// actions (hide/show) where the JWT only carries the user_id.
	GetArtistIDByUserID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
}

// pgRepo is the PostgreSQL implementation of Repository.
type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates a review repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

// recomputeArtistRatingTx recalculates an artist's cached rating and review_count
// from their VISIBLE reviews, inside the given transaction. Counting visible-only
// means hiding a review correctly drops both the average and the count, keeping
// the cache aligned with what the public profile and discovery cards display.
//
// COALESCE handles the zero-reviews case: AVG over no rows is NULL, which would
// violate the NOT NULL rating column, so it falls back to 0.00.
func recomputeArtistRatingTx(ctx context.Context, tx pgx.Tx, artistID uuid.UUID) error {
	_, err := tx.Exec(ctx, `
		UPDATE artists
		SET rating = COALESCE(
		        (SELECT AVG(rating) FROM reviews
		         WHERE artist_id = $1 AND is_visible = TRUE),
		        0),
		    review_count = (
		        SELECT COUNT(*) FROM reviews
		        WHERE artist_id = $1 AND is_visible = TRUE),
		    updated_at = NOW()
		WHERE id = $1`,
		artistID,
	)
	if err != nil {
		return fmt.Errorf("recompute artist rating: %w", err)
	}
	return nil
}

// CreateReview inserts a new review row and recomputes the artist's cached rating
// in one transaction. The UNIQUE constraint on booking_id enforces one review per
// booking; a violation maps to ErrAlreadyReviewed.
func (r *pgRepo) CreateReview(ctx context.Context, rev *Review) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("create review: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit

	err = tx.QueryRow(ctx, `
		INSERT INTO reviews (id, booking_id, customer_id, artist_id, rating, comment, is_visible)
		VALUES ($1, $2, $3, $4, $5, $6, TRUE)
		RETURNING created_at`,
		rev.ID, rev.BookingID, rev.CustomerID, rev.ArtistID, rev.Rating, rev.Comment,
	).Scan(&rev.CreatedAt)
	if err != nil {
		var pgErr interface{ SQLState() string }
		if errors.As(err, &pgErr) && pgErr.SQLState() == uniqueViolationCode {
			return ErrAlreadyReviewed
		}
		return fmt.Errorf("create review: %w", err)
	}

	if err := recomputeArtistRatingTx(ctx, tx, rev.ArtistID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("create review: commit: %w", err)
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

// GetReviewsByArtist returns all visible reviews for an artist, newest first.
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

// DeleteReview permanently removes a review and recomputes the artist's cached
// rating in one transaction.
func (r *pgRepo) DeleteReview(ctx context.Context, reviewID uuid.UUID, artistID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("delete review: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `DELETE FROM reviews WHERE id = $1`, reviewID); err != nil {
		return fmt.Errorf("delete review: %w", err)
	}

	if err := recomputeArtistRatingTx(ctx, tx, artistID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("delete review: commit: %w", err)
	}
	return nil
}

// SetVisibility shows or hides a review and recomputes the artist's cached rating
// in one transaction (visible-only count).
func (r *pgRepo) SetVisibility(ctx context.Context, reviewID uuid.UUID, artistID uuid.UUID, visible bool) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("set review visibility: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	if _, err := tx.Exec(ctx, `UPDATE reviews SET is_visible = $1 WHERE id = $2`, visible, reviewID); err != nil {
		return fmt.Errorf("set review visibility: %w", err)
	}

	if err := recomputeArtistRatingTx(ctx, tx, artistID); err != nil {
		return err
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("set review visibility: commit: %w", err)
	}
	return nil
}

// GetBookingStatus returns the status, customer_id, and artist_id of a booking.
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

// GetBookingIDByReviewToken resolves a review-link token to the booking's ID
// and customer_id. Deliberately does NOT filter by status here - a token
// only ever exists because CompleteBooking generated one at the moment the
// booking became 'completed' (migration 013), so its existence already
// implies that. The status check still happens, just once, inside the
// existing CreateReview path this hands off to - no reason to duplicate it.
func (r *pgRepo) GetBookingIDByReviewToken(ctx context.Context, token string) (uuid.UUID, uuid.UUID, error) {
	var bookingID, customerID uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT id, customer_id
		FROM bookings
		WHERE review_token = $1
		AND deleted_at IS NULL`,
		token,
	).Scan(&bookingID, &customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, uuid.Nil, ErrInvalidReviewToken
		}
		return uuid.Nil, uuid.Nil, fmt.Errorf("get booking id by review token: %w", err)
	}
	return bookingID, customerID, nil
}

// GetBookingContextByToken returns the display fields for the review link's
// landing screen, resolved with no auth - only what's needed to render
// "Your Booking: X with Y, date, price" before the customer submits.
func (r *pgRepo) GetBookingContextByToken(ctx context.Context, token string) (*ReviewBookingContext, error) {
	ctxRow := &ReviewBookingContext{}
	err := r.db.QueryRow(ctx, `
		SELECT s.name, u.name, st.name, b.start_time, b.final_price
		FROM bookings b
		JOIN services s ON s.id = b.service_id
		JOIN artists  a ON a.id = b.artist_id
		JOIN users    u ON u.id = a.user_id
		JOIN stores  st ON st.id = b.store_id
		WHERE b.review_token = $1
		AND b.deleted_at IS NULL`,
		token,
	).Scan(&ctxRow.ServiceName, &ctxRow.ArtistName, &ctxRow.StoreName, &ctxRow.StartTime, &ctxRow.FinalPrice)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrInvalidReviewToken
		}
		return nil, fmt.Errorf("get booking context by review token: %w", err)
	}
	return ctxRow, nil
}

// GetArtistIDByUserID resolves a user's UUID to their artists.id.
// Returns ErrArtistNotFound if the user has no artist profile.
func (r *pgRepo) GetArtistIDByUserID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var artistID uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT id FROM artists WHERE user_id = $1`, userID).Scan(&artistID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrArtistNotFound
		}
		return uuid.Nil, fmt.Errorf("get artist id by user id: %w", err)
	}
	return artistID, nil
}
