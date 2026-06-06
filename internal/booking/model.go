// Package booking implements the booking domain for B-Edge,
// including slot availability, booking lifecycle, deposit flow,
// cancellation policy, and waitlist management.
package booking

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ── Booking status constants ──────────────────────────────────────────────────
// These map exactly to the CHECK constraint in 001_initial_schema.up.sql.

const (
	// StatusPending means the customer submitted — awaiting artist approval.
	StatusPending = "pending"

	// StatusApproved means the artist approved — awaiting deposit payment.
	StatusApproved = "approved"

	// StatusHeld means the slot is reserved during checkout for 10 minutes.
	StatusHeld = "held"

	// StatusDepositPending means the deposit deadline is set but not yet paid.
	StatusDepositPending = "deposit_pending"

	// StatusDepositPaid means the deposit was paid — awaiting artist confirmation.
	StatusDepositPaid = "deposit_paid"

	// StatusConfirmed means deposit verified — booking is confirmed.
	StatusConfirmed = "confirmed"

	// StatusCompleted means the service was delivered.
	StatusCompleted = "completed"

	// StatusCancelled means cancelled by artist or customer.
	StatusCancelled = "cancelled"

	// StatusExpired means the deposit deadline passed — slot released.
	StatusExpired = "expired"

	// StatusNoShow means the customer did not arrive.
	StatusNoShow = "no_show"

	// StatusRefundDue means refund approved but not yet sent.
	StatusRefundDue = "refund_due"

	// StatusRefunded means the refund was sent to the customer.
	StatusRefunded = "refunded"
)

// ── Channel constants ─────────────────────────────────────────────────────────

const (
	// ChannelCustomerPWA means booked through the customer Progressive Web App.
	ChannelCustomerPWA = "customer_pwa"

	// ChannelArtistDashboard means booked by the artist on behalf of a customer.
	ChannelArtistDashboard = "artist_dashboard"

	// ChannelWhatsAppBot means booked through the WhatsApp bot (Phase 3).
	ChannelWhatsAppBot = "whatsapp_bot"

	// ChannelWalkIn means the customer walked in without a prior booking.
	ChannelWalkIn = "walk_in"

	// ChannelPhone means booked via phone call by the artist.
	ChannelPhone = "phone"

	// ChannelInstagram means booked via Instagram DM converted to a booking.
	ChannelInstagram = "instagram"
)

// ── Slot hold duration ────────────────────────────────────────────────────────

// SlotHoldDuration is how long a slot is held during customer checkout.
// GIST constraint treats held slots as booked — no one else can take the slot.
const SlotHoldDuration = 10 * time.Minute

// ── Sentinel errors ───────────────────────────────────────────────────────────

var (
	// ErrBookingNotFound is returned when no booking matches the given criteria.
	ErrBookingNotFound = errors.New("booking not found")

	// ErrSlotUnavailable is returned when the requested slot is already taken.
	// Maps to PostgreSQL GIST exclusion violation — error code 23P01.
	ErrSlotUnavailable = errors.New("slot unavailable")

	// ErrBookingNotPending is returned when trying to approve a non-pending booking.
	ErrBookingNotPending = errors.New("booking is not in pending status")

	// ErrBookingNotApproved is returned when trying to confirm deposit on a non-approved booking.
	ErrBookingNotApproved = errors.New("booking is not in approved status")

	// ErrBookingNotCancellable is returned when a booking cannot be cancelled in its current status.
	ErrBookingNotCancellable = errors.New("booking cannot be cancelled in its current status")

	// ErrNotBookingOwner is returned when a user tries to act on a booking they do not own.
	ErrNotBookingOwner = errors.New("not authorised to act on this booking")
)

// ── Core structs ──────────────────────────────────────────────────────────────

