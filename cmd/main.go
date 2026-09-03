// Package main is the entry point for the B-Edge API server.
// It initialises configuration, database, telemetry, and the HTTP server.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	// Embeds the IANA timezone database directly into the binary. Without
	// this, time.LoadLocation("Asia/Beirut") silently fails on minimal
	// Docker base images that lack /usr/share/zoneinfo - falling back to
	// UTC and reintroducing the exact "today"/"this month" boundary bugs
	// this was meant to fix, with no error surfaced anywhere.
	_ "time/tzdata"

	artist "github.com/abdallahkadour/b-edge-api/internal/artist"
	"github.com/abdallahkadour/b-edge-api/internal/billing"
	"github.com/abdallahkadour/b-edge-api/internal/booking"
	"github.com/abdallahkadour/b-edge-api/internal/calendar"
	"github.com/abdallahkadour/b-edge-api/internal/client"
	"github.com/abdallahkadour/b-edge-api/internal/config"
	"github.com/abdallahkadour/b-edge-api/internal/customerauth"
	"github.com/abdallahkadour/b-edge-api/internal/discovery"
	"github.com/abdallahkadour/b-edge-api/internal/inbox"
	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/notification"
	product "github.com/abdallahkadour/b-edge-api/internal/product"
	review "github.com/abdallahkadour/b-edge-api/internal/review"
	"github.com/abdallahkadour/b-edge-api/internal/share"
	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/admin"
	"github.com/abdallahkadour/b-edge-api/internal/media"
	"github.com/abdallahkadour/b-edge-api/internal/onboarding"

	"github.com/abdallahkadour/b-edge-api/internal/earnings"

	_ "github.com/abdallahkadour/b-edge-api/docs"
	"github.com/abdallahkadour/b-edge-api/internal/domain/auth"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	fiberSwagger "github.com/gofiber/swagger"
)

// @title        B-Edge API
// @version      1.0
// @description  Beauty booking platform API for Lebanon and MENA.
// @host         localhost:3000
// @BasePath     /api/v1
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
func main() {
	// Load .env in development - ignored if not present
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, reading from environment")
	}

	// Initialise logger
	logger, err := config.NewLogger()
	if err != nil {
		log.Fatalf("Failed to initialise logger: %v", err)
	}
	defer logger.Sync() //nolint:errcheck

	// Validate required environment variables
	if err := config.ValidateEnv(); err != nil {
		logger.Fatal("Missing required environment variables", zap.Error(err))
	}

	// Initialise database pool
	pool, err := config.NewDatabase(logger)
	if err != nil {
		logger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer pool.Close()

	// Initialise OpenTelemetry tracing
	shutdownTracing, err := config.NewTelemetry(logger)
	if err != nil {
		logger.Fatal("Failed to initialise telemetry", zap.Error(err))
	}
	defer shutdownTracing() //nolint:errcheck

	// Create Fiber app
	app := fiber.New(fiber.Config{
		AppName:      "B-Edge API",
		ErrorHandler: apperror.ErrorHandler,
		// Fiber defaults to 4MB, which would reject an image upload
		// before it even reaches the handler - media.MaxUploadBytes is
		// 15MB, plus multipart overhead (boundaries, headers, the other
		// form fields) needs some headroom above that. Every other
		// request body in this API is small JSON, so raising this
		// globally rather than per-route costs nothing elsewhere.
		BodyLimit: 20 * 1024 * 1024,
	})

	// app.Use(func(c *fiber.Ctx) error {
	// 	c.Locals("logger", logger)
	// 	return c.Next()
	// })

	// app.Use(cors.New(cors.Config{
	// 	AllowOrigins:     "http://localhost:4200",
	// 	AllowMethods:     "GET,POST,PATCH,DELETE,OPTIONS",
	// 	AllowHeaders:     "Origin, Content-Type, Accept, Authorization",
	// 	AllowCredentials: true,
	// }))

	// Register global middleware
	middleware.Register(app, logger)

	// Subscription enforcement: block mutating requests from Suspended artists
	// (21+ days past period_end with no confirmed payment) on routes that affect
	// new business activity. Booking lifecycle (/api/v1/bookings) is excluded
	// because existing confirmed bookings are honored for suspended artists per
	// the monetization spec section 6.1. Billing (/api/v1/billing) is excluded
	// so a suspended artist can still submit a payment reference to unblock.
	// Discovery enforcement (hiding past_due/suspended from Discover) is handled
	// separately in discovery/repository.go via subscriptionVisibleCond.
	suspendedGuard := middleware.RequireActiveSubscription(middleware.NewPoolSubscriptionReader(pool))
	app.Use("/api/v1/artists", suspendedGuard)
	app.Use("/api/v1/products", suspendedGuard)
	app.Use("/api/v1/media", suspendedGuard)

	// Health check - unauthenticated, used by Kubernetes probes and Uptime Kuma
	app.Get("/api/v1/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":  "ok",
			"service": "b-edge-api",
			"env":     os.Getenv("APP_ENV"),
		})
	})

	app.Get("/swagger/*", fiberSwagger.HandlerDefault)

	auth.RegisterRoutes(app, pool, logger)
	customerauth.RegisterRoutes(app, pool, logger)
	booking.RegisterRoutes(app, pool, logger)
	artist.RegisterRoutes(app, pool, logger)
	review.RegisterRoutes(app, pool, logger)
	client.RegisterRoutes(app, pool, logger)
	discovery.RegisterRoutes(app, pool, logger)
	// Crawlable link previews at /a/:handle - deliberately outside
	// /api/v1, since this serves HTML to social crawlers rather than JSON
	// to a client. See project-docs/B-Edge-Share-Previews-Decision-v1.md.
	share.RegisterRoutes(app, pool, logger)
	// In-app notification centre. Distinct from internal/notification,
	// which is the outbound WhatsApp queue - see internal/inbox's package
	// comment for why the two are separate.
	inbox.RegisterRoutes(app, pool, logger)
	// "Add to calendar" links at /c/:token, also outside /api/v1 - these
	// are opened by a human from a WhatsApp message and serve HTML and an
	// .ics, not JSON. Twilio cannot attach text/calendar on WhatsApp, so a
	// fetched link is the only route to a calendar entry at all.
	calendar.RegisterRoutes(app, pool, logger)
	earnings.RegisterRoutes(app, pool, logger)
	product.RegisterRoutes(app, pool, logger)
	media.RegisterRoutes(app, pool, logger)
	onboarding.RegisterRoutes(app, pool, logger)
	admin.RegisterRoutes(app, pool, logger)
	billing.RegisterRoutes(app, pool, logger)
	// Start server in background goroutine
	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	go func() {
		logger.Info("B-Edge API starting", zap.String("port", port), zap.String("env", os.Getenv("APP_ENV")))
		if err := app.Listen(":" + port); err != nil {
			logger.Fatal("Server failed to start", zap.Error(err))
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())

	// The worker runs in its own goroutine, so it MUST recover its own
	// panics. Go terminates the entire process on an unrecovered panic in
	// any goroutine - Fiber's recover middleware only wraps HTTP request
	// goroutines and cannot reach this one. Without this, a single
	// malformed notification row would take the whole API down.
	//
	// Supervised with a restart rather than a bare recover: a worker that
	// panicked once and silently stopped would leave notifications queuing
	// forever with nothing to indicate why.
	superviseWorker(ctx, "notification", notification.NewWorker(pool, logger), logger)
	// Unstalls waitlist queues whose notified entry never confirmed. The
	// only case the lazy cascade cannot reach, because its trigger is
	// another slot opening - which is precisely what is not happening. See
	// internal/booking/waitlist_worker.go.
	superviseWorker(ctx, "waitlist", booking.NewWaitlistWorker(pool, logger), logger)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("Shutting down server gracefully...")
	cancel()
	if err := app.Shutdown(); err != nil {
		logger.Error("Error during server shutdown", zap.Error(err))
	}
	logger.Info("Server stopped")
}

