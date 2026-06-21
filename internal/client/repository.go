// Package client implements the artist-facing CRM for B-Edge.
package client

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository defines all database operations for the client CRM domain.
type Repository interface {
	// GetArtistIDByUserID resolves a user's UUID to their artists.id.
	// Returns ErrArtistNotFound if the user has no artist profile.
	GetArtistIDByUserID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)

	// ListClients returns the artist's clients (customers with at least one
	// completed booking), aggregated with booking metrics, the customer's average
	// rating of THIS artist, and the artist's private note. Optional q filters by
	// customer name or last service (case-insensitive).
	ListClients(ctx context.Context, artistID uuid.UUID, q string) ([]*ClientRow, error)

	// GetClient returns a single client's aggregated row for the given artist.
	// Returns ErrClientNotFound if the customer has no completed booking with
	// this artist.
	GetClient(ctx context.Context, artistID, customerID uuid.UUID) (*ClientRow, error)

	// GetClientHistory returns the customer's booking history with this artist,
	// newest first (all non-deleted bookings, not only completed).
	GetClientHistory(ctx context.Context, artistID, customerID uuid.UUID) ([]*BookingHistoryRow, error)

	// UpsertNote inserts or updates the artist's private note for a customer.
	// One note per (artist, customer) via the UNIQUE constraint. salonID is needed
	// because client_notes.salon_id is NOT NULL. Returns the stored content and
	// its updated_at timestamp.
	UpsertNote(ctx context.Context, salonID, artistID, customerID uuid.UUID, content string) (string, time.Time, error)

	// GetArtistSalonID returns the salon_id for an artist (needed to populate
	// client_notes.salon_id on insert).
	GetArtistSalonID(ctx context.Context, artistID uuid.UUID) (uuid.UUID, error)
}

// pgRepo is the PostgreSQL implementation of Repository.
type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates a client repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

// clientAggregateSelect is the shared SELECT + FROM + JOIN body for the client
// list and single-client queries. It aggregates a customer's COMPLETED bookings
// with this artist, LEFT JOINs the artist's private note, and computes the
// customer's average rating of THIS artist from reviews. %s is the extra WHERE
// predicate (search filter or single-customer filter).
//
// last_service / last_visit use DISTINCT ON-style window via a correlated
// subquery on the most recent completed booking.
const clientAggregateSelect = `
	SELECT
		b.customer_id,
		u.name,
		u.phone,
		COUNT(b.id)                              AS bookings_count,
		COALESCE(SUM(b.final_price), 0)          AS total_spent,
		(SELECT s2.name
		   FROM bookings b2
		   JOIN services s2 ON s2.id = b2.service_id
		  WHERE b2.customer_id = b.customer_id
		    AND b2.artist_id   = b.artist_id
		    AND b2.status = '` + completedStatus + `'
		    AND b2.deleted_at IS NULL
		  ORDER BY b2.start_time DESC
		  LIMIT 1)                               AS last_service,
		MAX(b.start_time)                        AS last_visit,
		(SELECT AVG(r.rating)
		   FROM reviews r
		  WHERE r.customer_id = b.customer_id
		    AND r.artist_id   = b.artist_id)     AS average_rating,
		COALESCE(cn.content, '')                 AS note_content
	FROM bookings b
	JOIN users u        ON u.id = b.customer_id
	LEFT JOIN client_notes cn
	       ON cn.artist_id   = b.artist_id
	      AND cn.customer_id  = b.customer_id
	WHERE b.artist_id = $1
	  AND b.status = '` + completedStatus + `'
	  AND b.deleted_at IS NULL
	  AND u.deleted_at IS NULL
	  %s
	GROUP BY b.customer_id, u.name, u.phone, b.artist_id, cn.content`

// scanClientRow scans one aggregated client row.
func scanClientRow(row pgx.Row) (*ClientRow, error) {
	c := &ClientRow{}
	if err := row.Scan(
		&c.CustomerID, &c.Name, &c.Phone,
		&c.BookingsCount, &c.TotalSpent,
		&c.LastService, &c.LastVisit, &c.AverageRating,
		&c.NoteContent,
	); err != nil {
		return nil, err
	}
	return c, nil
}

// GetArtistIDByUserID resolves a user's UUID to their artists.id.
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

