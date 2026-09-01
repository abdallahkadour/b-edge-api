// Package booking implements the booking domain for B-Edge.
package booking

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
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
// Returned when two bookings overlap for the same artist - double booking attempt.
const exclusionViolationCode = "23P01"

// reviewTokenLength matches the byte length used for password reset tokens
// in internal/domain/auth (generateSecureToken) - 32 random bytes, hex
// encoded to 64 characters, matching the VARCHAR(64) column.
const reviewTokenLength = 32

// generateReviewToken produces a cryptographically random, URL-safe token
// for the guest review-link feature. A customer proves "this is my
// appointment" by possessing this token - the same trust model as a
// password reset link - since they never receive a login session to prove
// identity any other way.
func generateReviewToken() (string, error) {
	b := make([]byte, reviewTokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate review token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

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

	// CreateGuestUser creates a minimal customer record for a guest booking.
	// Returns the new user's UUID to use as customer_id on the booking.
	CreateGuestUser(ctx context.Context, name string, phone string) (uuid.UUID, error)
	// GetArtistBookingsForDate returns all bookings for an artist on a date
	// that are in a blocking status. Used to build the blocked time ranges.
	GetArtistBookingsForDate(ctx context.Context, artistID uuid.UUID, date time.Time) ([]*Booking, error)

	// ListEnrichedBookingsForDay returns every one of an artist's bookings
	// on a calendar day, across ALL stores, with customer and service names
	// attached.
	//
	// One query rather than the two-plus-N the bulk shift preview would
	// otherwise need: same-store bookings to move, cross-store bookings to
	// check travel buffers against, and a customer name and phone for each.
	// Callers split by store_id in Go.
	//
	// Unlike the calendar queries this includes MovableStatuses rather than
	// CalendarStatuses - a held or deposit_pending booking still occupies
	// the artist's day and must be shifted with it.
	ListEnrichedBookingsForDay(ctx context.Context, artistID uuid.UUID, dayStart, dayEnd time.Time) ([]*EnrichedBooking, error)

	// GetArtistCrossStoreBookings returns artist bookings at stores OTHER than
	// the target store on a given date. Used to calculate travel buffers.
	GetArtistCrossStoreBookings(ctx context.Context, artistID uuid.UUID, excludeStoreID uuid.UUID, date time.Time) ([]*Booking, error)

	// GetArtistStoreBuffer returns the travel buffer between two stores for an artist.
	// Returns nil if no specific buffer is configured - caller uses store defaults.
	GetArtistStoreBuffer(ctx context.Context, artistID uuid.UUID, fromStoreID uuid.UUID, toStoreID uuid.UUID) (*ArtistStoreBuffer, error)

	// ── Booking lifecycle ───────────────────────────────────────────────

	// CreateBooking inserts a new booking. The GIST constraint is the final
	// atomic guard - returns ErrSlotUnavailable on exclusion violation.
	CreateBooking(ctx context.Context, b *Booking) error

	// GetBookingByID fetches a single booking by primary key.
	// Returns ErrBookingNotFound if not found or soft deleted.
	GetBookingByID(ctx context.Context, id uuid.UUID) (*Booking, error)

	// GetEnrichedBookingByID fetches one booking joined with customer, service,
	// and store display names. Returns ErrBookingNotFound if not found.
	GetEnrichedBookingByID(ctx context.Context, id uuid.UUID) (*EnrichedBooking, error)

	// ListEnrichedBookingsByArtist returns an artist's bookings (keyset paginated)
	// joined with customer, service, and store display names. If status is
	// non-empty, only bookings in that status are returned; empty means all.
	ListEnrichedBookingsByArtist(ctx context.Context, artistID uuid.UUID, status string, cursor time.Time, limit int) ([]*EnrichedBooking, error)

	// ListEnrichedBookingsForWeek returns an artist's committed appointments
	// (CalendarStatuses) within the 7-day window [weekStart, weekStart+7d),
	// ordered by start_time. No pagination - a week is bounded. Used by the
	// calendar grid.
	ListEnrichedBookingsForWeek(ctx context.Context, artistID uuid.UUID, weekStart time.Time) ([]*EnrichedBooking, error)

	// ListEnrichedBookingsByCustomer returns a customer's bookings (keyset
	// paginated) joined with service and store display names.
	ListEnrichedBookingsByCustomer(ctx context.Context, customerID uuid.UUID, cursor time.Time, limit int) ([]*EnrichedBooking, error)

	// GetBookingsByArtist returns paginated bookings for an artist.
	// cursor is the created_at of the last item on the previous page.
	GetBookingsByArtist(ctx context.Context, artistID uuid.UUID, cursor time.Time, limit int) ([]*Booking, error)

	// GetBookingsByCustomer returns paginated bookings for a customer.
	GetBookingsByCustomer(ctx context.Context, customerID uuid.UUID, cursor time.Time, limit int) ([]*Booking, error)

	// GetBookingsBySalon returns paginated bookings for a salon.
	GetBookingsBySalon(ctx context.Context, salonID uuid.UUID, cursor time.Time, limit int) ([]*Booking, error)

	// UpdateBookingStatus transitions a booking to a new status.
	UpdateBookingStatus(ctx context.Context, id uuid.UUID, status string) error

	// GetArtistIDByUserID resolves a requester's users.id to their artists.id.
	// bookings.artist_id references artists.id, not users.id, so any ownership
	// check comparing a requester against a booking's artist must resolve
	// through this first. Returns ErrArtistNotFound if the user has no
	// artist profile.
	GetArtistIDByUserID(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)

	// CreateWaitlistEntry adds a customer to the queue for a fully-booked
	// (artist, store, service, date) combination.
	CreateWaitlistEntry(ctx context.Context, artistID, storeID, serviceID, customerID uuid.UUID, date time.Time) (uuid.UUID, error)

	// GetWaitlistByArtist returns every active (waiting or notified) entry
	// for an artist, newest-service-request-first per queue but overall
	// ordered so the artist can scan their whole waitlist at a glance.
	GetWaitlistByArtist(ctx context.Context, artistID uuid.UUID) ([]*WaitlistEntryResponse, error)

	// NotifyNextWaitlistEntry is called after a booking cancellation/no-show
	// frees up a slot. First expires any stale 'notified' entry for this
	// exact (artist, store, service, date) group past its confirm_deadline
	// - the lazy-expiry mechanism migration 016 documents - then, if a
	// 'waiting' entry exists (the oldest one, FIFO), notifies it: stamps
	// notified_at/confirm_deadline, flips status to 'notified', and enqueues
	// the WhatsApp message. A no-op (returns nil, does nothing) if the
	// waitlist for this exact group is empty - the common case, since most
	// cancellations don't have anyone waiting.
	NotifyNextWaitlistEntry(ctx context.Context, artistID, storeID, serviceID uuid.UUID, date time.Time) error

	// GetBookingNotificationContext returns the customer name and service
	// name for a booking - the minimum needed to personalise a WhatsApp
	// message ("Hi Sarah! Your Bridal Makeup booking..."). A small, focused
	// query rather than reusing the full enriched-booking query, since
	// that pulls far more than a notification message needs.
	GetBookingNotificationContext(ctx context.Context, bookingID uuid.UUID) (customerName, serviceName string, err error)

	// EnqueueNotification inserts a pending row for the WhatsApp worker to
	// pick up. Always succeeds regardless of whether Twilio is actually
	// configured - the worker itself is what checks for credentials and
	// logs/skips if they're missing (see internal/notification/worker.go).
	// This means notifications simply queue up and start sending
	// automatically the moment real credentials exist; nothing here needs
	// to change when that happens.
	EnqueueNotification(ctx context.Context, bookingID *uuid.UUID, userID uuid.UUID, templateName, message string) error

	// AttachGuestAndSubmit repoints a held guest booking from the placeholder
	// customer to the real guest user and transitions held → pending, in one
	// guarded UPDATE. Returns ErrBookingNotHeld if the booking is no longer a
	// live held booking (already submitted or expired).
	AttachGuestAndSubmit(ctx context.Context, bookingID, guestUserID uuid.UUID, specialRequests *string) error

	// ApproveBooking transitions pending → approved, sets the deposit
	// deadline, and mints the calendar token, returning it. Approval is
	// the single choke point for the calendar link: both routes to
	// 'confirmed' descend from 'approved', so one call site covers every
	// booking that will ever be worth putting in a calendar.
	ApproveBooking(ctx context.Context, id uuid.UUID, depositDeadline time.Time) (calendarToken string, err error)

	// ConfirmDeposit transitions deposit_paid → confirmed.
	ConfirmDeposit(ctx context.Context, id uuid.UUID) error

	// ConfirmDepositReceived transitions approved → confirmed directly,
	// setting deposit_paid_at in the same statement. This is the primary
	// artist-facing action: she checks OMT/Wish and confirms the moment
	// the transfer lands, so the deposit_paid and confirmed steps collapse
	// into a single atomic transition rather than two separate clicks.
	// reference is an optional artist-entered note (e.g. a transaction
	// code) for her own reconciliation - nil leaves the column untouched.
	ConfirmDepositReceived(ctx context.Context, id uuid.UUID, reference *string) error

	// CancelBooking cancels a booking with a reason and sets cancelled_at.
	// refundDue=true sets status to refund_due instead of cancelled.
	CancelBooking(ctx context.Context, id uuid.UUID, reason string, refundDue bool) error

	// CompleteBooking transitions confirmed → completed, generating the
	// guest review-link token in the same statement and returning it so the
	// caller can enqueue the review-request notification immediately,
	// without a second query to re-fetch what was just written.
	CompleteBooking(ctx context.Context, id uuid.UUID) (reviewToken string, err error)

	// MarkNoShow transitions confirmed → no_show.
	MarkNoShow(ctx context.Context, id uuid.UUID) error

	// ── Background jobs ─────────────────────────────────────────────────

	// ReleaseExpiredHolds updates held bookings whose held_until has passed
	// back to their released state. Called by background job every minute.
	ReleaseExpiredHolds(ctx context.Context) (int64, error)

	// ExpireDeadlineBookings expires approved bookings whose deposit_deadline
	// has passed without payment. Called by background job every minute.
	ExpireDeadlineBookings(ctx context.Context) (int64, error)

	// ExpireStalePendingBookings expires this artist's pending requests whose
	// own start_time has already passed. Called lazily from the artist
	// bookings-list read path, not a background job - see the doc comment on
	// the implementation for why.
	ExpireStalePendingBookings(ctx context.Context, artistID uuid.UUID) (int64, error)
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
// This is the double-booking signal - two overlapping bookings for the same artist.
func isExclusionViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == exclusionViolationCode
}

// scanBooking scans a pgx row into a Booking struct.
// Column order must match every SELECT that uses this function.
//
// NOTE: b.SessionID is intentionally NOT scanned here. The bookings table has
// no session_id column yet - it will be added by migration 005 alongside
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
		&b.DepositReference,
		&b.ReviewToken, &b.CalendarToken,
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
// NOTE: session_id is intentionally excluded - see scanBooking.
const bookingSelectCols = `
	id, salon_id, store_id, artist_id, customer_id, service_id,
	start_time, end_time, held_until, status,
	original_price, discount_amount, final_price,
	deposit_amount, deposit_deadline, deposit_paid_at, deposit_reference, review_token, calendar_token,
	channel, special_requests, cancellation_reason,
	cancelled_at, completed_at, no_show_at,
	created_at, updated_at, deleted_at`

// ── Enriched booking queries ──────────────────────────────────────────────────

// enrichedSelectCols is the canonical column list for enriched booking SELECTs.
// Joins users (customer), services, and stores. Must match scanEnrichedBooking.
const enrichedSelectCols = `
	b.id, b.salon_id, b.store_id, b.artist_id, b.customer_id, b.service_id,
	b.start_time, b.end_time, b.held_until, b.status,
	b.original_price, b.discount_amount, b.final_price,
	b.deposit_amount, b.deposit_deadline, b.deposit_paid_at, b.deposit_reference, b.review_token, b.calendar_token,
	b.channel, b.special_requests, b.cancellation_reason,
	b.cancelled_at, b.completed_at, b.no_show_at,
	b.created_at, b.updated_at, b.deleted_at,
	u.name  AS customer_name,
	u.phone AS customer_phone,
	au.name AS artist_name,
	s.name  AS service_name,
	st.name AS store_name,
	st.city AS store_city`

// enrichedFrom is the shared FROM + JOIN clause for enriched booking queries.
// The artists/au double-join exists purely to surface the ARTIST's name
// needed by the customer-facing "My Bookings" screen (a customer looking at
// their own history needs to know whose appointment it was), which none of
// the other enriched-query consumers (artist's own bookings list, Calendar,
// Deposit Queue) actually need since they already know who they are. Harmless
// there - just an unused extra column - genuinely necessary here.
const enrichedFrom = `
	FROM bookings b
	JOIN users    u  ON u.id  = b.customer_id
	JOIN artists  art ON art.id = b.artist_id
	JOIN users    au ON au.id = art.user_id
	JOIN services s  ON s.id  = b.service_id
	JOIN stores   st ON st.id = b.store_id`

// scanEnrichedBooking scans a row (booking columns + joined names) into an
// EnrichedBooking. Column order must match enrichedSelectCols exactly.
func scanEnrichedBooking(row pgx.Row, e *EnrichedBooking) error {
	return row.Scan(
		&e.ID, &e.SalonID, &e.StoreID, &e.ArtistID, &e.CustomerID, &e.ServiceID,
		&e.StartTime, &e.EndTime, &e.HeldUntil, &e.Status,
		&e.OriginalPrice, &e.DiscountAmount, &e.FinalPrice,
		&e.DepositAmount, &e.DepositDeadline, &e.DepositPaidAt, &e.DepositReference, &e.ReviewToken, &e.CalendarToken,
		&e.Channel, &e.SpecialRequests, &e.CancellationReason,
		&e.CancelledAt, &e.CompletedAt, &e.NoShowAt,
		&e.CreatedAt, &e.UpdatedAt, &e.DeletedAt,
		&e.CustomerName, &e.CustomerPhone, &e.ArtistName, &e.ServiceName, &e.StoreName, &e.StoreCity,
	)
}

// scanEnrichedBookings scans multiple enriched rows.
func scanEnrichedBookings(rows pgx.Rows) ([]*EnrichedBooking, error) {
	var result []*EnrichedBooking
	for rows.Next() {
		e := &EnrichedBooking{}
		if err := scanEnrichedBooking(rows, e); err != nil {
			return nil, fmt.Errorf("scan enriched bookings: %w", err)
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("scan enriched bookings rows: %w", err)
	}
	return result, nil
}

// GetEnrichedBookingByID fetches one booking joined with display names.
func (r *pgRepo) GetEnrichedBookingByID(ctx context.Context, id uuid.UUID) (*EnrichedBooking, error) {
	e := &EnrichedBooking{}
	q := fmt.Sprintf(`SELECT %s %s WHERE b.id = $1 AND b.deleted_at IS NULL`,
		enrichedSelectCols, enrichedFrom)
	if err := scanEnrichedBooking(r.db.QueryRow(ctx, q, id), e); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrBookingNotFound
		}
		return nil, fmt.Errorf("get enriched booking by id: %w", err)
	}
	return e, nil
}

