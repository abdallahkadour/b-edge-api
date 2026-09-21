package subscription

import "fmt"

// visibility.go: the one definition of "is this artist sellable right now?"
// as a SQL fragment.
//
// ── Why this file exists ─────────────────────────────────────────────────
//
// It existed as THREE hand-written copies, in internal/discovery,
// internal/artist and internal/share. On 2026-09-20 one of them was
// corrected - `sub.cancelled_at IS NOT NULL` had been listed as a VISIBLE
// condition, so a cancelled artist stayed on Discover. The other two were
// not touched, and were found still wrong on 2026-09-21 by security test
// FRAUD-10: a cancelled artist was hidden from Discover and refused
// bookings, while their share link still rendered a card and their handle
// still resolved.
//
// internal/share's own comment stated the requirement it was violating:
// "a link preview must not render for an artist whose profile is itself
// hidden from Discover."
//
// All three were then made to agree by hand, each carrying a note that the
// rule lives in three places and moves together. That is a convention, and
// this project's history is unambiguous about what happens to conventions
// under a deadline. This file replaces the note with a single definition,
// and no_duplicate_test.go replaces the convention with a build failure.
//
// ── Why it lives in this leaf package ────────────────────────────────────
//
// Same reason Derive and Enforce do: three domains need the same answer and
// none of them may own it. GraceDays, which the fragment interpolates, is
// already defined here, so the window and the query that applies it now
// move together by construction rather than by two people remembering.
//
// ── Why it is a fragment rather than a function over Status ──────────────
//
// Derive answers the question in Go, one artist at a time. Discovery has to
// answer it for a whole result set inside a single query, and pulling every
// artist's subscription into the process to filter in Go would be a
// different and much worse thing. Until slot generation and discovery share
// one read model, the honest position is that the rule has a Go form and a
// SQL form. Keeping both in this file is what stops them diverging -
// TestVisibleArtistCond_AgreesWithEnforce pins them together.

// VisibleArtistCond returns a SQL boolean expression that is true when the
// artist aliased by artistAlias may be shown to customers.
//
// artistAlias is the table alias the caller gave the artists table - every
// current caller uses "a". Passing it explicitly rather than hardcoding it
// is deliberate: a hardcoded "a.id" silently matches nothing if a future
// query aliases differently, and "matches nothing" in a visibility filter
// means an empty Discover page with no error.
//
// Intended for a WHERE clause:
//
//	"... WHERE a.status = 'active' AND " + subscription.VisibleArtistCond("a")
func VisibleArtistCond(artistAlias string) string {
	return fmt.Sprintf(`EXISTS (
	SELECT 1 FROM subscriptions sub
	WHERE sub.artist_id = %[1]s.id
	AND sub.cancelled_at IS NULL
	AND (
		sub.plan_code = '%[2]s'
		OR (sub.trial_ends_at IS NOT NULL AND NOW() < sub.trial_ends_at)
		OR (sub.current_period_end IS NOT NULL AND NOW() < sub.current_period_end + INTERVAL '%[3]d days')
	)
)`, artistAlias, CompedPlanCode, GraceDays)
}
