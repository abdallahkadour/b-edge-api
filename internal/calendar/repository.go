package calendar

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrBookingNotFound is returned when no booking matches the token.
//
// One error for "no such token" and "token points at nothing usable", so a
// caller cannot distinguish a wrong token from a real one by the response -
// the same 404-not-403 posture used for billing invoices, media tags and
// notifications.
var ErrBookingNotFound = errors.New("booking not found")

// Booking is the single row an .ics needs.
type Booking struct {
	ID        uuid.UUID
	StartTime time.Time
	EndTime   time.Time
	Status    string
	Sequence  int

	ServiceName  string
	StoreName    string
	StoreCity    string
	StoreAddress string
	// Deleted reports a soft-deleted booking. Treated exactly like a
	// cancellation rather than like a 404, for the reason in
	// GetByCalendarToken's comment.
	Deleted bool
	// Timezone is the store's IANA zone. Carried even though Start/End are
	// already absolute, because the landing page shows a human-readable
	// local time and must not render it in the server's zone.
	Timezone string
}

// Repository reads the one booking a calendar link refers to.
type Repository interface {
	GetByCalendarToken(ctx context.Context, token string) (*Booking, error)
}

type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates a PostgreSQL-backed calendar Repository.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

// GetByCalendarToken resolves a token to its booking.
//
// Deliberately does NOT filter by status or deleted_at. A cancelled booking
// must still resolve, because that is exactly when the customer needs the
// .ics that WITHDRAWS the event from their calendar - 404ing a cancelled
// booking would leave a stale appointment sitting in their phone forever,
// which is worse than the problem this feature set out to solve. The
// service decides what to render; this only decides what exists.
func (r *pgRepo) GetByCalendarToken(ctx context.Context, token string) (*Booking, error) {
	b := &Booking{}
	err := r.db.QueryRow(ctx, `
		SELECT
			b.id, b.start_time, b.end_time, b.status, b.calendar_sequence,
			(b.deleted_at IS NOT NULL),
			s.name,
			st.name, st.city, COALESCE(st.address, ''), st.timezone
		FROM bookings b
		JOIN services s ON s.id = b.service_id
		JOIN stores   st ON st.id = b.store_id
		WHERE b.calendar_token = $1`,
		token,
	).Scan(
		&b.ID, &b.StartTime, &b.EndTime, &b.Status, &b.Sequence, &b.Deleted,
		&b.ServiceName, &b.StoreName, &b.StoreCity, &b.StoreAddress, &b.Timezone,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}
		return nil, fmt.Errorf("get booking by calendar token: %w", err)
	}
	return b, nil
}
