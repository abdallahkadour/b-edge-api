package membership

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/onboarding"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// ── Mocks ─────────────────────────────────────────────────────────────────

type mockRepo struct {
	invitations map[string]*Invitation // keyed by token hash
	byID        map[uuid.UUID]*Invitation
	liveContact *Invitation
	liveErr     error

	members     map[uuid.UUID]*Member
	activeCount int
	artistID    uuid.UUID
	artistSalon *uuid.UUID
	artistErr   error
	ownerUserID uuid.UUID
	contactUser *uuid.UUID
	salonName   string

	created        *Invitation
	statusSet      []InvitationStatus
	detached       []uuid.UUID
	transferredTo  *uuid.UUID
	detachErr      error
	transferCalled bool
	liveCount      int
	dayCount       int
	ceiling        int
	ceilingPlan    string
}

func newMockRepo() *mockRepo {
	return &mockRepo{
		invitations: map[string]*Invitation{},
		byID:        map[uuid.UUID]*Invitation{},
		members:     map[uuid.UUID]*Member{},
		salonName:   "Rania Studio",
		activeCount: 2,
	}
}

func (m *mockRepo) CreateInvitation(_ context.Context, inv *Invitation) error {
	inv.ID = uuid.New()
	inv.Status = StatusPending
	m.created = inv
	m.invitations[inv.TokenHash] = inv
	m.byID[inv.ID] = inv
	return nil
}

func (m *mockRepo) InvitationByTokenHash(_ context.Context, hash string) (*Invitation, error) {
	if i, ok := m.invitations[hash]; ok {
		return i, nil
	}
	return nil, ErrNotFound
}

func (m *mockRepo) InvitationByID(_ context.Context, _, id uuid.UUID) (*Invitation, error) {
	if i, ok := m.byID[id]; ok {
		return i, nil
	}
	return nil, ErrNotFound
}

func (m *mockRepo) LiveInvitationForContact(_ context.Context, _ uuid.UUID, _, _ *string) (*Invitation, error) {
	if m.liveErr != nil {
		return nil, m.liveErr
	}
	if m.liveContact != nil {
		return m.liveContact, nil
	}
	return nil, ErrNotFound
}

func (m *mockRepo) ListInvitations(_ context.Context, _ uuid.UUID) ([]*Invitation, error) {
	out := make([]*Invitation, 0, len(m.byID))
	for _, i := range m.byID {
		out = append(out, i)
	}
	return out, nil
}

func (m *mockRepo) ArtistCeiling(_ context.Context, _ uuid.UUID) (int, string, error) {
	return m.ceiling, m.ceilingPlan, nil
}

func (m *mockRepo) CountLiveInvitations(_ context.Context, _ uuid.UUID) (int, error) {
	return m.liveCount, nil
}

func (m *mockRepo) CountInvitationsSince(_ context.Context, _ uuid.UUID, _ time.Time) (int, error) {
	return m.dayCount, nil
}

func (m *mockRepo) SetInvitationStatus(_ context.Context, id uuid.UUID,
	st InvitationStatus, by *uuid.UUID) error {
	m.statusSet = append(m.statusSet, st)
	if i, ok := m.byID[id]; ok {
		i.Status = st
		i.AcceptedBy = by
	}
	return nil
}

func (m *mockRepo) ListMembers(_ context.Context, _ uuid.UUID, _ time.Time) ([]*Member, error) {
	out := make([]*Member, 0, len(m.members))
	for _, v := range m.members {
		out = append(out, v)
	}
	return out, nil
}

func (m *mockRepo) ActiveMemberCount(_ context.Context, _ uuid.UUID) (int, error) {
	return m.activeCount, nil
}

func (m *mockRepo) MemberByArtistID(_ context.Context, _, artistID uuid.UUID,
	_ time.Time) (*Member, error) {
	if v, ok := m.members[artistID]; ok {
		return v, nil
	}
	return nil, ErrNotFound
}

