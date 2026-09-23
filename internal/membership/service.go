package membership

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/onboarding"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/phone"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// defaultRegion is the ISO country used when a number arrives without a
// country code. Lebanon; the platform's other phone paths use the same.
const defaultRegion = "LB"

// OnboardingPort is the seam that keeps there being ONE onboarding path.
//
// Accepting an invitation has to create an artist row with status 'pending',
// link it to the salon's stores, and respect the same handle uniqueness and
// idempotency rules as founding a salon. Reimplementing that here would be a
// second copy of onboarding, which is the defect class this whole feature is
// organised against. internal/onboarding grew one method instead.
type OnboardingPort interface {
	CompleteIntoExistingSalon(ctx context.Context, userID, salonID uuid.UUID,
		profile onboarding.ArtistProfile) (uuid.UUID, error)
}

// Notifier queues the invitation message. Separate so the service can be
// tested without Twilio, and so a delivery failure cannot roll back an
// invitation - see Invite for why that ordering matters.
type Notifier interface {
	QueueSalonInvitation(ctx context.Context, invitationID uuid.UUID,
		toPhone, salonName, link string) error

	// RedactInvitationMessage removes the raw token from a queued message
	// once the invitation can no longer be redeemed. See
	// redactDeliveredInvitation for why the token has to be there at all.
	RedactInvitationMessage(ctx context.Context, invitationID uuid.UUID) error
}

// TokenInvalidator revokes refresh tokens. Ownership transfer must call it
// for BOTH parties: the salon role is resolved at token issue and baked into
// the access token, so without this the former owner keeps owner
// capabilities until their token expires.
type TokenInvalidator interface {
	RevokeAllForUser(ctx context.Context, userID uuid.UUID) error
}

type Service struct {
	repo Repository
	// validate is constructed once from validation.New(), never
	// validator.New(): the constructor registers a tag-name func so field
	// errors carry the JSON key the frontend maps onto inputs, and a custom
	// type func so rules apply inside optional.Field wrappers rather than to
	// the wrapper. CLAUDE.md, enforced convention.
	validate   *validator.Validate
	onboarding OnboardingPort
	notifier   Notifier
	tokens     TokenInvalidator
	audit      audit.Repository
	inviteBase string
	log        *zap.Logger
	now        func() time.Time
}

func NewService(repo Repository, ob OnboardingPort, n Notifier, ti TokenInvalidator,
	a audit.Repository, inviteBase string, log *zap.Logger) *Service {

	if a == nil {
		a = audit.NopRepository{}
	}
	return &Service{
		repo: repo, onboarding: ob, notifier: n, tokens: ti, audit: a, log: log,
		validate:   validation.New(),
		inviteBase: strings.TrimRight(inviteBase, "/"),
		now:        time.Now,
	}
}

// logf records a non-fatal failure. Nil-safe so tests can construct the
// service without a logger.
func (s *Service) logf(format string, args ...any) {
	if s.log != nil {
		s.log.Warn(fmt.Sprintf(format, args...))
	}
}

// ── Invitations ───────────────────────────────────────────────────────────

// InviteResult carries the created invitation and the link to it. The link
// is returned to the OWNER, not just sent, and that is deliberate:
// TWILIO_WHATSAPP_FROM is unset and every notification this platform has
// ever queued is dead, so a WhatsApp-only invitation would be a feature that
// cannot be used today. The dashboard shows the link to copy.
type InviteResult struct {
	Invitation *Invitation `json:"invitation"`
	Link       string      `json:"link"`
}

