// Package billing implements B-Edge's subscription billing system: the
// plan catalogue the public pricing page reads, each artist's current
// subscription, and the invoices tracking who has paid.
//
// There is no payment gateway (deposits flow artist-to-customer directly
// via OMT/Whish, never through B-Edge), so this package is B-Edge's own
// accounts-receivable system rather than a thin wrapper over Stripe -
// see B-Edge-Monetization-Implementation-Spec-v1.md for the full design.
// Notably: subscription status is never stored, only derived from dates at
// read time (see DeriveStatus), matching this codebase's existing
// no-background-scheduler constraint elsewhere.
package billing

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/subscription"
)

// ── Sentinel errors ───────────────────────────────────────────────────────────

var (
	// ErrPlanNotFound is returned when no plan matches the given code.
	ErrPlanNotFound = errors.New("plan not found")
	// ErrPlanCodeExists is returned when creating a plan whose code is already taken.
	ErrPlanCodeExists = errors.New("a plan with this code already exists")
	// ErrArtistNotFound is returned when no artist matches the given user ID.
	ErrArtistNotFound = errors.New("artist not found")
	// ErrSubscriptionNotFound is returned when no subscription matches the given artist or ID.
	ErrSubscriptionNotFound = errors.New("subscription not found")
	// ErrInvoiceNotFound is returned when no invoice matches the given ID.
	ErrInvoiceNotFound = errors.New("invoice not found")
	// ErrInvoiceWrongStatus is returned when an invoice action's required
	// starting status doesn't match - e.g. confirming an invoice that isn't
	// 'submitted', or submitting one that isn't 'issued'.
	ErrInvoiceWrongStatus = errors.New("invoice is not in the required status for this action")
)

// ── Core type ──────────────────────────────────────────────────────────────────

// Plan is one row of the subscription pricing catalogue.
//
// Deliberately never referenced by foreign key from subscriptions/invoices
// once those exist - both will snapshot their own price at signup/issue
// time instead of joining here. Editing a Plan changes what NEW signups
// see and pay; it must never retroactively change what an existing
// subscriber owes. See the spec's section 6.4 for the full reasoning and
// what "apply this new price to existing subscribers" looks like instead
// (a separate, explicit, audited action - not implemented yet).
type Plan struct {
	Code          string          `json:"code" db:"code"`
	Name          string          `json:"name" db:"name"`
	MonthlyPrice  decimal.Decimal `json:"monthly_price" db:"monthly_price"`
	Currency      string          `json:"currency" db:"currency"`
	SeatPrice     decimal.Decimal `json:"seat_price" db:"seat_price"`
	IncludedSeats int             `json:"included_seats" db:"included_seats"`
	Description   string          `json:"description" db:"description"`
	Features      []string        `json:"features" db:"features"`
	IsPublic      bool            `json:"is_public" db:"is_public"`
	SortOrder     int             `json:"sort_order" db:"sort_order"`
	UpdatedAt     time.Time       `json:"updated_at" db:"updated_at"`
}

// ── Request structs ───────────────────────────────────────────────────────────

// CreatePlanRequest is the body for POST /api/v1/admin/plans.
// Prices arrive as strings and are parsed with decimal.NewFromString in the
// service layer - matching the pattern already established in
// product.CreateProductRequest, not float64.
type CreatePlanRequest struct {
	Code          string   `json:"code" validate:"required,max=30"`
	Name          string   `json:"name" validate:"required,max=80"`
	MonthlyPrice  string   `json:"monthly_price" validate:"required"`
	Currency      string   `json:"currency" validate:"omitempty,len=3"`
	SeatPrice     string   `json:"seat_price"`
	IncludedSeats int      `json:"included_seats" validate:"omitempty,min=1"`
	Description   string   `json:"description" validate:"max=1000"`
	Features      []string `json:"features"`
	IsPublic      *bool    `json:"is_public"`
	SortOrder     int      `json:"sort_order"`
}

// UpdatePlanRequest is the body for PATCH /api/v1/admin/plans/:code.
// Every field is optional - only supplied fields are changed. Nil means
// "leave as-is", not "clear this field".
//
// This endpoint only ever changes the plans row itself and has zero effect
// on any existing subscription - migrating existing subscribers to a new
// price is a deliberately separate, not-yet-built action (spec section
// 6.4), so this handler can never accidentally do it as a side effect.
type UpdatePlanRequest struct {
	Name          *string   `json:"name" validate:"omitempty,max=80"`
	MonthlyPrice  *string   `json:"monthly_price"`
	SeatPrice     *string   `json:"seat_price"`
	IncludedSeats *int      `json:"included_seats" validate:"omitempty,min=1"`
	Description   *string   `json:"description" validate:"omitempty,max=1000"`
	Features      *[]string `json:"features"`
	IsPublic      *bool     `json:"is_public"`
	SortOrder     *int      `json:"sort_order"`
}

