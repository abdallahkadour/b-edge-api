package booking

// Golden-output tests for slot generation.
//
// These are NOT behaviour tests. The existing suite already covers what
// GetAvailableSlots should do in each situation, case by case. These pin the
// exact OUTPUT — every slot boundary, in order — of the algorithm as it
// stands before Sprint 5 rewrites its internals to run on a typed interval
// set (see B-Edge-Interval-Algebra-Decision-v1.md).
//
// Why that needs its own file. A refactor of this subsystem can keep every
// existing assertion green while silently shifting a boundary by fifteen
// minutes, dropping the last slot of a day, or reordering the list. Each of
// those is invisible to a test that asks "are there slots?" and obvious to
// one that asks "are they exactly these?".
//
// How to read a failure. If one of these fails, the refactor changed
// availability. That is not automatically wrong — but it must be a decision,
// not a side effect. Update the golden string only alongside an explanation
// of why the new output is correct.
//
// The fixtures deliberately exercise the interacting parts rather than one
// path at a time: trading window, same-day notice, existing bookings,
// cross-store travel buffers, the early-bird cutoff, and the 15-minute
// granularity all at once, because interaction is where a refactor breaks.

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// goldenOf renders slots as a single comparable string: "HH:MM-HH:MM" per
// slot in the store's zone, early-bird slots marked with a trailing "*".
//
// A string rather than a slice of structs on purpose - a diff on one line
// shows which boundary moved, where a struct diff buries it.
func goldenOf(t *testing.T, slots []*TimeSlot) string {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Beirut")
	require.NoError(t, err)

	parts := make([]string, 0, len(slots))
	for _, s := range slots {
		mark := ""
		if s.IsEarlyBird {
			mark = "*"
		}
		parts = append(parts, fmt.Sprintf("%s-%s%s",
			s.StartTime.In(loc).Format("15:04"),
			s.EndTime.In(loc).Format("15:04"), mark))
	}
	return strings.Join(parts, " ")
}

// goldenRepo builds the full fixture: a 09:00-17:00 trading day with a
// 60-minute service.
func goldenRepo() *mockRepo {
	return &mockRepo{
		getStoreStore:      defaultStore(),
		getBusinessHoursBH: &BusinessHours{IsOpen: true, OpenTime: "09:00:00", CloseTime: "17:00:00"},
		getServiceSvc:      &SalonService{DurationMin: 60},
	}
}

func goldenReq() GetAvailableSlotsRequest {
	r := validSlotsReq()
	r.Date = "2027-03-01" // Monday, far future: same-day notice cannot apply
	return r
}

// ── The baseline ──────────────────────────────────────────────────────────────

// An empty day. 09:00-17:00 with a 60-minute service on a 15-minute grid
// gives 29 slots: the last one starts at 16:00 and ends exactly at close.
//
// Note there is NO early-bird slot here even though the cutoff is 09:00: the
// comparison is strictly Before, and the day opens exactly at the cutoff, so
// nothing qualifies. See the next test for where the mark does appear.
//
// The final boundary is the one worth pinning. Slot generation loops while
// `current+duration <= closeTime`, so a refactor that turns that into a
// strict `<` silently loses the 16:00 slot and no existing test notices.
func TestGolden_EmptyDay(t *testing.T) {
	svc := newTestService(goldenRepo())

	slots, err := svc.GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	got := goldenOf(t, slots)
	assert.Equal(t, 29, len(slots), "09:00-17:00, 60min service, 15min grid")
	assert.True(t, strings.HasPrefix(got, "09:00-10:00 09:15-10:15 09:30-10:30"), "got: %s", got)
	assert.True(t, strings.HasSuffix(got, "15:45-16:45 16:00-17:00"),
		"the last slot must end exactly at close, not before it. got tail: %s", got[len(got)-40:])
}

