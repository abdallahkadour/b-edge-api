// Package membership owns a salon's roster: who is in it, how they got
// there, and how they leave.
//
// It is new because there was no way in. The only INSERT INTO salons in this
// codebase lives inside internal/onboarding's transaction, which creates a
// salon, its first artist and its first store together - so a salon has
// always been created BY its first artist and has never been able to gain a
// second one. artists.salon_id has been nullable since migration 001
// precisely so an artist could be attached to a salon; nothing ever attached
// a second.
package membership

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// InvitationStatus is where an invitation is in its lifecycle.
//
// expired is computed on read and written back, not set by a scheduler.
// B-Edge has no cron by design; booking holds and waitlist entries already
// self-heal the same way.
type InvitationStatus string

const (
	StatusPending  InvitationStatus = "pending"
	StatusAccepted InvitationStatus = "accepted"
	StatusDeclined InvitationStatus = "declined"
	StatusRevoked  InvitationStatus = "revoked"
	StatusExpired  InvitationStatus = "expired"
)

// MaxLiveInvitations and MaxInvitationsPerDay bound how many invitations one
// salon can issue.
//
// Nothing bounded this until security case SPAM-08 measured it: 30
// invitations to 30 distinct numbers went through without a limit. Every one
// queues a WhatsApp message, so the day Meta verification clears, an owner
// account - or anyone who takes one over - is a free SMS cannon firing from
// B-Edge's verified sender. The reputational damage lands on the platform,
// not the salon.
//
// The numbers are generous on purpose. A real salon onboarding a team might
// invite five or six people in an afternoon and re-send a couple; twenty
// live at once, or fifty in a day, is not a salon hiring.
const (
	MaxLiveInvitations   = 20
	MaxInvitationsPerDay = 50
)

// InvitationTTL is how long an invitation link stays usable.
//
// Seven days rather than 24 hours: delivery is over WhatsApp to a working
// artist who may not be at their phone, and the cost of an expired link is a
// second round-trip through the owner. Short enough that a revoked-by-
// forgetting invitation does not linger for a month.
const InvitationTTL = 7 * 24 * time.Hour

var (
	// ErrNotFound is the repository's single not-found signal. The service
	// turns it into errInvitationNotFound so that a missing invitation and
	// one belonging to another salon are indistinguishable.
	ErrNotFound = errors.New("invitation not found")

	// ErrAlreadyInSalon means the invitee is already attached to a salon.
	// An artist belongs to at most one (BR-1).
	ErrAlreadyInSalon = errors.New("already a member of a salon")

	// ErrLiveInvitationExists means a pending invitation already exists for
	// this contact in this salon. Callers resend rather than duplicate.
	ErrLiveInvitationExists = errors.New("a pending invitation already exists")
)

// Invitation is one invitation to join a salon.
//
// The raw token is never stored; TokenHash is a SHA-256 of it. A database
// dump must not yield working invitation links.
type Invitation struct {
	ID         uuid.UUID        `json:"id"`
	SalonID    uuid.UUID        `json:"salon_id"`
	InvitedBy  uuid.UUID        `json:"invited_by"`
	Phone      *string          `json:"phone,omitempty"`
	Email      *string          `json:"email,omitempty"`
	TokenHash  string           `json:"-"`
	Status     InvitationStatus `json:"status"`
	ExpiresAt  time.Time        `json:"expires_at"`
	AcceptedBy *uuid.UUID       `json:"accepted_by,omitempty"`
	AcceptedAt *time.Time       `json:"accepted_at,omitempty"`
	CreatedAt  time.Time        `json:"created_at"`
	UpdatedAt  time.Time        `json:"updated_at"`
}

// EffectiveStatus is the status as of now, applying lazy expiry without
// writing. A pending invitation past its deadline reads as expired even if
// nothing has yet updated the row.
func (i *Invitation) EffectiveStatus(now time.Time) InvitationStatus {
	if i.Status == StatusPending && !now.Before(i.ExpiresAt) {
		return StatusExpired
	}
	return i.Status
}

// Redeemable reports whether this invitation can still be accepted, and
// returns the reason it cannot when it cannot.
//
// The three failure modes return the SAME error deliberately. GET
// /invitations/:token is a public endpoint and therefore an enumeration
// surface: an unknown token, a revoked one and an expired one must be
// indistinguishable, or the endpoint tells a stranger which tokens once
// existed. Same reasoning as booking's single errBookingNotFound
// constructor, which exists so an ownership failure and an absence cannot
// diverge into different responses.
func (i *Invitation) Redeemable(now time.Time) error {
	if i.EffectiveStatus(now) != StatusPending {
		return ErrNotFound
	}
	return nil
}

// Member is one artist in a salon, as the roster screen shows them.
type Member struct {
	ArtistID    uuid.UUID `json:"artist_id"`
	UserID      uuid.UUID `json:"user_id"`
	DisplayName string    `json:"display_name"`
	Handle      *string   `json:"handle,omitempty"`
	AvatarURL   *string   `json:"avatar_url,omitempty"`
	Category    *string   `json:"category,omitempty"`
	Status      string    `json:"status"` // artists.status: pending|active|rejected
	IsOwner     bool      `json:"is_owner"`
	JoinedAt    time.Time `json:"joined_at"`

	// FutureBookings is why a member cannot always be removed (BR-5). It is
	// shown on the roster so an owner sees the obstacle before they hit it.
	FutureBookings int `json:"future_bookings"`
}