func (m *mockRepo) ArtistIDForUser(_ context.Context, _ uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
	return m.artistID, m.artistSalon, m.artistErr
}

func (m *mockRepo) DetachArtist(_ context.Context, _, artistID uuid.UUID) error {
	if m.detachErr != nil {
		return m.detachErr
	}
	m.detached = append(m.detached, artistID)
	return nil
}

func (m *mockRepo) SalonOwnerUserID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return m.ownerUserID, nil
}
func (m *mockRepo) SalonName(_ context.Context, _ uuid.UUID) (string, error) {
	return m.salonName, nil
}
func (m *mockRepo) UserDisplayName(_ context.Context, _ uuid.UUID) (string, error) {
	return "Rania", nil
}
func (m *mockRepo) TransferOwnership(_ context.Context, _, to uuid.UUID) error {
	m.transferCalled = true
	m.transferredTo = &to
	return nil
}
func (m *mockRepo) UserIDByContact(_ context.Context, _, _ *string) (*uuid.UUID, error) {
	return m.contactUser, nil
}

type mockOnboarding struct {
	artistID uuid.UUID
	err      error
	called   bool
	gotSalon uuid.UUID
}

func (m *mockOnboarding) CompleteIntoExistingSalon(_ context.Context, _, salonID uuid.UUID,
	_ onboarding.ArtistProfile) (uuid.UUID, error) {
	m.called = true
	m.gotSalon = salonID
	return m.artistID, m.err
}

type mockNotifier struct {
	calls    int
	err      error
	redacted []uuid.UUID
}

func (m *mockNotifier) QueueSalonInvitation(_ context.Context, _ uuid.UUID, _, _, _ string) error {
	m.calls++
	return m.err
}

func (m *mockNotifier) RedactInvitationMessage(_ context.Context, id uuid.UUID) error {
	m.redacted = append(m.redacted, id)
	return nil
}

type mockTokens struct{ revoked []uuid.UUID }

func (m *mockTokens) RevokeAllForUser(_ context.Context, id uuid.UUID) error {
	m.revoked = append(m.revoked, id)
	return nil
}

func newTestService(repo Repository, ob OnboardingPort) (*Service, *mockNotifier, *mockTokens) {
	n, tk := &mockNotifier{}, &mockTokens{}
	s := NewService(repo, ob, n, tk, nil, "https://app.b-edge.com", nil)
	return s, n, tk
}

func code(t *testing.T, err error) string {
	t.Helper()
	require.Error(t, err)
	var ae *apperror.AppError
	require.True(t, errors.As(err, &ae), "want an AppError, got %T: %v", err, err)
	return ae.Code
}

// ── Invite ────────────────────────────────────────────────────────────────

func TestInvite_ValidPhone_CreatesPendingInvitationAndLink(t *testing.T) {
	repo := newMockRepo()
	svc, notif, _ := newTestService(repo, &mockOnboarding{})

	res, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "1.2.3.4")

	require.NoError(t, err)
	assert.Equal(t, StatusPending, res.Invitation.Status)
	assert.Contains(t, res.Link, "https://app.b-edge.com/join/")
	assert.Equal(t, 1, notif.calls)
}

// The partial unique index cannot see that '70555123' and '+96170555123' are
// one person unless the column is already E.164. Same equivalence FRAUD-09
// pinned for deposit payers.
func TestInvite_NormalisesPhoneToE164BeforeStoring(t *testing.T) {
	repo := newMockRepo()
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70 555 123"}, "")

	require.NoError(t, err)
	require.NotNil(t, repo.created.Phone)
	assert.Equal(t, "+96170555123", *repo.created.Phone)
}

func TestInvite_StoresOnlyTheTokenHashNotTheToken(t *testing.T) {
	repo := newMockRepo()
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	res, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	require.NoError(t, err)
	rawToken := res.Link[len("https://app.b-edge.com/join/"):]
	assert.NotEqual(t, rawToken, repo.created.TokenHash,
		"the raw token must never be what is stored")
	assert.Equal(t, hashToken(rawToken), repo.created.TokenHash)
}

