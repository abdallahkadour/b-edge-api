// Package earnings implements the earnings domain for B-Edge,
// providing revenue aggregation and breakdown for the artist dashboard.
package earnings

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock repository ───────────────────────────────────────────────────────────

type periodSummaryCall struct {
	From, To time.Time
}

type mockRepo struct {
	artistID        uuid.UUID
	artistErr       error
	periodRevenue   decimal.Decimal
	periodBookings  int
	periodDeposits  decimal.Decimal
	periodErr       error
	dailyRows       []dailyEarningsRow
	dailyErr        error
	dailyFromCalled time.Time
	dailyToCalled   time.Time
	serviceRows     []serviceEarningsRow
	serviceErr      error
	// periodSummaryCalls records every GetPeriodSummary call's from/to, in
	// order (main period, today, this week, this month). Lets tests verify
	// which boundaries each stat actually queried, rather than only seeing
	// the single canned return value below.
	periodSummaryCalls []periodSummaryCall
}

func (m *mockRepo) GetArtistIDByUserID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return m.artistID, m.artistErr
}

func (m *mockRepo) GetPeriodSummary(_ context.Context, _ uuid.UUID, from, to time.Time) (decimal.Decimal, int, decimal.Decimal, error) {
	m.periodSummaryCalls = append(m.periodSummaryCalls, periodSummaryCall{From: from, To: to})
	return m.periodRevenue, m.periodBookings, m.periodDeposits, m.periodErr
}

func (m *mockRepo) GetDailyBreakdown(_ context.Context, _ uuid.UUID, from, to time.Time) ([]dailyEarningsRow, error) {
	m.dailyFromCalled = from
	m.dailyToCalled = to
	return m.dailyRows, m.dailyErr
}

func (m *mockRepo) GetServiceBreakdown(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]serviceEarningsRow, error) {
	return m.serviceRows, m.serviceErr
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func dec(s string) decimal.Decimal {
	d, _ := decimal.NewFromString(s)
	return d
}

func newService(repo Repository) *Service {
	return NewService(repo)
}

// ── Tests ─────────────────────────────────────────────────────────────────────

func TestGetSummary_DefaultPeriodCurrentMonth(t *testing.T) {
	artistID := uuid.New()
	userID := uuid.New()
	serviceID := uuid.New()

	repo := &mockRepo{
		artistID:       artistID,
		periodRevenue:  dec("1840.00"),
		periodBookings: 12,
		periodDeposits: dec("240.00"),
		dailyRows: []dailyEarningsRow{
			{Day: time.Now().UTC().AddDate(0, 0, -1), Revenue: dec("120.00")},
			{Day: time.Now().UTC(), Revenue: dec("200.00")},
		},
		serviceRows: []serviceEarningsRow{
			{ServiceID: serviceID, ServiceName: "Bridal Makeup", BookingsCount: 3, Revenue: dec("600.00")},
			{ServiceID: uuid.New(), ServiceName: "Hair Blowout", BookingsCount: 5, Revenue: dec("400.00")},
		},
	}

	svc := newService(repo)
	result, err := svc.GetSummary(context.Background(), userID, GetSummaryRequest{})

	require.NoError(t, err)
	assert.Equal(t, dec("1840.00"), result.TotalRevenue)
	assert.Equal(t, 12, result.TotalBookings)
	assert.Equal(t, dec("240.00"), result.TotalDeposits)
	assert.Len(t, result.DailyBreakdown, 2)
	assert.Len(t, result.ByService, 2)
	assert.Equal(t, "Bridal Makeup", result.ByService[0].ServiceName)

	// Default period should be current calendar month, computed from the
	// business's local timezone (Beirut), not raw UTC.
	now := beirutNow()
	expectedFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, businessLocation).UTC()
	assert.Equal(t, expectedFrom, result.Period.From)
}

func TestGetSummary_AverageBookingValue(t *testing.T) {
	artistID := uuid.New()
	userID := uuid.New()

	repo := &mockRepo{
		artistID:       artistID,
		periodRevenue:  dec("300.00"),
		periodBookings: 3,
		periodDeposits: dec("60.00"),
	}

	svc := newService(repo)
	result, err := svc.GetSummary(context.Background(), userID, GetSummaryRequest{})

	require.NoError(t, err)
	// 300 / 3 = 100
	// decimal.Div() always returns a result at DivisionPrecision (exponent -16),
	// while dec("100") parses to exponent 0. Same value, different internal
	// representation, so assert.Equal (reflect.DeepEqual) fails here even
	// though the numbers are equal. Use decimal's own semantic comparison.
	assert.True(t, dec("100").Equal(result.AverageBookingValue),
		"expected 100, got %s", result.AverageBookingValue.String())
}

func TestGetSummary_ZeroBookings_ZeroAverage(t *testing.T) {
	artistID := uuid.New()
	userID := uuid.New()

	repo := &mockRepo{
		artistID:       artistID,
		periodRevenue:  decimal.Zero,
		periodBookings: 0,
		periodDeposits: decimal.Zero,
	}

	svc := newService(repo)
	result, err := svc.GetSummary(context.Background(), userID, GetSummaryRequest{})

	require.NoError(t, err)
	// No divide-by-zero panic; average should be zero
	assert.Equal(t, decimal.Zero, result.AverageBookingValue)
	assert.Equal(t, 0, result.TotalBookings)
}

