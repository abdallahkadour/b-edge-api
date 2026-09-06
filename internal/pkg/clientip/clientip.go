// Package clientip answers "who sent this request" with an address that is
// never empty.
//
// # WHY A HELPER RATHER THAN c.IP()
//
// Fiber's c.IP() does the right thing in both ordinary cases: it returns the
// socket peer when no proxy is trusted, and the trusted header's value when
// one is. It returns the EMPTY STRING in a third case that is easy to miss -
// proxy trust is enabled, the connection came from a trusted proxy, and the
// configured header is absent.
//
// That happens more often than it sounds:
//
//   - a load-balancer or uptime health check hitting the origin from inside a
//     trusted range,
//   - anything reaching the origin directly while TRUSTED_PROXIES lists a
//     broad CIDR,
//   - a misconfigured edge that forwards without setting the header.
//
// An empty client IP is not a cosmetic problem. It is the same defect
// config/proxy.go exists to fix, one layer down:
//
//   - the per-IP rate limiter keys on it, so every header-less request shares
//     ONE bucket,
//   - the audit log records "" for an admin who confirmed money,
//   - request logs lose the only field that identifies an abusive client.
//
// Verified against the running stack on 2026-09-06: with TRUSTED_PROXIES set
// and no CF-Connecting-IP header, c.IP() logged an empty string.
//
// # A LEAF PACKAGE
//
// It lives here rather than in middleware because the audit call sites
// (admin, billing, domain/auth handlers) need it too, and a helper that every
// domain imports must not drag a domain's worth of dependencies with it. Same
// reasoning as internal/pkg/subscription and internal/pkg/bidi.
package clientip

import "github.com/gofiber/fiber/v2"

// From returns the client's IP, falling back to the socket peer.
//
// The fallback is deliberately the socket peer rather than a placeholder like
// "unknown": when the header is missing, the peer address is the most truthful
// answer available, and a real address keeps the rate limiter and the audit
// log meaningful instead of merely non-empty.
func From(c *fiber.Ctx) string {
	if ip := c.IP(); ip != "" {
		return ip
	}
	// RemoteIP reads the connection itself and is never affected by
	// ProxyHeader, so it cannot return the empty string the way c.IP() can.
	return c.Context().RemoteIP().String()
}