// requireVerifiedPhone reports whether the phone-verification rule is on.
//
// READ AT CALL TIME, NOT AT PACKAGE INIT, and that distinction is the whole
// comment. This was:
//
//	var requireVerifiedPhone = os.Getenv("REQUIRE_VERIFIED_PHONE_FOR_INVITE") == "true"
//
// Package-level variables initialise BEFORE main() runs, and main() is where
// godotenv.Load() reads .env (cmd/main.go:66). So that variable evaluated
// against an environment that did not yet contain anything from .env, read
// empty, and was permanently false. Setting the flag to true changed
// NOTHING - measured on 2026-09-23 by turning it on and watching an
// unverified artist be invited anyway.
//
// A security gate that silently cannot be switched on is worse than one that
// is off, because the config says it is on.
//
// Default OFF still: verifying a phone means sending to it, and outbound
// delivery does not work. See internal/pkg/devbypass for how this is
// exercised meanwhile.
func requireVerifiedPhone() bool {
	return os.Getenv("REQUIRE_VERIFIED_PHONE_FOR_INVITE") == "true"
}

func (s *Service) Invite(ctx context.Context, salonID, actorID uuid.UUID,
	req InviteRequest, ip string) (*InviteResult, error) {

	// 1. Normalise BEFORE any uniqueness check. Postgres sees '70555123'
	//    and '+96170555123' as different strings, so the partial unique
	//    index cannot tell they are one person unless the column is already
	//    E.164. Security test FRAUD-09 pinned the same equivalence for
	//    deposit payer numbers.
	ph, em, err := normaliseContact(req.Phone, req.Email)
	if err != nil {
		return nil, err
	}

	// 2. WHO IS BEING INVITED. Since migration 051 a salon may only invite
	//    somebody who already exists as a beauty professional on B-Edge.
	//
	//    Before this, an invitation was addressed to a phone NUMBER and
	//    nothing on the other end had to exist - whoever opened the link
	//    first became an artist in that salon. A mistyped digit invited a
	//    stranger, and the owner found out when that stranger appeared on
	//    the team page with the salon's client list.
	//
	//    ON ENUMERATION: telling the owner "that number is not registered"
	//    does reveal whether a given number belongs to a B-Edge artist.
	//    That is accepted deliberately. The endpoint is owner-authenticated,
	//    capped at MaxInvitationsPerDay, and the alternative - a vague
	//    refusal - makes the feature unusable, because the owner cannot tell
	//    a typo from a colleague who has not signed up. A bounded oracle
	//    behind authentication is the better trade.
	invitee, err := s.repo.InviteeByContact(ctx, ph, em)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, apperror.NotFound("ARTIST_NOT_REGISTERED",
				"That person does not have a B-Edge account yet. "+
					"Ask them to sign up as an artist first, then invite them.")
		}
		return nil, err
	}

	//    A registered CUSTOMER is a different refusal from an unregistered
	//    number, and the owner needs to be told which - otherwise they
	//    re-send to the same person forever.
	if invitee.ArtistID == nil {
		return nil, apperror.Conflict("NOT_AN_ARTIST",
			"That account exists but is not an artist account. "+
				"They need to register as a beauty professional before joining a salon.")
	}

	//    Refuse someone who already belongs to a salon (BR-1).
	if invitee.SalonID != nil {
		if *invitee.SalonID == salonID {
			return nil, apperror.Conflict("ALREADY_A_MEMBER",
				"That artist is already in this salon")
		}
		return nil, errAlreadyInSalon()
	}

	//    The specialty must be declared. A salon hiring a nail artist and a
	//    salon hiring a barber are not doing the same thing, and the owner
	//    cannot see which they are getting unless it is recorded. Nullable
	//    in the schema for the artists who predate 051; required here, so
	//    the rule lands on new members without rewriting anyone's profile.
	if invitee.Category == nil || *invitee.Category == "" {
		return nil, apperror.Conflict("ARTIST_NO_CATEGORY",
			"That artist has not set their specialty yet. "+
				"Ask them to choose one on their profile, then invite them.")
	}

	//    The number must be proven to be theirs.
	//
	//    OFF BY DEFAULT, and that is not timidity - verifying a phone means
	//    SENDING to it, and outbound delivery does not work yet. Zero
	//    notifications have ever been delivered, and no artist on the
	//    platform has even supplied a phone number, so switching this on
	//    today would refuse every invitation including to the launch artist.
	//
	//    Set REQUIRE_VERIFIED_PHONE_FOR_INVITE=true the day delivery works.
	//    The gate is written now, with the rest of the rules, rather than
	//    left as a TODO that gets forgotten.
	if requireVerifiedPhone() && invitee.PhoneVerifiedAt == nil {
		return nil, apperror.Conflict("PHONE_NOT_VERIFIED",
			"That artist has not verified their phone number yet. "+
				"Ask them to confirm it on their profile, then invite them.")
	}

	// 3. Resend rather than duplicate (FR-M7).
	if existing, err := s.repo.LiveInvitationForContact(ctx, salonID, ph, em); err == nil {
		if existing.Redeemable(s.now()) == nil {
			return nil, errLiveInvitationExists()
		}
		// Past its deadline: retire it lazily so the unique index frees up
		// and a fresh invitation can be issued. No scheduler needed.
		if err := s.repo.SetInvitationStatus(ctx, existing.ID, StatusExpired, nil); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, ErrNotFound) {
		return nil, err
	}

	// 4. Seat capacity (FR-B3) is NOT enforced here, and deliberately so.
	//
	//    plans.seat_price and plans.included_seats are populated for all six
	//    plans and read by no arithmetic anywhere - the invoice amount is a
	//    flat sub.MonthlyPrice. Until Phase 3 regrains subscriptions to the
	//    salon and makes those columns multiply, this service cannot know
	//    what a salon's seat allowance is, and a gate that invents one would
	//    either refuse invitations nobody is being charged for or wave
	//    through charges nobody agreed to.
	//
	//    InviteRequest.AcceptSeatCharge is accepted and ignored so the wire
	//    contract does not change under the frontend when T3.5 lands.

	// 5. The plan's artist ceiling.
	//
	//    included_seats stopped being "seats you pay for" and became a
	//    ceiling when per-seat billing was rejected (migration 050). It was
	//    then enforced by nothing - security case FRAUD-14 found a $45 Solo
	//    salon inviting freely, which made every tier above it unsellable.
	//
	//    Counts ACTIVE artists only. Someone mid-approval should not consume
	//    a place that an admin might never grant, and a pending artist takes
	//    no bookings.
	//
	//    Checked on the INVITE rather than on acceptance: telling an owner
	//    she is at her limit before she sends the message is a price
	//    conversation, telling the invitee after they accept is a broken
	//    promise made in the owner's name.
	if ceiling, planCode, err := s.repo.ArtistCeiling(ctx, salonID); err != nil {
		return nil, err
	} else if ceiling > 0 {
		active, err := s.repo.ActiveMemberCount(ctx, salonID)
		if err != nil {
			return nil, err
		}
		// Pending invitations count. Otherwise an owner at the ceiling can
		// queue twenty and let them all land.
		pending, err := s.repo.CountLiveInvitations(ctx, salonID)
		if err != nil {
			return nil, err
		}
		if active+pending >= ceiling {
			return nil, errAtArtistCeiling(planCode, ceiling)
		}
	}

	// 6. Bound the volume (SPAM-08). Checked after the duplicate and
	//    cross-salon refusals so an owner re-sending to one person is never
	//    told they have hit a limit.
	if live, err := s.repo.CountLiveInvitations(ctx, salonID); err != nil {
		return nil, err
	} else if live >= MaxLiveInvitations {
		return nil, errTooManyInvitations("that are still waiting")
	}
	if day, err := s.repo.CountInvitationsSince(ctx, salonID,
		s.now().Add(-24*time.Hour)); err != nil {
		return nil, err
	} else if day >= MaxInvitationsPerDay {
		return nil, errTooManyInvitations("today")
	}

	// 7. Generate the token; store only its hash.
	token, hash, err := newInvitationToken()
	if err != nil {
		return nil, err
	}

	inv := &Invitation{
		SalonID:   salonID,
		InvitedBy: actorID,
		Phone:     ph,
		Email:     em,
		TokenHash: hash,
		ExpiresAt: s.now().Add(InvitationTTL),
	}
	if err := s.repo.CreateInvitation(ctx, inv); err != nil {
		if errors.Is(err, ErrLiveInvitationExists) {
			return nil, errLiveInvitationExists()
		}
		return nil, err
	}

	_ = s.audit.Log(ctx, audit.Event{
		SalonID: &salonID, ActorID: &actorID, ActorRole: "artist",
		EntityType: "salon_invitation", EntityID: inv.ID, Action: "invite",
		NewValues: map[string]any{"phone": ph, "email": em}, IPAddress: ip,
	})

	link := s.inviteLink(token)

	// 9. Queue the notification OUTSIDE the transaction and ignore its
	//    error. Delivery is currently impossible - Meta verification is
	//    pending and 100% of queued notifications are dead - so letting a
	//    send failure roll back the invitation would mean no invitation
	//    could be created at all. The link above is the working channel.
	//    Non-fatal, but NOT silent. The first version discarded both errors
	//    with `_ =`, and the result was that queueing had never once
	//    succeeded and nothing said so - the security case written to check
	//    whether the raw token leaks into notifications passed because no
	//    notification was ever written at all. A swallowed error on a path
	//    that cannot fail loudly is a feature that quietly does not exist.
	if s.notifier != nil && ph != nil {
		if salonName, err := s.repo.SalonName(ctx, salonID); err != nil {
			s.logf("invite %s: could not read the salon name, no message queued: %v",
				inv.ID, err)
		} else if err := s.notifier.QueueSalonInvitation(ctx, inv.ID, *ph, salonName, link); err != nil {
			s.logf("invite %s: could not queue the WhatsApp message: %v", inv.ID, err)
		}
	}

	return &InviteResult{Invitation: inv, Link: link}, nil
}