func TestInvite_NeitherPhoneNorEmail_Refused(t *testing.T) {
	svc, _, _ := newTestService(newMockRepo(), &mockOnboarding{})
	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(), InviteRequest{}, "")
	assert.Equal(t, "INVALID_CONTACT", code(t, err))
}

func TestInvite_InviteeAlreadyInAnotherSalon_Refused(t *testing.T) {
	repo := newMockRepo()
	other, someone := uuid.New(), uuid.New()
	repo.contactUser = &someone
	repo.artistSalon = &other

	svc, _, _ := newTestService(repo, &mockOnboarding{})
	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	assert.Equal(t, "ALREADY_IN_SALON", code(t, err))
}

func TestInvite_InviteeAlreadyInThisSalon_RefusedDistinctly(t *testing.T) {
	repo := newMockRepo()
	salonID, someone := uuid.New(), uuid.New()
	repo.contactUser = &someone
	repo.artistSalon = &salonID

	svc, _, _ := newTestService(repo, &mockOnboarding{})
	_, err := svc.Invite(context.Background(), salonID, uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	assert.Equal(t, "ALREADY_A_MEMBER", code(t, err))
}

func TestInvite_LivePendingInvitationExists_RefusedRatherThanDuplicated(t *testing.T) {
	repo := newMockRepo()
	repo.liveContact = &Invitation{
		Status: StatusPending, ExpiresAt: time.Now().Add(time.Hour),
	}
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	assert.Equal(t, "INVITATION_EXISTS", code(t, err))
}

// An expired invitation must not block a fresh one - it is retired lazily so
// the partial unique index frees up.
func TestInvite_ExpiredInvitationExists_RetiredAndReplaced(t *testing.T) {
	repo := newMockRepo()
	old := &Invitation{
		ID: uuid.New(), Status: StatusPending,
		ExpiresAt: time.Now().Add(-time.Hour),
	}
	repo.liveContact = old
	repo.byID[old.ID] = old

	svc, _, _ := newTestService(repo, &mockOnboarding{})
	res, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	require.NoError(t, err)
	assert.Contains(t, repo.statusSet, StatusExpired, "the stale one must be retired")
	assert.NotNil(t, res.Invitation)
}

// TWILIO_WHATSAPP_FROM is unset and 100% of queued notifications are dead. An
// invitation that could not be created because delivery failed would be a
// feature nobody can use.
func TestInvite_NotifierFails_InvitationIsStillCreated(t *testing.T) {
	repo := newMockRepo()
	svc, notif, _ := newTestService(repo, &mockOnboarding{})
	notif.err = errors.New("twilio is unreachable")

	res, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	require.NoError(t, err)
	assert.NotNil(t, repo.created)
	assert.NotEmpty(t, res.Link, "the copyable link is the working channel today")
}

// ── Preview and Accept ────────────────────────────────────────────────────

func seedInvitation(repo *mockRepo, salonID uuid.UUID, status InvitationStatus,
	expires time.Time) string {
	raw, hash, _ := newInvitationToken()
	inv := &Invitation{
		ID: uuid.New(), SalonID: salonID, InvitedBy: uuid.New(),
		TokenHash: hash, Status: status, ExpiresAt: expires,
	}
	repo.invitations[hash] = inv
	repo.byID[inv.ID] = inv
	return raw
}

func TestPreview_ValidToken_ReturnsSalonAndInviter(t *testing.T) {
	repo := newMockRepo()
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	p, err := svc.Preview(context.Background(), raw)

	require.NoError(t, err)
	assert.Equal(t, "Rania Studio", p.SalonName)
	assert.Equal(t, "Rania", p.InvitedBy)
}

// The public token endpoint is an enumeration surface. Unknown, expired,
// revoked, declined and already-accepted must be indistinguishable.
func TestPreview_EveryUnusableToken_GivesTheIdenticalError(t *testing.T) {
	past, future := time.Now().Add(-time.Hour), time.Now().Add(time.Hour)
	cases := []struct {
		name   string
		status InvitationStatus
		expiry time.Time
	}{
		{"expired", StatusPending, past},
		{"revoked", StatusRevoked, future},
		{"declined", StatusDeclined, future},
		{"already accepted", StatusAccepted, future},
	}
	var codes []string
	for _, c := range cases {
		repo := newMockRepo()
		raw := seedInvitation(repo, uuid.New(), c.status, c.expiry)
		svc, _, _ := newTestService(repo, &mockOnboarding{})
		_, err := svc.Preview(context.Background(), raw)
		codes = append(codes, code(t, err))
	}
	// and a token that never existed
	svc, _, _ := newTestService(newMockRepo(), &mockOnboarding{})
	_, err := svc.Preview(context.Background(), "a-token-that-was-never-issued")
	codes = append(codes, code(t, err))

	for i, got := range codes {
		assert.Equal(t, "INVITATION_NOT_FOUND", got,
			"case %d must be indistinguishable from an unknown token", i)
	}
}

func TestPreview_EmptyToken_Refused(t *testing.T) {
	svc, _, _ := newTestService(newMockRepo(), &mockOnboarding{})
	_, err := svc.Preview(context.Background(), "   ")
	assert.Equal(t, "INVITATION_NOT_FOUND", code(t, err))
}

func TestAccept_ValidToken_CreatesArtistInTheInvitingSalon(t *testing.T) {
	repo := newMockRepo()
	repo.artistErr = ErrNotFound // the acceptor is not yet an artist
	salonID := uuid.New()
	raw := seedInvitation(repo, salonID, StatusPending, time.Now().Add(time.Hour))

	ob := &mockOnboarding{artistID: uuid.New()}
	svc, _, _ := newTestService(repo, ob)

	got, err := svc.Accept(context.Background(), raw, uuid.New(),
		onboarding.ArtistProfile{Handle: "new-artist", Category: "makeup"}, "")

	require.NoError(t, err)
	assert.Equal(t, ob.artistID, got)
	assert.True(t, ob.called, "the artist must be created through onboarding, not here")
	assert.Equal(t, salonID, ob.gotSalon)
	assert.Contains(t, repo.statusSet, StatusAccepted)
}

func TestAccept_TokenCannotBeUsedTwice(t *testing.T) {
	repo := newMockRepo()
	repo.artistErr = ErrNotFound
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	svc, _, _ := newTestService(repo, &mockOnboarding{artistID: uuid.New()})

	_, err := svc.Accept(context.Background(), raw, uuid.New(),
		onboarding.ArtistProfile{Handle: "a", Category: "makeup"}, "")
	require.NoError(t, err)

	_, err = svc.Accept(context.Background(), raw, uuid.New(),
		onboarding.ArtistProfile{Handle: "b", Category: "makeup"}, "")
	assert.Equal(t, "INVITATION_NOT_FOUND", code(t, err))
}

// Someone may have joined a salon between the invitation being sent and
// redeemed, so Invite's check cannot be trusted at Accept time.
func TestAccept_AcceptorJoinedAnotherSalonMeanwhile_Refused(t *testing.T) {
	repo := newMockRepo()
	other := uuid.New()
	repo.artistSalon = &other
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	_, err := svc.Accept(context.Background(), raw, uuid.New(),
		onboarding.ArtistProfile{Handle: "a", Category: "makeup"}, "")

	assert.Equal(t, "ALREADY_IN_SALON", code(t, err))
}

func TestAccept_HandleTaken_SurfacesAsHandleTaken(t *testing.T) {
	repo := newMockRepo()
	repo.artistErr = ErrNotFound
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	svc, _, _ := newTestService(repo, &mockOnboarding{err: onboarding.ErrHandleTaken})

	_, err := svc.Accept(context.Background(), raw, uuid.New(),
		onboarding.ArtistProfile{Handle: "rania", Category: "makeup"}, "")

	assert.Equal(t, "HANDLE_TAKEN", code(t, err))
}

func TestAccept_SalonDeletedMeanwhile_LooksLikeAnInvalidLink(t *testing.T) {
	repo := newMockRepo()
	repo.artistErr = ErrNotFound
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	svc, _, _ := newTestService(repo, &mockOnboarding{err: onboarding.ErrSalonNotFound})

	_, err := svc.Accept(context.Background(), raw, uuid.New(),
		onboarding.ArtistProfile{Handle: "a", Category: "makeup"}, "")

	assert.Equal(t, "INVITATION_NOT_FOUND", code(t, err))
}

// ── Revoke ────────────────────────────────────────────────────────────────

func TestRevoke_PendingInvitation_Revoked(t *testing.T) {
	repo := newMockRepo()
	salonID := uuid.New()
	seedInvitation(repo, salonID, StatusPending, time.Now().Add(time.Hour))
	var invID uuid.UUID
	for id := range repo.byID {
		invID = id
	}
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	require.NoError(t, svc.Revoke(context.Background(), salonID, uuid.New(), invID, ""))
	assert.Contains(t, repo.statusSet, StatusRevoked)
}

func TestRevoke_AlreadyAccepted_NotFound(t *testing.T) {
	repo := newMockRepo()
	salonID := uuid.New()
	seedInvitation(repo, salonID, StatusAccepted, time.Now().Add(time.Hour))
	var invID uuid.UUID
	for id := range repo.byID {
		invID = id
	}
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	err := svc.Revoke(context.Background(), salonID, uuid.New(), invID, "")
	assert.Equal(t, "INVITATION_NOT_FOUND", code(t, err))
}

// ── Removal ───────────────────────────────────────────────────────────────

func memberFixture(isOwner bool, future int, status string) *Member {
	return &Member{
		ArtistID: uuid.New(), UserID: uuid.New(), DisplayName: "Member",
		Status: status, IsOwner: isOwner, FutureBookings: future,
	}
}

func TestRemoveMember_CleanMember_DetachedAndTokensRevoked(t *testing.T) {
	repo := newMockRepo()
	m := memberFixture(false, 0, "active")
	repo.members[m.ArtistID] = m
	svc, _, tk := newTestService(repo, &mockOnboarding{})

	require.NoError(t, svc.RemoveMember(context.Background(), uuid.New(), uuid.New(), m.ArtistID, ""))
	assert.Equal(t, []uuid.UUID{m.ArtistID}, repo.detached)
	assert.Contains(t, tk.revoked, m.UserID)
}

func TestRemoveMember_Owner_Refused(t *testing.T) {
	repo := newMockRepo()
	m := memberFixture(true, 0, "active")
	repo.members[m.ArtistID] = m
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	err := svc.RemoveMember(context.Background(), uuid.New(), uuid.New(), m.ArtistID, "")
	assert.Equal(t, "CANNOT_REMOVE_OWNER", code(t, err))
	assert.Empty(t, repo.detached)
}

// BR-5: a customer must never discover their appointment evaporated because
// of an internal staffing change.
func TestRemoveMember_WithFutureBookings_Refused(t *testing.T) {
	repo := newMockRepo()
	m := memberFixture(false, 3, "active")
	repo.members[m.ArtistID] = m
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	err := svc.RemoveMember(context.Background(), uuid.New(), uuid.New(), m.ArtistID, "")
	assert.Equal(t, "HAS_FUTURE_BOOKINGS", code(t, err))
	assert.Empty(t, repo.detached)
}

func TestRemoveMember_NotInThisSalon_NotFound(t *testing.T) {
	svc, _, _ := newTestService(newMockRepo(), &mockOnboarding{})
	err := svc.RemoveMember(context.Background(), uuid.New(), uuid.New(), uuid.New(), "")
	assert.Equal(t, "MEMBER_NOT_FOUND", code(t, err))
}

// ── Leave ─────────────────────────────────────────────────────────────────

func TestLeave_Member_Detached(t *testing.T) {
	repo := newMockRepo()
	salonID := uuid.New()
	m := memberFixture(false, 0, "active")
	repo.members[m.ArtistID] = m
	repo.artistID, repo.artistSalon = m.ArtistID, &salonID
	repo.activeCount = 2
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	require.NoError(t, svc.Leave(context.Background(), salonID, m.UserID, ""))
	assert.Equal(t, []uuid.UUID{m.ArtistID}, repo.detached)
}

func TestLeave_Owner_MustTransferFirst(t *testing.T) {
	repo := newMockRepo()
	salonID := uuid.New()
	m := memberFixture(true, 0, "active")
	repo.members[m.ArtistID] = m
	repo.artistID, repo.artistSalon = m.ArtistID, &salonID
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	err := svc.Leave(context.Background(), salonID, m.UserID, "")
	assert.Equal(t, "OWNER_MUST_TRANSFER", code(t, err))
}

// BR-3: a salon must not be left owning stores, services and booking history
// with nobody in it.
func TestLeave_LastMember_Refused(t *testing.T) {
	repo := newMockRepo()
	salonID := uuid.New()
	m := memberFixture(false, 0, "active")
	repo.members[m.ArtistID] = m
	repo.artistID, repo.artistSalon = m.ArtistID, &salonID
	repo.activeCount = 1
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	err := svc.Leave(context.Background(), salonID, m.UserID, "")
	assert.Equal(t, "LAST_MEMBER", code(t, err))
	assert.Empty(t, repo.detached)
}

// ── Transfer ──────────────────────────────────────────────────────────────

// The role is baked into the access token at issue. Without revoking both
// sides, the former owner keeps owner capabilities until their token
// expires - risk R2 in the HLD.
func TestTransferOwnership_RevokesBothPartiesTokens(t *testing.T) {
	repo := newMockRepo()
	target := memberFixture(false, 0, "active")
	repo.members[target.ArtistID] = target
	previousOwner := uuid.New()
	repo.ownerUserID = previousOwner

	svc, _, tk := newTestService(repo, &mockOnboarding{})
	require.NoError(t, svc.TransferOwnership(context.Background(), uuid.New(),
		previousOwner, target.ArtistID, ""))

	assert.True(t, repo.transferCalled)
	assert.Equal(t, target.UserID, *repo.transferredTo)
	assert.Contains(t, tk.revoked, previousOwner, "the former owner must lose their token")
	assert.Contains(t, tk.revoked, target.UserID, "the new owner needs a token that says so")
}

func TestTransferOwnership_TargetNotActive_Refused(t *testing.T) {
	repo := newMockRepo()
	target := memberFixture(false, 0, "pending")
	repo.members[target.ArtistID] = target
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	err := svc.TransferOwnership(context.Background(), uuid.New(), uuid.New(),
		target.ArtistID, "")
	assert.Equal(t, "TARGET_NOT_ACTIVE_MEMBER", code(t, err))
}

func TestTransferOwnership_TargetIsAlreadyOwner_Refused(t *testing.T) {
	repo := newMockRepo()
	target := memberFixture(true, 0, "active")
	repo.members[target.ArtistID] = target
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	err := svc.TransferOwnership(context.Background(), uuid.New(), uuid.New(),
		target.ArtistID, "")
	assert.Equal(t, "ALREADY_OWNER", code(t, err))
}

func TestTransferOwnership_TargetNotInSalon_NotFound(t *testing.T) {
	svc, _, _ := newTestService(newMockRepo(), &mockOnboarding{})
	err := svc.TransferOwnership(context.Background(), uuid.New(), uuid.New(), uuid.New(), "")
	assert.Equal(t, "MEMBER_NOT_FOUND", code(t, err))
}

// ── Lazy expiry ───────────────────────────────────────────────────────────

func TestEffectiveStatus_PendingPastDeadline_ReadsExpiredWithoutAWrite(t *testing.T) {
	inv := &Invitation{Status: StatusPending, ExpiresAt: time.Now().Add(-time.Minute)}
	assert.Equal(t, StatusExpired, inv.EffectiveStatus(time.Now()))
	assert.Equal(t, StatusPending, inv.Status, "reading must not mutate the row")
}

func TestEffectiveStatus_TerminalStatesAreNotReinterpreted(t *testing.T) {
	past := time.Now().Add(-time.Hour)
	for _, st := range []InvitationStatus{StatusAccepted, StatusDeclined, StatusRevoked} {
		inv := &Invitation{Status: st, ExpiresAt: past}
		assert.Equal(t, st, inv.EffectiveStatus(time.Now()),
			"%s must not become expired just because time passed", st)
	}
}

func TestListInvitations_AppliesLazyExpiryOnRead(t *testing.T) {
	repo := newMockRepo()
	seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(-time.Hour))
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	got, err := svc.ListInvitations(context.Background(), uuid.New())
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, StatusExpired, got[0].Status,
		"the roster must never show a pending invitation that is actually dead")
}

