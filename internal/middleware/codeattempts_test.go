package middleware

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// FRAUD-20: both promo previews answer valid:true for a real code, so the
// only defence against guessing is how many guesses an address gets. Measured
// 2026-09-28 before this existed: ~5,800 an hour under the general limiter.

func previewApp(limit fiber.Handler) *fiber.App {
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	ok := func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) }
	app.Post("/orders/discount-preview", limit, ok)
	app.Post("/bookings/:id/discount-preview", limit, ok)
	return app
}

func try(t *testing.T, app *fiber.App, path string) (int, string) {
	t.Helper()
	resp, err := app.Test(httptest.NewRequest("POST", path, nil))
	require.NoError(t, err)
	raw, _ := io.ReadAll(resp.Body)
	var body struct {
		Error struct{ Code string } `json:"error"`
	}
	_ = json.Unmarshal(raw, &body)
	return resp.StatusCode, body.Error.Code
}

func TestPromoCodeAttempts_TwentyPerAddress_TheNextIsRefused(t *testing.T) {
	app := previewApp(NewPromoCodeAttempts())

	for i := 1; i <= 20; i++ {
		st, _ := try(t, app, "/orders/discount-preview")
		require.Equal(t, fiber.StatusOK, st, "attempt %d of 20 must be answered", i)
	}
	st, code := try(t, app, "/orders/discount-preview")

	assert.Equal(t, fiber.StatusTooManyRequests, st)
	assert.Equal(t, "TOO_MANY_CODE_ATTEMPTS", code,
		"its own code, so the app can say what happened instead of 'too many requests'")
}

func TestPromoCodeAttempts_BothPreviewsShareOneBudget(t *testing.T) {
	// A guesser alternating between the cart preview and the booking preview
	// must not get two budgets.
	app := previewApp(NewPromoCodeAttempts())
	for i := 1; i <= 20; i++ {
		path := "/orders/discount-preview"
		if i%2 == 0 {
			path = "/bookings/b1/discount-preview"
		}
		st, _ := try(t, app, path)
		require.Equal(t, fiber.StatusOK, st, "attempt %d", i)
	}

	st, code := try(t, app, "/bookings/b2/discount-preview")

	assert.Equal(t, fiber.StatusTooManyRequests, st)
	assert.Equal(t, "TOO_MANY_CODE_ATTEMPTS", code)
}

func TestPromoCodeAttempts_BudgetIsTwentyPerTenMinutes(t *testing.T) {
	// The numbers are the decision (security plan FRAUD-20): a customer tries
	// a code a handful of times; 120 an hour is hopeless against a real code.
	assert.Equal(t, 20, maxCodeAttempts)
	assert.Equal(t, "10m0s", codeAttemptWindow.String())
}
