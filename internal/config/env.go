// env.go validates that all required environment variables are present
// before the server starts. Fail fast - never silently use empty secrets.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// requiredEnvVars lists every environment variable B-Edge needs to operate.
var requiredEnvVars = []string{
	"DB_HOST",
	"DB_PORT",
	"DB_NAME",
	"DB_USER",
	"DB_PASSWORD",
	"JWT_SECRET",
	"JWT_REFRESH_SECRET",
	"CLIENT_URL",
	// APP_ENV gates production-only behaviour (stack traces, log format).
	// Required so a missing value can never silently select a dev default.
	"APP_ENV",
	// Cloudinary credentials back the signed-upload flow. Required at boot
	// rather than checked lazily: a missing secret would mean portfolio and
	// product image uploads silently fail at the moment an artist tries to
	// use them, which is a far worse way to discover the problem.
	"CLOUDINARY_CLOUD_NAME",
	"CLOUDINARY_API_KEY",
	"CLOUDINARY_API_SECRET",
}

// jwtMinLength is the minimum acceptable length for JWT secrets.
const jwtMinLength = 32

// ValidateEnv checks all required environment variables are set and
// that JWT secrets meet minimum length requirements.
func ValidateEnv() error {
	var missing []string

	for _, key := range requiredEnvVars {
		if os.Getenv(key) == "" {
			missing = append(missing, key)
		}
	}

	if len(missing) > 0 {
		return fmt.Errorf("missing required environment variables: %s", strings.Join(missing, ", "))
	}

	if len(os.Getenv("JWT_SECRET")) < jwtMinLength {
		return fmt.Errorf("JWT_SECRET must be at least %d characters", jwtMinLength)
	}

	if len(os.Getenv("JWT_REFRESH_SECRET")) < jwtMinLength {
		return fmt.Errorf("JWT_REFRESH_SECRET must be at least %d characters", jwtMinLength)
	}

	if err := refuseDevelopmentOnAPublicHost(); err != nil {
		return err
	}

	return nil
}

// refuseDevelopmentOnAPublicHost stops the server booting in development
// mode when it is configured to be reachable from the internet.
//
// # WHY THIS EXISTS
//
// `APP_ENV=development` turns on two things that must never face the
// public: a universal customer-login bypass (any phone number authenticates
// with a fixed code - internal/customerauth) and Go stack traces in error
// responses (internal/middleware/register.go).
//
// Both gates are written correctly and both fail closed. Neither was the
// problem. On 2026-09-21 a development-mode API was served to the launch
// artist over a public Cloudflare tunnel, and the bypass was verified
// exploitable from the internet: a POST to verify-otp returned an access
// token for a number that had never requested a code. Anyone holding the
// link could have signed in as anybody.
//
// Nothing connected "publicly reachable" to "not development". The
// configuration lives in a machine-local, gitignored .env, so the next
// tunnel could reopen it exactly the same way.
//
// # WHY A REFUSAL TO BOOT RATHER THAN A WARNING
//
// A warning goes into a log that, in this setup, nobody is reading while
// the demo is happening - which is precisely how it went unnoticed. The
// only signal strong enough is the server not starting.
//
// # WHAT COUNTS AS PUBLIC
//
// Any configured URL whose host is not a loopback or private address. That
// deliberately catches a Cloudflare tunnel, a real domain and a LAN IP,
// because all three put the bypass in front of someone other than the
// developer. Localhost-only development is untouched.
func refuseDevelopmentOnAPublicHost() error {
	if os.Getenv("APP_ENV") != "development" {
		return nil
	}

	for _, key := range []string{"API_PUBLIC_URL", "CLIENT_URL", "ARTIST_DASHBOARD_URL"} {
		// CLIENT_URL is a comma-separated allow-list, so each entry is
		// checked rather than the whole string.
		for _, raw := range strings.Split(os.Getenv(key), ",") {
			if host := publicHostOf(raw); host != "" {
				return fmt.Errorf(
					"refusing to start: APP_ENV=development exposes the customer-login "+
						"bypass and stack traces, but %s points at the public host %q. "+
						"Set APP_ENV=production, or point %s at localhost", key, host, key)
			}
		}
	}
	return nil
}

// publicHostOf returns the host of raw when it is reachable from outside
// this machine, and "" otherwise.
//
// Deliberately conservative: anything it cannot parse is treated as NOT
// public, so a malformed value fails open rather than blocking a legitimate
// local boot. The check exists to catch the obvious, dangerous case - a real
// hostname - not to be a URL validator.
func publicHostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	host := u.Hostname()

	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return ""
	}
	if ip := net.ParseIP(host); ip != nil {
		// Loopback and private ranges are not "the internet". A LAN address
		// is arguable, but a developer on their own machine is the common
		// case and blocking it would make this rule something people
		// disable rather than obey.
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() {
			return ""
		}
	}
	return host
}
