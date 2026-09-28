package booking

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

func TestPreviewDiscount_Route_SpendsTheSharedCodeBudget(t *testing.T) {
	// FRAUD-20. Shared with the cart's preview: spend the handed limiter
	// elsewhere, and this preview is refused at once. A malformed booking id
	// is answered (404) before any database call, so no database is needed
	// to see that the limiter runs first.
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	limit := middleware.NewPromoCodeAttempts()
	app.Post("/elsewhere", limit, func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	RegisterRoutes(app, nil, zap.NewNop(), limit)

	preview := func() int {
		req := httptest.NewRequest("POST", "/api/v1/bookings/not-a-booking/discount-preview", strings.NewReader(`{"code":"X"}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := app.Test(req)
		require.NoError(t, err)
		return resp.StatusCode
	}
	require.Equal(t, 404, preview(), "positive control: reachable, and answered before the database")
	for i := 0; i < 19; i++ {
		resp, err := app.Test(httptest.NewRequest("POST", "/elsewhere", nil))
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
	}

	assert.Equal(t, 429, preview())
}