// ListEnrichedBookingsByArtist returns an artist's bookings with display names,
// newest first, using keyset pagination on created_at. When status is non-empty,
// results are restricted to that status (dashboard tabs, deposit/refund queues).
func (r *pgRepo) ListEnrichedBookingsByArtist(ctx context.Context, artistID uuid.UUID, status string, cursor time.Time, limit int) ([]*EnrichedBooking, error) {
	// Build the WHERE clause. The status filter is appended as an extra
	// parameterised condition only when provided, so the "all statuses" path
	// keeps its original plan. $4 is used only in the filtered branch.
	where := `WHERE b.artist_id = $1 AND b.created_at < $2 AND b.deleted_at IS NULL`
	args := []any{artistID, cursor, limit + 1}
	if status != "" {
		where += ` AND b.status = $4`
		args = append(args, status)
	}

	q := fmt.Sprintf(`SELECT %s %s
		%s
		ORDER BY b.created_at DESC
		LIMIT $3`,
		enrichedSelectCols, enrichedFrom, where)

	rows, err := r.db.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list enriched bookings by artist: %w", err)
	}
	defer rows.Close()
	return scanEnrichedBookings(rows)
}

// ListEnrichedBookingsForWeek returns the artist's committed appointments within
// the 7-day window starting at weekStart, ordered by start_time. The window is
// half-open [weekStart, weekStart+7d). Only CalendarStatuses are included, so
// pending requests and cancelled/expired noise never appear on the grid.
func (r *pgRepo) ListEnrichedBookingsForWeek(ctx context.Context, artistID uuid.UUID, weekStart time.Time) ([]*EnrichedBooking, error) {
	weekEnd := weekStart.AddDate(0, 0, 7)
	q := fmt.Sprintf(`SELECT %s %s
		WHERE b.artist_id = $1
		AND b.start_time >= $2
		AND b.start_time <  $3
		AND b.status = ANY($4)
		AND b.deleted_at IS NULL
		ORDER BY b.start_time ASC`,
		enrichedSelectCols, enrichedFrom)
	rows, err := r.db.Query(ctx, q, artistID, weekStart, weekEnd, CalendarStatuses)
	if err != nil {
		return nil, fmt.Errorf("list enriched bookings for week: %w", err)
	}
	defer rows.Close()
	return scanEnrichedBookings(rows)
}

