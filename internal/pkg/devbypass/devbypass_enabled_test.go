//go:build devbypass

package devbypass

import "testing"

// This file runs only under `-tags devbypass`, the build `make dev` and
// .air.toml produce.
//
// APP_ENV is the inner lock. The build tag stops the code shipping; APP_ENV
// stops a tagged binary being useful if someone deploys one anyway. Both must
// agree, and neither is trusted alone.

func TestAllows_DevelopmentAndCorrectCode_True(t *testing.T) {
	t.Setenv("APP_ENV", "development")

	if !Allows(Code) {
		t.Fatalf("a dev build with APP_ENV=development must accept %q", Code)
	}
}

func TestAllows_WrongCode_False(t *testing.T) {
	t.Setenv("APP_ENV", "development")

	// POSITIVE CONTROL's inverse: the gate must be about the CODE, not merely
	// about the environment. A check that accepted anything in development
	// would pass the test above while making every OTP optional.
	for _, code := range []string{"0001", "326321", "", "00000", "000"} {
		if Allows(code) {
			t.Fatalf("accepted %q, which is not the bypass code", code)
		}
	}
}

func TestAllows_NonDevelopmentEnv_FailsClosed(t *testing.T) {
	// Every way APP_ENV can be wrong. Fail-closed means an unset, empty or
	// misspelled value keeps this SHUT - the inverse would mean a
	// misconfigured deploy silently ships a universal login bypass.
	for _, env := range []string{"production", "prod", "Development", "DEVELOPMENT", "dev", "staging", ""} {
		t.Setenv("APP_ENV", env)
		if Allows(Code) {
			t.Fatalf("APP_ENV=%q accepted the bypass code — it must fail closed", env)
		}
	}
}

func TestEnabled_DevBuild_True(t *testing.T) {
	if !Enabled() {
		t.Fatal("a devbypass build must report the bypass as enabled")
	}
}