// Preview answers the PUBLIC token endpoint.
//
// Everything unusable - unknown, expired, revoked, already accepted -
// returns the identical errInvitationNotFound. Anyone holding a URL can call
// this, so distinguishing those cases would tell a stranger which tokens
// once existed.
func (s *Service) Preview(ctx context.Context, rawToken string) (*InvitationPreview, error) {
	inv, err := s.loadRedeemable(ctx, rawToken)
	if err != nil {
		return nil, err
	}

	salonName, err := s.repo.SalonName(ctx, inv.SalonID)
	if err != nil {
		// The salon was deleted after the invitation was sent. Same answer:
		// the link is not usable, and why is not the invitee's business.
		return nil, errInvitationNotFound()
	}
	inviter, err := s.repo.UserDisplayName(ctx, inv.InvitedBy)
	if err != nil {
		inviter = salonName
	}

	needsSignup := true
	if u, err := s.repo.UserIDByContact(ctx, inv.Phone, inv.Email); err == nil && u != nil {
		needsSignup = false
	}

	return &InvitationPreview{
		SalonName: salonName, InvitedBy: inviter,
		ExpiresAt: inv.ExpiresAt, NeedsSignup: needsSignup,
	}, nil
}

// Accept attaches the caller to the salon the invitation names.
//
// The artist row is created through internal/onboarding, not here, so there
// is exactly one definition of what an artist is - including status
// 'pending', which keeps admin approval a gate rather than a suggestion
// (BR-7). An invitation does not bypass platform review.
func (s *Service) Accept(ctx context.Context, rawToken string, userID uuid.UUID,
	profile onboarding.ArtistProfile, ip string) (uuid.UUID, error) {

	inv, err := s.loadRedeemable(ctx, rawToken)
	if err != nil {
		return uuid.Nil, err
	}

	// The acceptor may have joined a salon between the invitation being
	// sent and redeemed. Re-checked here rather than trusted from Invite.
	if _, salon, err := s.repo.ArtistIDForUser(ctx, userID); err == nil && salon != nil {
		if *salon == inv.SalonID {
			return uuid.Nil, apperror.Conflict("ALREADY_A_MEMBER",
				"You are already in this salon")
		}
		return uuid.Nil, errAlreadyInSalon()
	} else if err != nil && !errors.Is(err, ErrNotFound) {
		return uuid.Nil, err
	}

	artistID, err := s.onboarding.CompleteIntoExistingSalon(ctx, userID, inv.SalonID, profile)
	if err != nil {
		switch {
		case errors.Is(err, onboarding.ErrAlreadyOnboarded):
			return uuid.Nil, errAlreadyInSalon()
		case errors.Is(err, onboarding.ErrHandleTaken):
			return uuid.Nil, apperror.Conflict("HANDLE_TAKEN",
				"That handle is already taken")
		case errors.Is(err, onboarding.ErrSalonNotFound):
			return uuid.Nil, errInvitationNotFound()
		}
		return uuid.Nil, err
	}

	if err := s.repo.SetInvitationStatus(ctx, inv.ID, StatusAccepted, &userID); err != nil {
		return uuid.Nil, err
	}
	s.redactDeliveredInvitation(ctx, inv.ID)

	_ = s.audit.Log(ctx, audit.Event{
		SalonID: &inv.SalonID, ActorID: &userID, ActorRole: "artist",
		EntityType: "salon_invitation", EntityID: inv.ID, Action: "accept",
		NewValues: map[string]any{"artist_id": artistID}, IPAddress: ip,
	})
	return artistID, nil
}

