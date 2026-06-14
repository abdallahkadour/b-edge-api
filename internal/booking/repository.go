// Package booking implements the booking domain for B-Edge.
package booking

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
)

// exclusionViolationCode is the PostgreSQL error code for GIST exclusion violations.
// Returned when two bookings overlap for the same artist — double booking attempt.
const exclusionViolationCode = "23P01"

// uniqueViolationCode is the PostgreSQL error code for unique constraint violations.
const uniqueViolationCode = "23505"

// Repository defines all database operations for the booking domain.
type Repository interface {
	// ── Slot algorithm queries ──────────────────────────────────────────

	// GetStore fetches the store configuration needed by the slot algorithm.
	GetStore(ctx context.Context, storeID uuid.UUID) (*Store, error)

	// GetBusinessHours fetches working hours for a store on a specific day.
	GetBusinessHours(ctx context.Context, storeID uuid.UUID, dayOfWeek int) (*BusinessHours, error)

	// GetBusinessHoursException checks if a store has a holiday or special hours on a date.
	GetBusinessHoursException(ctx context.Context, storeID uuid.UUID, date time.Time) (*BusinessHoursException, error)

	// GetService fetches a service by ID for duration and deposit info.
	GetService(ctx context.Context, serviceID uuid.UUID) (*SalonService, error)

	// GetArtistBookingsForDate returns all bookings for an artist on a date
	// that are in a blocking status. Used to build the blocked time ranges.
	GetArtistBookingsForDate(ctx context.Context, artistID uuid.UUID, date time.Time) ([]*Booking, error)

	// GetArtistCrossStoreBookings returns artist bookings at stores OTHER than
	// the target store on a given date. Used to calculate travel buffers.
	GetArtistCrossStoreBookings(ctx context.Context, artistID uuid.UUID, excludeStoreID uuid.UUID, date time.Time) ([]*Booking, error)

	// GetArtistStoreBuffer returns the travel buffer between two stores for an artist.
	// Returns nil if no specific buffer is configured — caller uses store defaults.
	GetArtistStoreBuffer(ctx context.Context, artistID uuid.UUID, fromStoreID uuid.UUID, toStoreID uuid.UUID) (*ArtistStoreBuffer, error)

	// ── Booking lifecycle ───────────────────────────────────────────────

	// CreateBooking inserts a new booking. The GIST constraint is the final
	// atomic guard — returns ErrSlotUnavailable on exclusion violation.
	CreateBooking(ctx context.Context, b *Booking) error

	// GetBookingByID fetches a single booking by primary key.
	// Returns ErrBookingNotFound if not found or soft deleted.
	GetBookingByID(ctx context.Context, id uuid.UUID) (*Booking, error)

	// GetBookingsByArtist returns paginated bookings for an artist.
	// cursor is the created_at of the last item on the previous page.
	GetBookingsByArtist(ctx context.Context, artistID uuid.UUID, cursor time.Time, limit int) ([]*Booking, error)

	// GetBookingsByCustomer returns paginated bookings for a customer.
	GetBookingsByCustomer(ctx context.Context, customerID uuid.UUID, cursor time.Time, limit int) ([]*Booking, error)

	// GetBookingsBySalon returns paginated bookings for a salon.
	GetBookingsBySalon(ctx context.Context, salonID uuid.UUID, cursor time.Time, limit int) ([]*Booking, error)

	// UpdateBookingStatus transitions a booking to a new status.
	UpdateBookingStatus(ctx context.Context, id uuid.UUID, status string) error

	// ApproveBooking transitions pending → approved and sets the deposit deadline.
	ApproveBooking(ctx context.Context, id uuid.UUID, depositDeadline time.Time) error

	// ConfirmDeposit transitions deposit_paid → confirmed.
	ConfirmDeposit(ctx context.Context, id uuid.UUID) error

	// CancelBooking cancels a booking with a reason and sets cancelled_at.
	// refundDue=true sets status to refund_due instead of cancelled.
	CancelBooking(ctx context.Context, id uuid.UUID, reason string, refundDue bool) error

	// CompleteBooking transitions confirmed → completed.
	CompleteBooking(ctx context.Context, id uuid.UUID) error

	// MarkNoShow transitions confirmed → no_show.
	MarkNoShow(ctx context.Context, id uuid.UUID) error

	// ── Background jobs ─────────────────────────────────────────────────

	// ReleaseExpiredHolds updates held bookings whose held_until has passed
	// back to their released state. Called by background job every minute.
	ReleaseExpiredHolds(ctx context.Context) (int64, error)

	// ExpireDeadlineBookings expires approved bookings whose deposit_deadline
	// has passed without payment. Called by background job every minute.
	ExpireDeadlineBookings(ctx context.Context) (int64, error)
}