// Booking represents a single appointment in the bookings table.
// Every field maps exactly to a column in the migration.
// Money fields use decimal.Decimal — never float64.
type Booking struct {
	ID         uuid.UUID `db:"id"`
	SalonID    uuid.UUID `db:"salon_id"`
	StoreID    uuid.UUID `db:"store_id"`
	ArtistID   uuid.UUID `db:"artist_id"`
	CustomerID uuid.UUID `db:"customer_id"`
	ServiceID  uuid.UUID `db:"service_id"`
	// SessionID links multiple bookings that belong to the same client session.
	// NULL for single-artist bookings. Set when a service requires multiple artists.
	// Added via migration 005 when multi-artist session support is implemented.
	SessionID          *uuid.UUID      `db:"session_id"`
	StartTime          time.Time       `db:"start_time"`
	EndTime            time.Time       `db:"end_time"`
	HeldUntil          *time.Time      `db:"held_until"`
	Status             string          `db:"status"`
	OriginalPrice      decimal.Decimal `db:"original_price"`
	DiscountAmount     decimal.Decimal `db:"discount_amount"`
	FinalPrice         decimal.Decimal `db:"final_price"`
	DepositAmount      decimal.Decimal `db:"deposit_amount"`
	DepositDeadline    *time.Time      `db:"deposit_deadline"`
	DepositPaidAt      *time.Time      `db:"deposit_paid_at"`
	Channel            string          `db:"channel"`
	SpecialRequests    *string         `db:"special_requests"`
	CancellationReason *string         `db:"cancellation_reason"`
	CancelledAt        *time.Time      `db:"cancelled_at"`
	CompletedAt        *time.Time      `db:"completed_at"`
	NoShowAt           *time.Time      `db:"no_show_at"`
	CreatedAt          time.Time       `db:"created_at"`
	UpdatedAt          time.Time       `db:"updated_at"`
	DeletedAt          *time.Time      `db:"deleted_at"`
}

// Store holds the booking-relevant fields from the stores table.
// Used by the slot availability algorithm.
type Store struct {
	ID                 uuid.UUID       `db:"id"`
	SalonID            uuid.UUID       `db:"salon_id"`
	Name               string          `db:"name"`
	City               string          `db:"city"`
	SameDayNoticeHours int             `db:"same_day_notice_hours"`
	EarlyBirdCutoff    *string         `db:"early_bird_cutoff"`
	EarlyBirdFee       decimal.Decimal `db:"early_bird_fee"`
	WeekdayBufferMin   int             `db:"weekday_buffer_min"`
	WeekendBufferMin   int             `db:"weekend_buffer_min"`
	IsActive           bool            `db:"is_active"`
}

// BusinessHours holds the working hours for a store on a given day of week.
type BusinessHours struct {
	ID        uuid.UUID `db:"id"`
	StoreID   uuid.UUID `db:"store_id"`
	DayOfWeek int       `db:"day_of_week"`
	OpenTime  string    `db:"open_time"`
	CloseTime string    `db:"close_time"`
	IsOpen    bool      `db:"is_open"`
}

// BusinessHoursException overrides regular hours for a specific date.
// Used for holidays and special operating hours.
type BusinessHoursException struct {
	ID            uuid.UUID `db:"id"`
	StoreID       uuid.UUID `db:"store_id"`
	ExceptionDate time.Time `db:"exception_date"`
	IsClosed      bool      `db:"is_closed"`
	OpenTime      *string   `db:"open_time"`
	CloseTime     *string   `db:"close_time"`
	Reason        *string   `db:"reason"`
}

var BlockingStatuses = []string{
	StatusPending,
	StatusApproved,
	StatusHeld,
	StatusDepositPending,
	StatusDepositPaid,
	StatusConfirmed,
}

// Service holds the booking-relevant fields from the services table.
type SalonService struct {
	ID                   uuid.UUID       `db:"id"`
	SalonID              uuid.UUID       `db:"salon_id"`
	Name                 string          `db:"name"`
	DurationMin          int             `db:"duration_min"`
	ActiveDurationMin    *int            `db:"active_duration_min"`
	Price                decimal.Decimal `db:"price"`
	DepositAmount        decimal.Decimal `db:"deposit_amount"`
	DepositDeadlineHours int             `db:"deposit_deadline_hours"`
	IsActive             bool            `db:"is_active"`
}

// ArtistStoreBuffer holds the travel buffer configuration for an artist
// travelling between two specific stores.
type ArtistStoreBuffer struct {
	ID               uuid.UUID `db:"id"`
	ArtistID         uuid.UUID `db:"artist_id"`
	FromStoreID      uuid.UUID `db:"from_store_id"`
	ToStoreID        uuid.UUID `db:"to_store_id"`
	WeekdayBufferMin int       `db:"weekday_buffer_min"`
	WeekendBufferMin int       `db:"weekend_buffer_min"`
}

// ── Slot availability types ───────────────────────────────────────────────────

