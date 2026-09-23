//go:build devbypass

// Package devbypass holds the fixed OTP code that lets local development
// work without a live Twilio account.
//
// ─────────────────────────────────────────────────────────────────────────
//  THIS FILE IS COMPILED ONLY WHEN THE `devbypass` BUILD TAG IS SET.
//  `make build` does not set it. `make dev` and .air.toml do.
// ─────────────────────────────────────────────────────────────────────────
//
// WHY A BUILD TAG AND NOT JUST APP_ENV
//
// This bypass previously lived as a const in internal/customerauth, gated on
// APP_ENV=="development" alone. On 2026-09-21 that was found to be
// insufficient in the worst possible way, and the security plan records it:
//
//     AUTH-11 — "Found live 2026-09-21: a development API was serving the
//     launch artist over a public tunnel and the OTP bypass was exploitable
//     from the internet."
//
// A runtime string is a single point of failure that depends on every deploy
// being configured correctly forever. Both AUTH-08 and AUTH-11 recommend the
// same remedy, twice, in their own words: "Prefer removing the bypass from
// production builds entirely (build tag), rather than relying on a runtime
// string." This is that.
//
// TWO LOCKS, NOT ONE
//
// The build tag is the outer lock: without it, Allows is the stub in
// devbypass_disabled.go and returns false unconditionally - the code below is
// not in the binary at all, so no configuration mistake can reach it.
//
// APP_ENV is kept as the inner lock anyway, because someone can build WITH
// the tag and deploy that binary. Both must agree. Neither is trusted alone.
//
// HOW TO REMOVE IT WHEN TWILIO WORKS
//
//  1. delete this directory
//  2. remove `-tags devbypass` from .air.toml and the Makefile's dev target
//  3. delete the two call sites (grep devbypass.Allows)
//
// The build breaks at each step until all three are done, which is the point:
// a half-removed bypass is worse than either state.
package devbypass

import (
	"os"
	"sync"
)

// Code is the fixed OTP accepted in development.
//
// SIX zeros, not four. The request struct validates
// `required,len=6,numeric` and a real OTP is six digits, so "0000" is
// rejected by validation before it ever reaches this check - measured, not
// assumed. Relaxing that rule to admit a shorter code would weaken OTP
// validation in PRODUCTION builds too, because the struct tag is not behind
// the build tag. Six zeros costs nothing and leaves the validator exactly as
// strict as it was.
//
// All-zeros by deliberate choice either way. A bypass that looks like a real
// code invites someone to assume it IS one; "000000" cannot be mistaken for
// a generated value in a log, a screenshot or a support conversation. It was
// 326321 before, which looked exactly like a genuine OTP.
const Code = "000000"

var warnOnce sync.Once

// Allows reports whether this code should skip real OTP verification.
//
// Fails CLOSED on anything unexpected: an unset, empty or misspelled APP_ENV
// keeps this shut. The inverse - defaulting open - would mean a misconfigured
// deploy silently ships a universal login bypass, which is the exact incident
// AUTH-11 records.
func Allows(code string) bool {
	if code != Code {
		return false
	}
	if os.Getenv("APP_ENV") != "development" {
		return false
	}
	warnOnce.Do(func() {
		// Once per process, on stderr, unconditionally. A bypass that runs
		// silently is one nobody remembers is there - and this one has
		// already been live on a public tunnel once.
		os.Stderr.WriteString(
			"\n" +
				"  ╔══════════════════════════════════════════════════════════╗\n" +
				"  ║  DEV OTP BYPASS IS ACTIVE — code " + Code + " accepted        ║\n" +
				"  ║  Built with -tags devbypass and APP_ENV=development.      ║\n" +
				"  ║  This binary MUST NOT be deployed or exposed publicly.    ║\n" +
				"  ╚══════════════════════════════════════════════════════════╝\n\n")
	})
	return true
}

// Enabled reports whether this build contains the bypass at all.
//
// Used by the startup banner so the fact is visible before anyone tries to
// use it, rather than only at first use.
func Enabled() bool { return true }
