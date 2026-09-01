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
// REQUIRED SEED - insert this user once (see seed_system_guest.sql):
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
	// submitted - it was already submitted or its 10-minute hold expired.
	ErrBookingNotHeld = errors.New("booking is not in held status")
	// ErrArtistNotFound is returned when a user has no matching artists row
	// used when resolving a requester's users.id to their artists.id for
	// ownership checks (bookings.artist_id references artists.id, not users.id).
	ErrArtistNotFound = errors.New("artist profile not found for user")
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
	SessionID       *uuid.UUID      `db:"session_id"`
	StartTime       time.Time       `db:"start_time"`
	EndTime         time.Time       `db:"end_time"`
	HeldUntil       *time.Time      `db:"held_until"`
	Status          string          `db:"status"`
	OriginalPrice   decimal.Decimal `db:"original_price"`
	DiscountAmount  decimal.Decimal `db:"discount_amount"`
	FinalPrice      decimal.Decimal `db:"final_price"`
	DepositAmount   decimal.Decimal `db:"deposit_amount"`
	DepositDeadline *time.Time      `db:"deposit_deadline"`
	DepositPaidAt   *time.Time      `db:"deposit_paid_at"`
	// DepositReference is an optional artist-entered note captured on
	// confirmation - e.g. an OMT/Wish transaction code. Free text, never
	// customer-facing.
	DepositReference *string `db:"deposit_reference"`
	// ReviewToken is generated when the booking completes, letting the guest
	// leave a review with no login required. See migration 013.
	ReviewToken        *string    `db:"review_token"`
	Channel            string     `db:"channel"`
	SpecialRequests    *string    `db:"special_requests"`
	CancellationReason *string    `db:"cancellation_reason"`
	CancelledAt        *time.Time `db:"cancelled_at"`
	CompletedAt        *time.Time `db:"completed_at"`
	NoShowAt           *time.Time `db:"no_show_at"`
	CreatedAt          time.Time  `db:"created_at"`
	UpdatedAt          time.Time  `db:"updated_at"`
	DeletedAt          *time.Time `db:"deleted_at"`
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
	// Timezone is the store's IANA zone (e.g. "Asia/Beirut"). early_bird_cutoff
	// and the store's business hours are wall-clock local times in this zone,
	// resolved to instants at query time so DST transitions are handled.
	Timezone string `db:"timezone"`
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

// CalendarStatuses are the booking statuses shown on the artist calendar grid.
// These are the committed/booked appointments - pending requests live in a
// separate "requests" tab, and held/expired/cancelled/refunded are noise that
// never belongs on the schedule. Mirrors how salon dashboards separate the
// confirmed calendar from the incoming-request queue.
var CalendarStatuses = []string{
	StatusApproved,
	StatusDepositPaid,
	StatusConfirmed,
	StatusCompleted,
	StatusNoShow,
}

// ListBookingsByArtistFilter is the optional filter for the artist bookings list
// endpoint. An empty Status means "all statuses" (the default list behaviour).
// When set, it must be one of the known booking statuses; the service validates.
type ListBookingsByArtistFilter struct {
	Status string // optional exact status match; "" = all
}

// ValidBookingStatuses is the set of statuses a client may filter on. Used to
// reject unknown ?status= values with a clear error instead of silently
// returning an empty list.
var ValidBookingStatuses = map[string]bool{
	StatusPending:        true,
	StatusApproved:       true,
	StatusHeld:           true,
	StatusDepositPending: true,
	StatusDepositPaid:    true,
	StatusConfirmed:      true,
	StatusCompleted:      true,
	StatusCancelled:      true,
	StatusExpired:        true,
	StatusNoShow:         true,
	StatusRefundDue:      true,
	StatusRefunded:       true,
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
// Sent when the customer taps a time slot on C-04. No identity is collected yet
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
	DepositReference   *string         `json:"deposit_reference,omitempty"`
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
		DepositReference:   b.DepositReference,
		Channel:            b.Channel,
		SpecialRequests:    b.SpecialRequests,
		CancellationReason: b.CancellationReason,
		CreatedAt:          b.CreatedAt,
	}
}

