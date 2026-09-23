//go:build !devbypass

package devbypass

import "testing"

// This file runs in a NORMAL build - the one `make build` produces and the
// one that would be deployed. It asserts the bypass is not merely disabled
// but absent.
//
// It is the more important of the two test files. AUTH-11 records a
// development API serving the launch artist over a public tunnel with an
// APP_ENV-gated bypass exploitable from the internet, found live on
// 2026-09-21. The whole point of the build tag is that no environment
// variable, no misconfiguration and no deploy mistake can reach the code -
// and that claim is worth executing rather than asserting.

func TestAllows_ProductionBuild_RefusesEverything(t *testing.T) {
	// APP_ENV is set to the ONE value that would open the gate in a dev
	// build. In this build it must change nothing.
	t.Setenv("APP_ENV", "development")

	for _, code := range []string{"0000", "326321", "", "123456", "0000 "} {
		if Allows(code) {
			t.Fatalf("a production build accepted %q — the bypass is reachable", code)
		}
	}
}

func TestEnabled_ProductionBuild_False(t *testing.T) {
	if Enabled() {
		t.Fatal("a production build reports the bypass as enabled")
	}
}
