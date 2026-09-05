// secheaders.go sets the response headers that tell a browser how much to
// trust what B-Edge sends it.
//
// # WHY THIS EXISTS
//
// B-Edge-Security-Test-Plan-v1.md, test CLIENT-04, was written knowing it
// would fail: "No security-header middleware exists anywhere - verified by
// grep, not inferred. Fixing this before the pass would make the results
// more useful." This is that fix, so the pen-test pass measures a defence
// rather than re-confirming its absence.
//
// These headers cost nothing at runtime and are the cheapest defence in the
// codebase. They are not a substitute for the edge layer §0.1 of that
// document says does not exist yet - a CDN or WAF still has to arrive before
// launch - but they close the classes a browser can enforce on its own.
//
// # WHY THE POLICY IS SO RESTRICTIVE
//
// Almost everything B-Edge serves is JSON, and JSON needs no scripts, no
// styles, no images, no frames and no form posts. `default-src 'none'` is
// therefore not an aggressive setting here; it is an accurate description of
// what an API response legitimately needs, and anything that later trips it
// is by definition something that should not be happening in a JSON body.
//
// Two endpoints are the exception, and both set their own policy over this
// one because middleware runs before the handler and the handler's Set wins:
//
//   - internal/share/handler.go serves an HTML share preview containing an
//     inline redirect script.
//   - internal/calendar/handler.go serves an HTML landing page containing an
//     inline stylesheet.
//
// Keeping the strict policy global and narrowing it at those two sites means
// a future HTML endpoint fails closed - it renders without inline script
// until someone deliberately widens it - rather than inheriting a permissive
// default nobody remembers granting.
package middleware

import (
	"github.com/gofiber/fiber/v2"
)

// APIContentSecurityPolicy is what a JSON response is allowed to pull in:
// nothing at all.
//
// frame-ancestors is the modern half of clickjacking defence and the reason
// X-Frame-Options is set alongside rather than instead of it - the older
// header is what pre-CSP browsers still understand.
//
// base-uri and form-action are included because both are ignored by
// default-src: without them, an injected <base> tag or a form pointed at an
// attacker's host would still be honoured on any HTML this API ever serves.
const APIContentSecurityPolicy = "default-src 'none'; " +
	"frame-ancestors 'none'; " +
	"base-uri 'none'; " +
	"form-action 'none'"

// permissionsPolicy switches off browser capabilities the API has no use
// for. Denying them here does not constrain the PWAs, which are served from
// a different origin and carry their own policy - this only ensures that
// anything rendered *from an API response* cannot reach for a camera or a
// location fix.
const permissionsPolicy = "accelerometer=(), camera=(), geolocation=(), " +
	"gyroscope=(), magnetometer=(), microphone=(), payment=(), usb=()"

// hstsMaxAge is one year in seconds, the value browsers require before they
// will treat a host as HTTPS-only with confidence.
//
// includeSubDomains is deliberate: api. and app. are both intended to be
// HTTPS, and the subdomain most worth protecting is the one nobody has
// created yet. `preload` is deliberately NOT set - that submits the domain
// to a browser-baked list which is slow and awkward to reverse, and it is
// not a commitment to make from a middleware default.
const hstsMaxAge = "max-age=31536000; includeSubDomains"

// SecurityHeaders sets the browser-enforced protections on every response.
//
// Registered early so the headers are present on responses that never reach
// a handler at all - a shed 503 from the concurrency limiter, a 429 from the
// rate limiter, a recovered panic. An error response is exactly when a
// browser is most likely to be looking at something unexpected.
func SecurityHeaders() fiber.Handler {
	return func(c *fiber.Ctx) error {
		// Never let a browser second-guess a declared Content-Type. This is
		// what stops a JSON body containing attacker-chosen text from being
		// sniffed and executed as HTML or JavaScript.
		c.Set("X-Content-Type-Options", "nosniff")

		// Clickjacking: B-Edge is never legitimately framed by anyone.
		c.Set("X-Frame-Options", "DENY")

		// Send the origin cross-site, the full URL same-origin, and nothing
		// at all when downgrading to HTTP. Keeps any path segment that might
		// carry an identifier out of third-party logs.
		c.Set("Referrer-Policy", "strict-origin-when-cross-origin")

		c.Set("Content-Security-Policy", APIContentSecurityPolicy)
		c.Set("Permissions-Policy", permissionsPolicy)

		// Isolate any HTML this API serves from a window that opened it, so
		// an opener cannot reach into it via window.opener.
		c.Set("Cross-Origin-Opener-Policy", "same-origin")

		// HSTS only over TLS. Sending it on a plaintext response is at best
		// ignored, and at worst - on a shared development host - pins a
		// name to HTTPS that has no certificate, which is a self-inflicted
		// outage that survives in the browser cache for a year.
		//
		// Behind a load balancer the connection to this process is plain
		// HTTP, so X-Forwarded-Proto is the honest signal and is checked
		// first. It is trustworthy only because it arrives from our own
		// proxy; if B-Edge is ever exposed directly, this check must go.
		if c.Get("X-Forwarded-Proto") == "https" || c.Protocol() == "https" {
			c.Set("Strict-Transport-Security", hstsMaxAge)
		}

		return c.Next()
	}
}
