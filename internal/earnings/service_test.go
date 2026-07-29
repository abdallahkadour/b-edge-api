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

type mockRepo struct {
	artistID        uuid.UUID
	artistErr       error
	periodRevenue   decimal.Decimal
	periodBookings  int
	periodDeposits  decimal.Decimal
	periodErr       error
	dailyRows       []dailyEarningsRow
	dailyErr        error
	serviceRows     []serviceEarningsRow
	serviceErr      error
}

func (m *mockRepo) GetArtistIDByUserID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return m.artistID, m.artistErr
}

func (m *mockRepo) GetPeriodSummary(_ context.Context, _ uuid.UUID, _, _ time.Time) (decimal.Decimal, int, decimal.Decimal, error) {
	return m.periodRevenue, m.periodBookings, m.periodDeposits, m.periodErr
}

func (m *mockRepo) GetDailyBreakdown(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]dailyEarningsRow, error) {
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

	// Default period should be current calendar month
	now := time.Now().UTC()
	expectedFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
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
	assert.Equal(t, dec("100"), result.AverageBookingValue)
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
	assert.Equal(t, "2026-06-01", result.Period.From.Format("2006-01-02"))
	// to is exclusive: 2026-07-01
	assert.Equal(t, "2026-07-01", result.Period.To.Format("2006-01-02"))
	assert.Equal(t, dec("500.00"), result.TotalRevenue)
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

	now := time.Now().UTC()
	expectedFrom := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	expectedTo := expectedFrom.AddDate(0, 1, 0)

	assert.Equal(t, expectedFrom, from)
	assert.Equal(t, expectedTo, to)
}

func TestParseDateRange_ValidRange(t *testing.T) {
	from, to, err := parseDateRange("2026-01-01", "2026-01-31")
	require.NoError(t, err)

	assert.Equal(t, "2026-01-01", from.Format("2006-01-02"))
	// Exclusive upper bound: 2026-02-01
	assert.Equal(t, "2026-02-01", to.Format("2006-01-02"))
}

func TestParseDateRange_SameDay(t *testing.T) {
	from, to, err := parseDateRange("2026-06-15", "2026-06-15")
	require.NoError(t, err)

	assert.Equal(t, "2026-06-15", from.Format("2006-01-02"))
	// Next day exclusive
	assert.Equal(t, "2026-06-16", to.Format("2006-01-02"))
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

	// from must be Monday
	assert.Equal(t, time.Monday, from.Weekday())
	// to must be the following Monday (7 days later)
	assert.Equal(t, from.AddDate(0, 0, 7), to)
	// Both at midnight UTC
	assert.Equal(t, 0, from.Hour())
	assert.Equal(t, 0, to.Hour())
}
