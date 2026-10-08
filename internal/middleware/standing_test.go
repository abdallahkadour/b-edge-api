// standing_test.go tests the per-request account check RequireAuth runs once
// one is installed (security AUTH-14b and AUTH-22, 2026-10-08).
//
// Before it, RequireAuth trusted the access token completely for its 15
// minutes: a member removed from a salon kept reading its orders and shipping
// them, and a suspended account renewed its session indefinitely. The token
// still proves WHO is calling; the database now says what that person is
// allowed to be, on every request.
package middleware

import (
	"context"
	"errors"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

func withStanding(t *testing.T, f StandingFunc) {
	t.Helper()
	UseCurrentStanding(f)
	t.Cleanup(func() { UseCurrentStanding(nil) })
}

// newStandingApp answers with what a handler would read from Locals.
func newStandingApp(reached *bool) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	app.Get("/me", RequireAuth(), func(c *fiber.Ctx) error {
		if reached != nil {
			*reached = true
		}
		salon := "none"
		if s := SalonIDFromContext(c); s != nil {
			salon = s.String()
		}
		return c.SendString(salon + "|" + string(SalonRoleFromContext(c)))
	})
	return app
}

func TestRequireAuth_RemovedMember_TheTokenStillNamesTheSalon_SheHasNone(t *testing.T) {
	withStanding(t, func(_ context.Context, _ uuid.UUID) (*Standing, error) {
		return &Standing{SalonID: nil, SalonRole: salonrole.None}, nil
	})
	tok := salonToken(t, salonrole.Member, true)

	st, body := do(t, newStandingApp(nil), "GET", "/me", tok)

	assert.Equal(t, 200, st)
	assert.Equal(t, "none|"+string(salonrole.None), body,
		"the salon comes from the database, not from a token issued before she was removed")
}

func TestRequireAuth_FormerOwner_AfterATransfer_IsAMemberNow(t *testing.T) {
	salon := uuid.New()
	withStanding(t, func(_ context.Context, _ uuid.UUID) (*Standing, error) {
		return &Standing{SalonID: &salon, SalonRole: salonrole.Member}, nil
	})
	tok := salonToken(t, salonrole.Owner, true)

	_, body := do(t, newStandingApp(nil), "GET", "/me", tok)

	assert.Equal(t, salon.String()+"|"+string(salonrole.Member), body, "security AUTH-15: no second owner")
}

func TestRequireAuth_SuspendedAccount_Refused(t *testing.T) {
	withStanding(t, func(_ context.Context, _ uuid.UUID) (*Standing, error) {
		return &Standing{Suspended: true}, nil
	})
	reached := false

	st, body := do(t, newStandingApp(&reached), "GET", "/me", salonToken(t, salonrole.Owner, true))

	assert.Equal(t, 403, st)
	assert.Equal(t, "ACCOUNT_SUSPENDED", errCode(t, body))
	assert.False(t, reached)
}

func TestRequireAuth_DeletedAccount_Refused(t *testing.T) {
	withStanding(t, func(_ context.Context, _ uuid.UUID) (*Standing, error) {
		return nil, ErrAccountGone
	})
	reached := false

	st, body := do(t, newStandingApp(&reached), "GET", "/me", salonToken(t, salonrole.Owner, true))

	assert.Equal(t, 401, st)
	assert.Equal(t, "TOKEN_INVALID", errCode(t, body))
	assert.False(t, reached)
}

func TestRequireAuth_StandingUnreadable_FailsClosed(t *testing.T) {
	withStanding(t, func(_ context.Context, _ uuid.UUID) (*Standing, error) {
		return nil, errors.New("connection refused")
	})
	reached := false

	st, _ := do(t, newStandingApp(&reached), "GET", "/me", salonToken(t, salonrole.Owner, true))

	assert.Equal(t, 500, st, "an account that cannot be checked is not let through on the token's word")
	assert.False(t, reached)
}

func TestRequireAuth_NothingInstalled_TrustsTheToken(t *testing.T) {
	// What every handler unit test relies on: with no check installed the
	// token is read exactly as before. Production installs the check in
	// domain/auth.RegisterRoutes, and auth's own test proves it does.
	UseCurrentStanding(nil)
	tok := salonToken(t, salonrole.Owner, true)

	_, body := do(t, newStandingApp(nil), "GET", "/me", tok)

	assert.NotContains(t, body, "none|")
	assert.Contains(t, body, "|"+string(salonrole.Owner))
}
