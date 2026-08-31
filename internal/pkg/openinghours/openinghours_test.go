// Package openinghours tests. Pure functions over primitives — no
// repository, no database, no clock except the one passed in.
package openinghours

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const beirutTZ = "Asia/Beirut"

func beirut(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(beirutTZ)
	require.NoError(t, err, "tzdata must be available")
	return loc
}

func openDay() *DayHours {
	return &DayHours{IsOpen: true, OpenTime: "09:00:00", CloseTime: "17:00:00"}
}

func strPtr(s string) *string { return &s }

// ── Resolve: closed cases ─────────────────────────────────────────────────────

// An exception marked closed wins outright, even when the weekday has
// perfectly good regular hours.
func TestResolve_ExceptionClosed_ReturnsNotOpen(t *testing.T) {
	date := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	_, open, err := Resolve(beirutTZ, date, openDay(), &Exception{IsClosed: true})

	require.NoError(t, err)
	assert.False(t, open, "a holiday closure overrides regular hours")
}

func TestResolve_WeekdayNotTrading_ReturnsNotOpen(t *testing.T) {
	date := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	_, open, err := Resolve(beirutTZ, date, &DayHours{IsOpen: false}, nil)

	require.NoError(t, err)
	assert.False(t, open)
}

// A missing hours row means closed. The repository returns (nil, nil) for a
// weekday with no configuration, and reading that as "open" would make
// every unconfigured day bookable.
func TestResolve_NilDayHours_ReturnsNotOpen(t *testing.T) {
	date := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	_, open, err := Resolve(beirutTZ, date, nil, nil)

	require.NoError(t, err)
	assert.False(t, open, "no hours row configured must read as closed, never as open")
}

// ── Resolve: the window itself ────────────────────────────────────────────────

func TestResolve_RegularHours_ResolvesInStoreZone(t *testing.T) {
	loc := beirut(t)
	date := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	w, open, err := Resolve(beirutTZ, date, openDay(), nil)

	require.NoError(t, err)
	require.True(t, open)
	assert.Equal(t, time.Date(2026, 8, 12, 9, 0, 0, 0, loc), w.OpenAt)
	assert.Equal(t, time.Date(2026, 8, 12, 17, 0, 0, 0, loc), w.CloseAt)
}

func TestResolve_ExceptionCustomHours_OverridesRegular(t *testing.T) {
	loc := beirut(t)
	date := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	exc := &Exception{OpenTime: strPtr("11:00:00"), CloseTime: strPtr("15:00:00")}

	w, open, err := Resolve(beirutTZ, date, openDay(), exc)

	require.NoError(t, err)
	require.True(t, open)
	assert.Equal(t, time.Date(2026, 8, 12, 11, 0, 0, 0, loc), w.OpenAt)
	assert.Equal(t, time.Date(2026, 8, 12, 15, 0, 0, 0, loc), w.CloseAt)
}

// A half-specified override is ignored rather than half-applied — matching
// the behaviour of the inline implementation this replaced, which required
// both fields before overriding either.
func TestResolve_ExceptionOnlyOpenTime_IgnoresOverride(t *testing.T) {
	loc := beirut(t)
	date := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	w, open, err := Resolve(beirutTZ, date, openDay(), &Exception{OpenTime: strPtr("11:00:00")})

	require.NoError(t, err)
	require.True(t, open)
	assert.Equal(t, time.Date(2026, 8, 12, 9, 0, 0, 0, loc), w.OpenAt,
		"an override missing its close time must not apply at all")
}

func TestResolve_MalformedTime_ReturnsError(t *testing.T) {
	date := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	_, _, err := Resolve(beirutTZ, date, &DayHours{IsOpen: true, OpenTime: "not-a-time", CloseTime: "17:00:00"}, nil)

	require.Error(t, err)
}

// ── DST: the reason wall-clock times are resolved per-date ────────────────────