// ── Subscription status ──────────────────────────────────────────────────────

// Status is an artist's derived subscription lifecycle state.
//
// A type ALIAS for subscription.Status, not a distinct type: the state
// machine itself lives in internal/pkg/subscription so that
// internal/middleware can share one implementation with this package
// without closing an import cycle (billing/handler.go imports middleware
// for RequireAuth). The alias keeps every existing billing.Status* call
// site working unchanged.
type Status = subscription.Status

// The six derived subscription states, re-exported from the leaf package
// that defines them so callers of this domain need not know it exists.
const (
	StatusTrialing  = subscription.StatusTrialing
	StatusActive    = subscription.StatusActive
	StatusGrace     = subscription.StatusGrace
	StatusPastDue   = subscription.StatusPastDue
	StatusSuspended = subscription.StatusSuspended
	StatusCancelled = subscription.StatusCancelled
)

// CurrencyUSD is the only currency B-Edge transacts in.
//
// Decided 2026-08-30, closing the question
// B-Edge-Monetization-Implementation-Spec-v1.md section 11 had left open.
// Enforced at the database by migration 026's CHECK constraints on
// plans/subscriptions/invoices, and in the service layer so a bad value is
// a 400 rather than a constraint violation. The currency columns remain in
// the schema deliberately - they cost nothing and preserve the option.
const CurrencyUSD = "USD"

// GraceDays and PastDueDays are the graduated enforcement windows past
// current_period_end, re-exported from internal/pkg/subscription.
//
// GraceDays is interpolated into SQL by discovery.Repository's and
// artist.Repository's visibility filters; PastDueDays is read by
// middleware.RequireActiveSubscription. Both previously risked drift -
// middleware in particular hand-copied the 21 with a comment asking
// whoever changed one to remember the other. They now come from one
// declaration.
const (
	GraceDays   = subscription.GraceDays
	PastDueDays = subscription.PastDueDays
)

// DeriveStatus computes an artist's current subscription state from dates
// alone - see the Status doc comment for why this is never stored.
//
// A thin adapter over subscription.Derive, which works over primitives so
// that middleware can call it with columns scanned straight from SQL
// without constructing a Subscription.
func DeriveStatus(s *Subscription, now time.Time) Status {
	return subscription.Derive(s.PlanCode, s.TrialEndsAt, s.CurrentPeriodEnd, s.CancelledAt, now)
}

// ── Subscription ──────────────────────────────────────────────────────────────

