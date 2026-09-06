// Package promo owns discount codes: the rows, who may use them, and the
// record of who did.
//
// # WHY IT IS NOT internal/pkg/discount
//
// internal/pkg/discount is the arithmetic - pure, no database, no clock. This
// package is everything around it: whether a code exists, whether it is still
// running, whether this customer has already used it, and writing down that
// they have.
//
// Splitting them that way is what keeps the pricing rules testable without
// fixtures. A Rule that reaches the resolver has already been ruled
// applicable; the resolver never asks "may they".
//
// # WHY A DOMAIN RATHER THAN A CORNER OF `artist`
//
// Codes look like salon configuration, which would put them beside services.
// Redemptions are not configuration - they are transactional records with a
// uniqueness constraint doing real concurrency work, and they are written
// inside the booking's own transaction. That is a domain, not a settings
// table.
package promo

import (
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Kind mirrors discount.Kind. Declared here rather than imported so the
// database row does not depend on the arithmetic package's type - the two
// happen to agree and are checked to agree in toRule.
const (
	KindFixed      = "fixed"
	KindPercentage = "percentage"
)

// Discount is a promotion as stored.
type Discount struct {
	ID          uuid.UUID `db:"id"`
	SalonID     uuid.UUID `db:"salon_id"`
	Code        string    `db:"code"`
	Description *string   `db:"description"`

	Kind  string          `db:"kind"`
	Value decimal.Decimal `db:"value"`

	StartsAt       *time.Time `db:"starts_at"`
	EndsAt         *time.Time `db:"ends_at"`
	MaxRedemptions *int       `db:"max_redemptions"`
	FirstTimeOnly  bool       `db:"first_time_only"`

	IsActive  bool      `db:"is_active"`
	CreatedAt time.Time `db:"created_at"`
	UpdatedAt time.Time `db:"updated_at"`
}

// Redemption is one use of a code, pending or consumed.
//
// BookingID and OrderID are mutually exclusive - the database enforces
// num_nonnulls(...) = 1, so a redemption belonging to both money paths or to
// neither cannot be stored.
type Redemption struct {
	ID         uuid.UUID  `db:"id"`
	DiscountID uuid.UUID  `db:"discount_id"`
	CustomerID uuid.UUID  `db:"customer_id"`
	BookingID  *uuid.UUID `db:"booking_id"`
	OrderID    *uuid.UUID `db:"order_id"`

	Amount decimal.Decimal `db:"amount"`
	// Consumed = false means the booking was cancelled in a way that does not
	// penalise the customer (D3.5), and the code is theirs again. The partial
	// unique index is on this column, so releasing frees the code without
	// deleting the history of what was offered.
	Consumed  bool      `db:"consumed"`
	CreatedAt time.Time `db:"created_at"`
}

// ── Requests ─────────────────────────────────────────────────────────────────

// CreateDiscountRequest is the admin/artist-facing create body.
type CreateDiscountRequest struct {
	Code        string  `json:"code"        validate:"required,min=3,max=32"`
	Description *string `json:"description" validate:"omitempty,max=255"`

	Kind string `json:"kind" validate:"required,oneof=fixed percentage"`
	// Value is a money string for the same reason every other amount in this
	// API is - see internal/pkg/money. Validated there, not by a struct tag.
	Value string `json:"value" validate:"required"`

	StartsAt       *time.Time `json:"starts_at"`
	EndsAt         *time.Time `json:"ends_at"`
	MaxRedemptions *int       `json:"max_redemptions" validate:"omitempty,min=1"`
	FirstTimeOnly  bool       `json:"first_time_only"`
}

// UpdateDiscountRequest patches a code. The CODE ITSELF is deliberately not
// editable: customers have it written down, and renaming it in place would
// silently break every link and poster carrying the old one. Deactivate and
// create a new one instead.
type UpdateDiscountRequest struct {
	Description    *string    `json:"description"    validate:"omitempty,max=255"`
	Value          *string    `json:"value"`
	StartsAt       *time.Time `json:"starts_at"`
	EndsAt         *time.Time `json:"ends_at"`
	MaxRedemptions *int       `json:"max_redemptions" validate:"omitempty,min=1"`
	IsActive       *bool      `json:"is_active"`
}

// ── Responses ────────────────────────────────────────────────────────────────

// DiscountResponse is a code as its owner sees it.
type DiscountResponse struct {
	ID          uuid.UUID  `json:"id"`
	Code        string     `json:"code"`
	Description *string    `json:"description,omitempty"`
	Kind        string     `json:"kind"`
	Value       string     `json:"value"`
	StartsAt    *time.Time `json:"starts_at,omitempty"`
	EndsAt      *time.Time `json:"ends_at,omitempty"`

	MaxRedemptions *int `json:"max_redemptions,omitempty"`
	// RedemptionCount counts CONSUMED redemptions only, matching what
	// max_redemptions is checked against.
	RedemptionCount int  `json:"redemption_count"`
	FirstTimeOnly   bool `json:"first_time_only"`

	IsActive  bool      `json:"is_active"`
	CreatedAt time.Time `json:"created_at"`
}

// PreviewResponse is what the booking funnel shows when a customer types a
// code, BEFORE committing to anything.
//
// It reports the whole breakdown rather than just the discount, because a
// customer typing a code wants to see the number they will actually pay - and
// because the resolver may have given less than the code advertises (it caps
// at the deposit), which is exactly the moment to say so rather than at
// checkout.
type PreviewResponse struct {
	Code  string `json:"code"`
	Valid bool   `json:"valid"`
	// Reason is set when Valid is false, in language a customer can act on.
	Reason string `json:"reason,omitempty"`

	Subtotal      string `json:"subtotal,omitempty"`
	DiscountTotal string `json:"discount_total,omitempty"`
	Final         string `json:"final,omitempty"`
	Deposit       string `json:"deposit,omitempty"`
	Balance       string `json:"balance,omitempty"`
}

func toDiscountResponse(d *Discount, redemptions int) *DiscountResponse {
	return &DiscountResponse{
		ID:              d.ID,
		Code:            d.Code,
		Description:     d.Description,
		Kind:            d.Kind,
		Value:           d.Value.StringFixed(2),
		StartsAt:        d.StartsAt,
		EndsAt:          d.EndsAt,
		MaxRedemptions:  d.MaxRedemptions,
		RedemptionCount: redemptions,
		FirstTimeOnly:   d.FirstTimeOnly,
		IsActive:        d.IsActive,
		CreatedAt:       d.CreatedAt,
	}
}
