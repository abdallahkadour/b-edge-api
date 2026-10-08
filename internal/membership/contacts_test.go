package membership

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Security AUTH-20 (2026-10-08). An invitation given BOTH a phone and an
// email was sent even when they belonged to two different artists. The
// invite and accept lookups each take "the first user matching phone or
// email", LIMIT 1, so which of the two could join was decided by row order -
// measured live: the email's owner was refused and the phone's owner joined.
// Nobody but the owner can say who the invitation was for, so it is refused
// at invite. The Team screen only ever sends a phone; this closes the API.

func TestInvite_PhoneAndEmailOfTwoDifferentPeople_Refused(t *testing.T) {
	repo := newMockRepo()
	repo.byPhone, repo.byEmail = ptrUUID(uuid.New()), ptrUUID(uuid.New())
	svc, notif, _ := newTestService(repo, &mockOnboarding{})

	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123", Email: "someone.else@test.local"}, "")

	assert.Equal(t, "CONTACTS_DISAGREE", code(t, err))
	assert.Nil(t, repo.created, "nothing may be written for an invitation nobody can attribute")
	assert.Equal(t, 0, notif.calls)
}

func TestInvite_PhoneOfOneArtistEmailOfNobody_Refused(t *testing.T) {
	// Allowing this would leave the email free for someone to register
	// later, and the accept lookup would then match two people again.
	repo := newMockRepo()
	repo.byPhone = ptrUUID(uuid.New())
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	_, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123", Email: "unclaimed@test.local"}, "")

	assert.Equal(t, "CONTACTS_DISAGREE", code(t, err))
	assert.Nil(t, repo.created)
}

func TestInvite_PhoneAndEmailOfTheSamePerson_Sent(t *testing.T) {
	repo := newMockRepo()
	same := uuid.New()
	repo.byPhone, repo.byEmail = &same, &same
	svc, notif, _ := newTestService(repo, &mockOnboarding{})

	res, err := svc.Invite(context.Background(), uuid.New(), uuid.New(),
		InviteRequest{Phone: "70555123", Email: "her@test.local"}, "")

	require.NoError(t, err)
	assert.Equal(t, StatusPending, res.Invitation.Status)
	assert.Equal(t, 1, notif.calls)
}
