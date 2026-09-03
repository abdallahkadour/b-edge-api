package booking

// Tests for the interval algebra.
//
// This is a pure type with no database and no clock, so full coverage is
// cheap and worth insisting on here specifically — every future scheduling
// feature is going to rest on it, and a wrong answer propagates silently
// into availability rather than failing loudly.

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// at builds a time on a fixed day; only hours and minutes matter here.
func at(hour, min int) time.Time {
	return time.Date(2027, time.March, 1, hour, min, 0, 0, time.UTC)
}

func iv(fromH, fromM, toH, toM int, kind IntervalKind) Interval {
	return Interval{Start: at(fromH, fromM), End: at(toH, toM), Kind: kind}
}

func svc(fromH, fromM, toH, toM int) Interval {
	return iv(fromH, fromM, toH, toM, KindService)
}

// ── Half-open semantics ───────────────────────────────────────────────────────

// The single most important property in this file. Two appointments that
// touch do not overlap, which is what makes back-to-back bookings possible.
// It also matches the database's `tstzrange(start, end, '[)')`, so the
// application and the GIST exclusion constraint agree by construction.
func TestInterval_TouchingDoesNotOverlap(t *testing.T) {
	a := svc(10, 0, 11, 0)
	b := svc(11, 0, 12, 0)

	assert.False(t, a.Overlaps(b), "[10:00,11:00) and [11:00,12:00) must not overlap")
	assert.False(t, b.Overlaps(a), "and the relation must be symmetric")
}

func TestInterval_OverlapByOneMinute(t *testing.T) {
	a := svc(10, 0, 11, 0)
	b := svc(10, 59, 12, 0)

	assert.True(t, a.Overlaps(b))
	assert.True(t, b.Overlaps(a))
}

func TestInterval_ContainedOverlaps(t *testing.T) {
	outer := svc(9, 0, 17, 0)
	inner := svc(12, 0, 13, 0)

	assert.True(t, outer.Overlaps(inner))
	assert.True(t, inner.Overlaps(outer))
}

// ── Empty and inverted ────────────────────────────────────────────────────────

// A zero-length interval is nothing, not an instant. A zero-minute buffer is
// the DEFAULT configuration, so this is the common case rather than an edge.
func TestInterval_ZeroLengthIsEmpty(t *testing.T) {
	assert.True(t, svc(10, 0, 10, 0).IsEmpty())
	assert.False(t, svc(10, 0, 10, 1).IsEmpty())
}

// An inverted interval can only come from a caller bug. Treating it as empty
// makes it occupy nothing; the alternative — treating it as a span — would
// block from its end to the end of time.
func TestInterval_InvertedIsEmpty(t *testing.T) {
	assert.True(t, svc(11, 0, 10, 0).IsEmpty())
}

func TestInterval_EmptyOverlapsNothing(t *testing.T) {
	empty := svc(10, 0, 10, 0)
	real := svc(9, 0, 17, 0)

	assert.False(t, empty.Overlaps(real), "a zero-length span cannot collide")
	assert.False(t, real.Overlaps(empty))
}

// ── The set ───────────────────────────────────────────────────────────────────

func TestOccupancy_AddDropsEmptyIntervals(t *testing.T) {
	o := NewOccupancy(svc(10, 0, 11, 0), svc(12, 0, 12, 0), svc(13, 0, 12, 0))

	assert.Equal(t, 1, o.Len(), "zero-length and inverted intervals are not stored")
}

// Handing out the backing slice would let a caller mutate occupancy through
// something that reads like a getter.
func TestOccupancy_IntervalsReturnsACopy(t *testing.T) {
	o := NewOccupancy(svc(10, 0, 11, 0))

	got := o.Intervals()
	got[0].End = at(23, 0)

	assert.Equal(t, at(11, 0), o.Intervals()[0].End, "mutating the result must not affect the set")
}

func TestOccupancy_BlocksArtist(t *testing.T) {
	o := NewOccupancy(svc(10, 0, 11, 0), svc(14, 0, 15, 0))

	assert.True(t, o.BlocksArtist(svc(10, 30, 11, 30)), "overlaps the first")
	assert.True(t, o.BlocksArtist(svc(14, 30, 16, 0)), "overlaps the second")
	assert.False(t, o.BlocksArtist(svc(11, 0, 14, 0)), "sits exactly in the gap")
	assert.False(t, o.BlocksArtist(svc(16, 0, 17, 0)), "after everything")
}

func TestOccupancy_EmptySetBlocksNothing(t *testing.T) {
	assert.False(t, NewOccupancy().BlocksArtist(svc(9, 0, 17, 0)))
}

