package membership

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/onboarding"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// An invitation is for the person it was sent to (founder's decision,
// 2026-09-29, closing AUTH-16 and FRAUD-11). Before this, the link itself was
// the credential: anyone who got hold of it - a forwarded message, a shared
// screen - could join the salon as that person, or quietly decline it on
// their behalf without an account at all.

func TestAccept_SomeoneOtherThanTheInvitee_RefusedAndNothingChanges(t *testing.T) {
	repo := newMockRepo()
	repo.artistErr = ErrNotFound
	invitee := uuid.New()
	repo.contactUser = &invitee
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	ob := &mockOnboarding{artistID: uuid.New()}
	svc, _, _ := newTestService(repo, ob)

	_, err := svc.Accept(context.Background(), raw, uuid.New(),
		onboarding.ArtistProfile{Handle: "thief", Category: "makeup"}, "")

	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "INVITATION_NOT_FOR_YOU", appErr.Code)
	assert.Equal(t, 403, appErr.HTTPStatus)
	assert.False(t, ob.called, "no artist is created for the wrong person")
	assert.Empty(t, repo.statusSet, "the invitation is still there for the right person")
}

func TestAccept_TheInvitee_Joins(t *testing.T) {
	repo := newMockRepo()
	repo.artistErr = ErrNotFound
	invitee := uuid.New()
	repo.contactUser = &invitee
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	svc, _, _ := newTestService(repo, &mockOnboarding{artistID: uuid.New()})

	_, err := svc.Accept(context.Background(), raw, invitee,
		onboarding.ArtistProfile{Handle: "invitee", Category: "makeup"}, "")

	require.NoError(t, err)
	assert.Contains(t, repo.statusSet, StatusAccepted)
}

func TestAccept_NobodyHoldsTheInvitedNumberAnyMore_Refused(t *testing.T) {
	// The invitee changed her number or closed her account. The invitation
	// no longer names anyone, so nobody may redeem it.
	repo := newMockRepo()
	repo.artistErr = ErrNotFound
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	ob := &mockOnboarding{artistID: uuid.New()}
	svc, _, _ := newTestService(repo, ob)

	_, err := svc.Accept(context.Background(), raw, uuid.New(),
		onboarding.ArtistProfile{Handle: "x", Category: "makeup"}, "")

	assert.Equal(t, "INVITATION_NOT_FOR_YOU", code(t, err))
	assert.False(t, ob.called)
}

func TestAccept_SomeoneElseAlreadyInTheSalon_IsToldItIsNotTheirs(t *testing.T) {
	// Who the invitation is for is decided first: a member holding someone
	// else's link learns only that it is not hers, not the salon's roster.
	repo := newMockRepo()
	salon := uuid.New()
	repo.artistSalon = &salon
	invitee := uuid.New()
	repo.contactUser = &invitee
	raw := seedInvitation(repo, salon, StatusPending, time.Now().Add(time.Hour))
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	_, err := svc.Accept(context.Background(), raw, uuid.New(),
		onboarding.ArtistProfile{Handle: "x", Category: "makeup"}, "")

	assert.Equal(t, "INVITATION_NOT_FOR_YOU", code(t, err))
}

func TestDecline_SomeoneOtherThanTheInvitee_RefusedAndNothingChanges(t *testing.T) {
	repo := newMockRepo()
	invitee := uuid.New()
	repo.contactUser = &invitee
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	svc, notif, _ := newTestService(repo, &mockOnboarding{})

	err := svc.Decline(context.Background(), raw, uuid.New())

	assert.Equal(t, "INVITATION_NOT_FOR_YOU", code(t, err))
	assert.Empty(t, repo.statusSet, "a stranger cannot burn someone's invitation")
	assert.Empty(t, notif.redacted)
}

func TestDecline_TheInvitee_Declines(t *testing.T) {
	repo := newMockRepo()
	invitee := uuid.New()
	repo.contactUser = &invitee
	raw := seedInvitation(repo, uuid.New(), StatusPending, time.Now().Add(time.Hour))
	svc, _, _ := newTestService(repo, &mockOnboarding{})

	require.NoError(t, svc.Decline(context.Background(), raw, invitee))
	assert.Contains(t, repo.statusSet, StatusDeclined)
}

func TestDeclineRoute_NeedsALogin(t *testing.T) {
	// FRAUD-11: declining was public. No database is needed to see the
	// login refused first; recover turns a handler reaching the nil pool
	// into a 500, which is what the unguarded route produced.
	t.Setenv("JWT_SECRET", "test-secret-at-least-32-characters-long")
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	app.Use(recover.New())
	RegisterRoutes(app, nil, zap.NewNop(), "http://localhost:4300/join/")

	resp, err := app.Test(httptest.NewRequest("POST", "/api/v1/invitations/abc/decline", nil))

	require.NoError(t, err)
	assert.Equal(t, 401, resp.StatusCode)
}
