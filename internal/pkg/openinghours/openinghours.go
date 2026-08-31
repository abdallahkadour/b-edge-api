// Package openinghours resolves whether a store is open on a given date,
// and between which instants.
//
// It exists as a leaf package - importing nothing else in this codebase -
// because the same question has two very different callers. internal/booking
// asks it to decide where slot generation may start and stop; internal/discovery
// asks it to put an "Open now" badge on a public profile. Before this package
// existed the logic lived inline inside GetAvailableSlots' step 1 and was
// reachable only by generating a full slot list, so the second caller would
// have had to either duplicate it or pull the whole booking domain into the
// discovery read path.
//
// Everything here works on primitives and small value types rather than on
// booking.Store / booking.BusinessHours, so no domain owns it and it never
// needs to know the shape of a database row. Those domain types (which are
// themselves duplicated between internal/booking and internal/artist) map
// onto these in one line at each call site.
//
// The wall-clock contract, unchanged from migration 010's header: business
// hours are stored as PostgreSQL TIME values with no date and no zone, and
// mean local time at the store. They are resolved against the store's IANA
// zone for the specific date being asked about, so a salon that opens at
// 09:00 opens at 09:00 where it physically stands, in summer and winter
// alike. Pre-converting to UTC would freeze one half of the year and break
// at the next clock change.
package openinghours

import (
	"fmt"
	"time"
)

// timeLayout is the PostgreSQL TIME wire format these strings arrive in.
const timeLayout = "15:04:05"

// DayHours is a store's regular hours for one day of the week.
type DayHours struct {
	// IsOpen false means the store does not trade that weekday at all,
	// regardless of the times below.
	IsOpen    bool
	OpenTime  string // "HH:MM:SS", local wall-clock
	CloseTime string // "HH:MM:SS", local wall-clock
}

// Exception is a one-off override for a specific calendar date - a holiday
// closure, or different hours for one day.
type Exception struct {
	// IsClosed true closes the store for the whole date and takes
	// precedence over everything else.
	IsClosed bool
	// OpenTime and CloseTime override DayHours when BOTH are present.
	// One without the other is ignored, matching the original inline
	// behaviour rather than half-applying an override.
	OpenTime  *string
	CloseTime *string
}

// Window is the resolved trading period for one calendar date, as absolute
// instants rather than wall-clock strings.
type Window struct {
	OpenAt  time.Time
	CloseAt time.Time
}

// Location resolves an IANA timezone name to a *time.Location, falling back
// to UTC when the name is empty or unloadable.
//
// The fallback should be unreachable in practice: stores.timezone is
// NOT NULL DEFAULT 'Asia/Beirut' (migration 010) and cmd/main.go
// blank-imports time/tzdata so the IANA database is compiled into the
// binary. Reaching it indicates a genuinely malformed zone string, not a
// missing tzdata file.
func Location(tz string) *time.Location {
	if tz == "" {
		return time.UTC
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return time.UTC
	}
	return loc
}

// ParseTimeIn combines a PostgreSQL TIME string with a date to produce an
// instant, interpreting the wall-clock time in the given location.
//
// Constructing the time.Date in loc rather than in UTC is the whole point:
// Go applies that zone's DST rules for that specific date, so "09:00:00" in
// Asia/Beirut resolves to 06:00Z in summer and 07:00Z in winter
// automatically.
func ParseTimeIn(date time.Time, timeStr string, loc *time.Location) (time.Time, error) {
	t, err := time.Parse(timeLayout, timeStr)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse store time %q: %w", timeStr, err)
	}
	return time.Date(
		date.Year(), date.Month(), date.Day(),
		t.Hour(), t.Minute(), t.Second(), 0,
		loc,
	), nil
}

// LocalDate returns midnight on the given calendar date in loc. Callers pass
// a date parsed from "YYYY-MM-DD", which carries no zone; this anchors it to
// the store's day rather than to a UTC day.
func LocalDate(date time.Time, loc *time.Location) time.Time {
	return time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, loc)
}

// Resolve computes a store's trading window for one calendar date.
//
// open is false when the store does not trade that date at all - either the
// exception closes it, or the weekday is not a trading day. In that case
// Window is the zero value and must not be read.
//
// The precedence, unchanged from the inline implementation this replaces:
//  1. An exception with IsClosed wins outright.
//  2. Otherwise the weekday's regular hours decide whether the store trades.
//  3. An exception carrying BOTH a custom open and close time then overrides
//     those hours.
//
// day may be nil, meaning no hours row exists for that weekday, which is
// treated as closed - the repository returns (nil, nil) for a missing row
// rather than an error, and the caller must not read that as "open".
func Resolve(tz string, date time.Time, day *DayHours, exc *Exception) (Window, bool, error) {
	if exc != nil && exc.IsClosed {
		return Window{}, false, nil
	}
	if day == nil || !day.IsOpen {
		return Window{}, false, nil
	}

	loc := Location(tz)
	localDate := LocalDate(date, loc)

	openAt, err := ParseTimeIn(localDate, day.OpenTime, loc)
	if err != nil {
		return Window{}, false, fmt.Errorf("resolve opening hours: open time: %w", err)
	}
	closeAt, err := ParseTimeIn(localDate, day.CloseTime, loc)
	if err != nil {
		return Window{}, false, fmt.Errorf("resolve opening hours: close time: %w", err)
	}

	// A custom-hours exception overrides the weekday's times. Both must be
	// present; a parse failure here leaves the regular hours in place rather
	// than failing the whole request, matching the original behaviour, which
	// discarded these errors deliberately.
	if exc != nil && exc.OpenTime != nil && exc.CloseTime != nil {
		if o, err := ParseTimeIn(localDate, *exc.OpenTime, loc); err == nil {
			openAt = o
		}
		if c, err := ParseTimeIn(localDate, *exc.CloseTime, loc); err == nil {
			closeAt = c
		}
	}

	return Window{OpenAt: openAt, CloseAt: closeAt}, true, nil
}

// IsOpenAt reports whether the store is trading at the given instant on the
// window's date. Half-open [OpenAt, CloseAt) - a store is not open at its
// own closing instant.
func (w Window) IsOpenAt(t time.Time) bool {
	return !t.Before(w.OpenAt) && t.Before(w.CloseAt)
}

// IsSameDayIn reports whether date falls on the same calendar day as now,
// evaluated in loc.
//
// This is the store-local equivalent of asking "is this today?". Evaluating
// it in UTC instead is wrong for two hours every night in Lebanon: at 23:00
// UTC it is already tomorrow in Beirut, so a UTC comparison answers a
// different question than the one the caller means.
func IsSameDayIn(date, now time.Time, loc *time.Location) bool {
	d := date.In(loc)
	n := now.In(loc)
	return d.Year() == n.Year() && d.Month() == n.Month() && d.Day() == n.Day()
}
