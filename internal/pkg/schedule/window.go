// Package schedule intersects a store's opening window with an artist's
// personal working hours.
//
// Pure by construction: no clock, no database, no logging, no imports beyond
// time. That is what makes the rule below exhaustively testable, and this
// particular rule needs to be, because it sits directly on the booking path.
//
// ══ THE DEFAULT THAT CARRIES THE WHOLE FEATURE ═════════════════════════════
//
// An artist who has declared no working hours is available for the ENTIRE
// store window. Absence of a rota means "no personal restriction", never
// "never available".
//
// Every artist on B-Edge today has zero rows in artist_schedules, so for all
// of them Intersect must return the store window unchanged - it must be an
// identity operation. Invert this default and five live calendars silently
// empty themselves, the booking funnel shows nothing, and the first person to
// notice is a customer who cannot book.
//
// TestIntersect_NoArtistRota_ReturnsStoreWindowUnchanged pins it, and
// scripts/capture-slot-baseline.py proves it end to end by comparing real
// slot output captured before migration 047 against the same output after.
//
// ── Scope, stated so the next person meets it deliberately ────────────────
//
// A Window is a single contiguous interval, so an artist works at most one
// window per day per store - matching the UNIQUE (artist_id, store_id,
// day_of_week) constraint in migration 047. Split shifts (09:00-13:00 and
// 17:00-21:00) need an interval SET here and a relaxed constraint there.
// That generalisation is the same one physical resources and split bookings
// will need; doing it once on purpose beats doing it three times by accident.
package schedule

import "time"

// Window is a half-open interval [Start, End). A zero Window means "no
// interval" - read the surrounding doc comments carefully, because what an
// empty ARTIST window means is deliberately different from what an empty
// STORE window means.
type Window struct {
	Start time.Time
	End   time.Time
}

// IsZero reports whether the window contains no time at all. A window whose
// end is not strictly after its start holds nothing bookable; a zero-length
// window is not a slot.
func (w Window) IsZero() bool {
	return !w.Start.Before(w.End)
}

// Intersect returns the overlap of a store's opening window and an artist's
// working window for the same day.
//
// The two empty cases mean opposite things, and conflating them is the single
// most dangerous mistake available in this package:
//
//   - An empty ARTIST window means the artist declared no rota for this day,
//     so the store window is returned UNCHANGED. Callers that know the artist
//     is genuinely off must not call this with an empty window - they must
//     not call it at all, or must pass the store window as zero.
//   - An empty STORE window means the store is shut. Nothing an artist
//     declares can open it, so the result is empty.
//
// Neither argument's timezone is inspected; comparisons use the instants
// given. Callers build both windows from the store's local date, so they are
// already in the same frame - internal/booking does this via parseStoreTimeIn.
func Intersect(store, artist Window) Window {
	if artist.IsZero() {
		// No personal restriction declared. This is the path every artist on
		// the platform takes today, and it must change nothing.
		return store
	}
	if store.IsZero() {
		// An artist's rota cannot open a closed store.
		return Window{}
	}

	start, end := store.Start, store.End
	if artist.Start.After(start) {
		start = artist.Start
	}
	if artist.End.Before(end) {
		end = artist.End
	}
	if !start.Before(end) {
		// Disjoint, or touching at a boundary. An evening-only artist at a
		// morning-only store has no bookable time, and neither does an
		// artist whose shift starts exactly when the store closes.
		return Window{}
	}
	return Window{Start: start, End: end}
}

// DayRota is an artist's declared working hours for one weekday at one store,
// as stored in artist_schedules. Times are clock times; the date they apply
// to comes from the store window passed to ResolveDay.
type DayRota struct {
	StartMinutes int  // minutes past midnight, store-local
	EndMinutes   int  // minutes past midnight, store-local
	IsWorking    bool // false: the artist does not work this weekday here
}

// DayException is a one-off personal override for a specific date, as stored
// in artist_schedule_exceptions.
type DayException struct {
	// IsUnavailable true means away for the whole day; StartMinutes and
	// EndMinutes are then meaningless and the table's CHECK forbids them.
	IsUnavailable bool
	StartMinutes  int
	EndMinutes    int
}

// ResolveDay produces the artist's bookable window for one date, given the
// store's opening window for that date and whatever the artist has declared.
//
// Precedence, most specific first:
//
//  1. A personal exception for this date wins outright. Away for the day
//     yields nothing; different hours replace the weekly rota entirely.
//  2. The weekly rota for this weekday. IsWorking false yields nothing.
//  3. Nothing declared: the full store window.
//
// rota and exc are nil when the artist has declared nothing, which is the
// case for every artist on the platform today.
func ResolveDay(store Window, rota *DayRota, exc *DayException) Window {
	if store.IsZero() {
		return Window{}
	}

	if exc != nil {
		if exc.IsUnavailable {
			return Window{}
		}
		return Intersect(store, windowOn(store.Start, exc.StartMinutes, exc.EndMinutes))
	}

	if rota != nil {
		if !rota.IsWorking {
			return Window{}
		}
		return Intersect(store, windowOn(store.Start, rota.StartMinutes, rota.EndMinutes))
	}

	return store
}

// windowOn places two clock times on the same calendar day as ref, in ref's
// location, so the result can be compared with the store window directly.
//
// Uses ref's date rather than a separate date argument specifically so the
// two windows cannot end up on different days or in different zones - the
// kind of mismatch that produces an empty intersection with no obvious cause.
func windowOn(ref time.Time, startMinutes, endMinutes int) Window {
	y, m, d := ref.Date()
	loc := ref.Location()
	midnight := time.Date(y, m, d, 0, 0, 0, 0, loc)
	return Window{
		Start: midnight.Add(time.Duration(startMinutes) * time.Minute),
		End:   midnight.Add(time.Duration(endMinutes) * time.Minute),
	}
}
