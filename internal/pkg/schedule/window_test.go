package schedule

import (
	"testing"
	"time"
)

var beirut = mustLoad("Asia/Beirut")

func mustLoad(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// at builds an instant on 2026-09-21 in Beirut, the store's own zone.
func at(hour, min int) time.Time {
	return time.Date(2026, 9, 21, hour, min, 0, 0, beirut)
}

func win(fromH, fromM, toH, toM int) Window {
	return Window{Start: at(fromH, fromM), End: at(toH, toM)}
}

func mins(h, m int) int { return h*60 + m }

func assertWindow(t *testing.T, got, want Window, what string) {
	t.Helper()
	if !got.Start.Equal(want.Start) || !got.End.Equal(want.End) {
		t.Errorf("%s:\n  got  %s - %s\n  want %s - %s", what,
			got.Start.Format(time.RFC3339), got.End.Format(time.RFC3339),
			want.Start.Format(time.RFC3339), want.End.Format(time.RFC3339))
	}
}

// ══ The identity property ═════════════════════════════════════════════════

// TestIntersect_NoArtistRota_ReturnsStoreWindowUnchanged is the single most
// important test in this feature.
//
// Every artist on B-Edge has zero rows in artist_schedules. If this ever
// fails, migration 047 empties five live calendars the day it ships and the
// first person to find out is a customer who cannot book.
func TestIntersect_NoArtistRota_ReturnsStoreWindowUnchanged(t *testing.T) {
	store := win(9, 0, 18, 0)
	got := Intersect(store, Window{})

	assertWindow(t, got, store, "an artist with no rota must get the whole store window")
	if got != store {
		t.Error("the returned window must be the store window exactly, not a rebuilt copy")
	}
}

// TestResolveDay_NothingDeclared_ReturnsStoreWindow is the same property one
// level up, through the function internal/booking actually calls.
func TestResolveDay_NothingDeclared_ReturnsStoreWindow(t *testing.T) {
	store := win(9, 0, 18, 0)
	assertWindow(t, ResolveDay(store, nil, nil), store,
		"no rota and no exception must be an identity operation")
}

// ══ Intersect ═════════════════════════════════════════════════════════════

func TestIntersect_ArtistStartsLater_ClipsStart(t *testing.T) {
	assertWindow(t, Intersect(win(9, 0, 18, 0), win(12, 0, 18, 0)), win(12, 0, 18, 0),
		"an afternoon-only artist")
}

func TestIntersect_ArtistEndsEarlier_ClipsEnd(t *testing.T) {
	assertWindow(t, Intersect(win(9, 0, 18, 0), win(9, 0, 13, 0)), win(9, 0, 13, 0),
		"a morning-only artist")
}

func TestIntersect_ArtistInsideStore_ReturnsArtistWindow(t *testing.T) {
	assertWindow(t, Intersect(win(9, 0, 18, 0), win(11, 0, 15, 0)), win(11, 0, 15, 0),
		"both ends clipped")
}

// An artist cannot extend the store's opening hours by declaring a longer
// shift. The store is the outer bound.
func TestIntersect_ArtistWiderThanStore_ReturnsStoreWindow(t *testing.T) {
	store := win(9, 0, 18, 0)
	assertWindow(t, Intersect(store, win(6, 0, 23, 0)), store,
		"a rota cannot open the store early or keep it open late")
}

func TestIntersect_Disjoint_ReturnsZero(t *testing.T) {
	got := Intersect(win(9, 0, 12, 0), win(17, 0, 21, 0))
	if !got.IsZero() {
		t.Errorf("an evening artist at a morning-only store has no bookable time, got %v", got)
	}
}

// Touching boundaries produce a zero-length window, which is not a slot.
func TestIntersect_TouchingBoundaries_ReturnsZero(t *testing.T) {
	if got := Intersect(win(9, 0, 12, 0), win(12, 0, 18, 0)); !got.IsZero() {
		t.Errorf("a shift starting exactly at closing time is not bookable, got %v", got)
	}
	if got := Intersect(win(12, 0, 18, 0), win(9, 0, 12, 0)); !got.IsZero() {
		t.Errorf("a shift ending exactly at opening time is not bookable, got %v", got)
	}
}

// A closed store stays closed. This is the asymmetry with the empty-artist
// case, and the reason both are tested explicitly rather than by symmetry.
func TestIntersect_StoreClosed_ReturnsZero(t *testing.T) {
	if got := Intersect(Window{}, win(9, 0, 18, 0)); !got.IsZero() {
		t.Errorf("an artist's rota must not open a closed store, got %v", got)
	}
}

func TestIntersect_BothEmpty_ReturnsZero(t *testing.T) {
	if got := Intersect(Window{}, Window{}); !got.IsZero() {
		t.Errorf("want zero, got %v", got)
	}
}

// ══ IsZero ════════════════════════════════════════════════════════════════

func TestIsZero_EqualStartAndEnd_IsZero(t *testing.T) {
	if !(Window{Start: at(9, 0), End: at(9, 0)}).IsZero() {
		t.Error("a zero-length window holds nothing bookable")
	}
}

func TestIsZero_InvertedWindow_IsZero(t *testing.T) {
	if !(Window{Start: at(18, 0), End: at(9, 0)}).IsZero() {
		t.Error("an inverted window must not read as a real interval")
	}
}

// ══ ResolveDay precedence ═════════════════════════════════════════════════

func TestResolveDay_RotaNotWorking_ReturnsZero(t *testing.T) {
	got := ResolveDay(win(9, 0, 18, 0), &DayRota{IsWorking: false}, nil)
	if !got.IsZero() {
		t.Errorf("an artist who does not work this weekday has no window, got %v", got)
	}
}

func TestResolveDay_RotaWorking_IntersectsWithStore(t *testing.T) {
	rota := &DayRota{StartMinutes: mins(12, 0), EndMinutes: mins(20, 0), IsWorking: true}
	assertWindow(t, ResolveDay(win(9, 0, 18, 0), rota, nil), win(12, 0, 18, 0),
		"a 12:00-20:00 shift at a store closing at 18:00")
}

// An exception overrides the weekly rota entirely - it does not merge with it.
func TestResolveDay_ExceptionUnavailable_BeatsRota(t *testing.T) {
	rota := &DayRota{StartMinutes: mins(9, 0), EndMinutes: mins(18, 0), IsWorking: true}
	exc := &DayException{IsUnavailable: true}
	if got := ResolveDay(win(9, 0, 18, 0), rota, exc); !got.IsZero() {
		t.Errorf("a day off must beat the weekly rota, got %v", got)
	}
}

func TestResolveDay_ExceptionHours_BeatRota(t *testing.T) {
	rota := &DayRota{StartMinutes: mins(9, 0), EndMinutes: mins(12, 0), IsWorking: true}
	exc := &DayException{StartMinutes: mins(14, 0), EndMinutes: mins(17, 0)}
	assertWindow(t, ResolveDay(win(9, 0, 18, 0), rota, exc), win(14, 0, 17, 0),
		"one-off hours replace the rota rather than intersecting with it")
}

// An exception on a day the artist does not normally work still lets them
// work - otherwise "I will come in this Sunday" is unexpressible.
func TestResolveDay_ExceptionHours_OverrideNotWorkingRota(t *testing.T) {
	rota := &DayRota{IsWorking: false}
	exc := &DayException{StartMinutes: mins(10, 0), EndMinutes: mins(14, 0)}
	assertWindow(t, ResolveDay(win(9, 0, 18, 0), rota, exc), win(10, 0, 14, 0),
		"coming in on a normally-off day")
}

func TestResolveDay_StoreClosed_BeatsEverything(t *testing.T) {
	rota := &DayRota{StartMinutes: mins(9, 0), EndMinutes: mins(18, 0), IsWorking: true}
	exc := &DayException{StartMinutes: mins(9, 0), EndMinutes: mins(18, 0)}
	for _, c := range []struct {
		name string
		r    *DayRota
		e    *DayException
	}{
		{"nothing declared", nil, nil},
		{"with a rota", rota, nil},
		{"with an exception", nil, exc},
		{"with both", rota, exc},
	} {
		if got := ResolveDay(Window{}, c.r, c.e); !got.IsZero() {
			t.Errorf("%s: a closed store must stay closed, got %v", c.name, got)
		}
	}
}

// ══ Time handling ═════════════════════════════════════════════════════════

// The rota carries clock times; the store window carries instants. If the
// two are not placed on the same date in the same zone, the intersection
// silently comes back empty and looks like "the artist is not available".
func TestResolveDay_RotaIsPlacedOnTheStoresDateAndZone(t *testing.T) {
	store := Window{
		Start: time.Date(2026, 12, 25, 9, 0, 0, 0, beirut),
		End:   time.Date(2026, 12, 25, 18, 0, 0, 0, beirut),
	}
	rota := &DayRota{StartMinutes: mins(10, 0), EndMinutes: mins(16, 0), IsWorking: true}
	got := ResolveDay(store, rota, nil)

	want := Window{
		Start: time.Date(2026, 12, 25, 10, 0, 0, 0, beirut),
		End:   time.Date(2026, 12, 25, 16, 0, 0, 0, beirut),
	}
	assertWindow(t, got, want, "the rota must land on the store's own date")
	if got.Start.Location().String() != beirut.String() {
		t.Errorf("result left Beirut: %s", got.Start.Location())
	}
}

// A store window expressed in UTC must not have the rota placed on the UTC
// date when that is a different calendar day locally. Using the store
// window's own reference instant is what prevents this.
func TestResolveDay_UsesTheStoreWindowsOwnReference(t *testing.T) {
	// 2026-09-21 23:30 Beirut is 2026-09-21 20:30 UTC - same date here, but
	// the point is that the result must follow the window we were handed.
	store := Window{
		Start: time.Date(2026, 9, 21, 20, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 21, 23, 0, 0, 0, time.UTC),
	}
	rota := &DayRota{StartMinutes: mins(21, 0), EndMinutes: mins(22, 0), IsWorking: true}
	got := ResolveDay(store, rota, nil)

	want := Window{
		Start: time.Date(2026, 9, 21, 21, 0, 0, 0, time.UTC),
		End:   time.Date(2026, 9, 21, 22, 0, 0, 0, time.UTC),
	}
	assertWindow(t, got, want, "the rota must be placed in the store window's own location")
}