// ── Enriched booking types ────────────────────────────────────────────────────

// EnrichedBooking is a Booking joined with the human-readable names every
// booking-display screen needs: customer name/phone, service name, store name/city.
// It is the internal row type returned by the enriched repository queries.
type EnrichedBooking struct {
	Booking
	CustomerName  string  `db:"customer_name"`
	CustomerPhone *string `db:"customer_phone"`
	ArtistName    string  `db:"artist_name"`
	ServiceName   string  `db:"service_name"`
	StoreName     string  `db:"store_name"`
	StoreCity     string  `db:"store_city"`
}

// EnrichedBookingResponse is the client representation of a booking with all the
// joined names resolved. Returned by booking detail and list endpoints so the
// dashboard and customer screens never have to resolve UUIDs themselves.
//
// Money fields serialize as strings via decimal.Decimal to preserve precision.
type EnrichedBookingResponse struct {
	ID         uuid.UUID `json:"id"`
	SalonID    uuid.UUID `json:"salon_id"`
	StoreID    uuid.UUID `json:"store_id"`
	ArtistID   uuid.UUID `json:"artist_id"`
	CustomerID uuid.UUID `json:"customer_id"`
	ServiceID  uuid.UUID `json:"service_id"`
	// Joined display fields
	CustomerName  string  `json:"customer_name"`
	CustomerPhone *string `json:"customer_phone,omitempty"`
	ArtistName    string  `json:"artist_name"`
	ServiceName   string  `json:"service_name"`
	StoreName     string  `json:"store_name"`
	StoreCity     string  `json:"store_city"`
	// Times
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`
	Status    string    `json:"status"`
	// Money
	OriginalPrice  decimal.Decimal `json:"original_price"`
	DiscountAmount decimal.Decimal `json:"discount_amount"`
	FinalPrice     decimal.Decimal `json:"final_price"`
	DepositAmount  decimal.Decimal `json:"deposit_amount"`
	// Deposit lifecycle
	DepositDeadline  *time.Time `json:"deposit_deadline,omitempty"`
	DepositPaidAt    *time.Time `json:"deposit_paid_at,omitempty"`
	DepositReference *string    `json:"deposit_reference,omitempty"`
	// ReviewToken is present once the booking is completed. Artist-facing
	// only - used to build a review-request link to send the customer
	// manually (Calendar detail view) until automated WhatsApp delivery
	// exists. Deliberately NOT exposed on the plain BookingResponse the
	// guest funnel returns.
	ReviewToken *string `json:"review_token,omitempty"`
	// Meta
	Channel            string    `json:"channel"`
	SpecialRequests    *string   `json:"special_requests,omitempty"`
	CancellationReason *string   `json:"cancellation_reason,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

// toEnrichedResponse converts an EnrichedBooking row to its client representation.
func toEnrichedResponse(e *EnrichedBooking) *EnrichedBookingResponse {
	return &EnrichedBookingResponse{
		ID:                 e.ID,
		SalonID:            e.SalonID,
		StoreID:            e.StoreID,
		ArtistID:           e.ArtistID,
		CustomerID:         e.CustomerID,
		ServiceID:          e.ServiceID,
		CustomerName:       e.CustomerName,
		CustomerPhone:      e.CustomerPhone,
		ArtistName:         e.ArtistName,
		ServiceName:        e.ServiceName,
		StoreName:          e.StoreName,
		StoreCity:          e.StoreCity,
		StartTime:          e.StartTime,
		EndTime:            e.EndTime,
		Status:             e.Status,
		OriginalPrice:      e.OriginalPrice,
		DiscountAmount:     e.DiscountAmount,
		FinalPrice:         e.FinalPrice,
		DepositAmount:      e.DepositAmount,
		DepositDeadline:    e.DepositDeadline,
		DepositPaidAt:      e.DepositPaidAt,
		DepositReference:   e.DepositReference,
		ReviewToken:        e.ReviewToken,
		Channel:            e.Channel,
		SpecialRequests:    e.SpecialRequests,
		CancellationReason: e.CancellationReason,
		CreatedAt:          e.CreatedAt,
	}
}

const (
	RoleCustomer = "customer"
	RoleArtist   = "artist"
	RoleAdmin    = "admin"
)

// ── Waitlist (PRD §9.5) ──────────────────────────────────────────────────────

// Waitlist entry statuses.
const (
	WaitlistStatusWaiting   = "waiting"
	WaitlistStatusNotified  = "notified"
	WaitlistStatusExpired   = "expired"
	WaitlistStatusCancelled = "cancelled"
)

// waitlistConfirmWindow is how long a notified customer has to book before
// the next person in line gets notified instead. PRD §9.5 gives "e.g. 30
// minutes" as an example, not a hard number - 30 minutes taken as the
// actual default.
const waitlistConfirmWindow = 30 * time.Minute

// WaitlistEntry mirrors a row in waitlist_entries.
type WaitlistEntry struct {
	ID              uuid.UUID
	ArtistID        uuid.UUID
	StoreID         uuid.UUID
	ServiceID       uuid.UUID
	CustomerID      uuid.UUID
	RequestedDate   time.Time // date only, time component unused
	Status          string
	NotifiedAt      *time.Time
	ConfirmDeadline *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// JoinWaitlistRequest is the body for POST /bookings/waitlist. Public - no
// account needed, matching the guest-booking philosophy everywhere else in
// this app. Identity is resolved by phone, same as a guest booking.
type JoinWaitlistRequest struct {
	ArtistID      string `json:"artist_id"      validate:"required,uuid"`
	StoreID       string `json:"store_id"       validate:"required,uuid"`
	ServiceID     string `json:"service_id"     validate:"required,uuid"`
	RequestedDate string `json:"requested_date" validate:"required"` // YYYY-MM-DD
	Name          string `json:"name"           validate:"required,min=2,max=100"`
	Phone         string `json:"phone"          validate:"required,min=7,max=20"`
}

// WaitlistEntryResponse is the artist-facing view of a queue entry
// includes customer name/phone (the artist-facing bookings list already
// exposes these for the same reason: the artist needs to be able to reach
// the person, not just know an ID exists).
type WaitlistEntryResponse struct {
	ID              uuid.UUID  `json:"id"`
	ServiceID       uuid.UUID  `json:"service_id"`
	ServiceName     string     `json:"service_name"`
	CustomerName    string     `json:"customer_name"`
	CustomerPhone   string     `json:"customer_phone"`
	RequestedDate   string     `json:"requested_date"`
	Status          string     `json:"status"`
	NotifiedAt      *time.Time `json:"notified_at,omitempty"`
	ConfirmDeadline *time.Time `json:"confirm_deadline,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// ── Bulk schedule operations ─────────────────────────────────────────────────

// MovableStatuses are the booking statuses a bulk schedule operation may
// move or cancel.
//
// Deliberately narrower than BlockingStatuses. A completed or no-show
// booking still occupies its slot for availability purposes, but it is
// history - rewriting when it happened would be falsifying a record.
// cancelled/expired/refunded are already dead and excluded from the
// overlap constraint anyway.
var MovableStatuses = []string{
	StatusPending, StatusApproved, StatusHeld,
	StatusDepositPending, StatusDepositPaid, StatusConfirmed,
}

// isMovable reports whether a booking's status permits a bulk move.
func isMovable(status string) bool {
	for _, s := range MovableStatuses {
		if s == status {
			return true
		}
	}
	return false
}

// ShiftPreviewRequest is the body for POST /bookings/schedule/shift-preview.
type ShiftPreviewRequest struct {
	StoreID string `json:"store_id" validate:"required,uuid4"`
	// Date is the store-LOCAL calendar day, "YYYY-MM-DD". Resolved through
	// the store's IANA zone rather than the server's - at 23:00 UTC it is
	// already tomorrow in Beirut, so a UTC reading would preview the wrong
	// day for two hours every night.
	Date string `json:"date" validate:"required"`
	// ShiftMinutes may be negative to pull a day earlier. Bounded so a
	// mistyped value cannot fling a day into next week.
	//
	// Zero is rejected, but NOT by `required` - that would conflate "you
	// sent 0" with "you sent nothing" and produce a validation error whose
	// message contradicts the min/max range advertised right here. A
	// pointer plus an explicit check lets the two cases carry different,
	// honest errors. Found by E2E-TEST-PLAN.md section 12.2 (finding 2).
	ShiftMinutes *int `json:"shift_minutes" validate:"required,min=-240,max=240"`
}

// ShiftPreviewItem is one booking that would move.
type ShiftPreviewItem struct {
	BookingID    uuid.UUID `json:"booking_id"`
	CustomerName string    `json:"customer_name"`
	ServiceName  string    `json:"service_name"`
	CurrentStart time.Time `json:"current_start"`
	NewStart     time.Time `json:"new_start"`
	NewEnd       time.Time `json:"new_end"`
	Status       string    `json:"status"`
	// HasPhone is false when no number is on file, meaning this customer
	// cannot be notified and would need a call.
	HasPhone bool `json:"has_phone"`
}

// ShiftBlockerReason explains why a shift cannot be applied.
type ShiftBlockerReason string

// The reasons a shift is refused. Each names a booking so the artist can
// act on it rather than being told only that "something" is wrong.
const (
	// BlockerPastClosing - the booking would end after the store closes.
	BlockerPastClosing ShiftBlockerReason = "past_closing"
	// BlockerBeforeOpening - a negative shift would start it before opening.
	BlockerBeforeOpening ShiftBlockerReason = "before_opening"
	// BlockerIntoPast - a negative shift would move it to a past time.
	BlockerIntoPast ShiftBlockerReason = "into_past"
	// BlockerTravelBuffer - would leave too little travel time against a
	// booking at another store. Specific to artists working several
	// branches; see stores.weekday_buffer_min and artist_store_buffers.
	BlockerTravelBuffer ShiftBlockerReason = "travel_buffer"
	// BlockerStoreClosed - the store does not trade that day at all.
	BlockerStoreClosed ShiftBlockerReason = "store_closed"
)

// ShiftBlocker is one reason the shift cannot proceed.
type ShiftBlocker struct {
	Reason    ShiftBlockerReason `json:"reason"`
	BookingID *uuid.UUID         `json:"booking_id,omitempty"`
	Detail    string             `json:"detail"`
}

// ShiftSkipReason explains why a booking is excluded from the shift.
type ShiftSkipReason string

// Skipped bookings are not errors - they are simply out of scope.
const (
	// SkipInProgress - already started. Telling someone mid-appointment
	// that they are delayed is meaningless.
	SkipInProgress ShiftSkipReason = "in_progress"
	// SkipTerminalStatus - completed, cancelled or no-show. History.
	SkipTerminalStatus ShiftSkipReason = "terminal_status"
)

// ShiftSkipped is one booking excluded from the operation.
type ShiftSkipped struct {
	BookingID uuid.UUID       `json:"booking_id"`
	Reason    ShiftSkipReason `json:"reason"`
	Status    string          `json:"status"`
}

// ShiftPreviewResponse is the full dry-run result.
//
// Read-only by construction: this endpoint writes nothing, so an artist can
// see the consequences of a shift before committing to one. Blockers are
// all-or-nothing - a partially shifted day is worse than an unshifted one,
// because half the customers get told about a change that did not happen to
// the other half.
type ShiftPreviewResponse struct {
	Date         string             `json:"date"`
	ShiftMinutes int                `json:"shift_minutes"`
	Movable      []ShiftPreviewItem `json:"movable"`
	Skipped      []ShiftSkipped     `json:"skipped"`
	Blockers     []ShiftBlocker     `json:"blockers"`
	// CanApply is true only when there are movable bookings and no
	// blockers. The UI should gate the apply button on exactly this.
	CanApply bool `json:"can_apply"`
	// NotifiableCount is how many movable bookings have a phone number.
	// A gap between this and len(Movable) is customers who must be phoned.
	NotifiableCount int `json:"notifiable_count"`
}