func (s *Service) Decline(ctx context.Context, rawToken string) error {
	inv, err := s.loadRedeemable(ctx, rawToken)
	if err != nil {
		return err
	}
	if err := s.repo.SetInvitationStatus(ctx, inv.ID, StatusDeclined, nil); err != nil {
		return err
	}
	s.redactDeliveredInvitation(ctx, inv.ID)
	return nil
}

func (s *Service) Revoke(ctx context.Context, salonID, actorID, invID uuid.UUID, ip string) error {
	inv, err := s.repo.InvitationByID(ctx, salonID, invID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return errInvitationNotFound()
		}
		return err
	}
	if inv.EffectiveStatus(s.now()) != StatusPending {
		return errInvitationNotFound()
	}
	if err := s.repo.SetInvitationStatus(ctx, inv.ID, StatusRevoked, nil); err != nil {
		return err
	}
	s.redactDeliveredInvitation(ctx, inv.ID)
	_ = s.audit.Log(ctx, audit.Event{
		SalonID: &salonID, ActorID: &actorID, ActorRole: "artist",
		EntityType: "salon_invitation", EntityID: inv.ID, Action: "revoke",
		IPAddress: ip,
	})
	return nil
}

func (s *Service) ListInvitations(ctx context.Context, salonID uuid.UUID) ([]*Invitation, error) {
	invs, err := s.repo.ListInvitations(ctx, salonID)
	if err != nil {
		return nil, err
	}
	// Lazy expiry applied on read, so the roster never shows a pending
	// invitation that is actually dead. Not written back here - a read
	// should not need a write to be correct.
	now := s.now()
	for _, i := range invs {
		i.Status = i.EffectiveStatus(now)
	}
	return invs, nil
}