func TestOccupancy_EmptyCandidateIsNeverBlocked(t *testing.T) {
	o := NewOccupancy(svc(9, 0, 17, 0))

	assert.False(t, o.BlocksArtist(svc(12, 0, 12, 0)))
	assert.False(t, o.OverlapsAny(svc(12, 0, 12, 0)))
}

// ── Kinds ─────────────────────────────────────────────────────────────────────

// The reason the set is typed at all. A processing gap is time the artist is
// free even though the chair and the customer are not, so it must not block
// the artist — while still colliding under OverlapsAny, which is the
// question a resource will ask.
func TestOccupancy_ProcessingGapDoesNotBlockTheArtist(t *testing.T) {
	o := NewOccupancy(iv(10, 0, 11, 0, KindProcessingGap))
	candidate := svc(10, 15, 10, 45)

	assert.False(t, o.BlocksArtist(candidate), "the artist is free during a processing gap")
	assert.True(t, o.OverlapsAny(candidate), "but the chair is not")
}

func TestIntervalKind_BlocksArtist(t *testing.T) {
	assert.True(t, KindService.BlocksArtist())
	assert.True(t, KindTravel.BlocksArtist())
	assert.True(t, KindBuffer.BlocksArtist())
	assert.False(t, KindProcessingGap.BlocksArtist(), "the one exception, and the point of kinds")
}

// Every currently-produced kind blocks, so today the two questions agree.
// Pinned so that the day they diverge, it is because someone changed a kind
// deliberately.
func TestOccupancy_TodayEveryProducedKindBlocks(t *testing.T) {
	for _, k := range []IntervalKind{KindService, KindTravel, KindBuffer} {
		o := NewOccupancy(iv(10, 0, 11, 0, k))
		assert.True(t, o.BlocksArtist(svc(10, 30, 11, 30)), "kind %s", k)
	}
}

// ── Merging ───────────────────────────────────────────────────────────────────

func TestOccupancy_MergedCoalescesOverlapping(t *testing.T) {
	o := NewOccupancy(svc(10, 0, 12, 0), svc(11, 0, 13, 0))

	merged := o.Merged()

	require.Len(t, merged, 1)
	assert.Equal(t, at(10, 0), merged[0].Start)
	assert.Equal(t, at(13, 0), merged[0].End)
}

// Touching intervals DO merge — a continuous busy stretch is one span. This
// is the opposite of the overlap rule, and the difference is deliberate:
// merging describes time, overlapping decides bookability.
func TestOccupancy_MergedCoalescesTouching(t *testing.T) {
	o := NewOccupancy(svc(10, 0, 11, 0), svc(11, 0, 12, 0))

	merged := o.Merged()

	require.Len(t, merged, 1, "[10,11) and [11,12) describe one continuous busy stretch")
	assert.Equal(t, at(12, 0), merged[0].End)
}

// The trap this file exists to prevent: merging before an overlap test
// changes the answer. A candidate ending exactly at 11:00 collides with the
// merged span but with neither original.
func TestOccupancy_MergingMustNotBeUsedForOverlapDecisions(t *testing.T) {
	o := NewOccupancy(svc(11, 0, 12, 0), svc(12, 0, 13, 0))
	candidate := svc(10, 0, 11, 0)

	assert.False(t, o.BlocksArtist(candidate), "touches the first booking, does not overlap it")

	merged := NewOccupancy(o.Merged()...)
	assert.False(t, merged.BlocksArtist(candidate),
		"and merging must not have changed that answer")
}

func TestOccupancy_MergedSortsByStart(t *testing.T) {
	o := NewOccupancy(svc(15, 0, 16, 0), svc(9, 0, 10, 0), svc(12, 0, 13, 0))

	merged := o.Merged()

	require.Len(t, merged, 3)
	assert.Equal(t, at(9, 0), merged[0].Start)
	assert.Equal(t, at(12, 0), merged[1].Start)
	assert.Equal(t, at(15, 0), merged[2].Start)
}

// A fully contained interval must not extend the span it sits inside.
func TestOccupancy_MergedContainedDoesNotShrinkTheSpan(t *testing.T) {
	o := NewOccupancy(svc(9, 0, 17, 0), svc(12, 0, 13, 0))

	merged := o.Merged()

	require.Len(t, merged, 1)
	assert.Equal(t, at(9, 0), merged[0].Start)
	assert.Equal(t, at(17, 0), merged[0].End)
}

func TestOccupancy_MergedExcludesNonBlockingKinds(t *testing.T) {
	o := NewOccupancy(svc(10, 0, 11, 0), iv(11, 0, 12, 0, KindProcessingGap))

	merged := o.Merged()

	require.Len(t, merged, 1)
	assert.Equal(t, at(11, 0), merged[0].End, "the processing gap must not extend the busy span")
}

