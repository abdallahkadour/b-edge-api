package product

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

func TestPreviewOrderDiscount_Route_NeedsNoLogin(t *testing.T) {
	// The bearer group at /api/v1/orders runs its auth for every route under
	// that prefix registered after it. The preview has to be reachable by a
	// guest, like placing the order it previews: an empty body must reach
	// validation (422), not a login wall (401). No database is touched -
	// validation fails first.
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	RegisterRoutes(app, nil, zap.NewNop(), middleware.NewPromoCodeAttempts())

	req := httptest.NewRequest("POST", "/api/v1/orders/discount-preview", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)

	require.NoError(t, err)
	assert.Equal(t, 422, resp.StatusCode)
}

func TestPreviewOrderDiscount_Route_SpendsTheSharedCodeBudget(t *testing.T) {
	// FRAUD-20. The budget is shared with the booking preview, so the route
	// must use the limiter it is HANDED, not one of its own: spend the
	// handed one elsewhere first, and the preview is refused at once.
	app := fiber.New(fiber.Config{ErrorHandler: apperror.ErrorHandler})
	limit := middleware.NewPromoCodeAttempts()
	app.Post("/elsewhere", limit, func(c *fiber.Ctx) error { return c.SendStatus(fiber.StatusOK) })
	RegisterRoutes(app, nil, zap.NewNop(), limit)
	for i := 0; i < 20; i++ {
		resp, err := app.Test(httptest.NewRequest("POST", "/elsewhere", nil))
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
	}

	req := httptest.NewRequest("POST", "/api/v1/orders/discount-preview", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := app.Test(req)

	require.NoError(t, err)
	assert.Equal(t, 429, resp.StatusCode)
}