// ── Roster ────────────────────────────────────────────────────────────────

func (s *Service) ListMembers(ctx context.Context, salonID uuid.UUID) ([]*Member, error) {
	return s.repo.ListMembers(ctx, salonID, s.now())
}

// RemoveMember detaches an artist from the salon.
//
// Nothing they did is deleted (BR-4): bookings, reviews, earnings and client
// notes all keep pointing at their artist row. Only the salon link goes.
func (s *Service) RemoveMember(ctx context.Context, salonID, actorID, artistID uuid.UUID,
	ip string) error {

	m, err := s.repo.MemberByArtistID(ctx, salonID, artistID, s.now())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return errMemberNotFound()
		}
		return err
	}
	if m.IsOwner {
		return errCannotRemoveOwner()
	}
	// BR-5. A customer must never find their appointment has evaporated
	// because of an internal staffing change.
	if m.FutureBookings > 0 {
		return errHasFutureBookings(m.FutureBookings)
	}
	if err := s.repo.DetachArtist(ctx, salonID, artistID); err != nil {
		if errors.Is(err, ErrNotFound) {
			return errMemberNotFound()
		}
		return err
	}
	if s.tokens != nil {
		_ = s.tokens.RevokeAllForUser(ctx, m.UserID)
	}
	_ = s.audit.Log(ctx, audit.Event{
		SalonID: &salonID, ActorID: &actorID, ActorRole: "artist",
		EntityType: "artist", EntityID: artistID, Action: "remove_member",
		IPAddress: ip,
	})
	return nil
}

