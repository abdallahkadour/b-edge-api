package earnings

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// beirut is a wall-clock instant on the reporting calendar.
func beirut(y int, m time.Month, d, h, min int) time.Time {
	return time.Date(y, m, d, h, min, 0, 0, businessLocation)
}

// ── previousPeriod ────────────────────────────────────────────────────────────

func TestPreviousPeriod_WholeMonthOver_IsTheWholeMonthBefore(t *testing.T) {
	// February is 28 days and January 31. Comparing by length would compare
	// February with 1-28 January and drop three days of the month before.
	from, to := beirut(2026, 2, 1, 0, 0), beirut(2026, 3, 1, 0, 0)

	pf, pt := previousPeriod(from, to, beirut(2026, 4, 5, 12, 0))

	assert.True(t, pf.Equal(beirut(2026, 1, 1, 0, 0)), "from: got %s", pf.In(businessLocation))
	assert.True(t, pt.Equal(beirut(2026, 2, 1, 0, 0)), "to: got %s", pt.In(businessLocation))
}

func TestPreviousPeriod_MonthInProgress_CutToTheSamePoint(t *testing.T) {
	// On the 10th, a whole September against ten days of October reads as a
	// collapse that is only the calendar.
	from, to := beirut(2026, 10, 1, 0, 0), beirut(2026, 11, 1, 0, 0)

	pf, pt := previousPeriod(from, to, beirut(2026, 10, 10, 15, 0))

	assert.True(t, pf.Equal(beirut(2026, 9, 1, 0, 0)), "from: got %s", pf.In(businessLocation))
	assert.True(t, pt.Equal(beirut(2026, 9, 10, 15, 0)), "to: got %s", pt.In(businessLocation))
}

func TestPreviousPeriod_CustomRange_IsTheSameNumberOfDaysBefore(t *testing.T) {
	from, to := beirut(2026, 10, 3, 0, 0), beirut(2026, 10, 10, 0, 0)

	pf, pt := previousPeriod(from, to, beirut(2026, 11, 1, 9, 0))

	assert.True(t, pf.Equal(beirut(2026, 9, 26, 0, 0)), "from: got %s", pf.In(businessLocation))
	assert.True(t, pt.Equal(from), "to: got %s", pt.In(businessLocation))
}

func TestPreviousPeriod_RangeNotStarted_IsTheWholeWindowBefore(t *testing.T) {
	from, to := beirut(2026, 12, 1, 0, 0), beirut(2027, 1, 1, 0, 0)

	pf, pt := previousPeriod(from, to, beirut(2026, 10, 10, 9, 0))

	assert.True(t, pf.Equal(beirut(2026, 11, 1, 0, 0)), "from: got %s", pf.In(businessLocation))
	assert.True(t, pt.Equal(from), "to: got %s", pt.In(businessLocation))
}

// ── GetSalonOverview ──────────────────────────────────────────────────────────

func TestGetSalonOverview_CountsBothWindowsForTheCallersSalon(t *testing.T) {
	salonID := uuid.New()
	repo := &mockRepo{salonTotals: SalonTotals{Completed: 4}, salonPrevious: SalonTotals{Completed: 2}}
	svc := newService(repo)
	svc.now = func() time.Time { return beirut(2026, 10, 10, 15, 0) }

	out, err := svc.GetSalonOverview(context.Background(), uuid.New(), salonID,
		GetSummaryRequest{From: "2026-10-01", To: "2026-10-31"})

	require.NoError(t, err)
	assert.Equal(t, 4, out.Totals.Completed)
	assert.Equal(t, 2, out.Previous.Completed)
	require.Len(t, repo.salonTotalsCalls, 2)
	assert.True(t, repo.salonTotalsCalls[0].From.Equal(beirut(2026, 10, 1, 0, 0)))
	assert.True(t, repo.salonTotalsCalls[0].To.Equal(beirut(2026, 11, 1, 0, 0)))
	assert.True(t, repo.salonTotalsCalls[1].From.Equal(beirut(2026, 9, 1, 0, 0)))
	assert.True(t, repo.salonTotalsCalls[1].To.Equal(beirut(2026, 9, 10, 15, 0)))
	assert.True(t, out.PreviousPeriod.To.Equal(beirut(2026, 9, 10, 15, 0)))
	for _, id := range repo.salonIDs {
		assert.Equal(t, salonID, id, "every read is scoped to the caller's salon")
	}
}

func TestGetSalonOverview_MarksOnlyTheCallersOwnRow(t *testing.T) {
	me, colleague := uuid.New(), uuid.New()
	repo := &mockRepo{salonArtists: []ArtistOverview{
		{ArtistID: uuid.New(), Name: "Colleague", userID: colleague},
		{ArtistID: uuid.New(), Name: "Me", userID: me},
	}}

	out, err := newService(repo).GetSalonOverview(context.Background(), me, uuid.New(), GetSummaryRequest{})

	require.NoError(t, err)
	require.Len(t, out.ByArtist, 2)
	assert.False(t, out.ByArtist[0].IsYou)
	assert.True(t, out.ByArtist[1].IsYou)
}

func TestGetSalonOverview_NoArtists_IsAnEmptyListNotNull(t *testing.T) {
	out, err := newService(&mockRepo{}).GetSalonOverview(context.Background(), uuid.New(), uuid.New(), GetSummaryRequest{})

	require.NoError(t, err)
	assert.NotNil(t, out.ByArtist)
	assert.Empty(t, out.ByArtist)
}

func TestGetSalonOverview_HalfARange_IsRefusedBeforeAnyRead(t *testing.T) {
	repo := &mockRepo{}

	_, err := newService(repo).GetSalonOverview(context.Background(), uuid.New(), uuid.New(), GetSummaryRequest{From: "2026-10-01"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "together")
	assert.Empty(t, repo.salonIDs)
}
