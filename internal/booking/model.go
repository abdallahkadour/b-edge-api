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

const (
	StatusPending        = "pending"
	StatusApproved       = "approved"
	StatusHeld           = "held"
	StatusDepositPending = "deposit_pending"
	StatusDepositPaid    = "deposit_paid"
	StatusConfirmed      = "confirmed"
	StatusCompleted      = "completed"
	StatusCancelled      = "cancelled"
	StatusExpired        = "expired"
	StatusNoShow         = "no_show"
	StatusRefundDue      = "refund_due"
	StatusRefunded       = "refunded"
)

// ── Channel constants ─────────────────────────────────────────────────────────

const (
	ChannelCustomerPWA     = "customer_pwa"
	ChannelArtistDashboard = "artist_dashboard"
	ChannelWhatsAppBot     = "whatsapp_bot"
	ChannelWalkIn          = "walk_in"
	ChannelPhone           = "phone"
	ChannelInstagram       = "instagram"
)

// SlotHoldDuration is how long a slot is held during customer checkout.
const SlotHoldDuration = 10 * time.Minute

// SystemGuestPlaceholderID is the customer_id used for a held guest booking
// BEFORE the customer enters their name and phone on the details screen (C-05).
//
// The customer taps a slot on C-04 to hold it, but their identity is not known
// until C-05. The bookings table requires customer_id NOT NULL, so a held guest
// booking points at this single well-known system user until SubmitGuestBooking
// creates the real guest user and repoints the booking.
//
// A held booking is never shown in the artist dashboard (held is not a dashboard
// status), so this placeholder never appears in any UI. If a guest abandons the
// form, ReleaseExpiredHolds expires the booking and there is nothing to clean up
// because no real guest user was ever created.
//
// REQUIRED SEED — insert this user once (see seed_system_guest.sql):
//
//	INSERT INTO users (id, name, email, password_hash, role, phone, status)
//	VALUES ('00000000-0000-0000-0000-0000000000ff', 'Held Slot Placeholder',
//	        'system_held@bedge.system', 'SYSTEM_ACCOUNT_NO_LOGIN',
//	        'customer', '+00000000000', 'active')
//	ON CONFLICT (id) DO NOTHING;
var SystemGuestPlaceholderID = uuid.MustParse("00000000-0000-0000-0000-0000000000ff")

// ── Sentinel errors ───────────────────────────────────────────────────────────

var (
	ErrBookingNotFound       = errors.New("booking not found")
	ErrSlotUnavailable       = errors.New("slot unavailable")
	ErrBookingNotPending     = errors.New("booking is not in pending status")
	ErrBookingNotApproved    = errors.New("booking is not in approved status")
	ErrBookingNotCancellable = errors.New("booking cannot be cancelled in its current status")
	ErrNotBookingOwner       = errors.New("not authorised to act on this booking")
	// ErrBookingNotHeld is returned when a held guest booking can no longer be
	// submitted — it was already submitted or its 10-minute hold expired.
	ErrBookingNotHeld = errors.New("booking is not in held status")
)

// ── Core structs ──────────────────────────────────────────────────────────────

// Booking represents a single appointment in the bookings table.
type Booking struct {
	ID         uuid.UUID `db:"id"`
	SalonID    uuid.UUID `db:"salon_id"`
	StoreID    uuid.UUID `db:"store_id"`
	ArtistID   uuid.UUID `db:"artist_id"`
	CustomerID uuid.UUID `db:"customer_id"`
	ServiceID  uuid.UUID `db:"service_id"`
	// SessionID is reserved for migration 005 (multi-artist sessions). Always nil.
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

// HoldGuestSlotResponse is returned when a guest holds a slot.
type HoldGuestSlotResponse struct {
	BookingID uuid.UUID `json:"booking_id"`
	HeldUntil time.Time `json:"held_until"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
}

// Store holds booking-relevant fields from the stores table.
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

// BusinessHours holds working hours for a store on a given day.
type BusinessHours struct {
	ID        uuid.UUID `db:"id"`
	StoreID   uuid.UUID `db:"store_id"`
	DayOfWeek int       `db:"day_of_week"`
	OpenTime  string    `db:"open_time"`
	CloseTime string    `db:"close_time"`
	IsOpen    bool      `db:"is_open"`
}

// BusinessHoursException overrides regular hours for a specific date.
type BusinessHoursException struct {
	ID            uuid.UUID `db:"id"`
	StoreID       uuid.UUID `db:"store_id"`
	ExceptionDate time.Time `db:"exception_date"`
	IsClosed      bool      `db:"is_closed"`
	OpenTime      *string   `db:"open_time"`
	CloseTime     *string   `db:"close_time"`
	Reason        *string   `db:"reason"`
}

// BlockingStatuses are the booking statuses that occupy a slot.
var BlockingStatuses = []string{
	StatusPending,
	StatusApproved,
	StatusHeld,
	StatusDepositPending,
	StatusDepositPaid,
	StatusConfirmed,
}

// SalonService holds booking-relevant fields from the services table.
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

// ArtistStoreBuffer holds travel buffer config between two stores.
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

// TimeRange is an internal helper representing a blocked time window.
type TimeRange struct {
	Start time.Time
	End   time.Time
}

// Overlaps returns true if this TimeRange overlaps with another.
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
// Used by authenticated customers and the artist dashboard.
type CreateBookingRequest struct {
	ArtistID        string  `json:"artist_id"        validate:"required,uuid"`
	StoreID         string  `json:"store_id"         validate:"required,uuid"`
	ServiceID       string  `json:"service_id"       validate:"required,uuid"`
	StartTime       string  `json:"start_time"       validate:"required"`
	SpecialRequests *string `json:"special_requests"`
	Channel         string  `json:"channel"          validate:"required,oneof=customer_pwa artist_dashboard walk_in phone instagram"`
}

// HoldGuestSlotRequest is the body for POST /api/v1/bookings/guest/hold.
//
// Sent when the customer taps a time slot on C-04. No identity is collected yet —
// only the chosen slot. The server creates a held booking (10-minute hold) pointed
// at the system placeholder customer and returns its ID for the submit step.
type HoldGuestSlotRequest struct {
	ArtistID  string `json:"artist_id"  validate:"required,uuid"`
	StoreID   string `json:"store_id"   validate:"required,uuid"`
	ServiceID string `json:"service_id" validate:"required,uuid"`
	StartTime string `json:"start_time" validate:"required"`
}

// SubmitGuestBookingRequest is the body for PATCH /api/v1/bookings/guest/:id/submit.
//
// Sent when the customer fills the details form on C-05. Carries the guest's
// identity, which is used to create the real guest user that replaces the
// placeholder on the held booking.
type SubmitGuestBookingRequest struct {
	Name            string  `json:"name"             validate:"required,min=2,max=100"`
	Phone           string  `json:"phone"            validate:"required,min=7,max=20"`
	SpecialRequests *string `json:"special_requests"`
}

// ApproveBookingRequest is the request body for PATCH /bookings/:id/approve.
type ApproveBookingRequest struct {
	DepositDeadlineHours *int `json:"deposit_deadline_hours"`
}

// CancelBookingRequest is the request body for PATCH /bookings/:id/cancel.
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
