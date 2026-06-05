// Package middleware provides Fiber middleware for the B-Edge API.
package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/jwt"
)

// RequireAuth validates the Bearer token and injects auth context into Fiber Locals.
// Downstream handlers read user_id, salon_id, and role via c.Locals().
func RequireAuth() fiber.Handler {
	return func(c *fiber.Ctx) error {
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return apperror.Unauthorized("TOKEN_MISSING", "Authentication required")
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return apperror.Unauthorized("TOKEN_INVALID", "Authentication failed")
		}

		claims, err := jwt.VerifyAccessToken(parts[1])
		if err != nil {
			return apperror.Unauthorized("TOKEN_INVALID", "Authentication failed")
		}

		c.Locals("user_id", claims.UserID)
		c.Locals("salon_id", claims.SalonID)
		c.Locals("role", claims.Role)

		return c.Next()
	}
}

// RequireRole checks the authenticated user has one of the allowed roles.
// Must be chained after RequireAuth.
func RequireRole(roles ...string) fiber.Handler {
	return func(c *fiber.Ctx) error {
		role, ok := c.Locals("role").(string)
		if !ok {
			return apperror.Forbidden("FORBIDDEN", "You do not have permission to perform this action")
		}

		for _, r := range roles {
			if role == r {
				return c.Next()
			}
		}

		return apperror.Forbidden("FORBIDDEN", "You do not have permission to perform this action")
	}
}

// UserIDFromContext extracts the user UUID from Fiber Locals.
// Panics if RequireAuth was not applied — use only on authenticated routes.
func UserIDFromContext(c *fiber.Ctx) uuid.UUID {
	return c.Locals("user_id").(uuid.UUID)
}

// SalonIDFromContext extracts the salon UUID pointer from Fiber Locals.
// Returns nil for users not associated with a salon.
func SalonIDFromContext(c *fiber.Ctx) *uuid.UUID {
	v := c.Locals("salon_id")
	if v == nil {
		return nil
	}
	id, ok := v.(*uuid.UUID)
	if !ok {
		return nil
	}
	return id
}

// RoleFromContext extracts the user role string from Fiber Locals.
func RoleFromContext(c *fiber.Ctx) string {
	role, _ := c.Locals("role").(string)
	return role
}
