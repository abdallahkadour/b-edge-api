// register.go attaches all global middleware to the Fiber app in the correct order.
package middleware

import (
	"os"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"go.uber.org/zap"
)

// maxRequestsPerWindow is the rate limit ceiling per IP address.
const maxRequestsPerWindow = 100

// rateLimitWindow is the sliding window duration for the rate limiter.
const rateLimitWindow = 15 * time.Minute

// Register attaches global middleware in the correct order:
// recover → requestid → logger → cors → rate limiter.
// Auth middleware is applied per-route, not globally.
func Register(app *fiber.App, logger *zap.Logger) {
	// 1. Recover from panics — must be first so it wraps everything
	app.Use(recover.New(recover.Config{
		EnableStackTrace: os.Getenv("APP_ENV") != "production",
	}))

	// 2. Assign X-Request-ID to every request
	app.Use(requestid.New())

	// 3. Structured request logging via Zap
	app.Use(NewLogger(logger))

	// 4. CORS — allow only the configured client origin
	clientURL := os.Getenv("CLIENT_URL")
	if clientURL == "" {
		clientURL = "http://localhost:4200"
	}
	app.Use(cors.New(cors.Config{
		AllowOrigins:     clientURL,
		AllowHeaders:     "Origin, Content-Type, Accept, Authorization, X-Request-ID",
		AllowMethods:     "GET,POST,PUT,PATCH,DELETE,OPTIONS",
		AllowCredentials: true,
	}))

	// 5. Rate limiter — 100 requests per 15 minutes per IP
	app.Use(limiter.New(limiter.Config{
		Max:        maxRequestsPerWindow,
		Expiration: rateLimitWindow,
		LimitReached: func(c *fiber.Ctx) error {
			return c.Status(fiber.StatusTooManyRequests).JSON(fiber.Map{
				"data": nil,
				"error": fiber.Map{
					"code":    "RATE_LIMIT_EXCEEDED",
					"message": "Too many requests. Please try again in a few minutes.",
				},
				"meta": nil,
			})
		},
	}))
}