// ListEnrichedBookingsForDay returns an artist's bookings across all stores
// within [dayStart, dayEnd), enriched with customer and service details.
//
// The window is half-open and supplied by the caller already resolved to
// absolute instants, because "a day" depends on the STORE's timezone rather
// than the server's - at 23:00 UTC it is already tomorrow in Beirut.
func (r *pgRepo) ListEnrichedBookingsForDay(ctx context.Context, artistID uuid.UUID, dayStart, dayEnd time.Time) ([]*EnrichedBooking, error) {
	q := fmt.Sprintf(`SELECT %s %s
		WHERE b.artist_id = $1
		AND b.start_time >= $2
		AND b.start_time <  $3
		AND b.deleted_at IS NULL
		ORDER BY b.start_time ASC`,
		enrichedSelectCols, enrichedFrom)
	rows, err := r.db.Query(ctx, q, artistID, dayStart, dayEnd)
	if err != nil {
		return nil, fmt.Errorf("list enriched bookings for day: %w", err)
	}
	defer rows.Close()
	return scanEnrichedBookings(rows)
}

// ListEnrichedBookingsByCustomer returns a customer's bookings with display names,
// newest first, using keyset pagination on created_at.
func (r *pgRepo) ListEnrichedBookingsByCustomer(ctx context.Context, customerID uuid.UUID, cursor time.Time, limit int) ([]*EnrichedBooking, error) {
	q := fmt.Sprintf(`SELECT %s %s
		WHERE b.customer_id = $1 AND b.created_at < $2 AND b.deleted_at IS NULL
		ORDER BY b.created_at DESC
		LIMIT $3`,
		enrichedSelectCols, enrichedFrom)
	rows, err := r.db.Query(ctx, q, customerID, cursor, limit+1)
	if err != nil {
		return nil, fmt.Errorf("list enriched bookings by customer: %w", err)
	}
	defer rows.Close()
	return scanEnrichedBookings(rows)
}