// pgRepo is the PostgreSQL implementation of Repository.
type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates a booking repository backed by the given connection pool.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

// isExclusionViolation reports whether err is a PostgreSQL GIST exclusion violation.
// This is the double-booking signal — two overlapping bookings for the same artist.
func isExclusionViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode
}

// scanBooking scans a pgx row into a Booking struct.
// Column order must match every SELECT that uses this function.
//
// NOTE: b.SessionID is intentionally NOT scanned here. The bookings table has
// no session_id column yet — it will be added by migration 005 alongside
// multi-artist session support. Until then SessionID always remains nil.
func scanBooking(row pgx.Row, b *Booking) error {
	return row.Scan(
		&b.ID,
		&b.SalonID,
		&b.StoreID,
		&b.ArtistID,
		&b.CustomerID,
		&b.ServiceID,
		&b.StartTime,
		&b.EndTime,
		&b.HeldUntil,
		&b.Status,
		&b.OriginalPrice,
		&b.DiscountAmount,
		&b.FinalPrice,
		&b.DepositAmount,
		&b.DepositDeadline,
		&b.DepositPaidAt,
		&b.Channel,
		&b.SpecialRequests,
		&b.CancellationReason,
		&b.CancelledAt,
		&b.CompletedAt,
		&b.NoShowAt,
		&b.CreatedAt,
		&b.UpdatedAt,
		&b.DeletedAt,
	)
}

// bookingSelectCols is the canonical column list for booking SELECT queries.
// Must match scanBooking and scanBookings exactly.
//
// NOTE: session_id is intentionally excluded — see scanBooking.
const bookingSelectCols = `
	id, salon_id, store_id, artist_id, customer_id, service_id,
	start_time, end_time, held_until, status,
	original_price, discount_amount, final_price,
	deposit_amount, deposit_deadline, deposit_paid_at,
	channel, special_requests, cancellation_reason,
	cancelled_at, completed_at, no_show_at,
	created_at, updated_at, deleted_at`

// ── Slot algorithm queries ────────────────────────────────────────────────────

// GetStore fetches store configuration for the slot algorithm.
func (r *pgRepo) GetStore(ctx context.Context, storeID uuid.UUID) (*Store, error) {
	s := &Store{}
	err := r.db.QueryRow(ctx, `
		SELECT id, salon_id, name, city,
		       same_day_notice_hours, early_bird_cutoff, early_bird_fee,
		       weekday_buffer_min, weekend_buffer_min, is_active
		FROM stores
		WHERE id = $1 AND is_active = TRUE`,
		storeID,
	).Scan(
		&s.ID, &s.SalonID, &s.Name, &s.City,
		&s.SameDayNoticeHours, &s.EarlyBirdCutoff, &s.EarlyBirdFee,
		&s.WeekdayBufferMin, &s.WeekendBufferMin, &s.IsActive,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("store not found: %w", err)
		}
		return nil, fmt.Errorf("get store: %w", err)
	}
	return s, nil
}

// GetBusinessHours fetches working hours for a store on a day of week.
func (r *pgRepo) GetBusinessHours(ctx context.Context, storeID uuid.UUID, dayOfWeek int) (*BusinessHours, error) {
	bh := &BusinessHours{}
	err := r.db.QueryRow(ctx, `
		SELECT id, store_id, day_of_week, open_time, close_time, is_open
		FROM business_hours
		WHERE store_id = $1 AND day_of_week = $2`,
		storeID, dayOfWeek,
	).Scan(&bh.ID, &bh.StoreID, &bh.DayOfWeek, &bh.OpenTime, &bh.CloseTime, &bh.IsOpen)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // no hours configured for this day — treat as closed
		}
		return nil, fmt.Errorf("get business hours: %w", err)
	}
	return bh, nil
}