func TestGetSummary_CustomDateRange(t *testing.T) {
	artistID := uuid.New()
	userID := uuid.New()

	repo := &mockRepo{
		artistID:       artistID,
		periodRevenue:  dec("500.00"),
		periodBookings: 5,
		periodDeposits: dec("100.00"),
	}

	svc := newService(repo)
	result, err := svc.GetSummary(context.Background(), userID, GetSummaryRequest{
		From: "2026-06-01",
		To:   "2026-06-30",
	})

	require.NoError(t, err)
	// Period.From/To are UTC instants; convert back to Beirut local time to
	// check the calendar dates that were actually requested.
	assert.Equal(t, "2026-06-01", result.Period.From.In(businessLocation).Format("2006-01-02"))
	// to is exclusive: 2026-07-01
	assert.Equal(t, "2026-07-01", result.Period.To.In(businessLocation).Format("2006-01-02"))
	assert.Equal(t, dec("500.00"), result.TotalRevenue)
}

// TestGetSummary_DailyBreakdown_UsesRequestedPeriod guards against a
// regression where GetDailyBreakdown was called with a hardcoded last-7-days
// window instead of the requested from/to - so a custom historical range
// would show correct totals but an empty daily chart. GetDailyBreakdown must
// receive the exact same period as GetPeriodSummary.
func TestGetSummary_DailyBreakdown_UsesRequestedPeriod(t *testing.T) {
	artistID := uuid.New()
	userID := uuid.New()

	repo := &mockRepo{
		artistID:       artistID,
		periodRevenue:  dec("500.00"),
		periodBookings: 5,
		periodDeposits: dec("100.00"),
	}

	svc := newService(repo)
	_, err := svc.GetSummary(context.Background(), userID, GetSummaryRequest{
		From: "2026-06-01",
		To:   "2026-06-30",
	})

	require.NoError(t, err)
	// dailyFromCalled/dailyToCalled are UTC instants; convert back to Beirut
	// local time to check the calendar dates that were actually requested
	// same reasoning as TestGetSummary_CustomDateRange and the
	// TestParseDateRange_* tests above.
	assert.Equal(t, "2026-06-01", repo.dailyFromCalled.In(businessLocation).Format("2006-01-02"),
		"GetDailyBreakdown must use the requested period, not a fixed window")
	assert.Equal(t, "2026-07-01", repo.dailyToCalled.In(businessLocation).Format("2006-01-02"),
		"GetDailyBreakdown must use the requested period, not a fixed window")
}

// TestGetSummary_ThisMonth_IndependentOfRequestedRange guards against a
// regression where ThisMonth aliased totalRevenue/totalBookings - the
// requested period's totals - instead of running its own query for the real
// current calendar month. That was invisible as long as the API always
// defaulted to the current month; a caller requesting an unrelated custom
// range (e.g. via a date-range picker) would silently see "this month"
// report that unrelated range instead of the actual current month, even
// though Today and ThisWeek were already correctly independent.
func TestGetSummary_ThisMonth_IndependentOfRequestedRange(t *testing.T) {
	artistID := uuid.New()
	userID := uuid.New()

	repo := &mockRepo{
		artistID:       artistID,
		periodRevenue:  dec("50.00"),
		periodBookings: 1,
		periodDeposits: dec("0.00"),
	}

	svc := newService(repo)
	// A custom range far from the current month - if ThisMonth reused this
	// range's boundaries the bug would be invisible, so the range must be
	// something the real current month could never coincidentally match.
	_, err := svc.GetSummary(context.Background(), userID, GetSummaryRequest{
		From: "2020-01-01",
		To:   "2020-01-05",
	})
	require.NoError(t, err)

	require.Len(t, repo.periodSummaryCalls, 4,
		"expected 4 GetPeriodSummary calls: main period, today, this week, this month")

	// Call 0 is the main period - must match the REQUESTED range.
	mainCall := repo.periodSummaryCalls[0]
	assert.Equal(t, "2020-01-01", mainCall.From.In(businessLocation).Format("2006-01-02"))

	// Call 3 is "this month" - must match the REAL current month, not the
	// requested 2020 range above.
	expectedMonthFrom, expectedMonthTo := currentMonthRange()
	monthCall := repo.periodSummaryCalls[3]
	assert.True(t, monthCall.From.Equal(expectedMonthFrom),
		"ThisMonth query must use the real current month's start, not the requested range")
	assert.True(t, monthCall.To.Equal(expectedMonthTo),
		"ThisMonth query must use the real current month's end, not the requested range")
	assert.False(t, monthCall.From.Equal(mainCall.From),
		"ThisMonth must NOT reuse the requested period's boundaries")
}