// ── Slot algorithm queries ────────────────────────────────────────────────────

// GetStore fetches store configuration for the slot algorithm.
func (r *pgRepo) GetStore(ctx context.Context, storeID uuid.UUID) (*Store, error) {
	s := &Store{}
	err := r.db.QueryRow(ctx, `
		SELECT id, salon_id, name, city,
		       same_day_notice_hours, early_bird_cutoff, early_bird_fee,
		       weekday_buffer_min, weekend_buffer_min, is_active, timezone
		FROM stores
		WHERE id = $1 AND is_active = TRUE`,
		storeID,
	).Scan(
		&s.ID, &s.SalonID, &s.Name, &s.City,
		&s.SameDayNoticeHours, &s.EarlyBirdCutoff, &s.EarlyBirdFee,
		&s.WeekdayBufferMin, &s.WeekendBufferMin, &s.IsActive, &s.Timezone,
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
			return nil, nil // no hours configured for this day - treat as closed
		}
		return nil, fmt.Errorf("get business hours: %w", err)
	}
	return bh, nil
}

// GetBusinessHoursException checks for a holiday or special hours on a date.
// Returns nil if no exception exists - caller uses regular business hours.
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
			return nil, nil // no exception - normal day
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
		WHERE id = $1 AND is_active = TRUE`,
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
			return nil, nil // no override - use store defaults
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
// NOTE: session_id is intentionally NOT inserted here - the bookings table has
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

// CreateWaitlistEntry adds a customer to the queue.
func (r *pgRepo) CreateWaitlistEntry(ctx context.Context, artistID, storeID, serviceID, customerID uuid.UUID, date time.Time) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `
		INSERT INTO waitlist_entries (artist_id, store_id, service_id, customer_id, requested_date)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		artistID, storeID, serviceID, customerID, date,
	).Scan(&id)
	if err != nil {
		return uuid.Nil, fmt.Errorf("create waitlist entry: %w", err)
	}
	return id, nil
}