// GetBusinessHoursException checks for a holiday or special hours on a date.
// Returns nil if no exception exists — caller uses regular business hours.
func (r *pgRepo) GetBusinessHoursException(ctx context.Context, storeID uuid.UUID, date time.Time) (*BusinessHoursException, error) {
	ex := &BusinessHoursException{}
	err := r.db.QueryRow(ctx, `
		SELECT id, store_id, exception_date, is_closed, open_time, close_time, reason
		FROM business_hours_exceptions
		WHERE store_id = $1 AND exception_date = $2::date`,
		storeID, date,
	).Scan(&ex.ID, &ex.StoreID, &ex.ExceptionDate, &ex.IsClosed, &ex.OpenTime, &ex.CloseTime, &ex.Reason)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // no exception — normal day
		}
		return nil, fmt.Errorf("get business hours exception: %w", err)
	}
	return ex, nil
}

// GetService fetches a service by ID.
func (r *pgRepo) GetService(ctx context.Context, serviceID uuid.UUID) (*SalonService, error) {
	s := &SalonService{}
	err := r.db.QueryRow(ctx, `
		SELECT id, salon_id, name, duration_min, active_duration_min,
		       price, deposit_amount, deposit_deadline_hours, is_active
		FROM services
		WHERE id = $1 AND is_active = TRUE AND deleted_at IS NULL`,
		serviceID,
	).Scan(
		&s.ID, &s.SalonID, &s.Name, &s.DurationMin, &s.ActiveDurationMin,
		&s.Price, &s.DepositAmount, &s.DepositDeadlineHours, &s.IsActive,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("service not found: %w", err)
		}
		return nil, fmt.Errorf("get service: %w", err)
	}
	return s, nil
}

// GetArtistBookingsForDate returns all blocking bookings for an artist on a date.
// Used by the slot algorithm to build blocked time ranges.
func (r *pgRepo) GetArtistBookingsForDate(ctx context.Context, artistID uuid.UUID, date time.Time) ([]*Booking, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM bookings
		WHERE artist_id = $1
		AND DATE(start_time AT TIME ZONE 'UTC') = $2::date
		AND status = ANY($3)
		AND deleted_at IS NULL
		ORDER BY start_time ASC`,
		bookingSelectCols),
		artistID,
		date,
		BlockingStatuses,
	)
	if err != nil {
		return nil, fmt.Errorf("get artist bookings for date: %w", err)
	}
	defer rows.Close()
	return scanBookings(rows)
}

// GetArtistCrossStoreBookings returns artist bookings at stores other than
// the target store on a given date. Used to calculate travel buffers.
func (r *pgRepo) GetArtistCrossStoreBookings(ctx context.Context, artistID uuid.UUID, excludeStoreID uuid.UUID, date time.Time) ([]*Booking, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM bookings
		WHERE artist_id = $1
		AND store_id != $2
		AND DATE(start_time AT TIME ZONE 'UTC') = $3::date
		AND status = ANY($4)
		AND deleted_at IS NULL
		ORDER BY start_time ASC`,
		bookingSelectCols),
		artistID,
		excludeStoreID,
		date,
		BlockingStatuses,
	)
	if err != nil {
		return nil, fmt.Errorf("get artist cross store bookings: %w", err)
	}
	defer rows.Close()
	return scanBookings(rows)
}

// GetArtistStoreBuffer returns the configured travel buffer for an artist
// between two specific stores. Returns nil if no override exists.
func (r *pgRepo) GetArtistStoreBuffer(ctx context.Context, artistID uuid.UUID, fromStoreID uuid.UUID, toStoreID uuid.UUID) (*ArtistStoreBuffer, error) {
	buf := &ArtistStoreBuffer{}
	err := r.db.QueryRow(ctx, `
		SELECT id, artist_id, from_store_id, to_store_id,
		       weekday_buffer_min, weekend_buffer_min
		FROM artist_store_buffers
		WHERE artist_id = $1 AND from_store_id = $2 AND to_store_id = $3`,
		artistID, fromStoreID, toStoreID,
	).Scan(
		&buf.ID, &buf.ArtistID, &buf.FromStoreID, &buf.ToStoreID,
		&buf.WeekdayBufferMin, &buf.WeekendBufferMin,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // no override — use store defaults
		}
		return nil, fmt.Errorf("get artist store buffer: %w", err)
	}
	return buf, nil
}

// ── Booking lifecycle ─────────────────────────────────────────────────────────