// Subscription is one artist's current plan/seat assignment.
//
// monthly_price/currency are snapshotted here, not read through plan_code -
// see plan_code's own comment and migration 024 for why: an admin editing
// a plan's price must never retroactively change what an existing
// subscriber is being charged.
type Subscription struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	ArtistID         uuid.UUID       `json:"artist_id" db:"artist_id"`
	PlanCode         string          `json:"plan_code" db:"plan_code"`
	Seats            int             `json:"seats" db:"seats"`
	MonthlyPrice     decimal.Decimal `json:"monthly_price" db:"monthly_price"`
	Currency         string          `json:"currency" db:"currency"`
	TrialEndsAt      *time.Time      `json:"trial_ends_at" db:"trial_ends_at"`
	CurrentPeriodEnd *time.Time      `json:"current_period_end" db:"current_period_end"`
	CancelledAt      *time.Time      `json:"cancelled_at" db:"cancelled_at"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
	UpdatedAt        time.Time       `json:"updated_at" db:"updated_at"`
}

// SubscriptionResponse is a Subscription plus the fields a caller actually
// wants but that don't belong stored on the row itself: the derived
// Status (see DeriveStatus) and a human-readable plan name.
type SubscriptionResponse struct {
	Subscription
	Status   Status `json:"status"`
	PlanName string `json:"plan_name"`
}

// UpdateSubscriptionRequest is the body for
// PATCH /api/v1/admin/billing/subscriptions/:id. Every field is optional.
//
// Setting PlanCode re-snapshots MonthlyPrice/Currency from that plan's
// CURRENT price at the moment of this explicit admin action - this is the
// legitimate "move this one account to a different price" case the spec's
// section 6.4 distinguishes from a plan price edit silently touching
// existing subscribers. The two are different code paths on purpose:
// UpdatePlan (this file) never touches a subscription; this one only ever
// touches the one subscription an admin explicitly named.
type UpdateSubscriptionRequest struct {
	PlanCode         *string `json:"plan_code" validate:"omitempty,max=30"`
	Seats            *int    `json:"seats" validate:"omitempty,min=1"`
	TrialEndsAt      *string `json:"trial_ends_at"`      // RFC3339, optional
	CurrentPeriodEnd *string `json:"current_period_end"` // RFC3339, optional
	// Cancel, if true, sets cancelled_at to now. If false, clears an
	// existing cancellation (reinstates the subscription). Nil leaves it
	// untouched - this is a tri-state on purpose, not a plain bool, which
	// could never distinguish "leave as-is" from "un-cancel".
	Cancel *bool `json:"cancel"`
}

// ── Invoice ───────────────────────────────────────────────────────────────────

// InvoiceStatus values - see migration 025's CHECK constraint. Only the
// transitions issued→submitted→paid and (issued|submitted)→void are legal;
// paid and void are both terminal.
const (
	InvoiceIssued    = "issued"
	InvoiceSubmitted = "submitted"
	InvoicePaid      = "paid"
	InvoiceVoid      = "void"
)

// Invoice is one billing period for one subscription.
//
// Amount/Currency/SeatsBilled/PlanCode are a snapshot taken when the
// invoice was generated, deliberately never recomputed from the live
// subscription or joined from plans - if the artist's plan changes after
// this invoice was issued, this invoice must still say what was actually
// owed for the period it covers.
type Invoice struct {
	ID               uuid.UUID       `json:"id" db:"id"`
	SubscriptionID   uuid.UUID       `json:"subscription_id" db:"subscription_id"`
	ArtistID         uuid.UUID       `json:"artist_id" db:"artist_id"`
	InvoiceNumber    int             `json:"invoice_number" db:"invoice_number"`
	PeriodStart      time.Time       `json:"period_start" db:"period_start"`
	PeriodEnd        time.Time       `json:"period_end" db:"period_end"`
	DueDate          time.Time       `json:"due_date" db:"due_date"`
	Amount           decimal.Decimal `json:"amount" db:"amount"`
	Currency         string          `json:"currency" db:"currency"`
	SeatsBilled      int             `json:"seats_billed" db:"seats_billed"`
	PlanCode         string          `json:"plan_code" db:"plan_code"`
	Status           string          `json:"status" db:"status"`
	PaymentReference *string         `json:"payment_reference" db:"payment_reference"`
	SubmittedAt      *time.Time      `json:"submitted_at" db:"submitted_at"`
	ConfirmedBy      *uuid.UUID      `json:"confirmed_by" db:"confirmed_by"`
	PaidAt           *time.Time      `json:"paid_at" db:"paid_at"`
	VoidReason       *string         `json:"void_reason" db:"void_reason"`
	CreatedAt        time.Time       `json:"created_at" db:"created_at"`
}

// SubmitInvoicePaymentRequest is the body for
// POST /api/v1/billing/invoices/:id/submit. PaymentReference is an
// artist-entered free-text OMT/Whish transaction code or note, mirroring
// bookings.deposit_reference (migration 011) - not validated beyond a
// length cap, and never treated as proof of payment on its own. Only an
// admin's confirm action treats the money as real (spec section 8).
type SubmitInvoicePaymentRequest struct {
	PaymentReference string `json:"payment_reference" validate:"omitempty,max=255"`
}

// VoidInvoiceRequest is the body for
// POST /api/v1/admin/billing/invoices/:id/void. A reason is required
// because voiding is a correction to a financial record - unlike a
// rejection reason elsewhere in this codebase, this one isn't optional.
type VoidInvoiceRequest struct {
	Reason string `json:"reason" validate:"required,max=1000"`
}

// SubscriptionOverviewRow is one line of the admin billing overview - every
// artist, their plan, derived status, and what they currently owe. Built
// from a join across subscriptions/artists/users/plans plus an aggregate
// over unpaid invoices; see Repository.ListSubscriptionsOverview.
//
// Seats/TrialEndsAt/CancelledAt were fetched by that query from the start
// (DeriveStatus needs them) but discarded rather than returned - fine while
// this row only ever backed the Billing tab's read-only roster, but the
// admin Artists tab needs these three to prefill an edit form with the
// subscription's actual current values rather than asking an admin to
// blindly overwrite fields they can't see.
type SubscriptionOverviewRow struct {
	ArtistID          uuid.UUID       `json:"artist_id"`
	ArtistName        string          `json:"artist_name"`
	SubscriptionID    uuid.UUID       `json:"subscription_id"`
	PlanCode          string          `json:"plan_code"`
	PlanName          string          `json:"plan_name"`
	Status            Status          `json:"status"`
	Seats             int             `json:"seats"`
	MonthlyPrice      decimal.Decimal `json:"monthly_price"`
	Currency          string          `json:"currency"`
	TrialEndsAt       *time.Time      `json:"trial_ends_at"`
	CurrentPeriodEnd  *time.Time      `json:"current_period_end"`
	CancelledAt       *time.Time      `json:"cancelled_at"`
	OutstandingAmount decimal.Decimal `json:"outstanding_amount"`
}
