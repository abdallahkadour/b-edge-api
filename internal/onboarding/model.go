// Package onboarding implements the self-service artist signup flow -
// the path from "I just registered" to "an admin approved my profile and
// I'm live on Discover".
//
// Deliberately minimal by design, not by omission. Marketplace onboarding
// research converges on one point: split what's required to enter the
// system from what can be added later, and measure success by time to
// first listing, not by how complete a profile is on day one. This
// package asks for exactly what's needed to submit for review - one
// salon, one store, one service - and everything else (business hours,
// additional services, a second store) is added afterward from the
// screens that already exist for that.
package onboarding

import (
	"errors"
	"regexp"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrAlreadyOnboarded is returned when a user who already has an
	// artist row tries to submit the onboarding form again. Onboarding is
	// a one-time transition, not an edit form - editing an existing
	// profile happens through the normal artist screens.
	ErrAlreadyOnboarded = errors.New("this account has already completed onboarding")

	// ErrHandleTaken is returned when the requested handle collides with
	// an existing one. Mapped from the database's unique constraint
	// rather than checked with a separate SELECT first, which would leave
	// a race window between the check and the insert.
	ErrHandleTaken = errors.New("this handle is already taken")

	// ErrNotOnboarded is returned by GetStatus when no artist row exists
	// yet for this user - distinguished from a genuine "pending" or
	// "active" status so the frontend can tell "hasn't started" from
	// "started and waiting for review" apart.
	ErrNotOnboarded = errors.New("onboarding has not been started")
)

// handlePattern mirrors the database CHECK constraint on artists.handle
// exactly (migration 012) - validated here too so a bad handle produces a
// clean 400 with a real message, not a raw constraint-violation string
// surfacing from Postgres.
var handlePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`)

// artistCategories mirrors the CHECK constraint from migration 008.
var artistCategories = map[string]bool{
	"makeup": true, "hair": true, "nails": true, "lashes": true, "skincare": true,
}

// CompleteOnboardingRequest bundles the artist profile, their salon, one
// store, and one service into a single submission. All four pieces are
// created together in one transaction - there is no such thing as a salon
// that exists without at least one bookable service, which is the state a
// multi-step, separately-submitted wizard could otherwise leave behind if
// someone abandoned it partway through.
type CompleteOnboardingRequest struct {
	// Profile
	Handle    string  `json:"handle"    validate:"required,min=3,max=50"`
	Bio       *string `json:"bio"       validate:"omitempty,max=1000"`
	Instagram *string `json:"instagram" validate:"omitempty,max=255"`
	Category  string  `json:"category"  validate:"required"`

	// Salon - the business entity. Named separately from the artist's own
	// display name since a salon can eventually have more than one artist,
	// even though today it's always exactly one.
	SalonName string `json:"salon_name" validate:"required,min=2,max=200"`

	// First store
	StoreName string  `json:"store_name" validate:"required,min=2,max=200"`
	City      string  `json:"city"       validate:"required,min=2,max=100"`
	Address   *string `json:"address"    validate:"omitempty,max=500"`

	// First service - price only, no deposit fields. Deposit amount and
	// deadline both have sane database defaults (0.00, 48h); asking a
	// brand-new artist to reason about deposit policy before they've
	// taken a single booking is exactly the kind of premature field this
	// flow is designed to skip. Adjustable afterward from Services.
	ServiceName        string `json:"service_name"         validate:"required,min=2,max=200"`
	ServiceDurationMin int    `json:"service_duration_min" validate:"required,min=15,max=480"`
	ServicePrice       string `json:"service_price"        validate:"required"`
}

// Validate runs checks that go/playground/validator's struct tags can't
// express: the handle format and the category enum, both mirrored from
// database CHECK constraints so a violation surfaces as a clean 400 here
// rather than a raw Postgres error.
func (r CompleteOnboardingRequest) Validate() error {
	if !handlePattern.MatchString(r.Handle) {
		return errors.New("handle must be lowercase letters, numbers, and hyphens only, and cannot start or end with a hyphen")
	}
	if !artistCategories[r.Category] {
		return errors.New("category must be one of: makeup, hair, nails, lashes, skincare")
	}
	return nil
}

// CompleteOnboardingResponse confirms what was created and what happens next.
type CompleteOnboardingResponse struct {
	ArtistID uuid.UUID `json:"artist_id"`
	Status   string    `json:"status"`
}

// OnboardingStatus is what GET /onboarding/status returns - just enough
// for the dashboard shell to decide whether to show the wizard, a
// "pending review" banner, or the normal dashboard.
type OnboardingStatus struct {
	Status    string     `json:"status"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
}
