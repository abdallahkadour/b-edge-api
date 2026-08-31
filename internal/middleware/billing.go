package middleware

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/jwt"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/subscription"
)

// SubscriptionSnapshot is the minimal subscription state the suspension
// guard needs - exactly the four columns subscription.Derive consumes, and
// deliberately not billing.Subscription, which this package cannot import
// (billing/handler.go imports this one for RequireAuth).
type SubscriptionSnapshot struct {
	PlanCode         string
	TrialEndsAt      *time.Time
	CurrentPeriodEnd *time.Time
	CancelledAt      *time.Time
}

// SubscriptionReader reads the one row RequireActiveSubscription needs.
//
// This is an interface rather than a *pgxpool.Pool so the guard's decision
// logic is testable without a database. A nil snapshot with a nil error
// means "this user has no subscription row" - a normal condition (the
// pre-Phase-3 signup gap), not an error.
type SubscriptionReader interface {
	SubscriptionByUserID(ctx context.Context, userID uuid.UUID) (*SubscriptionSnapshot, error)
}

// poolSubscriptionReader is the production SubscriptionReader, backed by the
// real connection pool.
type poolSubscriptionReader struct {
	db *pgxpool.Pool
}

// NewPoolSubscriptionReader returns the SubscriptionReader used in
// production, reading directly from the subscriptions table.
func NewPoolSubscriptionReader(db *pgxpool.Pool) SubscriptionReader {
	return &poolSubscriptionReader{db: db}
}

// SubscriptionByUserID returns nil, nil when the artist has no subscription
// row, translating pgx.ErrNoRows at the boundary so callers never need to
// know which driver produced it.
func (r *poolSubscriptionReader) SubscriptionByUserID(ctx context.Context, userID uuid.UUID) (*SubscriptionSnapshot, error) {
	var s SubscriptionSnapshot
	err := r.db.QueryRow(ctx, `
		SELECT sub.plan_code, sub.trial_ends_at, sub.current_period_end, sub.cancelled_at
		FROM subscriptions sub
		JOIN artists a ON a.id = sub.artist_id
		WHERE a.user_id = $1`,
		userID,
	).Scan(&s.PlanCode, &s.TrialEndsAt, &s.CurrentPeriodEnd, &s.CancelledAt)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

// RequireActiveSubscription blocks mutating requests from artists whose
// subscription is Suspended (21+ days past current_period_end with no confirmed
// payment). It parses the JWT itself rather than reading from c.Locals so it
// can safely run as path-scoped app.Use middleware before RequireAuth fires.
//
// Always passes through: GET/HEAD/OPTIONS, requests with no or invalid JWT
// (RequireAuth handles those downstream), non-artist callers (admins act on
// behalf of the platform and are never blocked by billing state), artists with
// no subscription row (pre-Phase-3 signup gap, defensive fail-open), and any
// read failure (fail-open rather than locking an artist out over a blip).
//
// Applied at three path prefixes in cmd/main.go. Booking lifecycle routes
// (/api/v1/bookings) are excluded because existing confirmed bookings are honored
// for suspended artists per B-Edge-Monetization-Implementation-Spec-v1.md
// section 6.1. Billing routes (/api/v1/billing) are excluded so a suspended
// artist can still submit a payment reference to unblock themselves.
func RequireActiveSubscription(reader SubscriptionReader) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Read-only requests are never blocked.
		m := c.Method()
		if m == fiber.MethodGet || m == fiber.MethodHead || m == fiber.MethodOptions {
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

		snap, err := reader.SubscriptionByUserID(c.UserContext(), claims.UserID)
		if err != nil {
			return c.Next() // read error — fail open rather than locking out an artist
		}
		if snap == nil {
			return c.Next() // no subscription row yet — fail open
		}

		// One shared derivation with internal/billing — see
		// internal/pkg/subscription. This previously re-implemented
		// DeriveStatus inline against a hand-copied 21-day constant, with
		// nothing enforcing that the two stayed in step.
		//
		// Only Suspended is blocked here. Every other state (comped,
		// trialing, active, grace, past_due, cancelled) passes through.
		// Note this is a NARROWER rule than the two other enforcement
		// points in this codebase: booking blocks past_due as well, and
		// discovery hides anyone past grace. Unifying those three is a
		// pending decision, not an oversight.
		status := subscription.Derive(
			snap.PlanCode, snap.TrialEndsAt, snap.CurrentPeriodEnd, snap.CancelledAt, time.Now(),
		)
		if status != subscription.StatusSuspended {
			return c.Next()
		}

		return apperror.Forbidden("SUBSCRIPTION_SUSPENDED",
			"Your account is suspended for non-payment. Submit your payment reference from your Billing screen to restore access.")
	}
}