// GetArtistSalonID returns the salon_id for an artist.
func (r *pgRepo) GetArtistSalonID(ctx context.Context, artistID uuid.UUID) (uuid.UUID, error) {
	var salonID *uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT salon_id FROM artists WHERE id = $1`, artistID).Scan(&salonID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrArtistNotFound
		}
		return uuid.Nil, fmt.Errorf("get artist salon id: %w", err)
	}
	if salonID == nil {
		return uuid.Nil, fmt.Errorf("artist has no salon")
	}
	return *salonID, nil
}

// ListClients returns the artist's aggregated clients, optionally filtered by a
// case-insensitive search on customer name or most-recent service.
func (r *pgRepo) ListClients(ctx context.Context, artistID uuid.UUID, q string) ([]*ClientRow, error) {
	var predicate string
	args := []any{artistID}
	if q != "" {
		// HAVING-style name/service filter applied post-aggregation would be
		// complex; instead filter on name here and rely on last_service search via
		// the same ILIKE on the customer name. Service-name search is handled by
		// matching any of the customer's completed service names.
		predicate = `AND (u.name ILIKE $2 OR EXISTS (
			SELECT 1 FROM bookings bf
			JOIN services sf ON sf.id = bf.service_id
			WHERE bf.customer_id = b.customer_id
			  AND bf.artist_id = b.artist_id
			  AND bf.status = '` + completedStatus + `'
			  AND sf.name ILIKE $2))`
		args = append(args, "%"+q+"%")
	}

	query := fmt.Sprintf(clientAggregateSelect+`
		ORDER BY last_visit DESC NULLS LAST`, predicate)

	rows, err := r.db.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list clients: %w", err)
	}
	defer rows.Close()

	var result []*ClientRow
	for rows.Next() {
		c, err := scanClientRow(rows)
		if err != nil {
			return nil, fmt.Errorf("scan client row: %w", err)
		}
		result = append(result, c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list clients rows: %w", err)
	}
	return result, nil
}

// GetClient returns a single aggregated client row, or ErrClientNotFound.
func (r *pgRepo) GetClient(ctx context.Context, artistID, customerID uuid.UUID) (*ClientRow, error) {
	predicate := "AND b.customer_id = $2"
	query := fmt.Sprintf(clientAggregateSelect, predicate)

	c, err := scanClientRow(r.db.QueryRow(ctx, query, artistID, customerID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrClientNotFound
		}
		return nil, fmt.Errorf("get client: %w", err)
	}
	return c, nil
}

// GetClientHistory returns the customer's booking history with this artist,
// newest first. Includes all non-deleted bookings so the timeline shows the full
// relationship (completed, cancelled, no_show, etc.).
func (r *pgRepo) GetClientHistory(ctx context.Context, artistID, customerID uuid.UUID) ([]*BookingHistoryRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT b.id, s.name, st.name, b.start_time, b.status, b.final_price
		FROM bookings b
		JOIN services s ON s.id = b.service_id
		JOIN stores st  ON st.id = b.store_id
		WHERE b.artist_id = $1
		  AND b.customer_id = $2
		  AND b.deleted_at IS NULL
		ORDER BY b.start_time DESC`,
		artistID, customerID,
	)
	if err != nil {
		return nil, fmt.Errorf("get client history: %w", err)
	}
	defer rows.Close()

	var result []*BookingHistoryRow
	for rows.Next() {
		h := &BookingHistoryRow{}
		if err := rows.Scan(
			&h.ID, &h.ServiceName, &h.StoreName,
			&h.StartTime, &h.Status, &h.FinalPrice,
		); err != nil {
			return nil, fmt.Errorf("scan history row: %w", err)
		}
		result = append(result, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get client history rows: %w", err)
	}
	return result, nil
}

// UpsertNote inserts or updates the artist's private note for a customer,
// returning the stored content and its updated_at. One note per (artist, customer).
func (r *pgRepo) UpsertNote(ctx context.Context, salonID, artistID, customerID uuid.UUID, content string) (string, time.Time, error) {
	var stored string
	var updatedAt time.Time
	err := r.db.QueryRow(ctx, `
		INSERT INTO client_notes (salon_id, artist_id, customer_id, content)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (artist_id, customer_id)
		DO UPDATE SET content = EXCLUDED.content, updated_at = NOW()
		RETURNING content, updated_at`,
		salonID, artistID, customerID, content,
	).Scan(&stored, &updatedAt)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("upsert note: %w", err)
	}
	return stored, updatedAt, nil
}
