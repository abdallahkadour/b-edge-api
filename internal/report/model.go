// Package report lets a client or artist raise a problem, and lets an admin
// act on it.
//
// It exists because of what B-Edge is not: there are no card rails here, so
// there is no chargeback, no escrow and no intermediary holding anyone's
// money. When a deposit reaches the wrong person, the platform is the only
// recourse there is. Migration 039 gave clients an authoritative payment
// destination to check against; this is what they do when the check fails.
package report

import (
	"time"

	"github.com/google/uuid"
)

// Category is what kind of problem is being raised.
//
// These are the threat model, not generic support topics. Each names something
// the trust & safety review found could happen here.
type Category string

const (
	// CategoryWrongPaymentDetails is the important one: it is the tripwire for
	// the substituted-account-number attack. A cluster of these against one
	// artist is the signal that somebody is impersonating them.
	CategoryWrongPaymentDetails Category = "wrong_payment_details"
	CategoryPaymentNotReceived  Category = "payment_not_received"
	CategoryDepositNotReturned  Category = "deposit_not_returned"
	CategoryDidNotAttend        Category = "did_not_attend"
	CategoryImpersonation       Category = "impersonation"
	CategoryInappropriate       Category = "inappropriate_behaviour"
	CategoryOther               Category = "other"
)

var validCategories = map[Category]string{
	CategoryWrongPaymentDetails: "Asked to pay different details",
	CategoryPaymentNotReceived:  "Payment not received",
	CategoryDepositNotReturned:  "Deposit not returned",
	CategoryDidNotAttend:        "Did not attend",
	CategoryImpersonation:       "Impersonation",
	CategoryInappropriate:       "Inappropriate behaviour",
	CategoryOther:               "Something else",
}

// Valid reports whether c is a category the platform accepts.
func (c Category) Valid() bool { _, ok := validCategories[c]; return ok }

// Label is how the category is named to a human.
func (c Category) Label() string {
	if l, ok := validCategories[c]; ok {
		return l
	}
	return string(c)
}

// Status is where a report is in its lifecycle.
type Status string

const (
	StatusOpen      Status = "open"
	StatusReviewing Status = "reviewing"
	StatusResolved  Status = "resolved"
	StatusDismissed Status = "dismissed"
)

// Valid reports whether s is a status an admin may move a report to.
func (s Status) Valid() bool {
	switch s {
	case StatusOpen, StatusReviewing, StatusResolved, StatusDismissed:
		return true
	}
	return false
}

// IsTerminal reports whether a report has been dealt with.
func (s Status) IsTerminal() bool { return s == StatusResolved || s == StatusDismissed }

// Report is one raised problem.
type Report struct {
	ID             uuid.UUID  `db:"id"`
	ReporterUserID uuid.UUID  `db:"reporter_user_id"`
	ReporterRole   string     `db:"reporter_role"`
	BookingID      *uuid.UUID `db:"booking_id"`
	ArtistID       *uuid.UUID `db:"artist_id"`
	Category       Category   `db:"category"`
	Description    string     `db:"description"`
	Status         Status     `db:"status"`
	ResolutionNote *string    `db:"resolution_note"`
	ResolvedBy     *uuid.UUID `db:"resolved_by"`
	ResolvedAt     *time.Time `db:"resolved_at"`
	CreatedAt      time.Time  `db:"created_at"`
	UpdatedAt      time.Time  `db:"updated_at"`
}

// CreateReportRequest raises a problem.
//
// A minimum description length is enforced rather than accepting anything
// non-empty: "bad" is not something an admin can act on, and a report nobody
// can act on wastes the reporter's time as much as the reviewer's.
type CreateReportRequest struct {
	BookingID   *string `json:"booking_id"  validate:"omitempty,uuid"`
	ArtistID    *string `json:"artist_id"   validate:"omitempty,uuid"`
	Category    string  `json:"category"    validate:"required"`
	Description string  `json:"description" validate:"required,min=10,max=2000"`
}

// ResolveReportRequest is an admin moving a report along.
type ResolveReportRequest struct {
	Status Status  `json:"status" validate:"required"`
	Note   *string `json:"note"   validate:"omitempty,max=2000"`
}

// Response is a report as its reporter sees it.
type Response struct {
	ID            uuid.UUID `json:"id"`
	Category      string    `json:"category"`
	CategoryLabel string    `json:"category_label"`
	Description   string    `json:"description"`
	Status        string    `json:"status"`
	BookingID     *string   `json:"booking_id,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
}

// AdminResponse is a report as the queue shows it, with who raised it.
type AdminResponse struct {
	Response
	ReporterName   string  `json:"reporter_name"`
	ReporterRole   string  `json:"reporter_role"`
	ReporterPhone  *string `json:"reporter_phone,omitempty"`
	ArtistName     *string `json:"artist_name,omitempty"`
	ResolutionNote *string `json:"resolution_note,omitempty"`
}

func toResponse(r *Report) Response {
	out := Response{
		ID:            r.ID,
		Category:      string(r.Category),
		CategoryLabel: r.Category.Label(),
		Description:   r.Description,
		Status:        string(r.Status),
		CreatedAt:     r.CreatedAt,
	}
	if r.BookingID != nil {
		s := r.BookingID.String()
		out.BookingID = &s
	}
	return out
}

// Categories lists what a reporter may choose, for the UI to render.
func Categories() []map[string]string {
	order := []Category{
		CategoryWrongPaymentDetails, CategoryPaymentNotReceived,
		CategoryDepositNotReturned, CategoryDidNotAttend,
		CategoryImpersonation, CategoryInappropriate, CategoryOther,
	}
	out := make([]map[string]string, 0, len(order))
	for _, c := range order {
		out = append(out, map[string]string{"id": string(c), "label": c.Label()})
	}
	return out
}