// GetWaitlistByArtist returns every active waitlist entry for an artist,
// with the customer/service names an artist actually needs to act on it.
func (r *pgRepo) GetWaitlistByArtist(ctx context.Context, artistID uuid.UUID) ([]*WaitlistEntryResponse, error) {
	rows, err := r.db.Query(ctx, `
		SELECT w.id, w.service_id, s.name, u.name, u.phone,
		       w.requested_date, w.status, w.notified_at, w.confirm_deadline, w.created_at
		FROM waitlist_entries w
		JOIN services s ON s.id = w.service_id
		JOIN users    u ON u.id = w.customer_id
		WHERE w.artist_id = $1
		AND w.status IN ('waiting', 'notified')
		ORDER BY w.requested_date ASC, w.created_at ASC`,
		artistID,
	)
	if err != nil {
		return nil, fmt.Errorf("get waitlist by artist: %w", err)
	}
	defer rows.Close()

	var result []*WaitlistEntryResponse
	for rows.Next() {
		e := &WaitlistEntryResponse{}
		var date time.Time
		var phone *string
		if err := rows.Scan(
			&e.ID, &e.ServiceID, &e.ServiceName, &e.CustomerName, &phone,
			&date, &e.Status, &e.NotifiedAt, &e.ConfirmDeadline, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan waitlist entry: %w", err)
		}
		e.RequestedDate = date.Format("2006-01-02")
		if phone != nil {
			e.CustomerPhone = *phone
		}
		result = append(result, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("get waitlist by artist: rows: %w", err)
	}
	return result, nil
}

// NotifyNextWaitlistEntry: expire any stale notified entry for this exact
// group, then notify the oldest waiting one, if any. See the Repository
// interface doc comment and migration 016 for the lazy-expiry reasoning.
func (r *pgRepo) NotifyNextWaitlistEntry(ctx context.Context, artistID, storeID, serviceID uuid.UUID, date time.Time) error {
	// Step 1: expire a stale 'notified' entry for this exact group, if one
	// exists and its window has passed. Harmless no-op if there isn't one.
	_, err := r.db.Exec(ctx, `
		UPDATE waitlist_entries
		SET status = $1, updated_at = NOW()
		WHERE artist_id = $2 AND store_id = $3 AND service_id = $4 AND requested_date = $5
		AND status = $6
		AND confirm_deadline < NOW()`,
		WaitlistStatusExpired, artistID, storeID, serviceID, date, WaitlistStatusNotified,
	)
	if err != nil {
		return fmt.Errorf("notify next waitlist entry: expire stale: %w", err)
	}

	// Step 2: find the oldest still-waiting entry for this exact group.
	var entryID, customerID uuid.UUID
	err = r.db.QueryRow(ctx, `
		SELECT id, customer_id
		FROM waitlist_entries
		WHERE artist_id = $1 AND store_id = $2 AND service_id = $3 AND requested_date = $4
		AND status = $5
		ORDER BY created_at ASC
		LIMIT 1`,
		artistID, storeID, serviceID, date, WaitlistStatusWaiting,
	).Scan(&entryID, &customerID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // nobody waiting for this exact group - the common case
		}
		return fmt.Errorf("notify next waitlist entry: find waiting: %w", err)
	}

	deadline := time.Now().Add(waitlistConfirmWindow)
	_, err = r.db.Exec(ctx, `
		UPDATE waitlist_entries
		SET status = $1, notified_at = NOW(), confirm_deadline = $2, updated_at = NOW()
		WHERE id = $3`,
		WaitlistStatusNotified, deadline, entryID,
	)
	if err != nil {
		return fmt.Errorf("notify next waitlist entry: mark notified: %w", err)
	}

	message := fmt.Sprintf(
		"Good news! A spot just opened up for %s. Book now before it's gone - you have about %d minutes: %s/book/%s",
		date.Format("Mon, 2 Jan"), int(waitlistConfirmWindow.Minutes()), customerPWAURL, artistID.String(),
	)
	if err := r.EnqueueNotification(ctx, nil, customerID, "waitlist_slot_open", message); err != nil {
		return fmt.Errorf("notify next waitlist entry: enqueue: %w", err)
	}
	return nil
}

