// Package middleware provides Fiber middleware for the B-Edge API.
package middleware

import (
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/jwt"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
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

		// A token minted before salon_role existed decodes with the zero
		// value, which is salonrole.None and holds no capabilities. Coerce
		// anything unrecognised to the same, so a malformed claim cannot
		// index the matrix as a role nobody defined.
		sr := claims.SalonRole
		if !salonrole.Valid(sr) {
			sr = salonrole.None
		}
		c.Locals("salon_role", sr)

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
// Panics if RequireAuth was not applied - use only on authenticated routes.
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

// SalonRoleFromContext extracts the caller's standing within their salon.
//
// Returns salonrole.None when RequireAuth has not run or the token predates
// the claim - both of which must behave as "holds nothing" rather than as an
// error, so that a missing role can never read as a permitted one.
func SalonRoleFromContext(c *fiber.Ctx) salonrole.Role {
	r, ok := c.Locals("salon_role").(salonrole.Role)
	if !ok {
		return salonrole.None
	}
	return r
}

// RequireSalonCapability refuses a request whose salon role does not hold cap.
// Must be chained after RequireAuth.
//
// ── Why 403 and not 404 ────────────────────────────────────────────────────
//
// This project's convention is that ownership failures return 404 so that
// object IDs cannot be enumerated - security test AUTH-02 found seven domains
// leaking existence through 403. That convention does not apply here, because
// the refusal describes the CALLER rather than an object. A salon member
// already knows their salon's services exist; they are standing in the salon
// looking at them. There is nothing to enumerate.
//
// So SALON_ROLE_FORBIDDEN joins the deliberate 403 list alongside NO_SALON,
// ACCOUNT_SUSPENDED, NOT_AN_ARTIST and SUBSCRIPTION_SUSPENDED.
//
// Reads are deliberately not guarded. A member can see the salon's service
// menu, stores and hours - they have to, in order to work. What this stops is
// writing them.
func RequireSalonCapability(cap salonrole.Capability) fiber.Handler {
	return func(c *fiber.Ctx) error {
		if _, ok := c.Locals("user_id").(uuid.UUID); !ok {
			// RequireSalonCapability was registered without RequireAuth in
			// front of it. Fail closed and loudly rather than reading a role
			// that was never set.
			return apperror.Unauthorized("TOKEN_MISSING", "Authentication required")
		}

		if SalonIDFromContext(c) == nil {
			return apperror.Forbidden("NO_SALON", "You are not associated with a salon")
		}

		if !salonrole.Can(SalonRoleFromContext(c), cap) {
			return apperror.Forbidden("SALON_ROLE_FORBIDDEN",
				"Only the salon owner can do this")
		}

		return c.Next()
	}
}
