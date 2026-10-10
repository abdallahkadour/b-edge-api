package middleware

// RequireAuth hands the services the same caller it hands the handlers, so
// an activity row names who acted and the booking guards can see an owner.

import (
	"context"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/caller"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

func newCallerApp() *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	app.Get("/me", RequireAuth(), func(c *fiber.Ctx) error {
		who, ok := caller.From(c.UserContext())
		if !ok {
			return c.SendString("nobody")
		}
		salon := "none"
		if who.SalonID != nil {
			salon = who.SalonID.String()
		}
		same := who.UserID == UserIDFromContext(c)
		return c.SendString(salon + "|" + string(who.SalonRole) + "|" + who.Role + "|" + map[bool]string{true: "same-user", false: "other-user"}[same])
	})
	return app
}

func TestRequireAuth_PutsTheCallerOnTheRequestContext(t *testing.T) {
	_, body := do(t, newCallerApp(), "GET", "/me", salonToken(t, salonrole.Owner, true))

	assert.Contains(t, body, "|owner|artist|same-user")
}

func TestRequireAuth_CallerCarriesTheDatabasesStanding_NotTheTokens(t *testing.T) {
	salon := uuid.New()
	withStanding(t, func(_ context.Context, _ uuid.UUID) (*Standing, error) {
		return &Standing{SalonID: &salon, SalonRole: salonrole.Member}, nil
	})

	_, body := do(t, newCallerApp(), "GET", "/me", salonToken(t, salonrole.Owner, true))

	assert.Equal(t, salon.String()+"|member|artist|same-user", body,
		"a former owner's token must not make a service believe she still owns the salon")
}