// GetBookingNotificationContext returns the customer and service names
// needed to personalise a WhatsApp message.
func (r *pgRepo) GetBookingNotificationContext(ctx context.Context, bookingID uuid.UUID) (string, string, error) {
	var customerName, serviceName string
	err := r.db.QueryRow(ctx, `
		SELECT u.name, s.name
		FROM bookings b
		JOIN users    u ON u.id = b.customer_id
		JOIN services s ON s.id = b.service_id
		WHERE b.id = $1`,
		bookingID,
	).Scan(&customerName, &serviceName)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrBookingNotFound
		}
		return "", "", fmt.Errorf("get booking notification context: %w", err)
	}
	return customerName, serviceName, nil
}

// EnqueueNotification inserts a pending notification row. See the
// Repository interface doc comment for why this is always safe to call
// regardless of whether Twilio is configured yet.
func (r *pgRepo) EnqueueNotification(ctx context.Context, bookingID *uuid.UUID, userID uuid.UUID, templateName, message string) error {
	payload, err := json.Marshal(map[string]string{"message": message})
	if err != nil {
		return fmt.Errorf("enqueue notification: marshal payload: %w", err)
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO notifications (booking_id, user_id, template_name, channel, payload)
		VALUES ($1, $2, $3, 'whatsapp', $4)`,
		bookingID, userID, templateName, payload,
	)
	if err != nil {
		return fmt.Errorf("enqueue notification: %w", err)
	}
	return nil
}

// AttachGuestAndSubmit repoints a held guest booking to the real guest user and
// transitions it held → pending atomically.
//
// The UPDATE is guarded by `status = 'held' AND held_until > NOW()`, so if the
// background ReleaseExpiredHolds job expired the booking between the service-layer
// read and this write, zero rows are affected and ErrBookingNotHeld is returned.
// The database is the final arbiter - no lost update is possible.
func (r *pgRepo) AttachGuestAndSubmit(
	ctx context.Context,
	bookingID, guestUserID uuid.UUID,
	specialRequests *string,
) error {
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET customer_id      = $2,
		    special_requests = $3,
		    status           = $4,
		    held_until       = NULL,
		    updated_at       = NOW()
		WHERE id         = $1
		AND status       = $5
		AND held_until   > NOW()
		AND deleted_at IS NULL`,
		bookingID, guestUserID, specialRequests, StatusPending, StatusHeld,
	)
	if err != nil {
		return fmt.Errorf("attach guest and submit: %w", err)
	}
	if result.RowsAffected() == 0 {
		return ErrBookingNotHeld
	}
	return nil
}

