// salonrole_test.go tests the owner/member boundary inside a salon - the
// guard that did not exist before 2026-09-21, when any artist carrying a
// salon_id claim could write every salon-scoped row.
package middleware

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/jwt"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

// newCapabilityApp builds a Fiber app shaped like a real guarded route group:
// RequireAuth, then the capability guard, then a handler. It uses the real
// apperror.ErrorHandler so assertions are on the status production returns.
func newCapabilityApp(cap salonrole.Capability) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	app.Post("/guarded", RequireAuth(), RequireSalonCapability(cap),
		func(c *fiber.Ctx) error { return c.SendString("ok") })
	// An unguarded read, to prove the guard is scoped to the route rather
	// than to the token.
	app.Get("/open", RequireAuth(), func(c *fiber.Ctx) error {
		return c.SendString("ok")
	})
	return app
}

func salonToken(t *testing.T, role salonrole.Role, withSalon bool) string {
	t.Helper()
	_ = os.Setenv("JWT_SECRET", "test_secret_for_middleware_salonrole_tests_0123456789")
	var salonID *uuid.UUID
	if withSalon {
		id := uuid.New()
		salonID = &id
	}
	tok, err := jwt.GenerateAccessToken(uuid.New(), salonID, "artist", role)
	require.NoError(t, err)
	return tok
}

func do(t *testing.T, app *fiber.App, method, path, token string) (int, string) {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func errCode(t *testing.T, body string) string {
	t.Helper()
	var parsed struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &parsed))
	if parsed.Error == nil {
		return ""
	}
	return parsed.Error.Code
}

// ── The boundary ───────────────────────────────────────────────────────────

func TestRequireSalonCapability_Owner_PassesThrough(t *testing.T) {
	app := newCapabilityApp(salonrole.ServicesWrite)
	status, _ := do(t, app, fiber.MethodPost, "/guarded", salonToken(t, salonrole.Owner, true))
	assert.Equal(t, fiber.StatusOK, status)
}

func TestRequireSalonCapability_MemberLacksCapability_Returns403(t *testing.T) {
	app := newCapabilityApp(salonrole.ServicesWrite)
	status, body := do(t, app, fiber.MethodPost, "/guarded", salonToken(t, salonrole.Member, true))

	assert.Equal(t, fiber.StatusForbidden, status)
	assert.Equal(t, "SALON_ROLE_FORBIDDEN", errCode(t, body))
}

// TestRequireSalonCapability_MemberHoldsCapability_PassesThrough is the
// counterweight: a guard that refuses a member everything would stop them
// working, which is a broken account rather than a boundary.
func TestRequireSalonCapability_MemberHoldsCapability_PassesThrough(t *testing.T) {
	app := newCapabilityApp(salonrole.OwnScheduleWrite)
	status, _ := do(t, app, fiber.MethodPost, "/guarded", salonToken(t, salonrole.Member, true))
	assert.Equal(t, fiber.StatusOK, status)
}

// ── Failing closed ─────────────────────────────────────────────────────────

func TestRequireSalonCapability_NoSalon_Returns403NoSalon(t *testing.T) {
	app := newCapabilityApp(salonrole.ServicesWrite)
	status, body := do(t, app, fiber.MethodPost, "/guarded", salonToken(t, salonrole.None, false))

	assert.Equal(t, fiber.StatusForbidden, status)
	assert.Equal(t, "NO_SALON", errCode(t, body))
}

// TestRequireSalonCapability_SalonWithoutRole_Refuses covers the token minted
// before the salon_role claim existed: it carries a salon but no role, and
// must lose salon writes rather than keep them.
func TestRequireSalonCapability_SalonWithoutRole_Refuses(t *testing.T) {
	app := newCapabilityApp(salonrole.ServicesWrite)
	status, body := do(t, app, fiber.MethodPost, "/guarded", salonToken(t, salonrole.None, true))

	assert.Equal(t, fiber.StatusForbidden, status)
	assert.Equal(t, "SALON_ROLE_FORBIDDEN", errCode(t, body))
}

func TestRequireSalonCapability_NoToken_Returns401(t *testing.T) {
	app := newCapabilityApp(salonrole.ServicesWrite)
	status, _ := do(t, app, fiber.MethodPost, "/guarded", "")
	assert.Equal(t, fiber.StatusUnauthorized, status)
}

// TestRequireSalonCapability_WithoutRequireAuth_FailsClosed covers a
// registration mistake: the guard mounted with no RequireAuth in front of it.
// It must refuse rather than read a role that was never set.
func TestRequireSalonCapability_WithoutRequireAuth_FailsClosed(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	app.Post("/unchained", RequireSalonCapability(salonrole.ServicesWrite),
		func(c *fiber.Ctx) error { return c.SendString("ok") })

	status, _ := do(t, app, fiber.MethodPost, "/unchained",
		salonToken(t, salonrole.Owner, true))
	assert.Equal(t, fiber.StatusUnauthorized, status,
		"a guard registered without RequireAuth must fail closed")
}

// ── Reads stay open ────────────────────────────────────────────────────────

// TestRequireSalonCapability_DoesNotAffectUnguardedRoutes proves the guard is
// scoped to the route it is registered on. A member must still be able to
// read the salon's services, stores and hours - they cannot work otherwise.
func TestRequireSalonCapability_DoesNotAffectUnguardedRoutes(t *testing.T) {
	app := newCapabilityApp(salonrole.ServicesWrite)
	status, _ := do(t, app, fiber.MethodGet, "/open", salonToken(t, salonrole.Member, true))
	assert.Equal(t, fiber.StatusOK, status)
}

// ── SalonRoleFromContext ───────────────────────────────────────────────────

func TestSalonRoleFromContext_NoAuth_ReturnsNone(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	app.Get("/peek", func(c *fiber.Ctx) error {
		return c.SendString(string(SalonRoleFromContext(c)))
	})
	status, body := do(t, app, fiber.MethodGet, "/peek", "")
	assert.Equal(t, fiber.StatusOK, status)
	assert.Equal(t, string(salonrole.None), body,
		"an unauthenticated request must read as None, never as a permitted role")
}

func TestSalonRoleFromContext_Owner_ReturnsOwner(t *testing.T) {
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	app.Get("/peek", RequireAuth(), func(c *fiber.Ctx) error {
		return c.SendString(string(SalonRoleFromContext(c)))
	})
	_, body := do(t, app, fiber.MethodGet, "/peek", salonToken(t, salonrole.Owner, true))
	assert.Equal(t, string(salonrole.Owner), body)
}
