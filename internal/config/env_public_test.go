package config

import (
	"strings"
	"testing"
)

// These pin the rule that stops a development-mode server facing the
// internet. The scenario is not hypothetical: on 2026-09-21 a
// development-mode API was reachable over a public tunnel and the
// customer-login bypass was verified exploitable from outside.

func TestPublicHostOf_TreatsLocalAddressesAsPrivate(t *testing.T) {
	for _, raw := range []string{
		"http://localhost:4200",
		"http://localhost",
		"http://127.0.0.1:3000",
		"http://[::1]:3000",
		"http://192.168.1.50:4200", // LAN
		"http://10.0.0.8",          // private
		"http://172.20.10.2:8080",  // the iPhone hotspot range
		"",
		"not a url at all",
	} {
		if got := publicHostOf(raw); got != "" {
			t.Errorf("publicHostOf(%q) = %q, want \"\" (local or unparseable)", raw, got)
		}
	}
}

func TestPublicHostOf_DetectsRealHosts(t *testing.T) {
	cases := map[string]string{
		// The exact shape that was live.
		"https://perry-turbo-covering-palace.trycloudflare.com": "perry-turbo-covering-palace.trycloudflare.com",
		"https://app.b-edge.com":                                "app.b-edge.com",
		"http://203.0.113.10:3000":                              "203.0.113.10",
	}
	for raw, want := range cases {
		if got := publicHostOf(raw); got != want {
			t.Errorf("publicHostOf(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestRefuseDevelopmentOnAPublicHost_BlocksTheConfigurationThatLeaked(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("CLIENT_URL", "https://perry-turbo-covering-palace.trycloudflare.com")

	err := refuseDevelopmentOnAPublicHost()

	if err == nil {
		t.Fatal("development mode on a public tunnel was allowed to boot - " +
			"this is the exact configuration that exposed the login bypass")
	}
	if !strings.Contains(err.Error(), "trycloudflare.com") {
		t.Errorf("the error should name the offending host, got: %v", err)
	}
}

// CLIENT_URL is a comma-separated allow-list. A public entry hiding behind
// a local one must still be caught.
func TestRefuseDevelopmentOnAPublicHost_ChecksEveryEntryInTheList(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("CLIENT_URL", "http://localhost:4200,https://app.b-edge.com")

	if err := refuseDevelopmentOnAPublicHost(); err == nil {
		t.Fatal("a public host later in the allow-list was not detected")
	}
}

// Ordinary local development must be completely unaffected, or this rule
// becomes something people work around instead of obey.
func TestRefuseDevelopmentOnAPublicHost_AllowsLocalhostDevelopment(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("CLIENT_URL", "http://localhost:4200,http://localhost:4300")
	t.Setenv("API_PUBLIC_URL", "")
	t.Setenv("ARTIST_DASHBOARD_URL", "http://localhost:4300")

	if err := refuseDevelopmentOnAPublicHost(); err != nil {
		t.Fatalf("localhost development was blocked: %v", err)
	}
}

// Production on a public host is the normal deployment and must boot.
func TestRefuseDevelopmentOnAPublicHost_AllowsProductionAnywhere(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("CLIENT_URL", "https://app.b-edge.com")

	if err := refuseDevelopmentOnAPublicHost(); err != nil {
		t.Fatalf("production on a public host was blocked: %v", err)
	}
}
