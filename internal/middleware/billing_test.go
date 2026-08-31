// Package middleware's tests for the subscription suspension guard.
//
// Every case here runs through a real fiber app via app.Test, matching the
// pattern in concurrency_limiter_test.go — no fiber.Ctx is constructed by
// hand. The guard's status branches only became reachable once
// RequireActiveSubscription took a SubscriptionReader instead of a concrete
// *pgxpool.Pool; before that, anything past the JWT checks needed a live
// database and went untested.
package middleware

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/jwt"
)

// TestMain sets the JWT secrets the guard needs to verify tokens. Matches
// the pattern in internal/booking and internal/artist.
func TestMain(m *testing.M) {
	_ = os.Setenv("JWT_SECRET", "test_secret_for_middleware_billing_tests_only_0123456789")
	_ = os.Setenv("JWT_REFRESH_SECRET", "test_refresh_secret_for_middleware_billing_tests_0123456789")
	_ = os.Setenv("APP_ENV", "test")
	os.Exit(m.Run())
}

// ── Test doubles ──────────────────────────────────────────────────────────────

type stubReader struct {
	snap *SubscriptionSnapshot
	err  error
	// captured for assertions
	called bool
}

func (s *stubReader) SubscriptionByUserID(_ context.Context, _ uuid.UUID) (*SubscriptionSnapshot, error) {
	s.called = true
	return s.snap, s.err
}

// newGuardApp builds a fiber app with the guard mounted and a trivial
// mutating route behind it. It uses the real apperror.ErrorHandler so the
// assertions below are on the HTTP status the production app would actually
// return, not on a test-only translation of it.
func newGuardApp(reader SubscriptionReader) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	app.Use(RequireActiveSubscription(reader))
	app.Post("/thing", func(c *fiber.Ctx) error { return c.SendString("ok") })
	app.Get("/thing", func(c *fiber.Ctx) error { return c.SendString("ok") })
	return app
}

func tokenForRole(t *testing.T, userID uuid.UUID, role string) string {
	t.Helper()
	tok, err := jwt.GenerateAccessToken(userID, nil, role)
	require.NoError(t, err)
	return tok
}

func artistToken(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	return tokenForRole(t, userID, "artist")
}