// ── SPAM-08: invitation volume ────────────────────────────────────────────

// Nothing bounded this until the security run measured 30 invitations to 30
// distinct numbers going through. Every one queues a WhatsApp message, so
// once delivery works an owner account is an SMS cannon firing from B-Edge's
// verified sender.

func TestInvite_TooManyLiveInvitations_Refused(t *testing.T) {
	repo := newMockRepo()
	repo.liveCount = MaxLiveInvitations
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	assert.Equal(t, "INVITATION_LIMIT", code(t, err))
	assert.Nil(t, repo.created, "nothing may be written once the cap is reached")
}

func TestInvite_TooManyInvitationsToday_Refused(t *testing.T) {
	repo := newMockRepo()
	repo.dayCount = MaxInvitationsPerDay
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	assert.Equal(t, "INVITATION_LIMIT", code(t, err))
}

// One below each cap still works. A limit that fires early would stop a
// salon onboarding a team, which is the feature.
func TestInvite_JustUnderBothCaps_Succeeds(t *testing.T) {
	repo := newMockRepo()
	repo.liveCount = MaxLiveInvitations - 1
	repo.dayCount = MaxInvitationsPerDay - 1
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	res, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	require.NoError(t, err)
	assert.NotNil(t, res.Invitation)
}

// The cap is checked AFTER the duplicate refusal, so an owner re-sending to
// one person is never told they have hit a limit.
func TestInvite_DuplicateIsReportedBeforeTheLimit(t *testing.T) {
	repo := newMockRepo()
	repo.liveCount = MaxLiveInvitations
	repo.liveContact = &Invitation{Status: StatusPending, ExpiresAt: time.Now().Add(time.Hour)}
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	assert.Equal(t, "INVITATION_EXISTS", code(t, err),
		"re-sending to one person must not surface as a rate limit")
}

