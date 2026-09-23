//go:build !devbypass

// Package devbypass — the PRODUCTION variant.
//
// This file is what a normal build compiles. There is no bypass code here,
// no constant to leak, and no environment variable that can turn one on:
// Allows returns false, always, and the compiler removes the call.
//
// The enabled variant lives in devbypass_enabled.go behind `-tags devbypass`.
// Read that file's header for why this is a build tag rather than a runtime
// check — the short version is AUTH-11, which records a development API with
// an APP_ENV-gated bypass being served to the launch artist over a public
// tunnel and exploitable from the internet.
package devbypass

// Allows always reports false in a production build.
//
// Not "false unless configured" — false. The code that could return true is
// not in this binary.
func Allows(_ string) bool { return false }

// Enabled reports whether this build contains the bypass at all.
func Enabled() bool { return false }
