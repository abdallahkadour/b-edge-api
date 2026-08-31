// Bulk schedule shift evaluation tests.
//
// evaluateShift is deliberately pure - no repository, no clock - so every
// rule about someone's working day is expressible without a database. All
// fixtures use Asia/Beirut rather than UTC, because the whole point of the
// times involved is that they are the artist's local wall clock.
package booking

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func beirutLoc(t *testing.T) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Beirut")
	require.NoError(t, err, "tzdata must be available")
	return loc
}

// shiftDay builds a trading day of 09:00-17:00 Beirut on a fixed date.
func shiftDay(t *testing.T) (open, close, now time.Time) {
	t.Helper()
	loc := beirutLoc(t)
	open = time.Date(2026, 9, 15, 9, 0, 0, 0, loc)
	close = time.Date(2026, 9, 15, 17, 0, 0, 0, loc)
	now = time.Date(2026, 9, 15, 8, 0, 0, 0, loc) // before opening
	return
}

func bk(t *testing.T, storeID uuid.UUID, hour, mins, durMin int, status string) *EnrichedBooking {
	t.Helper()
	loc := beirutLoc(t)
	start := time.Date(2026, 9, 15, hour, mins, 0, 0, loc)
	phone := "+96170123456"
	return &EnrichedBooking{
		Booking: Booking{
			ID:        uuid.New(),
			StoreID:   storeID,
			StartTime: start,
			EndTime:   start.Add(time.Duration(durMin) * time.Minute),
			Status:    status,
		},
		CustomerName:  "Layla",
		CustomerPhone: &phone,
		ServiceName:   "Bridal makeup",
	}
}

func baseInput(t *testing.T, storeID uuid.UUID, shiftMin int, same ...*EnrichedBooking) shiftInput {
	t.Helper()
	open, cl, now := shiftDay(t)
	return shiftInput{
		SameStore:    same,
		OpenAt:       open,
		CloseAt:      cl,
		StoreOpen:    true,
		BufferFor:    func(uuid.UUID) int { return 0 },
		ShiftMinutes: shiftMin,
		Now:          now,
	}
}

// ── The happy path ────────────────────────────────────────────────────────────

func TestEvaluateShift_SimpleForwardShift_AllMove(t *testing.T) {
	store := uuid.New()
	in := baseInput(t, store, 10,
		bk(t, store, 10, 0, 60, StatusConfirmed),
		bk(t, store, 11, 0, 60, StatusConfirmed),
	)

	got := evaluateShift(in)

	require.Len(t, got.Movable, 2)
	assert.Empty(t, got.Blockers)
	assert.True(t, got.CanApply)
	assert.Equal(t, 10, got.Movable[0].NewStart.Minute())
	assert.Equal(t, 2, got.NotifiableCount)
}

// Adjacent bookings are the case that breaks a naive implementation at the
// database level (see migration 029). The evaluator must not object to them
// - they are perfectly valid once both have moved.
func TestEvaluateShift_AdjacentBookings_NoBlockers(t *testing.T) {
	store := uuid.New()
	in := baseInput(t, store, 10,
		bk(t, store, 9, 0, 60, StatusConfirmed),  // 09:00-10:00
		bk(t, store, 10, 0, 60, StatusConfirmed), // 10:00-11:00
	)

	got := evaluateShift(in)

	assert.Empty(t, got.Blockers, "shifting a contiguous block is valid; only the DB write order was ever the problem")
	assert.True(t, got.CanApply)
}

// ── Blockers ──────────────────────────────────────────────────────────────────

// The case Rania will hit most: the last appointment of the day cannot
// absorb a delay.
func TestEvaluateShift_PastClosing_BlocksAndNamesTheBooking(t *testing.T) {
	store := uuid.New()
	last := bk(t, store, 16, 30, 60, StatusConfirmed) // ends 17:30 after a +60
	in := baseInput(t, store, 60, last)

	got := evaluateShift(in)

	require.Len(t, got.Blockers, 1)
	assert.Equal(t, BlockerPastClosing, got.Blockers[0].Reason)
	require.NotNil(t, got.Blockers[0].BookingID)
	assert.Equal(t, last.ID, *got.Blockers[0].BookingID,
		"the artist must be told WHICH booking is the problem")
	assert.False(t, got.CanApply)
}