func TestOccupancy_MergedEmptySetIsNil(t *testing.T) {
	assert.Nil(t, NewOccupancy().Merged())
}

// ── Free space ────────────────────────────────────────────────────────────────

func TestOccupancy_FreeWithin_SplitsAroundBusy(t *testing.T) {
	o := NewOccupancy(svc(11, 0, 12, 0), svc(14, 0, 15, 0))

	free := o.FreeWithin(at(9, 0), at(17, 0))

	require.Len(t, free, 3)
	assert.Equal(t, [2]time.Time{at(9, 0), at(11, 0)}, [2]time.Time{free[0].Start, free[0].End})
	assert.Equal(t, [2]time.Time{at(12, 0), at(14, 0)}, [2]time.Time{free[1].Start, free[1].End})
	assert.Equal(t, [2]time.Time{at(15, 0), at(17, 0)}, [2]time.Time{free[2].Start, free[2].End})
}

func TestOccupancy_FreeWithin_NothingBusyIsOneWholeWindow(t *testing.T) {
	free := NewOccupancy().FreeWithin(at(9, 0), at(17, 0))

	require.Len(t, free, 1)
	assert.Equal(t, at(9, 0), free[0].Start)
	assert.Equal(t, at(17, 0), free[0].End)
}

func TestOccupancy_FreeWithin_FullyBookedIsNoGaps(t *testing.T) {
	o := NewOccupancy(svc(9, 0, 17, 0))

	assert.Empty(t, o.FreeWithin(at(9, 0), at(17, 0)))
}

// Busy time outside the window must not create phantom gaps inside it.
func TestOccupancy_FreeWithin_IgnoresBusyOutsideTheWindow(t *testing.T) {
	o := NewOccupancy(svc(6, 0, 8, 0), svc(19, 0, 21, 0))

	free := o.FreeWithin(at(9, 0), at(17, 0))

	require.Len(t, free, 1)
	assert.Equal(t, at(9, 0), free[0].Start)
	assert.Equal(t, at(17, 0), free[0].End)
}

// A booking straddling the window edge trims the free space rather than
// being ignored or extending past the boundary.
func TestOccupancy_FreeWithin_ClipsStraddlingIntervals(t *testing.T) {
	o := NewOccupancy(svc(8, 0, 10, 0), svc(16, 0, 18, 0))

	free := o.FreeWithin(at(9, 0), at(17, 0))

	require.Len(t, free, 1)
	assert.Equal(t, at(10, 0), free[0].Start, "clipped to where the busy span actually ends")
	assert.Equal(t, at(16, 0), free[0].End)
}

func TestOccupancy_FreeWithin_EmptyWindowIsNil(t *testing.T) {
	o := NewOccupancy(svc(11, 0, 12, 0))

	assert.Nil(t, o.FreeWithin(at(12, 0), at(12, 0)))
	assert.Nil(t, o.FreeWithin(at(13, 0), at(12, 0)), "inverted window")
}

// A processing gap is free time for the artist, so it must appear as a gap.
func TestOccupancy_FreeWithin_ProcessingGapIsFreeForTheArtist(t *testing.T) {
	o := NewOccupancy(iv(11, 0, 12, 0, KindProcessingGap))

	free := o.FreeWithin(at(9, 0), at(17, 0))

	require.Len(t, free, 1, "the artist is free the whole window")
	assert.Equal(t, at(9, 0), free[0].Start)
	assert.Equal(t, at(17, 0), free[0].End)
}

// AddRange is the shape slot generation uses, where a caller has a bare
// start/end and a kind rather than a built Interval.
func TestOccupancy_AddRange(t *testing.T) {
	o := &Occupancy{}
	o.AddRange(at(10, 0), at(11, 0), KindService)
	o.AddRange(at(12, 0), at(12, 0), KindBuffer) // zero-length: dropped

	require.Equal(t, 1, o.Len())
	assert.Equal(t, KindService, o.Intervals()[0].Kind)
	assert.True(t, o.BlocksArtist(svc(10, 30, 10, 45)))
}

// OverlapsAny must return false when the set is non-empty but nothing
// collides — the loop has to run to completion, not short-circuit.
func TestOccupancy_OverlapsAny_NoCollision(t *testing.T) {
	o := NewOccupancy(svc(10, 0, 11, 0), iv(14, 0, 15, 0, KindProcessingGap))

	assert.False(t, o.OverlapsAny(svc(11, 0, 14, 0)), "sits in the gap between both")
	assert.True(t, o.OverlapsAny(svc(14, 30, 14, 45)), "but a processing gap still collides")
}
