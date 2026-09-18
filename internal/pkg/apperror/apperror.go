// Package apperror defines B-Edge application error types and the global
// Fiber error handler that converts them to standard JSON responses.
package apperror

import (
	"net/http"

	"github.com/gofiber/fiber/v2"
	"go.uber.org/zap"
)

// AppError is a structured application error containing an HTTP status code,
// a machine-readable code, a human-readable message, and optional field-level details.
type AppError struct {
	HTTPStatus int          `json:"-"`
	Code       string       `json:"code"`
	Message    string       `json:"message"`
	Details    []FieldError `json:"details,omitempty"`
}

// FieldError represents a validation failure on a specific request field.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Error implements the error interface so AppError can be returned as error.
func (e *AppError) Error() string {
	return e.Message
}

// BadRequest creates a 400 Bad Request AppError.
func BadRequest(code, message string) *AppError {
	return &AppError{HTTPStatus: http.StatusBadRequest, Code: code, Message: message}
}

// Unauthorized creates a 401 Unauthorized AppError.
func Unauthorized(code, message string) *AppError {
	return &AppError{HTTPStatus: http.StatusUnauthorized, Code: code, Message: message}
}

// Forbidden creates a 403 Forbidden AppError.
func Forbidden(code, message string) *AppError {
	return &AppError{HTTPStatus: http.StatusForbidden, Code: code, Message: message}
}

// NotFound creates a 404 Not Found AppError.
func NotFound(code, message string) *AppError {
	return &AppError{HTTPStatus: http.StatusNotFound, Code: code, Message: message}
}

// Conflict creates a 409 Conflict AppError.
func Conflict(code, message string) *AppError {
	return &AppError{HTTPStatus: http.StatusConflict, Code: code, Message: message}
}

// TooManyRequests creates a 429 Too Many Requests AppError.
//
// This exists because its absence had a visible consequence. The OTP request
// limiter is the one rate limit expressed as a domain error rather than by the
// middleware, and with no 429 helper here it reached for BadRequest - so a
// throttled client got a 400 carrying code RATE_LIMITED.
//
// Two things broke quietly as a result:
//
//   - The shared front-end interceptor keys on `err.status === 429` and never
//     reads the code, so the rate-limit banner both apps ship never appeared
//     for OTP throttling. The user saw a generic failure instead.
//   - The handler below logs at 404 and above, so a 400 was never recorded.
//     Someone hammering the OTP endpoint left no trace in the logs at all.
//
// Caught by executing the documented security plan on 2026-09-18 (SPAM-02).
func TooManyRequests(code, message string) *AppError {
	return &AppError{HTTPStatus: http.StatusTooManyRequests, Code: code, Message: message}
}

// UnprocessableEntity creates a 422 Unprocessable Entity AppError with field details.
func UnprocessableEntity(code string, details []FieldError) *AppError {
	return &AppError{
		HTTPStatus: http.StatusUnprocessableEntity,
		Code:       code,
		Message:    "Please check the highlighted fields",
		Details:    details,
	}
}

// Internal creates a 500 Internal Server Error AppError.
// Never expose raw error details in production.
func Internal(code, message string) *AppError {
	return &AppError{HTTPStatus: http.StatusInternalServerError, Code: code, Message: message}
}

// check if the err is instanceOf AppError
//
//	if appErr, ok := err.(*AppError); ok {
//	 ^^^^^^^^^^^^^^^^^^^^^^^^^^^   ^^
//	 assignment                    condition
//
// ErrorHandler is a global Fiber error handler that converts AppError values
// and native Fiber errors into the standard B-Edge JSON response envelope.
// ErrorHandler is a global Fiber error handler that converts AppError values
// and native Fiber errors into the standard B-Edge JSON response envelope.
func ErrorHandler(c *fiber.Ctx, err error) error {
	// 1. Safely extract your pre-tagged logger from context locals
	log, ok := c.Locals("logger").(*zap.Logger)
	if !ok {
		log, _ = zap.NewDevelopment() // Fallback developer default if context isn't ready
	}

	// Handle typed AppError
	if appErr, ok := err.(*AppError); ok {
		// Operational rule: Log everything 404 (Not Found/Data Drift) or higher.
		// This automatically captures your missing artist profile bug while ignoring input typos!
		if appErr.HTTPStatus >= http.StatusNotFound {
			log.Warn("Operational domain exception intercepted",
				zap.Int("status", appErr.HTTPStatus),
				zap.String("code", appErr.Code),
				zap.String("path", c.Path()),
				zap.String("method", c.Method()),
				zap.String("request_id", c.Get("X-Request-ID")), // Pairs perfectly with your tracing ids!
				zap.Error(err),                                  // Captures the full upstream error wrapping stack trace
			)
		}

		return c.Status(appErr.HTTPStatus).JSON(fiber.Map{
			"data":  nil,
			"error": appErr,
			"meta":  nil,
		})
	}

	// Handle native Fiber errors (e.g. 404 from router, 405 method not allowed)
	if fiberErr, ok := err.(*fiber.Error); ok {
		log.Info("Native routing exception intercepted",
			zap.Int("status", fiberErr.Code),
			zap.String("path", c.Path()),
			zap.String("method", c.Method()),
		)

		return c.Status(fiberErr.Code).JSON(fiber.Map{
			"data": nil,
			"error": fiber.Map{
				"code":    "NOT_FOUND",
				"message": fiberErr.Message,
			},
			"meta": nil,
		})
	}

	// Unknown error - return 500, never expose internal details to client
	// CRITICAL: We log this at .Error level because this means a server panic or lost database link!
	log.Error("Critical system error or panic caught",
		zap.String("path", c.Path()),
		zap.String("method", c.Method()),
		zap.Error(err),
	)

	return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
		"data": nil,
		"error": fiber.Map{
			"code":    "INTERNAL_ERROR",
			"message": "Something went wrong on our end. Please try again.",
		},
		"meta": nil,
	})
}

// func ErrorHandler(c *fiber.Ctx, err error) error {
// 	// Handle typed AppError
// 	if appErr, ok := err.(*AppError); ok {
// 		return c.Status(appErr.HTTPStatus).JSON(fiber.Map{
// 			"data":  nil,
// 			"error": appErr,
// 			"meta":  nil,
// 		})
// 	}

// 	// Handle native Fiber errors (e.g. 404 from router, 405 method not allowed)
// 	if fiberErr, ok := err.(*fiber.Error); ok {
// 		return c.Status(fiberErr.Code).JSON(fiber.Map{
// 			"data": nil,
// 			"error": fiber.Map{
// 				"code":    "NOT_FOUND",
// 				"message": fiberErr.Message,
// 			},
// 			"meta": nil,
// 		})
// 	}

// 	// Unknown error - return 500, never expose internal details
// 	return c.Status(http.StatusInternalServerError).JSON(fiber.Map{
// 		"data": nil,
// 		"error": fiber.Map{
// 			"code":    "INTERNAL_ERROR",
// 			"message": "Something went wrong on our end. Please try again.",
// 		},
// 		"meta": nil,
// 	})