// All-or-nothing: one bad booking blocks the batch, but every movable one is
// still reported so the artist can see the whole picture.
func TestEvaluateShift_OneBlockerAmongMany_BlocksWholeBatch(t *testing.T) {
	store := uuid.New()
	in := baseInput(t, store, 60,
		bk(t, store, 10, 0, 60, StatusConfirmed),
		bk(t, store, 16, 30, 60, StatusConfirmed), // overruns
	)

	got := evaluateShift(in)

	assert.False(t, got.CanApply)
	assert.Len(t, got.Movable, 2, "the preview still lists everything that would move")
	assert.Len(t, got.Blockers, 1)
}

func TestEvaluateShift_NegativeShiftBeforeOpening_Blocks(t *testing.T) {
	store := uuid.New()
	in := baseInput(t, store, -60, bk(t, store, 9, 30, 60, StatusConfirmed))

	got := evaluateShift(in)

	require.NotEmpty(t, got.Blockers)
	assert.Equal(t, BlockerBeforeOpening, got.Blockers[0].Reason)
}

// A negative shift must never move a booking into the past, even when it
// stays inside opening hours.
func TestEvaluateShift_NegativeShiftIntoPast_Blocks(t *testing.T) {
	store := uuid.New()
	loc := beirutLoc(t)
	in := baseInput(t, store, -60, bk(t, store, 12, 0, 60, StatusConfirmed))
	in.Now = time.Date(2026, 9, 15, 11, 30, 0, 0, loc) // 11:30; -60 lands at 11:00

	got := evaluateShift(in)

	var found bool
	for _, b := range got.Blockers {
		if b.Reason == BlockerIntoPast {
			found = true
		}
	}
	assert.True(t, found, "a booking must never be moved to a time that has already passed")
}

func TestEvaluateShift_StoreClosedThatDay_BlocksImmediately(t *testing.T) {
	store := uuid.New()
	in := baseInput(t, store, 10, bk(t, store, 10, 0, 60, StatusConfirmed))
	in.StoreOpen = false

	got := evaluateShift(in)

	require.Len(t, got.Blockers, 1)
	assert.Equal(t, BlockerStoreClosed, got.Blockers[0].Reason)
	assert.Empty(t, got.Movable, "no point evaluating bookings against hours that do not exist")
}

// ── Travel buffers: the B-Edge-specific rule ─────────────────────────────────

// Rania works several branches. A shift that erases her travel time produces
// a schedule that looks fine on screen and cannot be worked in reality.
func TestEvaluateShift_ErodesTravelBuffer_Blocks(t *testing.T) {
	storeA, storeB := uuid.New(), uuid.New()
	in := baseInput(t, storeA, 60, bk(t, storeA, 10, 0, 60, StatusConfirmed)) // → 11:00-12:00
	in.CrossStore = []*EnrichedBooking{bk(t, storeB, 13, 0, 60, StatusConfirmed)}
	in.BufferFor = func(uuid.UUID) int { return 150 } // 2.5h, the weekday default

	got := evaluateShift(in)

	require.NotEmpty(t, got.Blockers)
	assert.Equal(t, BlockerTravelBuffer, got.Blockers[0].Reason)
	assert.Contains(t, got.Blockers[0].Detail, "150",
		"the message must state how much travel time is actually needed")
}

// The same shift is fine when the gap is large enough.
func TestEvaluateShift_SufficientTravelBuffer_NoBlocker(t *testing.T) {
	storeA, storeB := uuid.New(), uuid.New()
	in := baseInput(t, storeA, 10, bk(t, storeA, 9, 0, 60, StatusConfirmed)) // → 09:10-10:10
	in.CrossStore = []*EnrichedBooking{bk(t, storeB, 14, 0, 60, StatusConfirmed)}
	in.BufferFor = func(uuid.UUID) int { return 150 }

	got := evaluateShift(in)

	assert.Empty(t, got.Blockers)
	assert.True(t, got.CanApply)
}

// A cancelled booking at the other branch imposes no travel requirement -
// the artist is not going there.
func TestEvaluateShift_CancelledCrossStoreBooking_ImposesNoBuffer(t *testing.T) {
	storeA, storeB := uuid.New(), uuid.New()
	in := baseInput(t, storeA, 60, bk(t, storeA, 10, 0, 60, StatusConfirmed))
	in.CrossStore = []*EnrichedBooking{bk(t, storeB, 13, 0, 60, StatusCancelled)}
	in.BufferFor = func(uuid.UUID) int { return 150 }

	got := evaluateShift(in)

	assert.Empty(t, got.Blockers)
}

// ── Skipping ──────────────────────────────────────────────────────────────────

