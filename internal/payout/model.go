// Package payout owns where a salon's money should be sent.
//
// It exists as its own package rather than inside artist for the reason the
// trust & safety review gives: this is the answer to "where do I send my
// deposit", and it is read by the booking funnel and by product checkout,
// which are two different domains. Putting it in either one would make the
// other import a domain it has no other business with.
package payout

import (
	"time"

	"github.com/google/uuid"
)

// Method is how money actually moves in Lebanon.
type Method string

const (
	MethodWhish Method = "whish"
	MethodOMT   Method = "omt"
)

// Valid reports whether m is a method the platform supports.
//
// Checked in the service as well as by the CHECK constraint, so an unknown
// method is a readable 400 rather than a constraint violation surfacing as a
// 500.
func (m Method) Valid() bool {
	return m == MethodWhish || m == MethodOMT
}

// Label is how the method is named to a human.
func (m Method) Label() string {
	switch m {
	case MethodWhish:
		return "Whish"
	case MethodOMT:
		return "OMT"
	default:
		return string(m)
	}
}

// PaymentMethod is one destination a salon can receive money at.
type PaymentMethod struct {
	ID          uuid.UUID `db:"id"`
	SalonID     uuid.UUID `db:"salon_id"`
	Method      Method    `db:"method"`
	AccountName string    `db:"account_name"`
	AccountRef  string    `db:"account_ref"`
	IsActive    bool      `db:"is_active"`
	CreatedAt   time.Time `db:"created_at"`
	UpdatedAt   time.Time `db:"updated_at"`
}

// UpsertPaymentMethodRequest creates or replaces one destination.
//
// Upsert rather than separate create and update, because the salon may hold
// at most one account per method. "Change my Whish number" and "add my Whish
// number" are the same operation from the artist's point of view, and
// modelling them separately only invites a duplicate that the UNIQUE
// constraint would then reject with a confusing error.
type UpsertPaymentMethodRequest struct {
	Method      string `json:"method"       validate:"required,oneof=whish omt"`
	AccountName string `json:"account_name" validate:"required,min=2,max=200"`
	AccountRef  string `json:"account_ref"  validate:"required,min=4,max=40"`
}

// PaymentMethodResponse is a destination as the owning artist sees it.
type PaymentMethodResponse struct {
	ID          uuid.UUID `json:"id"`
	Method      string    `json:"method"`
	MethodLabel string    `json:"method_label"`
	AccountName string    `json:"account_name"`
	AccountRef  string    `json:"account_ref"`
	IsActive    bool      `json:"is_active"`
}

// PublicPaymentMethodResponse is a destination as a paying client sees it.
//
// Identical to the artist's view by design. There is nothing to redact: the
// client is about to send money here, so withholding any part of it would
// defeat the entire purpose. It is a separate type so that stays a decision
// rather than an accident - if a private field is ever added to the table,
// this type will not silently start publishing it.
type PublicPaymentMethodResponse struct {
	Method      string `json:"method"`
	MethodLabel string `json:"method_label"`
	AccountName string `json:"account_name"`
	AccountRef  string `json:"account_ref"`
}

func toResponse(p *PaymentMethod) PaymentMethodResponse {
	return PaymentMethodResponse{
		ID:          p.ID,
		Method:      string(p.Method),
		MethodLabel: p.Method.Label(),
		AccountName: p.AccountName,
		AccountRef:  p.AccountRef,
		IsActive:    p.IsActive,
	}
}

func toPublicResponse(p *PaymentMethod) PublicPaymentMethodResponse {
	return PublicPaymentMethodResponse{
		Method:      string(p.Method),
		MethodLabel: p.Method.Label(),
		AccountName: p.AccountName,
		AccountRef:  p.AccountRef,
	}
}
