// proxy.go configures who the API is willing to believe about a client's IP.
//
// # WHY THIS EXISTS
//
// Fiber's c.IP() returns the socket peer. That is correct when the API is
// exposed directly and WRONG the moment anything sits in front of it - a CDN,
// a load balancer, an ingress - because the socket peer is then the proxy.
//
// Three things in this codebase read c.IP(), and all three break silently:
//
//   - the per-IP rate limiter (middleware/register.go) keys on it, so every
//     user on earth would share ONE bucket of 600 requests per 5 minutes.
//     The API would rate-limit itself into an outage on day one.
//   - the audit log records it for admin approve/reject/confirm/void, so
//     every entry would name the proxy rather than the admin who acted.
//   - the request logger records it, making every log line useless for
//     tracing an abusive client.
//
// # WHY NOT JUST TRUST X-FORWARDED-FOR
//
// Because a header is client-supplied. If the origin is reachable directly -
// and until there is a WAF, it is - anyone can send
// `X-Forwarded-For: 1.2.3.4` and get a fresh rate-limit bucket per request,
// or write whatever they like into the audit log. Trusting the header
// unconditionally converts a broken control into a bypassable one, which is
// worse.
//
// So the header is believed ONLY when the connection actually came from a
// proxy we listed. Fiber enforces that with EnableTrustedProxyCheck.
//
// # THE DEFAULT IS TO TRUST NOTHING
//
// TRUSTED_PROXIES is optional and empty by default, which means c.IP() keeps
// returning the socket peer - correct for local development and for a
// directly-exposed origin. Proxy trust is something you switch ON when you
// put a proxy in front, not something to remember to switch off.
package config

import (
	"os"
	"strings"

	"github.com/gofiber/fiber/v2"
)

// defaultProxyHeader is what Cloudflare sets, and it is preferred over
// X-Forwarded-For for a reason: CF-Connecting-IP is always a single address
// that Cloudflare itself wrote, whereas X-Forwarded-For is a comma-separated
// list a client can prepend to. Fewer parsing decisions, fewer ways to be
// wrong.
//
// Override with PROXY_HEADER if the edge is not Cloudflare - AWS ALB and most
// nginx setups use X-Forwarded-For.
const defaultProxyHeader = "CF-Connecting-IP"

// ProxyConfig is the trust decision, resolved from the environment.
type ProxyConfig struct {
	// TrustedProxies is the CIDR list whose requests may carry a client-IP
	// header. Empty means trust nobody.
	TrustedProxies []string
	// Header is the header to read the client IP from, when the connection
	// came from a trusted proxy.
	Header string
}

// Enabled reports whether any proxy is trusted.
func (p ProxyConfig) Enabled() bool { return len(p.TrustedProxies) > 0 }

// LoadProxyConfig reads TRUSTED_PROXIES and PROXY_HEADER.
//
// TRUSTED_PROXIES is a comma-separated list of CIDRs or bare IPs, e.g. the
// contents of https://www.cloudflare.com/ips-v4 and ips-v6. It is a variable
// rather than a hardcoded Cloudflare list on purpose: those ranges change,
// and a redeploy is a worse way to pick up the change than an env var. It
// also keeps the API portable to a different edge.
func LoadProxyConfig() ProxyConfig {
	raw := strings.TrimSpace(os.Getenv("TRUSTED_PROXIES"))
	if raw == "" {
		return ProxyConfig{}
	}

	var cidrs []string
	for part := range strings.SplitSeq(raw, ",") {
		if p := strings.TrimSpace(part); p != "" {
			cidrs = append(cidrs, p)
		}
	}
	if len(cidrs) == 0 {
		return ProxyConfig{}
	}

	header := strings.TrimSpace(os.Getenv("PROXY_HEADER"))
	if header == "" {
		header = defaultProxyHeader
	}

	return ProxyConfig{TrustedProxies: cidrs, Header: header}
}

// Apply writes the proxy decision into a Fiber config.
//
// Deliberately mutates rather than returning a config: fiber.Config is built
// in one place in main.go with several unrelated settings, and splitting its
// construction across files would make the whole thing harder to read for the
// sake of two fields.
func (p ProxyConfig) Apply(cfg *fiber.Config) {
	if !p.Enabled() {
		return
	}
	cfg.EnableTrustedProxyCheck = true
	cfg.TrustedProxies = p.TrustedProxies
	cfg.ProxyHeader = p.Header
}
