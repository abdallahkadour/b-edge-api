// requestcontext.go gives every request a context that can actually end.
//
// # THE PROBLEM
//
// Handlers passed `c.Context()` to the service layer at 111 call sites. That
// returns `*fasthttp.RequestCtx`, which is not a request-scoped
// `context.Context` in the way the call sites assume:
//
//   - fasthttp POOLS AND RESETS it once the handler returns. Anything holding
//     it past that point is reading a struct that now belongs to a different
//     request.
//   - it carries NO DEADLINE. A query with no ceiling holds a pgx connection
//     for as long as PostgreSQL takes, and the pool is only 20 connections
//     against an in-flight ceiling of 300.
//
// The obvious fix - swap to `c.UserContext()` - is WORSE on its own, and that
// is worth writing down because it looks right. Fiber's implementation:
//
//	func (c *Ctx) UserContext() context.Context {
//	    ctx, ok := c.fasthttp.UserValue(userContextKey).(context.Context)
//	    if !ok {
//	        ctx = context.Background()   // <- no cancellation, no deadline
//	        c.SetUserContext(ctx)
//	    }
//	    return ctx
//	}
//
// Unset, it hands back `context.Background()`. Every call site would compile,
// every test would pass, and the one property being fixed - the ability to
// give up - would be gone entirely.
//
// # THE FIX
//
// This middleware SETS the user context, so `c.UserContext()` returns
// something real: derived from the request, bounded by a deadline, and
// cancelled when the handler chain unwinds.
//
// # WHAT EACH HALF BUYS
//
// The DEADLINE is the reliable half and the reason this exists. It bounds
// every request, so a pathological query cannot hold a pool connection
// indefinitely.
//
// Deriving from the request context is the best-effort half: fasthttp's
// disconnect signalling is not something to rely on, so this is not sold as
// "cancels the moment the user closes the tab". It is strictly better than
// `context.Background()` and strictly safer than passing the pooled
// RequestCtx down.
//
// Belt and braces with `statement_timeout` on the pool (see
// config/database.go): this bounds the request, that bounds the query, and
// neither depends on the other being remembered.
package middleware

import (
	"context"
	"time"

	"github.com/gofiber/fiber/v2"
)

// DefaultRequestTimeout bounds a single request.
//
// Fifteen seconds, not five. The tight bound belongs on the DATABASE, where
// statement_timeout protects the scarce resource; this one only needs to stop
// a request living for ever. Set too low it would break the legitimate slow
// path - a 15 MB portfolio upload being validated, re-encoded and forwarded to
// Cloudinary - and turn a working feature into a timeout to chase.
const DefaultRequestTimeout = 15 * time.Second

// RequestContext installs a deadline-bounded context on every request.
//
// Registered early, so it is set before any handler runs. `defer cancel()`
// fires as the chain unwinds, which releases the timer and marks the context
// done - correct, because nothing may legitimately use it after that point.
func RequestContext(timeout time.Duration) fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Derived from c.Context() - the fasthttp RequestCtx - deliberately.
		// Deriving from c.UserContext() here would derive from
		// context.Background(), since nothing has set one yet, and this
		// middleware would hand every request a context with no connection
		// lineage at all. Read synchronously, never retained.
		ctx, cancel := context.WithTimeout(c.Context(), timeout)
		defer cancel()

		c.SetUserContext(ctx)
		return c.Next()
	}
}