// Leave is RemoveMember from the other side, with two extra refusals: an
// owner must transfer first (BR-2), and the last member cannot leave the
// salon empty (BR-3).
func (s *Service) Leave(ctx context.Context, salonID, userID uuid.UUID, ip string) error {
	artistID, salon, err := s.repo.ArtistIDForUser(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return errMemberNotFound()
		}
		return err
	}
	if salon == nil || *salon != salonID {
		return errMemberNotFound()
	}

	m, err := s.repo.MemberByArtistID(ctx, salonID, artistID, s.now())
	if err != nil {
		return errMemberNotFound()
	}
	if m.IsOwner {
		return errOwnerMustTransfer()
	}
	if m.FutureBookings > 0 {
		return errHasFutureBookings(m.FutureBookings)
	}

	n, err := s.repo.ActiveMemberCount(ctx, salonID)
	if err != nil {
		return err
	}
	if n <= 1 {
		return errLastMember()
	}

	if err := s.repo.DetachArtist(ctx, salonID, artistID); err != nil {
		return err
	}
	if s.tokens != nil {
		_ = s.tokens.RevokeAllForUser(ctx, userID)
	}
	_ = s.audit.Log(ctx, audit.Event{
		SalonID: &salonID, ActorID: &userID, ActorRole: "artist",
		EntityType: "artist", EntityID: artistID, Action: "leave_salon",
		IPAddress: ip,
	})
	return nil
}

// TransferOwnership hands the salon to another active member.
//
// Both parties' refresh tokens are revoked afterwards, and that is not
// housekeeping: salonrole.Resolve runs at token issue, so until a token is
// reissued the former owner still carries salon_role=owner and the new owner
// still carries member. This is the cost of not hitting the database on
// every request, and it has to be paid here.
func (s *Service) TransferOwnership(ctx context.Context, salonID, actorID uuid.UUID,
	toArtistID uuid.UUID, ip string) error {

	target, err := s.repo.MemberByArtistID(ctx, salonID, toArtistID, s.now())
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return errMemberNotFound()
		}
		return err
	}
	if target.IsOwner {
		return apperror.Conflict("ALREADY_OWNER", "That artist already owns this salon")
	}
	// BR-2: the owner must be an active member. A pending or rejected
	// artist cannot be handed a salon.
	if target.Status != "active" {
		return errTargetNotActiveMember()
	}

	previousOwner, err := s.repo.SalonOwnerUserID(ctx, salonID)
	if err != nil {
		return err
	}
	if err := s.repo.TransferOwnership(ctx, salonID, target.UserID); err != nil {
		return err
	}

	if s.tokens != nil {
		_ = s.tokens.RevokeAllForUser(ctx, previousOwner)
		_ = s.tokens.RevokeAllForUser(ctx, target.UserID)
	}

	_ = s.audit.Log(ctx, audit.Event{
		SalonID: &salonID, ActorID: &actorID, ActorRole: "artist",
		EntityType: "salon", EntityID: salonID, Action: "transfer_ownership",
		OldValues: map[string]any{"owner_id": previousOwner},
		NewValues: map[string]any{"owner_id": target.UserID},
		IPAddress: ip,
	})
	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────

