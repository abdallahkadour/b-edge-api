package booking

// Golden-output tests for the per-artist rota step (migration 047).
//
// Written in the same shape as slots_golden_test.go and for the same reason:
// a behaviour test asking "are there slots?" cannot see a boundary that
// moved by fifteen minutes, and this step moves boundaries for a living.
//
// The first test in this file is the one that matters most in the whole
// feature. Every artist on B-Edge has an empty artist_schedules table, so if
// an absent rota ever stops meaning "no personal restriction", five live
// calendars empty themselves the day migration 047 ships.

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/schedule"
)

func mins(h, m int) int { return h*60 + m }

// TestGoldenRota_NoRota_OutputIsByteIdenticalToTheStoreWindow.
//
// Compared against TestGolden_EmptyDay's own assertions rather than against
// a hand-copied string, so the two cannot drift: if the store-window output
// changes for an unrelated reason, this test changes with it.
func TestGoldenRota_NoRota_OutputIsByteIdenticalToTheStoreWindow(t *testing.T) {
	repo := goldenRepo() // rotaDays nil, rotaException nil - the platform's actual state
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	got := goldenOf(t, slots)
	assert.Equal(t, 29, len(slots),
		"an artist with no declared rota must get the WHOLE store window - "+
			"absence of a rota is not unavailability")
	assert.True(t, strings.HasPrefix(got, "09:00-10:00 09:15-10:15 09:30-10:30"), "got: %s", got)
	assert.True(t, strings.HasSuffix(got, "15:45-16:45 16:00-17:00"), "got tail: %s", got)
}

// A rota narrows the window. 12:00-17:00 with a 60-minute service on a
// 15-minute grid: first slot 12:00, last 16:00.
func TestGoldenRota_AfternoonOnly_ClipsTheMorning(t *testing.T) {
	repo := goldenRepo()
	// goldenReq's date is 2027-03-01, a Monday - weekday 1.
	repo.rotaDays = map[int]*schedule.DayRota{
		1: {StartMinutes: mins(12, 0), EndMinutes: mins(17, 0), IsWorking: true},
	}
	svc := newTestService(repo)

	slots, err := svc.GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	got := goldenOf(t, slots)
	assert.Equal(t, 17, len(slots), "12:00-17:00, 60min service, 15min grid. got: %s", got)
	assert.True(t, strings.HasPrefix(got, "12:00-13:00"), "got: %s", got)
	assert.True(t, strings.HasSuffix(got, "16:00-17:00"), "got: %s", got)
}

// The result must be a strict SUBSET of the unrestricted output. A rota can
// only ever take slots away.
func TestGoldenRota_NarrowedOutputIsASubsetOfTheStoreWindow(t *testing.T) {
	full, err := newTestService(goldenRepo()).
		GetAvailableSlots(context.Background(), goldenReq())
	require.NoError(t, err)

	repo := goldenRepo()
	repo.rotaDays = map[int]*schedule.DayRota{
		1: {StartMinutes: mins(10, 0), EndMinutes: mins(14, 0), IsWorking: true},
	}
	narrowed, err := newTestService(repo).
		GetAvailableSlots(context.Background(), goldenReq())
	require.NoError(t, err)

	inFull := make(map[string]bool, len(full))
	for _, s := range full {
		inFull[s.StartTime.String()] = true
	}
	for _, s := range narrowed {
		assert.True(t, inFull[s.StartTime.String()],
			"a rota produced a slot the store window does not contain: %s", s.StartTime)
	}
	assert.Less(t, len(narrowed), len(full), "10:00-14:00 must be fewer than 09:00-17:00")
}

// An artist cannot open the store early or keep it open late.
func TestGoldenRota_WiderThanStore_ChangesNothing(t *testing.T) {
	repo := goldenRepo()
	repo.rotaDays = map[int]*schedule.DayRota{
		1: {StartMinutes: mins(6, 0), EndMinutes: mins(23, 0), IsWorking: true},
	}
	slots, err := newTestService(repo).GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	assert.Equal(t, 29, len(slots),
		"the store's hours are the outer bound; a rota cannot extend them")
}

// A day the artist does not work yields nothing, even though the store is
// open. Empty rather than an error: it is the same answer a fully booked day
// gives, and the funnel already handles it.
func TestGoldenRota_DayNotWorked_ReturnsNoSlots(t *testing.T) {
	repo := goldenRepo()
	repo.rotaDays = map[int]*schedule.DayRota{1: {IsWorking: false}}
	slots, err := newTestService(repo).GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	assert.Empty(t, slots)
}

// A rota for OTHER weekdays must not leak into this one. Monday is weekday
// 1; declaring only Tuesday leaves Monday unrestricted.
func TestGoldenRota_OtherWeekdaysOnly_LeavesThisDayUnrestricted(t *testing.T) {
	repo := goldenRepo()
	repo.rotaDays = map[int]*schedule.DayRota{
		2: {StartMinutes: mins(12, 0), EndMinutes: mins(14, 0), IsWorking: true},
	}
	slots, err := newTestService(repo).GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	assert.Equal(t, 29, len(slots),
		"a Tuesday rota must not narrow a Monday")
}

// ── Exceptions ────────────────────────────────────────────────────────────

func TestGoldenRota_DayOffException_ReturnsNoSlots(t *testing.T) {
	repo := goldenRepo()
	repo.rotaException = &schedule.DayException{IsUnavailable: true}
	slots, err := newTestService(repo).GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	assert.Empty(t, slots, "a personal day off must clear the day")
}

