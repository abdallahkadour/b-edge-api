// logger.go provides a structured Zap request logger middleware.
package middleware

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// NewLogger returns a Fiber middleware that logs each request using Zap.
// Logs: method, path, status, latency, IP, and request ID.
func NewLogger(logger *zap.Logger) fiber.Handler {
	return func(c *fiber.Ctx) error {
		start := time.Now()

		// Process request
		err := c.Next()

		// Log after response is sent
		fields := []zap.Field{
			zap.String("method", c.Method()),
			zap.String("path", c.Path()),
			zap.Int("status", c.Response().StatusCode()),
			zap.Duration("latency", time.Since(start)),
			zap.String("ip", c.IP()),
			zap.String("request_id", c.GetRespHeader("X-Request-Id")),
		}

		if err != nil {
			fields = append(fields, zap.Error(err))
			logger.Error("Request error", fields...)
		} else if c.Response().StatusCode() >= 500 {
			logger.Error("Server error", fields...)
		} else if c.Response().StatusCode() >= 400 {
			logger.Warn("Client error", fields...)
		} else {
			logger.Info("Request", fields...)
		}

		return err
	}
}