// redactDeliveredInvitation removes the raw token from the queued message
// once the invitation can no longer be used.
//
// ── DATA-03, and what this does and does not fix ─────────────────────────
//
// salon_invitations stores only a SHA-256, so a database dump yields no
// working links. The notification row next to it held the FULL link,
// token included, in cleartext - and defeated that design entirely. Anyone
// who could read `notifications` had working invitations.
//
// The token cannot simply be left out: the outbound WhatsApp message IS the
// link, so the plaintext has to exist until it is sent. What was
// indefensible was that it then stayed forever, and every notification this
// platform has ever queued is still undelivered.
//
// So the exposure is bounded to the invitation's own lifetime, which is the
// same exposure the recipient's message thread already carries. Once an
// invitation is accepted, declined, revoked or expired, the token is worth
// nothing and the payload is replaced with a message that says so.
//
// Best-effort: a failure here must not fail the caller's action, but it is
// logged rather than discarded - discarding an error on this exact path is
// what hid the queueing bug for a day.
func (s *Service) redactDeliveredInvitation(ctx context.Context, invID uuid.UUID) {
	if s.notifier == nil {
		return
	}
	if err := s.notifier.RedactInvitationMessage(ctx, invID); err != nil {
		s.logf("invitation %s: could not redact the queued token: %v", invID, err)
	}
}

// loadRedeemable resolves a raw token to a usable invitation, collapsing
// every failure into one indistinguishable answer.
func (s *Service) loadRedeemable(ctx context.Context, rawToken string) (*Invitation, error) {
	if strings.TrimSpace(rawToken) == "" {
		return nil, errInvitationNotFound()
	}
	inv, err := s.repo.InvitationByTokenHash(ctx, hashToken(rawToken))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, errInvitationNotFound()
		}
		return nil, err
	}
	if err := inv.Redeemable(s.now()); err != nil {
		// Lazy expiry: write back so the partial unique index frees up and
		// the owner can issue a fresh invitation to the same person.
		if inv.Status == StatusPending {
			_ = s.repo.SetInvitationStatus(ctx, inv.ID, StatusExpired, nil)
			s.redactDeliveredInvitation(ctx, inv.ID)
		}
		return nil, errInvitationNotFound()
	}
	return inv, nil
}

func (s *Service) inviteLink(token string) string {
	return fmt.Sprintf("%s/join/%s", s.inviteBase, token)
}

// normaliseContact validates and canonicalises the invitee's contact
// details. At least one is required; the CHECK constraint enforces the same
// thing at the database level.
func normaliseContact(rawPhone, rawEmail string) (*string, *string, error) {
	var ph, em *string

	if p := strings.TrimSpace(rawPhone); p != "" {
		norm, err := phone.Parse(p, defaultRegion, "phone")
		if err != nil {
			return nil, nil, err
		}
		ph = &norm
	}
	if e := strings.TrimSpace(rawEmail); e != "" {
		lower := strings.ToLower(e)
		em = &lower
	}
	if ph == nil && em == nil {
		return nil, nil, errInvalidContact()
	}
	return ph, em, nil
}

// newInvitationToken returns the raw token and its hash. 32 bytes of
// crypto/rand, URL-safe: the token travels in a link.
func newInvitationToken() (raw, hashed string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("generate invitation token: %w", err)
	}
	raw = base64.RawURLEncoding.EncodeToString(b)
	return raw, hashToken(raw), nil
}

// hashToken is SHA-256, not bcrypt, deliberately. The input is 256 bits of
// entropy rather than a human-chosen password, so there is nothing to
// brute-force and nothing a work factor would buy - and this runs on every
// link open, where bcrypt's cost would be a denial-of-service lever.
// internal/domain/auth hashes refresh tokens the same way.
func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}