// The same "09:00:00" string resolves to a different UTC instant either side
// of Lebanon's DST transition. This is the whole reason hours are stored as
// wall-clock TIME and resolved through the store's zone rather than
// pre-converted to UTC — see migration 010's header.
func TestResolve_SameWallClockAcrossDST_DiffersInUTC(t *testing.T) {
	winter := time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC)
	summer := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)

	wWinter, _, err := Resolve(beirutTZ, winter, openDay(), nil)
	require.NoError(t, err)
	wSummer, _, err := Resolve(beirutTZ, summer, openDay(), nil)
	require.NoError(t, err)

	assert.Equal(t, 7, wWinter.OpenAt.UTC().Hour(), "09:00 Beirut is 07:00Z in winter (UTC+2)")
	assert.Equal(t, 6, wSummer.OpenAt.UTC().Hour(), "09:00 Beirut is 06:00Z in summer (UTC+3)")
}

// ── Location fallback ─────────────────────────────────────────────────────────

func TestLocation_EmptyOrUnloadable_FallsBackToUTC(t *testing.T) {
	assert.Equal(t, time.UTC, Location(""))
	assert.Equal(t, time.UTC, Location("Not/AZone"))
}

// ── IsOpenAt ──────────────────────────────────────────────────────────────────

// Half-open [OpenAt, CloseAt): a store is open at its opening instant and
// closed at its closing instant.
func TestWindow_IsOpenAt_HalfOpenInterval(t *testing.T) {
	loc := beirut(t)
	w := Window{
		OpenAt:  time.Date(2026, 8, 12, 9, 0, 0, 0, loc),
		CloseAt: time.Date(2026, 8, 12, 17, 0, 0, 0, loc),
	}

	assert.True(t, w.IsOpenAt(w.OpenAt), "open at the opening instant")
	assert.True(t, w.IsOpenAt(time.Date(2026, 8, 12, 13, 0, 0, 0, loc)))
	assert.False(t, w.IsOpenAt(w.CloseAt), "closed at the closing instant")
	assert.False(t, w.IsOpenAt(time.Date(2026, 8, 12, 8, 59, 0, 0, loc)))
	assert.False(t, w.IsOpenAt(time.Date(2026, 8, 12, 17, 1, 0, 0, loc)))
}

// ── IsSameDayIn: the T2.3 bug fix ─────────────────────────────────────────────

// REGRESSION GUARD for a real bug this replaced.
//
// The former isToday() compared against time.Now().UTC(), so between 22:00
// and midnight UTC (Lebanon is UTC+2/+3) it disagreed with the store's own
// calendar day. GetAvailableSlots uses that answer to decide whether to
// apply same-day minimum notice, so for those two hours every night the
// notice window was applied to the wrong day.
func TestIsSameDayIn_LateUTCEvening_UsesStoreLocalDay(t *testing.T) {
	loc := beirut(t)

	// 23:30 UTC on Aug 11 is already 02:30 on Aug 12 in Beirut.
	nowUTC := time.Date(2026, 8, 11, 23, 30, 0, 0, time.UTC)
	aug12 := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)
	aug11 := time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC)

	assert.True(t, IsSameDayIn(aug12, nowUTC, loc),
		"Aug 12 IS today in Beirut when the UTC clock still says Aug 11 23:30")
	assert.False(t, IsSameDayIn(aug11, nowUTC, loc),
		"Aug 11 is already yesterday in Beirut at 23:30 UTC")

	// The old UTC-based comparison would have answered the opposite for both.
	assert.NotEqual(t,
		aug12.Day() == nowUTC.UTC().Day(),
		IsSameDayIn(aug12, nowUTC, loc),
		"this is precisely the case the UTC comparison got wrong")
}

func TestIsSameDayIn_MidDay_Agrees(t *testing.T) {
	loc := beirut(t)
	now := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)
	date := time.Date(2026, 8, 12, 0, 0, 0, 0, time.UTC)

	assert.True(t, IsSameDayIn(date, now, loc))
}