func TestGetSummary_OnlyFromProvided_ReturnsError(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	repo := &mockRepo{artistID: artistID}
	svc := newService(repo)

	_, err := svc.GetSummary(context.Background(), userID, GetSummaryRequest{
		From: "2026-06-01",
	})

	assert.Error(t, err)
}

func TestGetSummary_ToBeforeFrom_ReturnsError(t *testing.T) {
	userID := uuid.New()
	artistID := uuid.New()

	repo := &mockRepo{artistID: artistID}
	svc := newService(repo)

	_, err := svc.GetSummary(context.Background(), userID, GetSummaryRequest{
		From: "2026-06-30",
		To:   "2026-06-01",
	})

	assert.Error(t, err)
}

func TestGetSummary_ArtistNotFound_ReturnsError(t *testing.T) {
	userID := uuid.New()

	repo := &mockRepo{
		artistErr: ErrArtistNotFound,
	}
	svc := newService(repo)

	_, err := svc.GetSummary(context.Background(), userID, GetSummaryRequest{})
	assert.Error(t, err)
}

// ── parseDateRange unit tests ─────────────────────────────────────────────────

func TestParseDateRange_DefaultsToCurrentMonth(t *testing.T) {
	from, to, err := parseDateRange("", "")
	require.NoError(t, err)

	// Must add the month while still in Beirut's location, then convert to
	// UTC - same order the real function uses. Adding the month after
	// converting to UTC gives a different (wrong) result whenever adjacent
	// months have different day counts (e.g. 31-day → 30-day), even though
	// it happens to match for some month pairs like July→August.
	now := beirutNow()
	expectedFromLocal := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, businessLocation)
	expectedToLocal := expectedFromLocal.AddDate(0, 1, 0)

	assert.Equal(t, expectedFromLocal.UTC(), from)
	assert.Equal(t, expectedToLocal.UTC(), to)
}

func TestParseDateRange_ValidRange(t *testing.T) {
	from, to, err := parseDateRange("2026-01-01", "2026-01-31")
	require.NoError(t, err)

	// Returned instants are UTC; convert back to Beirut local time to check
	// the calendar dates that were actually requested. January is winter
	// (UTC+2, no DST), so this also guards the non-DST season.
	assert.Equal(t, "2026-01-01", from.In(businessLocation).Format("2006-01-02"))
	// Exclusive upper bound: 2026-02-01
	assert.Equal(t, "2026-02-01", to.In(businessLocation).Format("2006-01-02"))
}

func TestParseDateRange_SameDay(t *testing.T) {
	from, to, err := parseDateRange("2026-06-15", "2026-06-15")
	require.NoError(t, err)

	assert.Equal(t, "2026-06-15", from.In(businessLocation).Format("2006-01-02"))
	// Next day exclusive
	assert.Equal(t, "2026-06-16", to.In(businessLocation).Format("2006-01-02"))
}

// TestParseDateRange_ExplicitRange_MatchesDefaultRange guards the bug this
// whole change fixes: requesting the current month explicitly must return
// the exact same boundaries as requesting no range at all. Before this fix,
// the two branches disagreed by the Beirut/UTC offset.
func TestParseDateRange_ExplicitRange_MatchesDefaultRange(t *testing.T) {
	now := beirutNow()
	firstOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, businessLocation)
	lastOfMonth := firstOfMonth.AddDate(0, 1, -1)

	defaultFrom, defaultTo, err := parseDateRange("", "")
	require.NoError(t, err)

	explicitFrom, explicitTo, err := parseDateRange(
		firstOfMonth.Format("2006-01-02"),
		lastOfMonth.Format("2006-01-02"),
	)
	require.NoError(t, err)

	assert.True(t, defaultFrom.Equal(explicitFrom),
		"explicit 'this month' range must match the default range exactly")
	assert.True(t, defaultTo.Equal(explicitTo),
		"explicit 'this month' range must match the default range exactly")
}

func TestParseDateRange_InvalidFromFormat(t *testing.T) {
	_, _, err := parseDateRange("01-06-2026", "2026-06-30")
	assert.Error(t, err)
}

func TestParseDateRange_ToBeforeFrom(t *testing.T) {
	_, _, err := parseDateRange("2026-06-30", "2026-06-01")
	assert.Error(t, err)
}

// ── thisWeekRange unit tests ──────────────────────────────────────────────────

func TestThisWeekRange_MondayToSunday(t *testing.T) {
	from, to := thisWeekRange()

	// from/to are UTC instants representing Monday 00:00:00 in the business's
	// local timezone (Beirut) - since Beirut is UTC+3, that instant displays
	// as Sunday 21:00 in raw UTC, not Monday 00:00. Convert back to local
	// time to check the calendar properties that actually matter here.
	fromLocal := from.In(businessLocation)
	toLocal := to.In(businessLocation)

	assert.Equal(t, time.Monday, fromLocal.Weekday())
	// to must be the following Monday (7 days later), as an instant
	assert.Equal(t, from.AddDate(0, 0, 7), to)
	assert.Equal(t, 0, fromLocal.Hour())
	assert.Equal(t, 0, toLocal.Hour())
}