// History is not rewritten. A completed appointment did happen when it
// happened.
func TestEvaluateShift_TerminalStatuses_Skipped(t *testing.T) {
	store := uuid.New()
	in := baseInput(t, store, 10,
		bk(t, store, 10, 0, 60, StatusCompleted),
		bk(t, store, 11, 0, 60, StatusCancelled),
		bk(t, store, 12, 0, 60, StatusNoShow),
		bk(t, store, 13, 0, 60, StatusConfirmed),
	)

	got := evaluateShift(in)

	assert.Len(t, got.Skipped, 3)
	assert.Len(t, got.Movable, 1)
	for _, s := range got.Skipped {
		assert.Equal(t, SkipTerminalStatus, s.Reason)
	}
}

// Telling someone mid-appointment that they are delayed is meaningless.
func TestEvaluateShift_BookingAlreadyStarted_Skipped(t *testing.T) {
	store := uuid.New()
	loc := beirutLoc(t)
	in := baseInput(t, store, 10,
		bk(t, store, 10, 0, 60, StatusConfirmed), // under way
		bk(t, store, 14, 0, 60, StatusConfirmed), // still ahead
	)
	in.Now = time.Date(2026, 9, 15, 10, 30, 0, 0, loc)

	got := evaluateShift(in)

	require.Len(t, got.Skipped, 1)
	assert.Equal(t, SkipInProgress, got.Skipped[0].Reason)
	assert.Len(t, got.Movable, 1)
}

// ── Notification reachability ─────────────────────────────────────────────────

// A customer with no number cannot be told the time changed. Surfacing that
// count up front is the difference between the artist phoning them and them
// arriving an hour early.
func TestEvaluateShift_CustomerWithoutPhone_CountedSeparately(t *testing.T) {
	store := uuid.New()
	withPhone := bk(t, store, 10, 0, 60, StatusConfirmed)
	noPhone := bk(t, store, 12, 0, 60, StatusConfirmed)
	noPhone.CustomerPhone = nil

	got := evaluateShift(baseInput(t, store, 10, withPhone, noPhone))

	require.Len(t, got.Movable, 2)
	assert.Equal(t, 1, got.NotifiableCount,
		"the gap between movable and notifiable is who must be telephoned")
	assert.False(t, got.Movable[1].HasPhone)
}

// ── Degenerate cases ──────────────────────────────────────────────────────────

// An empty day is not an error, but it is not applicable either - there is
// nothing to shift and nobody to notify.
func TestEvaluateShift_EmptyDay_CannotApply(t *testing.T) {
	got := evaluateShift(baseInput(t, uuid.New(), 10))

	assert.Empty(t, got.Movable)
	assert.Empty(t, got.Blockers)
	assert.False(t, got.CanApply, "there is nothing to apply")
}

// Slices must be non-nil so JSON carries [] rather than null.
func TestEvaluateShift_EmptyResults_SerialiseAsEmptyArrays(t *testing.T) {
	got := evaluateShift(baseInput(t, uuid.New(), 10))

	assert.NotNil(t, got.Movable)
	assert.NotNil(t, got.Skipped)
	assert.NotNil(t, got.Blockers)
}

// ── gapBetween ────────────────────────────────────────────────────────────────

func TestGapBetween_DisjointEitherOrder(t *testing.T) {
	loc := beirutLoc(t)
	a1 := time.Date(2026, 9, 15, 9, 0, 0, 0, loc)
	a2 := time.Date(2026, 9, 15, 10, 0, 0, 0, loc)
	b1 := time.Date(2026, 9, 15, 12, 0, 0, 0, loc)
	b2 := time.Date(2026, 9, 15, 13, 0, 0, 0, loc)

	gap, ok := gapBetween(a1, a2, b1, b2)
	require.True(t, ok)
	assert.Equal(t, 2*time.Hour, gap)

	gap, ok = gapBetween(b1, b2, a1, a2)
	require.True(t, ok)
	assert.Equal(t, 2*time.Hour, gap, "order must not matter")
}

// Overlapping intervals leave zero travel time, which any positive buffer
// requirement then rejects.
func TestGapBetween_Overlapping_ReturnsZero(t *testing.T) {
	loc := beirutLoc(t)
	a1 := time.Date(2026, 9, 15, 9, 0, 0, 0, loc)
	a2 := time.Date(2026, 9, 15, 11, 0, 0, 0, loc)
	b1 := time.Date(2026, 9, 15, 10, 0, 0, 0, loc)
	b2 := time.Date(2026, 9, 15, 12, 0, 0, 0, loc)

	gap, ok := gapBetween(a1, a2, b1, b2)
	require.True(t, ok)
	assert.Equal(t, time.Duration(0), gap)
}
