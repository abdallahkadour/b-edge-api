package report

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrDuplicateOpen is returned when the reporter already has an open report
// about this booking.
var ErrDuplicateOpen = errors.New("an open report already exists for this booking")

// Repository stores and retrieves reports.
type Repository interface {
	Create(ctx context.Context, r *Report) error
	ListForReporter(ctx context.Context, userID uuid.UUID) ([]*Report, error)
	ListQueue(ctx context.Context, includeResolved bool) ([]*AdminResponse, error)
	Resolve(ctx context.Context, id, adminID uuid.UUID, status Status, note *string) (int64, error)
	// BookingBelongsTo reports whether this user is party to this booking,
	// which is what stops someone filing reports about strangers' bookings.
	BookingBelongsTo(ctx context.Context, bookingID, userID uuid.UUID) (bool, error)
	// ArtistIDForBooking resolves the artist so a report is always attributable
	// even when the reporter only supplied a booking.
	ArtistIDForBooking(ctx context.Context, bookingID uuid.UUID) (uuid.UUID, error)
}

type repository struct{ db *pgxpool.Pool }

// NewRepository creates a report repository.
func NewRepository(db *pgxpool.Pool) Repository { return &repository{db: db} }

func (r *repository) Create(ctx context.Context, rep *Report) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO reports
			(reporter_user_id, reporter_role, booking_id, artist_id, category, description)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, status, created_at, updated_at`,
		rep.ReporterUserID, rep.ReporterRole, rep.BookingID, rep.ArtistID,
		rep.Category, rep.Description,
	).Scan(&rep.ID, &rep.Status, &rep.CreatedAt, &rep.UpdatedAt)

	if err != nil {
		// 23505 is unique_violation - here it can only be the partial index
		// that allows one OPEN report per reporter per booking. Mapped to a
		// named error so the service can answer "you already told us" rather
		// than a 500.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return ErrDuplicateOpen
		}
		return fmt.Errorf("create report: %w", err)
	}
	return nil
}

func (r *repository) BookingBelongsTo(ctx context.Context, bookingID, userID uuid.UUID) (bool, error) {
	var ok bool
	// Either side of the booking may report it: the customer who paid, or the
	// artist who was booked.
	err := r.db.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM bookings b
			JOIN artists a ON a.id = b.artist_id
			WHERE b.id = $1 AND (b.customer_id = $2 OR a.user_id = $2)
		)`, bookingID, userID).Scan(&ok)
	if err != nil {
		return false, fmt.Errorf("booking belongs to: %w", err)
	}
	return ok, nil
}

func (r *repository) ArtistIDForBooking(ctx context.Context, bookingID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT artist_id FROM bookings WHERE id = $1`, bookingID).Scan(&id)
	if err != nil {
		return uuid.Nil, err
	}
	return id, nil
}

const reportColumns = `
	id, reporter_user_id, reporter_role, booking_id, artist_id, category,
	description, status, resolution_note, resolved_by, resolved_at,
	created_at, updated_at`

func scanReport(row pgx.Row) (*Report, error) {
	var r Report
	err := row.Scan(&r.ID, &r.ReporterUserID, &r.ReporterRole, &r.BookingID, &r.ArtistID,
		&r.Category, &r.Description, &r.Status, &r.ResolutionNote, &r.ResolvedBy,
		&r.ResolvedAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &r, nil
}

func (r *repository) ListForReporter(ctx context.Context, userID uuid.UUID) ([]*Report, error) {
	rows, err := r.db.Query(ctx,
		`SELECT `+reportColumns+` FROM reports WHERE reporter_user_id = $1 ORDER BY created_at DESC`,
		userID)
	if err != nil {
		return nil, fmt.Errorf("list reports: %w", err)
	}
	defer rows.Close()

	out := make([]*Report, 0)
	for rows.Next() {
		rep, err := scanReport(rows)
		if err != nil {
			return nil, fmt.Errorf("scan report: %w", err)
		}
		out = append(out, rep)
	}
	return out, rows.Err()
}

// ListQueue returns the admin queue, oldest first.
//
// Oldest-first is deliberate: someone waiting on an answer about money they
// have already sent must not be overtaken by a fresher complaint.
func (r *repository) ListQueue(ctx context.Context, includeResolved bool) ([]*AdminResponse, error) {
	rows, err := r.db.Query(ctx, `
		SELECT rp.id, rp.category, rp.description, rp.status, rp.booking_id,
		       rp.created_at, rp.resolution_note,
		       u.name, rp.reporter_role, u.phone, au.name
		FROM reports rp
		JOIN users u ON u.id = rp.reporter_user_id
		LEFT JOIN artists a ON a.id = rp.artist_id
		LEFT JOIN users au ON au.id = a.user_id
		WHERE ($1::boolean IS TRUE OR rp.status IN ('open', 'reviewing'))
		ORDER BY rp.status IN ('open','reviewing') DESC, rp.created_at ASC`,
		includeResolved)
	if err != nil {
		return nil, fmt.Errorf("list report queue: %w", err)
	}
	defer rows.Close()

	out := make([]*AdminResponse, 0)
	for rows.Next() {
		var (
			a          AdminResponse
			bookingID  *uuid.UUID
			category   Category
			artistName *string
		)
		if err := rows.Scan(&a.ID, &category, &a.Description, &a.Status, &bookingID,
			&a.CreatedAt, &a.ResolutionNote, &a.ReporterName, &a.ReporterRole,
			&a.ReporterPhone, &artistName); err != nil {
			return nil, fmt.Errorf("scan report queue: %w", err)
		}
		a.Category = string(category)
		a.CategoryLabel = category.Label()
		a.ArtistName = artistName
		if bookingID != nil {
			s := bookingID.String()
			a.BookingID = &s
		}
		out = append(out, &a)
	}
	return out, rows.Err()
}

// Resolve moves a report along and records who did it.
//
// Guarded on the report not already being terminal, so two admins acting at
// once cannot overwrite each other's resolution - the second gets zero rows
// and a conflict rather than silently winning.
func (r *repository) Resolve(ctx context.Context, id, adminID uuid.UUID, status Status, note *string) (int64, error) {
	// Every parameter is cast explicitly and `status` is passed as a plain
	// string. $3 appears three times, twice inside CASE/IN expressions, and
	// without the cast Postgres cannot infer its type there - which surfaced
	// as a 500 on every resolve rather than anything readable.
	tag, err := r.db.Exec(ctx, `
		UPDATE reports
		   SET status = $3::text,
		       resolution_note = COALESCE($4::text, resolution_note),
		       resolved_by = CASE WHEN $3::text IN ('resolved','dismissed') THEN $2::uuid ELSE resolved_by END,
		       resolved_at = CASE WHEN $3::text IN ('resolved','dismissed') THEN NOW() ELSE resolved_at END,
		       updated_at = NOW()
		 WHERE id = $1::uuid AND status IN ('open', 'reviewing')`,
		id, adminID, string(status), note)
	if err != nil {
		return 0, fmt.Errorf("resolve report: %w", err)
	}
	return tag.RowsAffected(), nil
}
