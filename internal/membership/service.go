package membership

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

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
	QueueSalonInvitation(ctx context.Context, toPhone, salonName, link string) error
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
	now        func() time.Time
}

func NewService(repo Repository, ob OnboardingPort, n Notifier, ti TokenInvalidator,
	a audit.Repository, inviteBase string) *Service {

	if a == nil {
		a = audit.NopRepository{}
	}
	return &Service{
		repo: repo, onboarding: ob, notifier: n, tokens: ti, audit: a,
		validate:   validation.New(),
		inviteBase: strings.TrimRight(inviteBase, "/"),
		now:        time.Now,
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

	// 2. Refuse someone who already belongs to a salon (BR-1). Checked
	//    against the account if one exists; if they have no account yet
	//    there is nothing to conflict with.
	if existingUser, err := s.repo.UserIDByContact(ctx, ph, em); err != nil {
		return nil, err
	} else if existingUser != nil {
		_, salon, err := s.repo.ArtistIDForUser(ctx, *existingUser)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if salon != nil {
			if *salon == salonID {
				return nil, apperror.Conflict("ALREADY_A_MEMBER",
					"That artist is already in this salon")
			}
			return nil, errAlreadyInSalon()
		}
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

	// 5. Generate the token; store only its hash.
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

	// 6. Queue the notification OUTSIDE the transaction and ignore its
	//    error. Delivery is currently impossible - Meta verification is
	//    pending and 100% of queued notifications are dead - so letting a
	//    send failure roll back the invitation would mean no invitation
	//    could be created at all. The link above is the working channel.
	if s.notifier != nil && ph != nil {
		salonName, err := s.repo.SalonName(ctx, salonID)
		if err == nil {
			_ = s.notifier.QueueSalonInvitation(ctx, *ph, salonName, link)
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
	return s.repo.SetInvitationStatus(ctx, inv.ID, StatusDeclined, nil)
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