// InviteRequest is the owner's submission.
type InviteRequest struct {
	Phone string `json:"phone" validate:"omitempty,min=6,max=20"`
	Email string `json:"email" validate:"omitempty,email,max=255"`

	// AcceptSeatCharge acknowledges that this invitation takes the salon
	// past its plan's included seats (FR-B3). Without it the service
	// refuses with SEAT_CHARGE_REQUIRED and reports the new total, so an
	// owner never discovers a price change on their next invoice.
	//
	// Inert until Phase 3 makes seats billable; the gate is wired now so
	// the contract does not change under the frontend later.
	AcceptSeatCharge bool `json:"accept_seat_charge"`
}

// InvitationPreview is what the PUBLIC token endpoint returns.
//
// Deliberately minimal: the salon's name and who invited you, and nothing
// else. Anyone holding a link can call it, so it must not disclose the
// roster, other invitees' numbers, or anything about the business.
type InvitationPreview struct {
	SalonName   string    `json:"salon_name"`
	InvitedBy   string    `json:"invited_by"`
	ExpiresAt   time.Time `json:"expires_at"`
	NeedsSignup bool      `json:"needs_signup"`
}

// TransferRequest names the member who becomes the new owner.
type TransferRequest struct {
	ArtistID uuid.UUID `json:"artist_id" validate:"required"`
}

// ── Error constructors ────────────────────────────────────────────────────
//
// One constructor per shape, called from BOTH the not-found branch and the
// ownership branch, so the two are identical by construction rather than by
// two literals that happen to agree. CLAUDE.md records how the original
// 403-vs-404 leak survived exactly that mistake.

func errInvitationNotFound() *apperror.AppError {
	return apperror.NotFound("INVITATION_NOT_FOUND",
		"That invitation link is not valid")
}

func errMemberNotFound() *apperror.AppError {
	return apperror.NotFound("MEMBER_NOT_FOUND", "No such member in this salon")
}

func errAlreadyInSalon() *apperror.AppError {
	return apperror.Conflict("ALREADY_IN_SALON",
		"That artist already belongs to a salon")
}

func errLiveInvitationExists() *apperror.AppError {
	return apperror.Conflict("INVITATION_EXISTS",
		"There is already a pending invitation for that contact")
}

func errCannotRemoveOwner() *apperror.AppError {
	return apperror.Conflict("CANNOT_REMOVE_OWNER",
		"The salon owner cannot be removed. Transfer ownership first")
}

func errHasFutureBookings(n int) *apperror.AppError {
	return apperror.Conflict("HAS_FUTURE_BOOKINGS",
		plural(n, "There is %d upcoming booking", "There are %d upcoming bookings")+
			" with this artist. Reassign or cancel them first")
}

func errOwnerMustTransfer() *apperror.AppError {
	return apperror.Conflict("OWNER_MUST_TRANSFER",
		"You own this salon. Transfer ownership before leaving")
}

func errLastMember() *apperror.AppError {
	return apperror.Conflict("LAST_MEMBER",
		"You are the only member of this salon, so it cannot be left empty")
}

func errTargetNotActiveMember() *apperror.AppError {
	return apperror.Conflict("TARGET_NOT_ACTIVE_MEMBER",
		"Ownership can only be transferred to an active member of this salon")
}

func errTooManyInvitations(what string) *apperror.AppError {
	return apperror.TooManyRequests("INVITATION_LIMIT",
		"This salon has issued too many invitations "+what+
			". Revoke some, or try again tomorrow")
}

func errAtArtistCeiling(planCode string, ceiling int) *apperror.AppError {
	return apperror.Conflict("PLAN_LIMIT_REACHED",
		fmt.Sprintf("Your %s plan covers %s. Upgrade to add more artists",
			planCode, plural(ceiling, "%d artist", "%d artists")))
}

func errInvalidContact() *apperror.AppError {
	return apperror.BadRequest("INVALID_CONTACT",
		"Enter a valid mobile number or email address")
}

// Invitee is the person a salon is trying to invite, as the database knows
// them. Assembled by Repository.InviteeByContact.
//
// ArtistID and SalonID are nil for a registered CUSTOMER - somebody with a
// B-Edge account who is not a professional. That is a different refusal from
// an unregistered number and the owner is told which.
type Invitee struct {
	UserID          uuid.UUID
	ArtistID        *uuid.UUID
	SalonID         *uuid.UUID
	Category        *string
	PhoneVerifiedAt *time.Time
}

// ArtistCategories is the specialty taxonomy, and the single source of truth
// for it in Go.
//
// It MUST match the CHECK constraint in migration 051. The database is the
// real enforcement - this exists so the API can reject a bad value with a
// field error the form can highlight, instead of surfacing a 23514 as a 500.
//
// Chosen against how the industry divides the work: US licensing treats
// cosmetology, esthetics, nail technology and barbering as separate
// credentials, and Fresha's own specialty-salon taxonomy names lash and brow
// studios as categories in their own right. See migration 051's header for
// what was deliberately left out and why.
var ArtistCategories = map[string]string{
	"makeup":       "Makeup artist",
	"hair":         "Hair stylist",
	"nails":        "Nail artist",
	"lashes":       "Lash artist",
	"brows":        "Brow artist",
	"skincare":     "Skincare / esthetician",
	"hair_removal": "Waxing & hair removal",
	"barber":       "Barber",
}

// ValidCategory reports whether a category is one the platform recognises.
func ValidCategory(c string) bool {
	_, ok := ArtistCategories[c]
	return ok
}

