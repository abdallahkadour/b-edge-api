// logger.go provides a structured Zap request logger middleware.
package middleware

import (
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// NewLogger returns a Fiber middleware that logs each request using Zap.
func NewLogger(rootLogger *zap.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		// 1. Let the request process completely down the pipeline first
		err := c.Next()

		// 2. Scan the text string of the URL path to determine the domain module
		module := "generic"
		path := c.Path()

		if strings.HasPrefix(path, "/api/v1/auth") {
			module = "auth"
		} else if strings.HasPrefix(path, "/api/v1/artists") {
			module = "artist"
		} else if strings.HasPrefix(path, "/api/v1/bookings") {
			module = "booking"
		} else if strings.HasPrefix(path, "/api/v1/reviews") {
			module = "review"
		}

		// 3. Assemble standard metadata properties (forcing "module" to be explicitly first)
		fields := []zap.Field{
			zap.String("module", module),
			zap.String("method", c.Method()),
			zap.String("path", path),
			zap.Int("status", c.Response().StatusCode()),
			zap.Duration("latency", time.Since(start)),
			zap.String("ip", c.IP()),
			zap.String("request_id", c.GetRespHeader("X-Request-Id")),
		}

		// 4. Output logs safely based on status codes
		if err != nil {
			fields = append(fields, zap.Error(err))
			rootLogger.Error("Request error", fields...)
		} else if c.Response().StatusCode() >= 500 {
			rootLogger.Error("Server error", fields...)
		} else if c.Response().StatusCode() >= 400 {
			rootLogger.Warn("Client error", fields...)
		} else {
			rootLogger.Info("Request", fields...)
		}

		return err
	}
}
