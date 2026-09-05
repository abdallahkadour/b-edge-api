// secheaders_test.go pins the browser-enforced protections from
// secheaders.go, which exist to close CLIENT-04 of
// B-Edge-Security-Test-Plan-v1.md.
//
// These are assertions, not conventions, for the reason AUTH-03 in that
// document gives about admin routes: a header that is only present because
// nobody has removed it yet is not a control. Each one here fails loudly if
// it disappears.
package middleware

import (
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSecHeadersTestApp() *fiber.App {
	app := fiber.New()
	app.Use(SecurityHeaders())
	app.Get("/ping", func(c *fiber.Ctx) error {
		return c.SendString("pong")
	})
	return app
}

// TestSecurityHeaders_OrdinaryResponse_CarriesEveryHeader is the CLIENT-04
// test case expressed in code: `curl -I` on any endpoint must show the full
// set.
func TestSecurityHeaders_OrdinaryResponse_CarriesEveryHeader(t *testing.T) {
	app := newSecHeadersTestApp()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/ping", nil))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
	assert.Equal(t, "strict-origin-when-cross-origin", resp.Header.Get("Referrer-Policy"))
	assert.Equal(t, "same-origin", resp.Header.Get("Cross-Origin-Opener-Policy"))
	assert.NotEmpty(t, resp.Header.Get("Permissions-Policy"))
	assert.Equal(t, APIContentSecurityPolicy, resp.Header.Get("Content-Security-Policy"))
}

// TestSecurityHeaders_DefaultPolicy_DeniesEverything guards the specific
// directives rather than merely that a policy exists. A CSP that had been
// quietly widened to 'unsafe-inline' globally would still be non-empty, and
// would still pass a presence-only check.
func TestSecurityHeaders_DefaultPolicy_DeniesEverything(t *testing.T) {
	assert.Contains(t, APIContentSecurityPolicy, "default-src 'none'")
	assert.Contains(t, APIContentSecurityPolicy, "frame-ancestors 'none'")
	assert.Contains(t, APIContentSecurityPolicy, "base-uri 'none'")
	assert.Contains(t, APIContentSecurityPolicy, "form-action 'none'")

	// The global policy must never permit inline execution. The two HTML
	// endpoints that need it narrow their own policy at the handler; this
	// is what stops that exception becoming the default for everything.
	assert.NotContains(t, APIContentSecurityPolicy, "unsafe-inline")
	assert.NotContains(t, APIContentSecurityPolicy, "unsafe-eval")
}

// TestSecurityHeaders_PlainHTTP_OmitsHSTS covers the case that would be a
// self-inflicted outage: pinning a hostname to HTTPS from a plaintext
// development server, which the browser then caches for a year.
func TestSecurityHeaders_PlainHTTP_OmitsHSTS(t *testing.T) {
	app := newSecHeadersTestApp()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/ping", nil))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Empty(t, resp.Header.Get("Strict-Transport-Security"))
}

// TestSecurityHeaders_ForwardedHTTPS_SetsHSTS is the production shape:
// TLS terminates at the load balancer, so the connection to this process is
// plain HTTP and X-Forwarded-Proto is the only signal that the client is on
// HTTPS. Without this branch HSTS would never be sent in production at all.
func TestSecurityHeaders_ForwardedHTTPS_SetsHSTS(t *testing.T) {
	app := newSecHeadersTestApp()

	req := httptest.NewRequest(fiber.MethodGet, "/ping", nil)
	req.Header.Set("X-Forwarded-Proto", "https")

	resp, err := app.Test(req)
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, hstsMaxAge, resp.Header.Get("Strict-Transport-Security"))
	assert.Contains(t, resp.Header.Get("Strict-Transport-Security"), "includeSubDomains")

	// preload is a slow-to-reverse commitment to a browser-baked list and
	// must not arrive by accident.
	assert.NotContains(t, resp.Header.Get("Strict-Transport-Security"), "preload")
}

// TestSecurityHeaders_ShedRequest_StillCarriesHeaders is why the middleware
// is registered second rather than last. A 503 from the concurrency limiter
// never reaches a handler, and an error response is precisely when a
// browser is most likely to be rendering something unexpected.
func TestSecurityHeaders_ShedRequest_StillCarriesHeaders(t *testing.T) {
	inFlightRequests.Store(maxInFlightRequests)
	t.Cleanup(func() { inFlightRequests.Store(0) })

	app := fiber.New()
	app.Use(SecurityHeaders())
	app.Use(concurrencyLimiter())
	app.Get("/ping", func(c *fiber.Ctx) error {
		return c.SendString("pong")
	})

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/ping", nil))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	require.Equal(t, fiber.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "nosniff", resp.Header.Get("X-Content-Type-Options"))
	assert.Equal(t, "DENY", resp.Header.Get("X-Frame-Options"))
	assert.Equal(t, APIContentSecurityPolicy, resp.Header.Get("Content-Security-Policy"))
}