// CreateBooking inserts a new booking.
// The GIST exclusion constraint on the database is the final atomic guard.
// If two requests race for the same slot, one wins and the other gets ErrSlotUnavailable.
//
// NOTE: session_id is intentionally NOT inserted here — the bookings table has
// no such column yet. See scanBooking for details. b.SessionID always remains
// nil until migration 005 adds the column and this INSERT is updated alongside it.
func (r *pgRepo) CreateBooking(ctx context.Context, b *Booking) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO bookings (
			id, salon_id, store_id, artist_id, customer_id, service_id,
			start_time, end_time, held_until, status,
			original_price, discount_amount, final_price,
			deposit_amount, deposit_deadline, channel, special_requests
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9, $10,
			$11, $12, $13,
			$14, $15, $16, $17
		)
		RETURNING created_at, updated_at`,
		b.ID, b.SalonID, b.StoreID, b.ArtistID, b.CustomerID, b.ServiceID,
		b.StartTime, b.EndTime, b.HeldUntil, b.Status,
		b.OriginalPrice, b.DiscountAmount, b.FinalPrice,
		b.DepositAmount, b.DepositDeadline, b.Channel, b.SpecialRequests,
	).Scan(&b.CreatedAt, &b.UpdatedAt)
	if err != nil {
		if isExclusionViolation(err) {
			return ErrSlotUnavailable
		}
		return fmt.Errorf("create booking: %w", err)
	}
	return nil
}

// GetBookingByID fetches a single booking by primary key.
func (r *pgRepo) GetBookingByID(ctx context.Context, id uuid.UUID) (*Booking, error) {
	b := &Booking{}
	err := scanBooking(r.db.QueryRow(ctx, fmt.Sprintf(`
		SELECT %s FROM bookings
		WHERE id = $1 AND deleted_at IS NULL`,
		bookingSelectCols), id), b)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}
		return nil, fmt.Errorf("get booking by id: %w", err)
	}
	return b, nil
}

// GetBookingsByArtist returns paginated bookings for an artist using keyset pagination.
func (r *pgRepo) GetBookingsByArtist(ctx context.Context, artistID uuid.UUID, cursor time.Time, limit int) ([]*Booking, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM bookings
		WHERE artist_id = $1
		AND created_at < $2
		AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $3`,
		bookingSelectCols),
		artistID, cursor, limit+1,
	)
	if err != nil {
		return nil, fmt.Errorf("get bookings by artist: %w", err)
	}
	defer rows.Close()
	return scanBookings(rows)
}

// GetBookingsByCustomer returns paginated bookings for a customer.
func (r *pgRepo) GetBookingsByCustomer(ctx context.Context, customerID uuid.UUID, cursor time.Time, limit int) ([]*Booking, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM bookings
		WHERE customer_id = $1
		AND created_at < $2
		AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $3`,
		bookingSelectCols),
		customerID, cursor, limit+1,
	)
	if err != nil {
		return nil, fmt.Errorf("get bookings by customer: %w", err)
	}
	defer rows.Close()
	return scanBookings(rows)
}

// GetBookingsBySalon returns paginated bookings for a salon.
func (r *pgRepo) GetBookingsBySalon(ctx context.Context, salonID uuid.UUID, cursor time.Time, limit int) ([]*Booking, error) {
	rows, err := r.db.Query(ctx, fmt.Sprintf(`
		SELECT %s FROM bookings
		WHERE salon_id = $1
		AND created_at < $2
		AND deleted_at IS NULL
		ORDER BY created_at DESC
		LIMIT $3`,
		bookingSelectCols),
		salonID, cursor, limit+1,
	)
	if err != nil {
		return nil, fmt.Errorf("get bookings by salon: %w", err)
	}
	defer rows.Close()
	return scanBookings(rows)
}

// UpdateBookingStatus transitions a booking to a new status.
func (r *pgRepo) UpdateBookingStatus(ctx context.Context, id uuid.UUID, status string) error {
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL`,
		status, id,
	)
	if err != nil {
		return fmt.Errorf("update booking status: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrBookingNotFound
	}
	return nil
}

// ApproveBooking transitions a pending booking to approved and sets deposit deadline.
func (r *pgRepo) ApproveBooking(ctx context.Context, id uuid.UUID, depositDeadline time.Time) error {
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1,
		    deposit_deadline = $2,
		    updated_at = NOW()
		WHERE id = $3
		AND status = $4
		AND deleted_at IS NULL`,
		StatusApproved, depositDeadline, id, StatusPending,
	)
	if err != nil {
		return fmt.Errorf("approve booking: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrBookingNotPending
	}
	return nil
}

// ConfirmDeposit marks a deposit as received and transitions to confirmed.
func (r *pgRepo) ConfirmDeposit(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1,
		    deposit_paid_at = NOW(),
		    updated_at = NOW()
		WHERE id = $2
		AND status = $3
		AND deleted_at IS NULL`,
		StatusConfirmed, id, StatusDepositPaid,
	)
	if err != nil {
		return fmt.Errorf("confirm deposit: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrBookingNotApproved
	}
	return nil
}