// TimeSlot represents a single available booking slot returned to the customer.
type TimeSlot struct {
	StartTime    time.Time       `json:"start_time"`
	EndTime      time.Time       `json:"end_time"`
	IsEarlyBird  bool            `json:"is_early_bird"`
	EarlyBirdFee decimal.Decimal `json:"early_bird_fee,omitempty"`
}

// TimeRange is an internal helper used by the slot algorithm
// to represent a blocked time window.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Overlaps returns true if this TimeRange overlaps with another.
// Used in the slot availability algorithm to detect conflicts.
func (r TimeRange) Overlaps(other TimeRange) bool {
	return r.Start.Before(other.End) && other.Start.Before(r.End)
}

// ── Request structs ───────────────────────────────────────────────────────────

// GetAvailableSlotsRequest is the query input for GET /api/v1/bookings/slots.
type GetAvailableSlotsRequest struct {
	ArtistID  string `query:"artist_id"  validate:"required,uuid"`
	StoreID   string `query:"store_id"   validate:"required,uuid"`
	ServiceID string `query:"service_id" validate:"required,uuid"`
	Date      string `query:"date"       validate:"required"`
}

// CreateBookingRequest is the request body for POST /api/v1/bookings.
type CreateBookingRequest struct {
	ArtistID        string  `json:"artist_id"         validate:"required,uuid"`
	StoreID         string  `json:"store_id"          validate:"required,uuid"`
	ServiceID       string  `json:"service_id"        validate:"required,uuid"`
	StartTime       string  `json:"start_time"        validate:"required"`
	SpecialRequests *string `json:"special_requests"`
	Channel         string  `json:"channel"           validate:"required,oneof=customer_pwa artist_dashboard walk_in phone instagram"`
}

// ApproveBookingRequest is the request body for PATCH /api/v1/bookings/:id/approve.
type ApproveBookingRequest struct {
	DepositDeadlineHours *int `json:"deposit_deadline_hours"`
}

// CancelBookingRequest is the request body for PATCH /api/v1/bookings/:id/cancel.
type CancelBookingRequest struct {
	Reason *string `json:"reason"`
}

// ── Response structs ──────────────────────────────────────────────────────────

// BookingResponse is the safe representation of a booking returned to clients.
type BookingResponse struct {
	ID                 uuid.UUID       `json:"id"`
	SalonID            uuid.UUID       `json:"salon_id"`
	StoreID            uuid.UUID       `json:"store_id"`
	ArtistID           uuid.UUID       `json:"artist_id"`
	CustomerID         uuid.UUID       `json:"customer_id"`
	ServiceID          uuid.UUID       `json:"service_id"`
	StartTime          time.Time       `json:"start_time"`
	EndTime            time.Time       `json:"end_time"`
	Status             string          `json:"status"`
	OriginalPrice      decimal.Decimal `json:"original_price"`
	DiscountAmount     decimal.Decimal `json:"discount_amount"`
	FinalPrice         decimal.Decimal `json:"final_price"`
	DepositAmount      decimal.Decimal `json:"deposit_amount"`
	DepositDeadline    *time.Time      `json:"deposit_deadline,omitempty"`
	DepositPaidAt      *time.Time      `json:"deposit_paid_at,omitempty"`
	Channel            string          `json:"channel"`
	SpecialRequests    *string         `json:"special_requests,omitempty"`
	CancellationReason *string         `json:"cancellation_reason,omitempty"`
	CreatedAt          time.Time       `json:"created_at"`
}

// toResponse converts a Booking to its safe client representation.
func toResponse(b *Booking) *BookingResponse {
	return &BookingResponse{
		ID:                 b.ID,
		SalonID:            b.SalonID,
		StoreID:            b.StoreID,
		ArtistID:           b.ArtistID,
		CustomerID:         b.CustomerID,
		ServiceID:          b.ServiceID,
		StartTime:          b.StartTime,
		EndTime:            b.EndTime,
		Status:             b.Status,
		OriginalPrice:      b.OriginalPrice,
		DiscountAmount:     b.DiscountAmount,
		FinalPrice:         b.FinalPrice,
		DepositAmount:      b.DepositAmount,
		DepositDeadline:    b.DepositDeadline,
		DepositPaidAt:      b.DepositPaidAt,
		Channel:            b.Channel,
		SpecialRequests:    b.SpecialRequests,
		CancellationReason: b.CancellationReason,
		CreatedAt:          b.CreatedAt,
	}
}

const (
	RoleCustomer = "customer"
	RoleArtist   = "artist"
	RoleAdmin    = "admin"
)
