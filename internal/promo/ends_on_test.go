package promo

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// "Ends 1 December" is a day on the SALON's calendar. The dashboard used to
// turn it into an instant in the device's timezone, so an artist whose laptop
// was on UTC made a code that ran three hours into the 2nd in Beirut, and the
// last second of the day was lost either way. The server now resolves the
// day, in the timezone of the salon's store.

func createEndingOn(t *testing.T, tz, day string) (*mockRepo, *DiscountResponse) {
	t.Helper()
	repo := &mockRepo{timezone: tz}
	res, err := newSvc(repo).Create(context.Background(), uuid.New(), CreateDiscountRequest{
		Code: "WINTER", Kind: KindPercentage, Value: "10", EndsOn: &day,
	})
	require.NoError(t, err)
	require.NotNil(t, repo.created)
	require.NotNil(t, repo.created.EndsAt)
	return repo, res
}

func utc(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestCreate_EndsOn_StopsAtTheNextMidnightInTheSalon(t *testing.T) {
	// Beirut in winter is UTC+2: the 1st ends at 22:00 UTC, and the check is
	// exclusive, so every moment of the 1st is still inside.
	repo, res := createEndingOn(t, "Asia/Beirut", "2026-12-01")

	assert.Equal(t, utc("2026-12-01T22:00:00Z"), repo.created.EndsAt.UTC())
	require.NotNil(t, res.EndsOn)
	assert.Equal(t, "2026-12-01", *res.EndsOn, "the artist sees back the day she picked")
}

func TestCreate_EndsOn_FollowsSummerTime(t *testing.T) {
	repo, _ := createEndingOn(t, "Asia/Beirut", "2026-10-15")

	assert.Equal(t, utc("2026-10-15T21:00:00Z"), repo.created.EndsAt.UTC(), "UTC+3 in summer")
}

func TestCreate_EndsOn_TheNightTheClocksGoForward(t *testing.T) {
	// Midnight on 28 March 2027 does not exist in Beirut - clocks jump to
	// 01:00. The 27th must still end at the first instant of the 28th.
	repo, _ := createEndingOn(t, "Asia/Beirut", "2027-03-27")

	assert.Equal(t, utc("2027-03-27T22:00:00Z"), repo.created.EndsAt.UTC())
}

func TestCreate_EndsOn_UsesTheSalonsTimezoneNotAFixedOne(t *testing.T) {
	repo, res := createEndingOn(t, "America/New_York", "2026-12-01")

	assert.Equal(t, utc("2026-12-02T05:00:00Z"), repo.created.EndsAt.UTC())
	assert.Equal(t, "2026-12-01", *res.EndsOn)
}

func TestCreate_EndsOnAndEndsAtTogether_Rejected(t *testing.T) {
	// Two answers to one question: refuse rather than pick one silently.
	repo := &mockRepo{timezone: "Asia/Beirut"}
	day, at := "2026-12-01", utc("2026-12-05T10:00:00Z")

	_, err := newSvc(repo).Create(context.Background(), uuid.New(), CreateDiscountRequest{
		Code: "WINTER", Kind: KindPercentage, Value: "10", EndsOn: &day, EndsAt: &at,
	})

	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, 422, appErr.HTTPStatus)
	assert.Nil(t, repo.created)
}

func TestCreate_MalformedEndsOn_Rejected(t *testing.T) {
	for _, day := range []string{"01/12/2026", "2026-13-01", "2026-12-01T00:00:00Z", "tomorrow"} {
		repo := &mockRepo{timezone: "Asia/Beirut"}
		_, err := newSvc(repo).Create(context.Background(), uuid.New(), CreateDiscountRequest{
			Code: "WINTER", Kind: KindPercentage, Value: "10", EndsOn: &day,
		})
		var appErr *apperror.AppError
		require.ErrorAs(t, err, &appErr, "ends_on %q", day)
		assert.Equal(t, 422, appErr.HTTPStatus, "ends_on %q", day)
		assert.Nil(t, repo.created, "ends_on %q", day)
	}
}

func TestCreate_TimezoneLookupFails_NotCreated(t *testing.T) {
	// Guessing a timezone would make the code end at the wrong hour, which
	// is the bug this replaces.
	repo := &mockRepo{timezoneErr: errors.New("connection reset")}
	day := "2026-12-01"

	_, err := newSvc(repo).Create(context.Background(), uuid.New(), CreateDiscountRequest{
		Code: "WINTER", Kind: KindPercentage, Value: "10", EndsOn: &day,
	})

	require.Error(t, err)
	assert.Nil(t, repo.created)
}

func TestListBySalon_ShowsTheLastDayOnTheSalonsCalendar(t *testing.T) {
	made := utc("2026-12-01T22:00:00Z")
	// Made on a laptop set to UTC before this fix: it really does run until
	// 01:59:59 on the 2nd in Beirut, and the list says so.
	legacy := utc("2026-12-01T23:59:59Z")
	repo := &mockRepo{timezone: "Asia/Beirut", list: []*DiscountResponse{
		{Code: "A", EndsAt: &made}, {Code: "B", EndsAt: &legacy}, {Code: "C"},
	}}

	list, err := newSvc(repo).ListBySalon(context.Background(), uuid.New())

	require.NoError(t, err)
	require.Len(t, list, 3)
	require.NotNil(t, list[0].EndsOn)
	assert.Equal(t, "2026-12-01", *list[0].EndsOn)
	require.NotNil(t, list[1].EndsOn)
	assert.Equal(t, "2026-12-02", *list[1].EndsOn)
	assert.Nil(t, list[2].EndsOn, "no end date, no last day")
}

func TestUpdate_ResponseCarriesTheLastDay(t *testing.T) {
	end := utc("2026-12-01T22:00:00Z")
	repo := &mockRepo{timezone: "Asia/Beirut", updated: &Discount{Code: "A", EndsAt: &end}}
	off := false

	res, err := newSvc(repo).Update(context.Background(), uuid.New(), uuid.New(), UpdateDiscountRequest{IsActive: &off})

	require.NoError(t, err)
	require.NotNil(t, res.EndsOn)
	assert.Equal(t, "2026-12-01", *res.EndsOn)
}
