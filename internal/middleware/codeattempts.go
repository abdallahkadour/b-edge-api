package middleware

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/limiter"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/clientip"
)

// maxCodeAttempts and codeAttemptWindow bound how many promo codes one
// address may try (security plan FRAUD-20).
//
// Both previews - POST /orders/discount-preview and
// POST /bookings/:id/discount-preview - are public, because a guest checks a
// code before she has an account, and both must answer valid:true for a real
// code. So the only defence against guessing codes is how many guesses an
// address gets. Under the general limiter alone that was measured on
// 2026-09-28 at 482 answers in 0.3 s - about 5,800 an hour - and a real code
// slipped in among them was picked out.
//
// 20 per 10 minutes is a handful of honest retries - a typo, a code from a
// different salon, trying it on the cart and then the booking - and 120 an
// hour is hopeless against any code worth handing out. Deliberately NOT done
// by lowering maxRequestsPerWindow: that limit has to stay loose enough for
// a salon's shared WiFi, and code guessing is one route's problem.
const (
	maxCodeAttempts   = 20
	codeAttemptWindow = 10 * time.Minute
)

// NewPromoCodeAttempts returns the code-attempt limiter.
//
// Create ONE and mount that same handler on both preview routes: the budget
// lives in the instance, so sharing it is what stops a guesser getting a
// second allowance by alternating between the cart and the booking preview.
// cmd/main.go does this. Separate instances would be separate budgets, which
// is what tests want and production must not have.
//
// Per address, keyed exactly as the general limiter is (clientip.From), so a
// forged header buys nothing here either (FRAUD-21).
func NewPromoCodeAttempts() fiber.Handler {
	return limiter.New(limiter.Config{
		Max:          maxCodeAttempts,
		Expiration:   codeAttemptWindow,
		KeyGenerator: clientip.From,
		// Its own code, not RATE_LIMIT_EXCEEDED: the app shows this message
		// under the code field, and keeps its "too many requests" banner for
		// the general limiter.
		LimitReached: func(c *fiber.Ctx) error {
			return apperror.TooManyRequests("TOO_MANY_CODE_ATTEMPTS",
				"Too many promo codes tried. Please wait a few minutes and try again.")
		},
	})
}