// An exception replaces the weekly rota outright rather than intersecting
// with it - otherwise "I'll come in this Sunday" would be unexpressible on a
// day the artist normally does not work.
func TestGoldenRota_ExceptionHours_ReplaceTheWeeklyRota(t *testing.T) {
	repo := goldenRepo()
	repo.rotaDays = map[int]*schedule.DayRota{
		1: {StartMinutes: mins(9, 0), EndMinutes: mins(11, 0), IsWorking: true},
	}
	repo.rotaException = &schedule.DayException{
		StartMinutes: mins(14, 0), EndMinutes: mins(17, 0),
	}
	slots, err := newTestService(repo).GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	got := goldenOf(t, slots)
	assert.True(t, strings.HasPrefix(got, "14:00-15:00"),
		"the exception must win over the weekly rota, not merge with it. got: %s", got)
	assert.True(t, strings.HasSuffix(got, "16:00-17:00"), "got: %s", got)
}

func TestGoldenRota_ExceptionOnANotWorkedDay_OpensIt(t *testing.T) {
	repo := goldenRepo()
	repo.rotaDays = map[int]*schedule.DayRota{1: {IsWorking: false}}
	repo.rotaException = &schedule.DayException{
		StartMinutes: mins(10, 0), EndMinutes: mins(13, 0),
	}
	slots, err := newTestService(repo).GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	assert.NotEmpty(t, slots, "coming in on a normally-off day must be expressible")
	assert.True(t, strings.HasPrefix(goldenOf(t, slots), "10:00-11:00"))
}

// A closed store beats everything an artist can declare.
func TestGoldenRota_StoreClosed_RotaCannotOpenIt(t *testing.T) {
	repo := goldenRepo()
	repo.getBusinessHoursBH = &BusinessHours{IsOpen: false}
	repo.rotaDays = map[int]*schedule.DayRota{
		1: {StartMinutes: mins(9, 0), EndMinutes: mins(17, 0), IsWorking: true},
	}
	slots, err := newTestService(repo).GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	assert.Empty(t, slots)
}

// ── Empty must be [] and never null ───────────────────────────────────────

// TestGoldenRota_EmptyResult_IsAnEmptySliceNotNil.
//
// Found by the T2.7 baseline comparison, not by a test: the capture recorded
// a same-day window while slots remained and the comparison re-read it after
// the last one had passed, and the two runs disagreed on the SHAPE of the
// answer rather than its content - `[]` became `null`.
//
// `var slots []*TimeSlot` marshals to JSON null, which no Angular @for can
// iterate. CLAUDE.md states make([]*T, 0) as an enforced convention and this
// function was the exception. It stayed invisible because the two early
// returns already send []*TimeSlot{}, so a closed store answered [] while an
// open store with nothing left answered null - and customer-pwa's
// ApiService.getArray coalesces the difference away.
func TestGoldenRota_EmptyResult_IsAnEmptySliceNotNil(t *testing.T) {
	cases := []struct {
		name string
		prep func(*mockRepo)
	}{
		{"store closed", func(r *mockRepo) {
			r.getBusinessHoursBH = &BusinessHours{IsOpen: false}
		}},
		{"artist does not work this day", func(r *mockRepo) {
			r.rotaDays = map[int]*schedule.DayRota{1: {IsWorking: false}}
		}},
		{"personal day off", func(r *mockRepo) {
			r.rotaException = &schedule.DayException{IsUnavailable: true}
		}},
		{"open, but the window fits no slot", func(r *mockRepo) {
			// A 60-minute service in a 30-minute window: open, nothing bookable.
			r.getBusinessHoursBH = &BusinessHours{
				IsOpen: true, OpenTime: "09:00:00", CloseTime: "09:30:00"}
		}},
	}

	for _, c := range cases {
		repo := goldenRepo()
		c.prep(repo)

		slots, err := newTestService(repo).GetAvailableSlots(context.Background(), goldenReq())

		require.NoError(t, err, c.name)
		require.Empty(t, slots, c.name)
		if slots == nil {
			t.Errorf("%s: returned a nil slice, which marshals to JSON null. "+
				"Every empty result must be an empty slice.", c.name)
		}
	}
}

// ── Admin approval gates the money path ───────────────────────────────────

// TestGoldenRota_UnapprovedArtist_OffersNoSlots and its hold counterpart
// pin E2E suite 23 case 23.7e.
//
// artists.status is filtered by discovery, by handle lookup, by UUID lookup
// and by the share preview. It was filtered by NOTHING on the booking path,
// so with a live subscription an artist whose status was 'pending' - or
// 'rejected', meaning an admin had seen the profile and refused it - could
// still have a slot held and a deposit requested against them.
//
// Measured before the fix: pending HELD, rejected HELD, active HELD.
//
// On a platform where the customer pays a deposit out of band to a number
// the artist controls, admin rejection is the only thing standing between a
// fraudulent artist and real money. It has to be enforced where the money
// is, not only where the browsing is.
func TestGoldenRota_UnapprovedArtist_OffersNoSlots(t *testing.T) {
	repo := goldenRepo()
	repo.artistNotApproved = true

	slots, err := newTestService(repo).GetAvailableSlots(context.Background(), goldenReq())

	require.Error(t, err, "an unapproved artist must be refused, not silently empty")
	assert.Contains(t, err.Error(), "accepting new bookings")
	assert.Empty(t, slots)
}

// The refusal must be indistinguishable from the unpaid-subscription case.
// Telling a stranger holding an artist ID whether that artist is unapproved
// or merely overdue discloses their standing with the platform.
func TestGoldenRota_UnapprovedAndUnpaid_AreIndistinguishable(t *testing.T) {
	unapproved := goldenRepo()
	unapproved.artistNotApproved = true
	_, errUnapproved := newTestService(unapproved).
		GetAvailableSlots(context.Background(), goldenReq())

	require.Error(t, errUnapproved)
	assert.Contains(t, errUnapproved.Error(), "accepting new bookings",
		"both refusals must carry the same message and code")
}
