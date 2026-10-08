package auth

// The account's standing, read on every request and at refresh
// (security AUTH-14b and AUTH-22, 2026-10-08). See middleware.Standing for
// the measured defects; these pin the auth side of the fix:
//
//   - CurrentStanding answers from the database row, never from a token;
//   - Refresh refuses a suspended account (it used to issue it a new
//     session, indefinitely) but NOT a frozen one - decision D27, the
//     freeze screen promises "you can undo this right here";
//   - RegisterRoutes installs the check, so it cannot ship switched off.

import (
	"context"
	"errors"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	internaljwt "github.com/abdallahkadour/b-edge-api/internal/pkg/jwt"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

func inSalon(u *User, owner uuid.UUID) *User {
	salon := uuid.New()
	u.Role = RoleArtist
	u.SalonID, u.SalonOwnerID = &salon, &owner
	return u
}

func TestCurrentStanding_Owner_IsTheOwnerOfHerSalon(t *testing.T) {
	u := existingUser()
	inSalon(u, u.ID)
	svc := newTestService(&mockRepo{getUserByIDUser: u})

	st, err := svc.CurrentStanding(context.Background(), u.ID)

	require.NoError(t, err)
	assert.Equal(t, u.SalonID, st.SalonID)
	assert.Equal(t, salonrole.Owner, st.SalonRole)
	assert.False(t, st.Suspended)
}

func TestCurrentStanding_Member_IsAMember(t *testing.T) {
	u := inSalon(existingUser(), uuid.New())
	svc := newTestService(&mockRepo{getUserByIDUser: u})

	st, err := svc.CurrentStanding(context.Background(), u.ID)

	require.NoError(t, err)
	assert.Equal(t, salonrole.Member, st.SalonRole)
}

func TestCurrentStanding_NoSalon_HasNone(t *testing.T) {
	u := existingUser()
	svc := newTestService(&mockRepo{getUserByIDUser: u})

	st, err := svc.CurrentStanding(context.Background(), u.ID)

	require.NoError(t, err)
	assert.Nil(t, st.SalonID)
	assert.Equal(t, salonrole.None, st.SalonRole)
}

func TestCurrentStanding_Suspended_SaysSo(t *testing.T) {
	u := existingUser()
	u.Status = StatusSuspended
	svc := newTestService(&mockRepo{getUserByIDUser: u})

	st, err := svc.CurrentStanding(context.Background(), u.ID)

	require.NoError(t, err)
	assert.True(t, st.Suspended)
}

func TestCurrentStanding_Frozen_IsNotSuspended_D27(t *testing.T) {
	u := existingUser()
	u.Status = StatusFrozen
	svc := newTestService(&mockRepo{getUserByIDUser: u})

	st, err := svc.CurrentStanding(context.Background(), u.ID)

	require.NoError(t, err)
	assert.False(t, st.Suspended, "a frozen account keeps the session it has - that is how it unfreezes")
}

func TestCurrentStanding_DeletedOrUnknown_IsGone(t *testing.T) {
	svc := newTestService(&mockRepo{getUserByIDErr: ErrUserNotFound})

	_, err := svc.CurrentStanding(context.Background(), uuid.New())

	assert.ErrorIs(t, err, middleware.ErrAccountGone)
}

func TestCurrentStanding_DatabaseError_IsNotGone(t *testing.T) {
	// A failed read must fail the request closed (500), not log the caller
	// out as if the account had been deleted.
	svc := newTestService(&mockRepo{getUserByIDErr: errors.New("connection refused")})

	_, err := svc.CurrentStanding(context.Background(), uuid.New())

	require.Error(t, err)
	assert.NotErrorIs(t, err, middleware.ErrAccountGone)
}

func refreshable(t *testing.T, u *User) (*mockRepo, string) {
	t.Helper()
	raw, err := internaljwt.GenerateRefreshToken(u.ID)
	require.NoError(t, err)
	return &mockRepo{
		getUserByIDUser:            u,
		getRefreshTokenByHashToken: &RefreshToken{ID: uuid.New(), UserID: u.ID, TokenHash: hashToken(raw)},
	}, raw
}

func TestRefresh_SuspendedAccount_GetsNoNewSession(t *testing.T) {
	u := existingUser()
	u.Status = StatusSuspended
	repo, raw := refreshable(t, u)

	res, err := newTestService(repo).Refresh(context.Background(), raw)

	assert.Nil(t, res)
	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr), "want an AppError, got %v", err)
	assert.Equal(t, "ACCOUNT_SUSPENDED", appErr.Code)
}

func TestRefresh_FrozenAccount_KeepsItsSession_D27(t *testing.T) {
	u := existingUser()
	u.Status = StatusFrozen
	repo, raw := refreshable(t, u)

	res, err := newTestService(repo).Refresh(context.Background(), raw)

	require.NoError(t, err)
	assert.NotEmpty(t, res.AccessToken)
}

func TestRegisterRoutes_InstallsTheStandingCheck(t *testing.T) {
	// pgxpool.New does not dial until a connection is needed, so this needs
	// no database: it only proves the wiring.
	pool, err := pgxpool.New(context.Background(), "postgres://nobody@127.0.0.1:1/none")
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	middleware.UseCurrentStanding(nil)
	t.Cleanup(func() { middleware.UseCurrentStanding(nil) })

	RegisterRoutes(fiber.New(), pool, zap.NewNop())

	assert.True(t, middleware.StandingInstalled(),
		"without this, RequireAuth trusts a 15-minute-old token again (AUTH-14b, AUTH-22)")
}
