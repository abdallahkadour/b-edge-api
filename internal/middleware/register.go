// register.go attaches all global middleware to the Fiber app in the correct order.
package middleware

import (
	"os"
	"sync/atomic"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/limiter"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"github.com/gofiber/fiber/v2/middleware/requestid"
	"go.uber.org/zap"
)

// maxRequestsPerWindow is the rate limit ceiling per IP address.
//
// Was 100 per 15 minutes - a ceiling a single legitimate customer or artist
// can hit through completely ordinary use, not abuse. Every dashboard page
// view fires several API calls on its own (list + counts + related lookups),
// and this limiter is per-IP, not per-user: a salon's own WiFi, a shared
// office network, or a customer behind carrier-grade NAT can pool many real
// people onto one IP. A 2026-08-19 QA pass tripped this twice through
// nothing but sequential page navigation during manual testing, each time
// locking out all further use for the rest of the window with zero
// explanation to whoever hit it (see the frontend rateLimitInterceptor for
// the other half of this fix). Raised well above realistic legitimate
// traffic while still meaningfully bounding a single IP hammering the API;
// endpoint-specific abuse (login, OTP) already has its own tighter limiter
// where it matters (see internal/customerauth's otpRateLimitMax) - this one
// only needs to be a blunt backstop, not the primary defense.
const maxRequestsPerWindow = 600

// rateLimitWindow is the sliding window duration for the rate limiter.
const rateLimitWindow = 5 * time.Minute

// maxInFlightRequests caps how many requests the process handles at once,
// across all clients combined - independent of the per-IP rate limiter
// above, which only throttles a single client hammering the API. This is
// what actually stands between a genuine traffic spike (many different
// people, many different IPs, all arriving within the same few seconds -
// e.g. a promotional post reaching thousands of followers) and the failure
// mode that spike causes without a limiter: requests pile up waiting for a
// free database connection (see maxDBConns in config/database.go), each one
// holding an open TCP connection and a live goroutine while it waits, until
// the process runs out of memory and gets killed outright.
//
// Deliberately not set equal to maxDBConns - many requests (health checks,
// static routes, some public GETs) never touch the database, so this needs
// real headroom above the DB pool size, not parity with it. 300 is a
// first-pass, deliberately conservative ceiling: comfortably above any
// concurrency this stage of B-Edge should see in normal operation, and
// comfortably below where in-flight goroutines become a real memory risk on
// the planned t3.medium. Not load-tested - revisit once real traffic
// numbers or a load test exist.
const maxInFlightRequests = 300

// inFlightRequests is the live count of requests currently being handled.
// Package-level and atomic because Fiber serves requests concurrently
// across goroutines - this has to be safe without a mutex on every request.
var inFlightRequests atomic.Int64

// concurrencyLimiter rejects requests past maxInFlightRequests with 503
// instead of accepting them and letting them queue behind the database
// pool. A fast, honest "busy" response beats a slow death by memory
// exhaustion under a real spike.
func concurrencyLimiter() fiber.Handler {
	return func(c *fiber.Ctx) error {
		current := inFlightRequests.Add(1)
		defer inFlightRequests.Add(-1)

		if current > maxInFlightRequests {
			c.Set(fiber.HeaderRetryAfter, "5")
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"data": nil,
				"error": fiber.Map{
					"code":    "SERVER_BUSY",
					"message": "B-Edge is experiencing high demand right now. Please try again in a few seconds.",
				},
				"meta": nil,
			})
		}

		return c.Next()
	}
}

// Register attaches global middleware in the correct order:
// recover → requestid → logger → cors → rate limiter → concurrency limiter.
// Auth middleware is applied per-route, not globally.
func Register(app *fiber.App, logger *zap.Logger) {
	// 1. Recover from panics - must be first so it wraps everything
	// Fails CLOSED, deliberately. The inverse (`!= "production"`) leaks
	// stack traces to clients whenever APP_ENV is unset, empty, or merely
	// misspelled ("Production", "prod") - and APP_ENV is now required at
	// startup, but this must stay safe even if that changes. Only an
	// explicit "development" turns traces on.
	app.Use(recover.New(recover.Config{
		EnableStackTrace: os.Getenv("APP_ENV") == "development",
	}))

	// 2. Assign X-Request-ID to every request
	app.Use(requestid.New())

	// 3. Structured request logging via Zap
	app.Use(NewLogger(logger))

	// 4. CORS - allow only the configured client origin
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

	// 5. Rate limiter - 100 requests per 15 minutes per IP
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

	// 6. Concurrency limiter - protects the process itself from a genuine
	// traffic spike, independent of the per-IP rate limiter above. Placed
	// last, after CORS (4), so a shed 503 still carries CORS headers -
	// without them the browser reports a CORS failure instead of letting
	// the frontend read the real "busy" response.
	app.Use(concurrencyLimiter())
}
