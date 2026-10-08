package middleware

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

// Standing is what the database says about a signed-in caller NOW, as
// opposed to what their access token said when it was issued.
//
// # Why RequireAuth asks
//
// An access token lives 15 minutes and carries the caller's salon and salon
// role. Until 2026-10-08 RequireAuth believed both for that whole time, and
// two security cases measured what that cost:
//
//   - AUTH-14b: a member removed from a salon kept reading its orders (with
//     customers' phones and delivery pins), its service menu and its team,
//     and marked orders shipped and delivered. The routes that refused her
//     were the ones whose service happened to re-read membership; the five
//     that did not, trusted the token.
//   - AUTH-22: a suspended account was refused at login but not at refresh,
//     so a session that existed before the suspension renewed itself
//     indefinitely.
//
// Fixing each route would leave the next route to be written with the same
// hole, so the check lives here, once: every authenticated request re-reads
// the account, and the salon and salon role handlers see come from the
// database. One indexed lookup per request. The token still proves WHO is
// calling; it no longer decides what that person currently is.
//
// A FROZEN account is deliberately let through (decision D27). Freezing is
// self-service, and the screen that offers it says "you can undo this right
// here" - unfreezing is a signed-in call, so refusing the session would make
// a self-freeze irreversible except through support. A frozen account cannot
// start a NEW session (login refuses it); the one it has, it keeps.
type Standing struct {
	Suspended bool
	SalonID   *uuid.UUID
	SalonRole salonrole.Role
}

// ErrAccountGone means the account no longer exists - deleted, or never did.
var ErrAccountGone = errors.New("account no longer exists")

// StandingFunc resolves a caller's Standing. Any error other than
// ErrAccountGone fails the request closed.
type StandingFunc func(ctx context.Context, userID uuid.UUID) (*Standing, error)

// currentStanding is installed once at startup, before the server accepts a
// request, and only read afterwards. A package variable rather than a
// RequireAuth parameter because RequireAuth() is called from dozens of
// route registrations that have no database handle; domain/auth, which owns
// the account and already derives the salon role at login, installs it.
var currentStanding StandingFunc

// UseCurrentStanding installs the per-request account check. nil removes it,
// which leaves RequireAuth trusting the token - what handler unit tests use.
func UseCurrentStanding(f StandingFunc) { currentStanding = f }

// StandingInstalled reports whether the check is on. domain/auth's test
// asserts its RegisterRoutes turns it on, so it cannot silently ship off.
func StandingInstalled() bool { return currentStanding != nil }
