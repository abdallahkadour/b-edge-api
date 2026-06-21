// Package client implements the artist-facing CRM for B-Edge: the list of an
// artist's clients (customers who have completed at least one booking), each
// client's aggregated history and metrics, and the artist's private per-client
// notes. Client identity and metrics are derived from bookings, users, and
// reviews; only the private note is stored (in the client_notes table).
package client

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// completedStatus is the booking status that counts toward client metrics.
// Spend and visit history reflect money actually earned, so only completed
// bookings are aggregated.
const completedStatus = "completed"

// ── Sentinel errors ───────────────────────────────────────────────────────────

var (
	// ErrClientNotFound is returned when a customer has no completed bookings
	// with the requesting artist (i.e. is not the artist's client).
	ErrClientNotFound = errors.New("client not found for this artist")

	// ErrArtistNotFound is returned when the requesting user has no artist profile.
	ErrArtistNotFound = errors.New("artist profile not found for user")
)

// ── Internal row types ────────────────────────────────────────────────────────

// ClientRow is one aggregated client row from the list query.
type ClientRow struct {
	CustomerID    uuid.UUID        `db:"customer_id"`
	Name          string           `db:"name"`
	Phone         *string          `db:"phone"`
	BookingsCount int              `db:"bookings_count"`
	TotalSpent    decimal.Decimal  `db:"total_spent"`
	LastService   *string          `db:"last_service"`
	LastVisit     *time.Time       `db:"last_visit"`
	AverageRating *decimal.Decimal `db:"average_rating"`
	NoteContent   string           `db:"note_content"`
}

// BookingHistoryRow is one past booking in a client's profile timeline.
type BookingHistoryRow struct {
	ID          uuid.UUID       `db:"id"`
	ServiceName string          `db:"service_name"`
	StoreName   string          `db:"store_name"`
	StartTime   time.Time       `db:"start_time"`
	Status      string          `db:"status"`
	FinalPrice  decimal.Decimal `db:"final_price"`
}

// ── Request structs ───────────────────────────────────────────────────────────

// UpsertNoteRequest is the body for PUT /api/v1/clients/:customer_id/notes.
type UpsertNoteRequest struct {
	Content string `json:"content" validate:"max=2000"`
}

// ── Response structs ──────────────────────────────────────────────────────────

// ClientCard is one entry in the artist's client list.
// Money serializes as a string via decimal to preserve precision.
type ClientCard struct {
	CustomerID    uuid.UUID        `json:"customer_id"`
	Name          string           `json:"name"`
	Phone         *string          `json:"phone,omitempty"`
	BookingsCount int              `json:"bookings_count"`
	TotalSpent    decimal.Decimal  `json:"total_spent"`
	LastService   *string          `json:"last_service,omitempty"`
	LastVisit     *time.Time       `json:"last_visit,omitempty"`
	AverageRating *decimal.Decimal `json:"average_rating,omitempty"`
	IsVIP         bool             `json:"is_vip"`
}

// ClientProfile is the full client view: identity, metrics, private note, and
// booking history.
type ClientProfile struct {
	CustomerID    uuid.UUID        `json:"customer_id"`
	Name          string           `json:"name"`
	Phone         *string          `json:"phone,omitempty"`
	BookingsCount int              `json:"bookings_count"`
	TotalSpent    decimal.Decimal  `json:"total_spent"`
	AverageRating *decimal.Decimal `json:"average_rating,omitempty"`
	IsVIP         bool             `json:"is_vip"`
	Note          string           `json:"note"`
	History       []BookingHistory `json:"history"`
}

// BookingHistory is one past service in the client profile timeline.
type BookingHistory struct {
	ID          uuid.UUID       `json:"id"`
	ServiceName string          `json:"service_name"`
	StoreName   string          `json:"store_name"`
	StartTime   time.Time       `json:"start_time"`
	Status      string          `json:"status"`
	FinalPrice  decimal.Decimal `json:"final_price"`
}

// NoteResponse is returned after upserting a client note.
type NoteResponse struct {
	CustomerID uuid.UUID `json:"customer_id"`
	Content    string    `json:"content"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// ── VIP rule (deferred) ───────────────────────────────────────────────────────

// isVIP determines whether a client qualifies for the VIP badge.
//
// TODO(VIP): the VIP rule is not yet decided. The client_notes table has no
// is_vip column, so VIP must either be derived (e.g. total_spent over a
// threshold, or bookings_count over N) or added as stored state. Until that
// product decision is made, every client is non-VIP and the frontend hides the
// crown. Wiring the real rule later is a single change here.
func isVIP(_ int, _ decimal.Decimal) bool {
	return false
}

// ── Converters ────────────────────────────────────────────────────────────────

// toClientCard converts an aggregated list row to its client card.
func toClientCard(r *ClientRow) *ClientCard {
	return &ClientCard{
		CustomerID:    r.CustomerID,
		Name:          r.Name,
		Phone:         r.Phone,
		BookingsCount: r.BookingsCount,
		TotalSpent:    r.TotalSpent,
		LastService:   r.LastService,
		LastVisit:     r.LastVisit,
		AverageRating: r.AverageRating,
		IsVIP:         isVIP(r.BookingsCount, r.TotalSpent),
	}
}

// toBookingHistory converts a history row to its client representation.
func toBookingHistory(r *BookingHistoryRow) BookingHistory {
	return BookingHistory{
		ID:          r.ID,
		ServiceName: r.ServiceName,
		StoreName:   r.StoreName,
		StartTime:   r.StartTime,
		Status:      r.Status,
		FinalPrice:  r.FinalPrice,
	}
}
