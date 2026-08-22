// concurrency_limiter_test.go tests the global in-flight-request ceiling
// that protects the process from a genuine traffic spike, distinct from the
// per-IP rate limiter tested implicitly via register.go's own wiring.
package middleware

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newLimiterTestApp() *fiber.App {
	app := fiber.New()
	app.Use(concurrencyLimiter())
	app.Get("/ping", func(c *fiber.Ctx) error {
		return c.SendString("pong")
	})
	return app
}

// TestConcurrencyLimiter_UnderCeiling_PassesThrough proves an ordinary
// request is unaffected when the process isn't under load.
func TestConcurrencyLimiter_UnderCeiling_PassesThrough(t *testing.T) {
	inFlightRequests.Store(0)
	app := newLimiterTestApp()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/ping", nil))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, fiber.StatusOK, resp.StatusCode)
}

// TestConcurrencyLimiter_AtCeiling_Returns503WithRetryAfter simulates
// maxInFlightRequests already being handled - the next arrival is the one
// that tips the process over the ceiling and must be shed, not queued.
func TestConcurrencyLimiter_AtCeiling_Returns503WithRetryAfter(t *testing.T) {
	inFlightRequests.Store(maxInFlightRequests)
	t.Cleanup(func() { inFlightRequests.Store(0) })

	app := newLimiterTestApp()
	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/ping", nil))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, fiber.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, "5", resp.Header.Get(fiber.HeaderRetryAfter))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "SERVER_BUSY")
}

// TestConcurrencyLimiter_DecrementsAfterRequestCompletes proves the counter
// doesn't leak - a limiter that only ever counts up would eventually shed
// every request regardless of real load.
func TestConcurrencyLimiter_DecrementsAfterRequestCompletes(t *testing.T) {
	inFlightRequests.Store(0)
	app := newLimiterTestApp()

	resp, err := app.Test(httptest.NewRequest(fiber.MethodGet, "/ping", nil))
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck

	assert.Equal(t, int64(0), inFlightRequests.Load())
}