// workerRestartDelay is how long to wait before restarting the notification
// worker after a panic - long enough that a consistently panicking worker
// doesn't spin the CPU, short enough that recovery is prompt.
const workerRestartDelay = 5 * time.Second

// backgroundWorker is anything that runs until its context is cancelled.
//
// An interface rather than *notification.Worker so the supervision below is
// shared: the notification worker is no longer the only long-running loop
// this process will own (waitlist auto-fill is the next), and each one
// needs the same panic-recover-and-restart guarantee. Duplicating that per
// worker is how one of them eventually ends up without it.
type backgroundWorker interface {
	Start(ctx context.Context)
}

// superviseWorker runs w in its own goroutine, restarting it after a panic
// until ctx is cancelled.
//
// The worker runs in its own goroutine, so it MUST recover its own panics.
// Go terminates the entire process on an unrecovered panic in any
// goroutine - Fiber's recover middleware only wraps HTTP request goroutines
// and cannot reach this one. Without this, a single malformed row would
// take the whole API down.
//
// Supervised with a restart rather than a bare recover: a worker that
// panicked once and silently stopped would leave its queue growing forever
// with nothing to indicate why.
func superviseWorker(ctx context.Context, name string, w backgroundWorker, logger *zap.Logger) {
	go func() {
		for {
			if ctx.Err() != nil {
				return // shutting down - don't restart
			}
			runWorkerOnce(ctx, name, w, logger)
			select {
			case <-ctx.Done():
				return
			case <-time.After(workerRestartDelay):
			}
		}
	}()
}

// runWorkerOnce runs a worker until it returns or panics, converting a
// panic into a logged error instead of a process exit. Returns normally in
// both cases so the supervising loop can decide what to do.
func runWorkerOnce(ctx context.Context, name string, w backgroundWorker, logger *zap.Logger) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("background worker panicked - will restart",
				zap.String("worker", name),
				zap.Any("panic", r),
				zap.ByteString("stack", debug.Stack()),
			)
		}
	}()
	w.Start(ctx)
}
