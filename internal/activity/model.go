// Package activity is the salon owner's read of the activity log
// (audit_events, by salon_id): who approved, confirmed, refunded, cancelled,
// moved; who changed a price, a payment account, the opening hours; who
// joined and who left (2026-10-10).
//
// The writing is done where the change happens (internal/audit's Record,
// called from each domain). This package only reads, and only for the
// owner - salonrole.ActivitySalonRead.
package activity

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Actor is who made the change.
type Actor struct {
	// ID is nil for a change nobody made by hand (a background job).
	ID *uuid.UUID `json:"id,omitempty"`
	// Name is the person's name; empty for the system.
	Name string `json:"name"`
	// Role is users.role at the time: artist, customer, admin, or system.
	Role string `json:"role"`
	// IsYou marks the caller's own changes.
	IsYou bool `json:"is_you"`
}

// Subject names what the change was about, joined at read time so a name
// that has since changed reads as it is now.
type Subject struct {
	ServiceName  string     `json:"service_name,omitempty"`
	CustomerName string     `json:"customer_name,omitempty"`
	ArtistName   string     `json:"artist_name,omitempty"`
	StoreName    string     `json:"store_name,omitempty"`
	StartTime    *time.Time `json:"start_time,omitempty"`
}

// Entry is one change.
type Entry struct {
	ID         uuid.UUID `json:"id"`
	At         time.Time `json:"at"`
	Actor      Actor     `json:"actor"`
	EntityType string    `json:"entity_type"`
	EntityID   uuid.UUID `json:"entity_id"`
	// Action is the audit vocabulary (internal/audit/activity.go) or, for
	// rows written before it, the older spellings: invite, accept, revoke,
	// remove_member, leave_salon, transfer_ownership, offering.update,
	// offering.off.
	Action string `json:"action"`
	// Old and New are what changed, as recorded. Absent when there was
	// nothing before (a creation) or nothing after (a deletion).
	Old     json.RawMessage `json:"old,omitempty" swaggertype:"object"`
	New     json.RawMessage `json:"new,omitempty" swaggertype:"object"`
	Subject Subject         `json:"subject"`
}

// kinds are the screen's filters, each a fixed SQL condition over the alias
// e. Constants, never request text - the request picks a key.
var kinds = map[string]string{
	"bookings": `(e.entity_type = 'booking' OR e.action = 'booking.shift_day')`,
	"payments": `(e.entity_type = 'payment_method' OR e.action = 'salon.no_show_policy')`,
	"menu":     `e.entity_type IN ('service', 'artist_service', 'discount', 'store', 'product')`,
	"shop":     `e.entity_type = 'order'`,
	"team":     `(e.entity_type = 'salon_invitation' OR e.action IN ('remove_member', 'leave_salon', 'transfer_ownership'))`,
}

// Filter narrows the feed. Zero values mean "everything".
type Filter struct {
	Kind    string
	ActorID *uuid.UUID
}