// ── DATA-03: the raw token must stop being recoverable ────────────────────

// salon_invitations stores only a SHA-256 so a database dump yields no
// working links. The queued WhatsApp message held the full link, token
// included, in cleartext - defeating that design entirely. The plaintext has
// to exist until the message is sent, so the exposure is bounded to the
// invitation's own lifetime instead: the moment it can no longer be
// redeemed, the payload is replaced.

func TestAccept_RedactsTheQueuedToken(t *testing.T) {
	repo := newMockRepo()
	repo.artistErr = ErrNotFound
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	svc, notif, _ := newTestService(repo, &mockOnboarding{artistID: uuid.New()})

	_, err := svc.Accept(context.Background(), raw, uuid.New(),
		onboarding.ArtistProfile{Handle: "a", Category: "makeup"}, "")

	require.NoError(t, err)
	assert.Len(t, notif.redacted, 1,
		"an accepted invitation's token is spent and must not stay readable")
}

func TestDecline_RedactsTheQueuedToken(t *testing.T) {
	repo := newMockRepo()
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	svc, notif, _ := newTestService(repo, &mockOnboarding{})

	require.NoError(t, svc.Decline(context.Background(), raw))
	assert.Len(t, notif.redacted, 1)
}

func TestRevoke_RedactsTheQueuedToken(t *testing.T) {
	repo := newMockRepo()
	salonID := uuid.New()
	seedInvitation(repo, salonID, StatusPending, time.Now().Add(time.Hour))
	var invID uuid.UUID
	for id := range repo.byID {
		invID = id
	}
	svc, notif, _ := newTestService(repo, &mockOnboarding{})

	require.NoError(t, svc.Revoke(context.Background(), salonID, uuid.New(), invID, ""))
	assert.Len(t, notif.redacted, 1)
}