// ApproveBooking transitions a pending booking to approved and sets deposit deadline.
func (r *pgRepo) ApproveBooking(ctx context.Context, id uuid.UUID, depositDeadline time.Time) (string, error) {
	// Reuses generateReviewToken - same 32 random bytes, same hex encoding,
	// same VARCHAR(64) column shape. Two token generators producing the
	// same thing under different names would be worse than one shared one.
	token, err := generateReviewToken()
	if err != nil {
		return "", err
	}

	// COALESCE, not a plain assignment: a booking only reaches 'approved'
	// once, because this UPDATE is guarded on status = pending - but if a
	// future path ever re-approves, overwriting the token would silently
	// break every calendar link already sent for it.
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1,
		    deposit_deadline = $2,
		    calendar_token = COALESCE(calendar_token, $3),
		    updated_at = NOW()
		WHERE id = $4
		AND status = $5
		AND deleted_at IS NULL`,
		StatusApproved, depositDeadline, token, id, StatusPending,
	)
	if err != nil {
		return "", fmt.Errorf("approve booking: %w", err)
	}
	if result.RowsAffected() == 0 {
		return "", ErrBookingNotPending
	}

	// Read back rather than returning the generated value: COALESCE may
	// have kept an existing token, in which case the one generated above
	// is not the one now stored.
	var stored string
	if err := r.db.QueryRow(ctx,
		`SELECT COALESCE(calendar_token, '') FROM bookings WHERE id = $1`, id,
	).Scan(&stored); err != nil {
		return "", fmt.Errorf("approve booking: read calendar token: %w", err)
	}
	return stored, nil
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

// ConfirmDepositReceived transitions a booking directly from approved to
// confirmed, stamping deposit_paid_at in the same UPDATE. This is the
// single-action path used when the artist confirms a deposit the moment
// she sees the transfer land, collapsing the deposit_paid intermediate
// status into one atomic write rather than two round trips.
//
// reference is bound via COALESCE($4, deposit_reference): a nil reference
// leaves the existing column value untouched (SQL NULL loses to the
// existing value in COALESCE), rather than overwriting a previously-entered
// note with NULL just because this particular call didn't supply one.
func (r *pgRepo) ConfirmDepositReceived(ctx context.Context, id uuid.UUID, reference *string) error {
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1,
		    deposit_paid_at = NOW(),
		    deposit_reference = COALESCE($4, deposit_reference),
		    updated_at = NOW()
		WHERE id = $2
		AND status = $3
		AND deleted_at IS NULL`,
		StatusConfirmed, id, StatusApproved, reference,
	)
	if err != nil {
		return fmt.Errorf("confirm deposit received: %w", err)
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

// CompleteBooking marks a confirmed booking as completed, and generates the
// guest review-link token in the same statement, returning it so the caller
// can enqueue the review-request notification without a second query.
// Regenerating a token that already exists is harmless in practice - a
// booking only reaches 'completed' once, since this UPDATE is guarded by
// "status = StatusConfirmed" and the row won't match a second time - but
// there's no reason to depend on that; a fresh token here is always correct.
func (r *pgRepo) CompleteBooking(ctx context.Context, id uuid.UUID) (string, error) {
	token, err := generateReviewToken()
	if err != nil {
		return "", err
	}

	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1,
		    completed_at = NOW(),
		    review_token = $2,
		    updated_at = NOW()
		WHERE id = $3
		AND status = $4
		AND deleted_at IS NULL`,
		StatusCompleted, token, id, StatusConfirmed,
	)
	if err != nil {
		return "", fmt.Errorf("complete booking: %w", err)
	}
	if result.RowsAffected() == 0 {
		return "", ErrBookingNotFound
	}
	return token, nil
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

// ExpireStalePendingBookings expires pending requests whose own appointment
// time has already passed - a request nobody acted on before its own
// start_time is no longer approvable (ApproveBooking's own start_time guard
// already rejects it), so leaving it sitting in 'pending' forever is stale
// bookkeeping, not a live state. Scoped to a single artist and called
// lazily from the read path that would otherwise show it (see
// ListEnrichedBookingsByArtist), the same self-healing shape as
// ReleaseExpiredHolds above.
func (r *pgRepo) ExpireStalePendingBookings(ctx context.Context, artistID uuid.UUID) (int64, error) {
	result, err := r.db.Exec(ctx, `
		UPDATE bookings
		SET status = $1, updated_at = NOW()
		WHERE artist_id = $2
		AND status = $3
		AND start_time < NOW()
		AND deleted_at IS NULL`,
		StatusExpired, artistID, StatusPending,
	)
	if err != nil {
		return 0, fmt.Errorf("expire stale pending bookings: %w", err)
	}
	return result.RowsAffected(), nil
}

// ── Scan helpers ──────────────────────────────────────────────────────────────

// scanBookings scans multiple rows into a slice of Booking pointers.
//
// NOTE: b.SessionID is intentionally NOT scanned here - see scanBooking.
func scanBookings(rows pgx.Rows) ([]*Booking, error) {
	var bookings []*Booking
	for rows.Next() {
		b := &Booking{}
		if err := rows.Scan(
			&b.ID, &b.SalonID, &b.StoreID, &b.ArtistID, &b.CustomerID, &b.ServiceID,
			&b.StartTime, &b.EndTime, &b.HeldUntil, &b.Status,
			&b.OriginalPrice, &b.DiscountAmount, &b.FinalPrice,
			&b.DepositAmount, &b.DepositDeadline, &b.DepositPaidAt, &b.DepositReference, &b.ReviewToken, &b.CalendarToken,
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

// CreateGuestUser resolves a guest booking's customer identity by phone.
// If an active user already exists with this phone number, reuses that
// identity rather than inserting a new row - this is what makes "one phone
// number = one account" true, which customer OTP login and My Bookings
// both depend on (see migration 014). Only inserts a new row when no
// existing user has this phone.
//
// Deliberately does NOT update the existing user's name to whatever was
// typed this time - the established name is treated as the real identity;
// overwriting it on every repeat booking risks clobbering it with a typo,
// or with someone booking on a friend's behalf using their own phone.
func (r *pgRepo) CreateGuestUser(ctx context.Context, name string, phone string) (uuid.UUID, error) {
	var existingID uuid.UUID
	err := r.db.QueryRow(ctx,
		`SELECT id FROM users WHERE phone = $1 AND deleted_at IS NULL`,
		phone,
	).Scan(&existingID)
	if err == nil {
		return existingID, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, fmt.Errorf("create guest user: lookup by phone: %w", err)
	}

	id := uuid.New()
	// Email is never used - guests cannot log in via email or receive
	// password resets there. The format guest_<uuid>@bedge.guest is unique
	// and out of normal email range.
	email := fmt.Sprintf("guest_%s@bedge.guest", id.String())

	_, err = r.db.Exec(ctx, `
		INSERT INTO users (id, name, email, password_hash, role, phone, status)
		VALUES ($1, $2, $3, 'GUEST_ACCOUNT_NO_PASSWORD', 'customer', $4, 'active')`,
		id, name, email, phone,
	)
	if err != nil {
		// Race: another concurrent guest submission with this same new
		// phone number won the insert between our lookup and our own
		// insert. Rather than fail the booking outright over a timing
		// coincidence, fall back to whichever row just won - the outcome
		// is identical to our lookup simply having run a moment later.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			var winnerID uuid.UUID
			lookupErr := r.db.QueryRow(ctx,
				`SELECT id FROM users WHERE phone = $1 AND deleted_at IS NULL`,
				phone,
			).Scan(&winnerID)
			if lookupErr == nil {
				return winnerID, nil
			}
		}
		return uuid.Nil, fmt.Errorf("create guest user: %w", err)
	}
	return id, nil
}

// toDecimal is a helper to convert a float64 from PostgreSQL NUMERIC to decimal.Decimal.
// Used when pgx scans NUMERIC columns that are not directly supported by shopspring.
func toDecimal(f float64) decimal.Decimal {
	return decimal.NewFromFloat(f)
}
