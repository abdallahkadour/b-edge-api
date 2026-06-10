// Package main is the entry point for the B-Edge API server.
// It initialises configuration, database, telemetry, and the HTTP server.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	artist "github.com/abdallahkadour/b-edge-api/internal/artist"
	"github.com/abdallahkadour/b-edge-api/internal/booking"
	"github.com/abdallahkadour/b-edge-api/internal/config"
	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/notification"
	review "github.com/abdallahkadour/b-edge-api/internal/review"
	"github.com/gofiber/fiber/v2"
	"github.com/joho/godotenv"
	"go.uber.org/zap"

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
	// Load .env in development — ignored if not present
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
	})

	// Register global middleware
	middleware.Register(app, logger)

	// Health check — unauthenticated, used by Kubernetes probes and Uptime Kuma
	app.Get("/api/v1/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":  "ok",
			"service": "b-edge-api",
			"env":     os.Getenv("APP_ENV"),
		})
	})

	app.Get("/swagger/*", fiberSwagger.HandlerDefault)

	auth.RegisterRoutes(app, pool, logger)
	booking.RegisterRoutes(app, pool, logger)
	artist.RegisterRoutes(app, pool, logger)
	review.RegisterRoutes(app, pool, logger)
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

	notifWorker := notification.NewWorker(pool, logger)
	go notifWorker.Start(ctx)

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