// Lazy expiry is a terminal transition too, and it happens on a READ - the
// path most likely to be forgotten.
func TestExpiry_RedactsTheQueuedToken(t *testing.T) {
	repo := newMockRepo()
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(-time.Hour))
	svc, notif, _ := newTestService(repo, &mockOnboarding{})

	_, err := svc.Preview(context.Background(), raw)

	require.Error(t, err, "an expired invitation is not previewable")
	assert.Len(t, notif.redacted, 1,
		"expiry retires the token and must redact it too")
}

// ── FRAUD-14: the plan's artist ceiling ───────────────────────────────────
//
// included_seats stopped being "seats you pay for" and became a ceiling when
// per-seat billing was rejected (migration 050). It was then enforced by
// nothing, which made every tier above the $45 entry unsellable - a Solo
// salon had exactly what a $249 Multi salon had.

func TestInvite_AtThePlanCeiling_Refused(t *testing.T) {
	repo := newMockRepo()
	repo.ceiling, repo.ceilingPlan, repo.activeCount = 1, "solo", 1
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	assert.Equal(t, "PLAN_LIMIT_REACHED", code(t, err))
	assert.Nil(t, repo.created, "no invitation may be written at the ceiling")
}

// Pending invitations count toward the ceiling, or an owner at her limit
// queues twenty and lets them all land.
func TestInvite_PendingInvitationsCountTowardTheCeiling(t *testing.T) {
	repo := newMockRepo()
	repo.ceiling, repo.ceilingPlan = 4, "studio"
	repo.activeCount, repo.liveCount = 2, 2

	svc, _, _ := newTestService(repo, &mockOnboarding{})
	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	assert.Equal(t, "PLAN_LIMIT_REACHED", code(t, err),
		"2 active + 2 pending fills a ceiling of 4")
}

