// Package httpcache says what a CDN may do with a response.
//
// # WHY THIS EXISTS
//
// Before 2026-09-06 this API sent no Cache-Control header on any endpoint.
// That is not "no caching" - it is UNDEFINED caching, and it has two costs.
//
// A CDN in front of an origin that says nothing about freshness will
// conservatively cache nothing, so the whole point of putting one there is
// lost: Discover and public artist profiles - the two most-requested surfaces
// in the product, both entirely public - would still be served by the Go
// binary for every visitor.
//
// The mirror risk is worse. An intermediate that guesses the OTHER way could
// cache an authenticated response and serve one customer's bookings to
// another. Silence invites both.
//
// # FAIL CLOSED
//
// SecurityHeaders sets `no-store` on every response as a default, and the
// small number of genuinely public reads opt back in by calling Public().
// The opposite arrangement - cache by default, opt out for private data - has
// exactly one failure mode and it is a data breach.
//
// # WHY max-age=0 WITH s-maxage
//
// s-maxage applies to shared caches (the CDN), max-age to the browser.
// Setting max-age=0 means a returning visitor always revalidates, while the
// CDN still absorbs the load. The freshness cost of a stale browser cache is
// paid by the one user who has it; the freshness cost of a stale CDN entry is
// paid by everyone, so the CDN window is chosen deliberately per surface
// below rather than globally.
//
// stale-while-revalidate lets the edge serve a slightly old copy while it
// fetches a new one, which keeps p99 flat during a cache miss storm.
//
// # THE FRESHNESS TRADE, NAMED
//
// discovery.StoreCard.open_status is derived per request from the clock. A
// 60-second CDN window means a store can show "Open now" for up to a minute
// after it closes.
//
// That is accepted, and 60s is chosen rather than 300s because of it. The
// badge is advisory - booking still goes through slot generation, which is
// never cached and which will refuse a closed store. A customer who taps a
// stale "Open" badge does not get a bad booking; they get accurate
// availability on the next screen.
package httpcache

import (
	"strconv"
	"time"

	"github.com/gofiber/fiber/v2"
)

// Windows chosen per surface, not globally. Each is the answer to "how wrong
// may this be for everyone at once".
const (
	// Discovery listing and artist profiles carry open_status, which moves
	// with the clock. See the freshness note above.
	Discovery = 60 * time.Second

	// Services, stores and portfolio media change only when an artist edits
	// them, which is rare and never time-sensitive.
	Catalogue = 5 * time.Minute

	// Reviews are append-only from the customer's side; a new one appearing
	// two minutes late costs nothing.
	Reviews = 2 * time.Minute

	// Share previews are read by crawlers - WhatsApp, Facebook, Twitter -
	// which fetch far more often than they need to.
	SharePreview = 10 * time.Minute
)

// staleWindow is how long an edge may serve a stale copy while revalidating.
// Deliberately generous: a stale-but-served response is better than a
// thundering herd on the origin, for data that is public and advisory.
const staleWindow = 5 * time.Minute

// Public marks a response as cacheable by a shared cache for ttl.
//
// Only for responses that contain NOTHING user-specific. If a response varies
// by who asked - even to hide a field - it is not Public, because a shared
// cache has no way to know that.
func Public(c *fiber.Ctx, ttl time.Duration) {
	c.Set(fiber.HeaderCacheControl,
		"public, max-age=0"+
			", s-maxage="+strconv.Itoa(int(ttl.Seconds()))+
			", stale-while-revalidate="+strconv.Itoa(int(staleWindow.Seconds())))
}

// NoStore forbids storing the response anywhere.
//
// The default for every response; call it explicitly only to re-assert the
// default on a route that would otherwise look cacheable to a reader.
func NoStore(c *fiber.Ctx) {
	c.Set(fiber.HeaderCacheControl, "no-store")
}