// The early-bird cutoff is 09:00 and the comparison is strictly Before, so
// exactly one slot qualifies - the 09:00 one does NOT, because 09:00 is not
// before 09:00. Pinned because an off-by-one here changes what customers are
// charged.
func TestGolden_EarlyBirdBoundaryIsExclusive(t *testing.T) {
	repo := goldenRepo()
	repo.getBusinessHoursBH = &BusinessHours{IsOpen: true, OpenTime: "08:00:00", CloseTime: "12:00:00"}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	got := goldenOf(t, slots)
	assert.Equal(t, "08:00-09:00* 08:15-09:15* 08:30-09:30* 08:45-09:45* 09:00-10:00 09:15-10:15 "+
		"09:30-10:30 09:45-10:45 10:00-11:00 10:15-11:15 10:30-11:30 10:45-11:45 11:00-12:00", got)
}

// ── Existing bookings ─────────────────────────────────────────────────────────

// A booking from 11:00-12:00 removes every slot that overlaps it. With a
// 60-minute service that is the four starting 10:15 through 11:45 - a slot
// starting 10:00 ends exactly at 11:00 and survives, because Overlaps is
// half-open.
//
// That touching boundary is the single most refactor-sensitive fact in this
// file: any interval type that treats [10:00,11:00) and [11:00,12:00) as
// overlapping silently destroys back-to-back bookings.
func TestGolden_BookingBlocksOverlapButNotTouching(t *testing.T) {
	repo := goldenRepo()
	repo.getArtistBookingsBookings = []*Booking{{
		StartTime: beirutTime(2027, time.March, 1, 11, 0),
		EndTime:   beirutTime(2027, time.March, 1, 12, 0),
	}}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	got := goldenOf(t, slots)
	assert.Contains(t, got, "10:00-11:00", "a slot ending exactly when a booking starts must survive")
	assert.Contains(t, got, "12:00-13:00", "a slot starting exactly when a booking ends must survive")
	for _, blocked := range []string{"10:15-11:15", "10:30-11:30", "10:45-11:45",
		"11:00-12:00", "11:15-12:15", "11:30-12:30", "11:45-12:45"} {
		assert.NotContains(t, got, blocked, "overlapping slot %s must be removed", blocked)
	}
	assert.Equal(t, 22, len(slots),
		"29 baseline minus the 7 whose 60-minute span crosses 11:00-12:00")
}