func TestInvite_BelowTheCeiling_Succeeds(t *testing.T) {
	repo := newMockRepo()
	repo.ceiling, repo.ceilingPlan = 4, "studio"
	repo.activeCount, repo.liveCount = 2, 1

	svc, _, _ := newTestService(repo, &mockOnboarding{})
	res, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	require.NoError(t, err)
	assert.NotNil(t, res.Invitation)
}

// A billing lookup that finds nothing must not silently stop a salon
// hiring. An under-charged salon is a far better failure than a blocked one.
func TestInvite_NoPlanFound_TreatedAsUnlimited(t *testing.T) {
	repo := newMockRepo()
	repo.ceiling, repo.activeCount = 0, 50 // ceiling 0 == "no plan resolved"

	svc, _, _ := newTestService(repo, &mockOnboarding{})
	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	require.NoError(t, err, "a missing plan must not block hiring")
}

// comped is 999 and must never be a limit: the launch artist and the whole
// internal roster run on it.
func TestInvite_CompedCeiling_NeverBinds(t *testing.T) {
	repo := newMockRepo()
	repo.ceiling, repo.ceilingPlan, repo.activeCount = 999, "comped", 40

	svc, _, _ := newTestService(repo, &mockOnboarding{})
	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123"}, "")

	require.NoError(t, err)
}