func doPost(t *testing.T, app *fiber.App, authHeader string) int {
	t.Helper()
	req := httptest.NewRequest(fiber.MethodPost, "/thing", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	_, _ = io.ReadAll(resp.Body)
	return resp.StatusCode
}

func suspendedSnapshot() *SubscriptionSnapshot {
	past := time.Now().AddDate(0, 0, -60)
	return &SubscriptionSnapshot{PlanCode: "starter", CurrentPeriodEnd: &past}
}

// ── Pre-query early exits (testable before the refactor too) ──────────────────

// Read-only requests never consult the subscription at all.
func TestRequireActiveSubscription_GetRequest_SkipsLookupEntirely(t *testing.T) {
	reader := &stubReader{snap: suspendedSnapshot()}
	app := newGuardApp(reader)

	req := httptest.NewRequest(fiber.MethodGet, "/thing", nil)
	req.Header.Set("Authorization", "Bearer "+artistToken(t, uuid.New()))
	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
	assert.False(t, reader.called, "a GET must never hit the database")
}

func TestRequireActiveSubscription_NoAuthHeader_PassesThrough(t *testing.T) {
	reader := &stubReader{snap: suspendedSnapshot()}
	assert.Equal(t, fiber.StatusOK, doPost(t, newGuardApp(reader), ""))
	assert.False(t, reader.called)
}

func TestRequireActiveSubscription_MalformedAuthHeader_PassesThrough(t *testing.T) {
	reader := &stubReader{snap: suspendedSnapshot()}
	assert.Equal(t, fiber.StatusOK, doPost(t, newGuardApp(reader), "NotBearer abc"))
	assert.False(t, reader.called)
}

func TestRequireActiveSubscription_InvalidToken_PassesThrough(t *testing.T) {
	reader := &stubReader{snap: suspendedSnapshot()}
	assert.Equal(t, fiber.StatusOK, doPost(t, newGuardApp(reader), "Bearer not-a-real-token"))
	assert.False(t, reader.called,
		"RequireAuth rejects bad tokens downstream — this guard must not also try to")
}

// Admins act on behalf of the platform and are never blocked by billing state.
func TestRequireActiveSubscription_AdminRole_PassesThrough(t *testing.T) {
	reader := &stubReader{snap: suspendedSnapshot()}
	tok := tokenForRole(t, uuid.New(), "admin")

	assert.Equal(t, fiber.StatusOK, doPost(t, newGuardApp(reader), "Bearer "+tok))
	assert.False(t, reader.called, "an admin must never be looked up")
}

// ── Status branches (unreachable before the T1.8 refactor) ────────────────────

func TestRequireActiveSubscription_Suspended_Blocks(t *testing.T) {
	reader := &stubReader{snap: suspendedSnapshot()}

	status := doPost(t, newGuardApp(reader), "Bearer "+artistToken(t, uuid.New()))

	assert.Equal(t, fiber.StatusForbidden, status,
		"a suspended artist must be blocked from mutating requests")
	assert.True(t, reader.called)
}

func TestRequireActiveSubscription_Active_PassesThrough(t *testing.T) {
	future := time.Now().AddDate(0, 0, 20)
	reader := &stubReader{snap: &SubscriptionSnapshot{PlanCode: "starter", CurrentPeriodEnd: &future}}

	assert.Equal(t, fiber.StatusOK, doPost(t, newGuardApp(reader), "Bearer "+artistToken(t, uuid.New())))
}

// past_due is explicitly NOT blocked here, even though internal/booking does
// block it. Pinning the divergence so that unifying the three enforcement
// points is a deliberate change rather than an accidental one.
func TestRequireActiveSubscription_PastDue_PassesThrough(t *testing.T) {
	past := time.Now().AddDate(0, 0, -10) // past grace (7), inside past_due (21)
	reader := &stubReader{snap: &SubscriptionSnapshot{PlanCode: "starter", CurrentPeriodEnd: &past}}

	assert.Equal(t, fiber.StatusOK, doPost(t, newGuardApp(reader), "Bearer "+artistToken(t, uuid.New())),
		"this guard blocks only Suspended — internal/booking is the stricter one")
}

func TestRequireActiveSubscription_Grace_PassesThrough(t *testing.T) {
	past := time.Now().AddDate(0, 0, -3)
	reader := &stubReader{snap: &SubscriptionSnapshot{PlanCode: "starter", CurrentPeriodEnd: &past}}

	assert.Equal(t, fiber.StatusOK, doPost(t, newGuardApp(reader), "Bearer "+artistToken(t, uuid.New())))
}

// A comped account with a long-expired period is still active and must pass.
func TestRequireActiveSubscription_CompedWithLapsedPeriod_PassesThrough(t *testing.T) {
	past := time.Now().AddDate(0, 0, -365)
	reader := &stubReader{snap: &SubscriptionSnapshot{PlanCode: "comped", CurrentPeriodEnd: &past}}

	assert.Equal(t, fiber.StatusOK, doPost(t, newGuardApp(reader), "Bearer "+artistToken(t, uuid.New())),
		"comped accounts never lapse — Rania must never be locked out")
}

// ── The two fail-open paths (security-relevant) ───────────────────────────────

// No subscription row is the pre-Phase-3 signup gap, not an error. Failing
// open here is deliberate: an artist who was never assigned a plan must not
// be locked out of their own dashboard.
func TestRequireActiveSubscription_NoSubscriptionRow_FailsOpen(t *testing.T) {
	reader := &stubReader{snap: nil, err: nil}

	assert.Equal(t, fiber.StatusOK, doPost(t, newGuardApp(reader), "Bearer "+artistToken(t, uuid.New())))
}

// A read failure must never lock an artist out. This is a deliberate
// security trade-off: a database blip degrades to unenforced billing rather
// than to an unusable product.
func TestRequireActiveSubscription_ReadError_FailsOpen(t *testing.T) {
	reader := &stubReader{err: errors.New("connection refused")}

	assert.Equal(t, fiber.StatusOK, doPost(t, newGuardApp(reader), "Bearer "+artistToken(t, uuid.New())),
		"a DB error must fail open, not lock the artist out")
}