// CancelBooking cancels a booking. If refundDue is true, status becomes refund_due.
func (r *pgRepo) CancelBooking(ctx context.Context, id uuid.UUID, reason string, refundDue bool) error {
	status := StatusCancelled
	if refundDue {
		status = StatusRefundDue
	}
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1,
		    cancellation_reason = $2,
		    cancelled_at = NOW(),
		    updated_at = NOW()
		WHERE id = $3
		AND status NOT IN ('completed', 'cancelled', 'expired', 'no_show', 'refund_due', 'refunded')
		AND deleted_at IS NULL`,
		status, reason, id,
	)
	if err != nil {
		return fmt.Errorf("cancel booking: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrBookingNotCancellable
	}
	return nil
}

// CompleteBooking marks a confirmed booking as completed.
func (r *pgRepo) CompleteBooking(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1,
		    completed_at = NOW(),
		    updated_at = NOW()
		WHERE id = $2
		AND status = $3
		AND deleted_at IS NULL`,
		StatusCompleted, id, StatusConfirmed,
	)
	if err != nil {
		return fmt.Errorf("complete booking: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrBookingNotFound
	}
	return nil
}

// MarkNoShow marks a confirmed booking as no_show.
func (r *pgRepo) MarkNoShow(ctx context.Context, id uuid.UUID) error {
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1,
		    no_show_at = NOW(),
		    updated_at = NOW()
		WHERE id = $2
		AND status = $3
		AND deleted_at IS NULL`,
		StatusNoShow, id, StatusConfirmed,
	)
	if err != nil {
		return fmt.Errorf("mark no show: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrBookingNotFound
	}
	return nil
}

// ── Background jobs ───────────────────────────────────────────────────────────

// ReleaseExpiredHolds releases held bookings whose 10-minute window has passed.
// Called by background job every minute.
func (r *pgRepo) ReleaseExpiredHolds(ctx context.Context) (int64, error) {
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1, updated_at = NOW()
		WHERE status = $2
		AND held_until < NOW()
		AND deleted_at IS NULL`,
		StatusExpired, StatusHeld,
	)
	if err != nil {
		return 0, fmt.Errorf("release expired holds: %w", err)
	}
	return result.RowsAffected(), nil
}

// ExpireDeadlineBookings expires approved bookings whose deposit deadline has passed.
// Called by background job every minute.
func (r *pgRepo) ExpireDeadlineBookings(ctx context.Context) (int64, error) {
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1, updated_at = NOW()
		WHERE status = $2
		AND deposit_deadline < NOW()
		AND deleted_at IS NULL`,
		StatusExpired, StatusApproved,
	)
	if err != nil {
		return 0, fmt.Errorf("expire deadline bookings: %w", err)
	}
	return result.RowsAffected(), nil
}

// ── Scan helpers ──────────────────────────────────────────────────────────────

// scanBookings scans multiple rows into a slice of Booking pointers.
//
// NOTE: b.SessionID is intentionally NOT scanned here — see scanBooking.
func scanBookings(rows pgx.Rows) ([]*Booking, error) {
	var bookings []*Booking
	for rows.Next() {
		b := &Booking{}
		if err := rows.Scan(
			&b.ID, &b.SalonID, &b.StoreID, &b.ArtistID, &b.CustomerID, &b.ServiceID,
			&b.StartTime, &b.EndTime, &b.HeldUntil, &b.Status,
			&b.OriginalPrice, &b.DiscountAmount, &b.FinalPrice,
			&b.DepositAmount, &b.DepositDeadline, &b.DepositPaidAt,
			&b.Channel, &b.SpecialRequests, &b.CancellationReason,
			&b.CancelledAt, &b.CompletedAt, &b.NoShowAt,
			&b.CreatedAt, &b.UpdatedAt, &b.DeletedAt,
		); err != nil {
			return nil, fmt.Errorf("scan bookings: %w", err)
		}
		bookings = append(bookings, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan bookings rows: %w", err)
	}
	return bookings, nil
}

// toDecimal is a helper to convert a float64 from PostgreSQL NUMERIC to decimal.Decimal.
// Used when pgx scans NUMERIC columns that are not directly supported by shopspring.
func toDecimal(f float64) decimal.Decimal {
	return decimal.NewFromFloat(f)
}
