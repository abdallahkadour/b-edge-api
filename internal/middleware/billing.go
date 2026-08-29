package middleware

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/jwt"
)

// suspendedAfterDays mirrors billing.pastDueDays (21) — the total window after
// current_period_end before a subscription becomes Suspended. Kept local to avoid
// a circular import: billing/handler.go imports this package for RequireAuth,
// so importing billing here closes the loop. If billing.pastDueDays changes,
// update this constant to match.
const suspendedAfterDays = 21

// RequireActiveSubscription blocks mutating requests from artists whose
// subscription is Suspended (21+ days past current_period_end with no confirmed
// payment). It parses the JWT itself rather than reading from c.Locals so it
// can safely run as path-scoped app.Use middleware before RequireAuth fires.
//
// Always passes through: GET/HEAD/OPTIONS, requests with no or invalid JWT
// (RequireAuth handles those downstream), non-artist callers (admins act on
// behalf of the platform and are never blocked by billing state), artists with
// no subscription row (pre-Phase-3 signup gap, defensive fail-open).
//
// Applied at three path prefixes in cmd/main.go. Booking lifecycle routes
// (/api/v1/bookings) are excluded because existing confirmed bookings are honored
// for suspended artists per B-Edge-Monetization-Implementation-Spec-v1.md
// section 6.1. Billing routes (/api/v1/billing) are excluded so a suspended
// artist can still submit a payment reference to unblock themselves.
func RequireActiveSubscription(db *pgxpool.Pool) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Read-only requests are never blocked.
		m := c.Method()
		if m == "GET" || m == "HEAD" || m == "OPTIONS" {
			return c.Next()
		}

		// Parse the JWT directly — app.Use middleware fires before route-specific
		// handlers in Fiber's pipeline, so c.Locals("user_id") is not yet set by
		// RequireAuth. RequireAuth still runs downstream and rejects invalid tokens.
		authHeader := c.Get("Authorization")
		if authHeader == "" {
			return c.Next()
		}
		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || parts[0] != "Bearer" {
			return c.Next()
		}
		claims, err := jwt.VerifyAccessToken(parts[1])
		if err != nil {
			return c.Next()
		}
		// Only artists can be suspended. Admins always pass.
		if claims.Role != "artist" {
			return c.Next()
		}

		var planCode string
		var trialEndsAt, currentPeriodEnd, cancelledAt *time.Time
		err = db.QueryRow(context.Background(), `
			SELECT sub.plan_code, sub.trial_ends_at, sub.current_period_end, sub.cancelled_at
			FROM subscriptions sub
			JOIN artists a ON a.id = sub.artist_id
			WHERE a.user_id = $1`,
			claims.UserID,
		).Scan(&planCode, &trialEndsAt, &currentPeriodEnd, &cancelledAt)

		if errors.Is(err, pgx.ErrNoRows) {
			return c.Next() // no subscription row yet — fail open
		}
		if err != nil {
			return c.Next() // DB error — fail open rather than locking out an artist
		}

		// Mirrors billing.DeriveStatus's Suspended branch exactly.
		// Every non-suspended state (comped, trialing, active, grace, past_due,
		// cancelled) passes through; only Suspended is blocked.
		now := time.Now()
		if cancelledAt != nil || planCode == "comped" {
			return c.Next()
		}
		if trialEndsAt != nil && now.Before(*trialEndsAt) {
			return c.Next()
		}
		if currentPeriodEnd == nil || now.Before(currentPeriodEnd.AddDate(0, 0, suspendedAfterDays)) {
			return c.Next() // active, grace, or past_due — not yet suspended
		}

		return apperror.Forbidden("SUBSCRIPTION_SUSPENDED",
			"Your account is suspended for non-payment. Submit your payment reference from your Billing screen to restore access.")
	}
}