// Two bookings that abut produce one continuous unavailable stretch. Pinned
// because merging adjacent intervals is exactly the kind of "tidy up" a set
// type invites, and merging must not change which slots are offered.
func TestGolden_AdjacentBookingsBlockContinuously(t *testing.T) {
	repo := goldenRepo()
	repo.getArtistBookingsBookings = []*Booking{
		{StartTime: beirutTime(2027, time.March, 1, 11, 0), EndTime: beirutTime(2027, time.March, 1, 12, 0)},
		{StartTime: beirutTime(2027, time.March, 1, 12, 0), EndTime: beirutTime(2027, time.March, 1, 13, 0)},
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	got := goldenOf(t, slots)
	assert.Contains(t, got, "10:00-11:00")
	assert.Contains(t, got, "13:00-14:00")
	assert.NotContains(t, got, "12:00-13:00")
	assert.NotContains(t, got, "11:00-12:00")
}

// Overlapping bookings must not double-block or produce a wider gap than the
// union of the two. A set type that concatenated rather than unioned would
// pass a naive "is it blocked" check and fail this.
func TestGolden_OverlappingBookingsBlockTheirUnionOnly(t *testing.T) {
	repo := goldenRepo()
	repo.getArtistBookingsBookings = []*Booking{
		{StartTime: beirutTime(2027, time.March, 1, 11, 0), EndTime: beirutTime(2027, time.March, 1, 12, 30)},
		{StartTime: beirutTime(2027, time.March, 1, 12, 0), EndTime: beirutTime(2027, time.March, 1, 13, 0)},
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	got := goldenOf(t, slots)
	assert.Contains(t, got, "10:00-11:00", "union starts at 11:00")
	assert.Contains(t, got, "13:00-14:00", "union ends at 13:00")
	assert.NotContains(t, got, "12:30-13:30")
}

// ── Travel buffers (the T5.7 proof) ───────────────────────────────────────────

// A cross-store booking blocks its own span PLUS a buffer either side. This
// is the complication Sprint 5 must be able to express as a typed interval;
// if the abstraction cannot hold it, the abstraction is wrong.
//
// defaultStore has a 150-minute weekday buffer, and 2027-03-01 is a Monday.
func TestGolden_CrossStoreTravelBuffer(t *testing.T) {
	repo := goldenRepo()
	elsewhere := []*Booking{{
		StoreID:   uuid.New(),
		StartTime: beirutTime(2027, time.March, 1, 13, 0),
		EndTime:   beirutTime(2027, time.March, 1, 14, 0),
	}}
	// The SAME booking must appear in both results. GetArtistBookingsForDate
	// has no store filter, so in production every cross-store booking is also
	// in the artist's own list: step 4 blocks its span, step 5 only adds the
	// buffers around it. Setting one without the other describes a state the
	// database cannot produce.
	repo.getArtistBookingsBookings = elsewhere
	repo.getCrossStoreBookings = elsewhere
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	got := goldenOf(t, slots)
	// Blocked is one continuous stretch: 10:30 (outbound buffer) through
	// 16:30 (end of return buffer), with the booking's own span inside it.
	assert.Equal(t, "09:00-10:00 09:15-10:15 09:30-10:30", got,
		"only the morning survives; 16:30 + 60min would run past the 17:00 close")
	assert.Equal(t, 3, len(slots))
}

// An artist-specific buffer override replaces the store default entirely -
// it does not add to it.
func TestGolden_ArtistBufferOverrideReplacesStoreDefault(t *testing.T) {
	repo := goldenRepo()
	elsewhere := []*Booking{{
		StoreID:   uuid.New(),
		StartTime: beirutTime(2027, time.March, 1, 13, 0),
		EndTime:   beirutTime(2027, time.March, 1, 14, 0),
	}}
	repo.getArtistBookingsBookings = elsewhere
	repo.getCrossStoreBookings = elsewhere
	repo.getArtistStoreBufferBuf = &ArtistStoreBuffer{WeekdayBufferMin: 30, WeekendBufferMin: 30}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	got := goldenOf(t, slots)
	assert.Contains(t, got, "11:30-12:30", "30min buffer before 13:00 leaves this free")
	assert.NotContains(t, got, "11:45-12:45", "overlaps the 12:30-13:00 outbound buffer")
	assert.Contains(t, got, "14:30-15:30", "30min return buffer ends at 14:30")
	assert.Equal(t, 18, len(slots),
		"11 before the blocked stretch (09:00-11:30) plus 7 after it (14:30-16:00)")
}

// ── Closed days ───────────────────────────────────────────────────────────────

// Every "no slots" path must return an EMPTY list, never a nil that a caller
// distinguishes from empty, and never an error.
func TestGolden_ClosedPathsAllReturnNoSlots(t *testing.T) {
	cases := map[string]func(*mockRepo){
		"weekday not trading": func(r *mockRepo) { r.getBusinessHoursBH = &BusinessHours{IsOpen: false} },
		"dated exception":     func(r *mockRepo) { r.getBusinessHoursExceptionEx = &BusinessHoursException{IsClosed: true} },
		"no hours configured": func(r *mockRepo) { r.getBusinessHoursBH = nil },
	}
	for name, mutate := range cases {
		repo := goldenRepo()
		mutate(repo)
		svc := newTestService(repo)

		slots, err := svc.GetAvailableSlots(context.Background(), goldenReq())

		require.NoError(t, err, name)
		assert.Empty(t, slots, name)
	}
}

// A service longer than the trading window yields nothing rather than a
// negative-length slot or a panic.
func TestGolden_ServiceLongerThanTradingDay(t *testing.T) {
	repo := goldenRepo()
	repo.getServiceSvc = &SalonService{DurationMin: 600} // 10h into an 8h day
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	assert.Empty(t, slots)
}
