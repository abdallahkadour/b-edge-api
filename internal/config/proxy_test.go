package config

import (
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/stretchr/testify/assert"
)

// TestLoadProxyConfig_Unset_TrustsNothing is the default that matters. Proxy
// trust must be something you switch ON when a proxy appears, never something
// to remember to switch off - an accidentally-trusted X-Forwarded-For lets any
// client mint a fresh rate-limit bucket per request.
func TestLoadProxyConfig_Unset_TrustsNothing(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "")

	got := LoadProxyConfig()

	assert.False(t, got.Enabled())
	assert.Empty(t, got.TrustedProxies)
}

func TestLoadProxyConfig_Whitespace_TrustsNothing(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "  , ,  ")

	assert.False(t, LoadProxyConfig().Enabled(), "a list of empties is not a list")
}

func TestLoadProxyConfig_ParsesAndTrims(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", " 173.245.48.0/20 , 103.21.244.0/22,2400:cb00::/32 ")

	got := LoadProxyConfig()

	assert.True(t, got.Enabled())
	assert.Equal(t, []string{"173.245.48.0/20", "103.21.244.0/22", "2400:cb00::/32"}, got.TrustedProxies)
}

// TestLoadProxyConfig_DefaultHeaderIsCloudflare - CF-Connecting-IP is a single
// address Cloudflare wrote itself, where X-Forwarded-For is a client-prependable
// list. Fewer parsing decisions, fewer ways to be wrong.
func TestLoadProxyConfig_DefaultHeaderIsCloudflare(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "173.245.48.0/20")
	t.Setenv("PROXY_HEADER", "")

	assert.Equal(t, "CF-Connecting-IP", LoadProxyConfig().Header)
}

func TestLoadProxyConfig_HeaderOverride(t *testing.T) {
	t.Setenv("TRUSTED_PROXIES", "10.0.0.0/8")
	t.Setenv("PROXY_HEADER", "X-Forwarded-For")

	assert.Equal(t, "X-Forwarded-For", LoadProxyConfig().Header)
}

// TestApply_Disabled_LeavesConfigUntouched - the zero value must not silently
// enable a trusted-proxy check with an empty list, which Fiber would treat as
// "trust the header from nobody" but is a confusing state to be in.
func TestApply_Disabled_LeavesConfigUntouched(t *testing.T) {
	cfg := fiber.Config{}

	ProxyConfig{}.Apply(&cfg)

	assert.False(t, cfg.EnableTrustedProxyCheck)
	assert.Empty(t, cfg.ProxyHeader)
	assert.Empty(t, cfg.TrustedProxies)
}

func TestApply_Enabled_SetsAllThreeFields(t *testing.T) {
	cfg := fiber.Config{}

	ProxyConfig{TrustedProxies: []string{"10.0.0.0/8"}, Header: "CF-Connecting-IP"}.Apply(&cfg)

	assert.True(t, cfg.EnableTrustedProxyCheck, "the header is worthless without the check")
	assert.Equal(t, "CF-Connecting-IP", cfg.ProxyHeader)
	assert.Equal(t, []string{"10.0.0.0/8"}, cfg.TrustedProxies)
}
