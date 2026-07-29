// Package earnings implements the earnings domain for B-Edge,
// providing revenue aggregation and breakdown for the artist dashboard.
package earnings

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/shopspring/decimal"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// Service handles all earnings business logic.
// It knows nothing about HTTP — no fiber.Ctx, no status codes.
// It knows nothing about SQL — all DB access goes through Repository.
type Service struct {
	repo Repository
}

// NewService creates a new earnings Service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// GetSummary returns the earnings summary for the authenticated artist.
//
// Default period: current calendar month (first day of month 00:00:00 UTC →
// first day of next month 00:00:00 UTC — exclusive upper bound).
//
// If from/to are provided they are parsed as YYYY-MM-DD and the upper bound
// is set to the end of the `to` day (exclusive: to + 1 day).
func (s *Service) GetSummary(ctx context.Context, userID uuid.UUID, req GetSummaryRequest) (*EarningsSummaryResponse, error) {
	// Resolve user → artist
	artistID, err := s.repo.GetArtistIDByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperror.NotFound("ARTIST_NOT_FOUND", "Artist profile not found")
		}
		return nil, fmt.Errorf("get earnings summary: %w", err)
	}

	// ── Parse date range ──────────────────────────────────────────────────────

	from, to, err := parseDateRange(req.From, req.To)
	if err != nil {
		return nil, err
	}

	// ── Main period summary ───────────────────────────────────────────────────

	totalRevenue, totalBookings, totalDeposits, err := s.repo.GetPeriodSummary(ctx, artistID, from, to)
	if err != nil {
		return nil, fmt.Errorf("get earnings summary: period summary: %w", err)
	}

	avgBookingValue := decimal.Zero
	if totalBookings > 0 {
		avgBookingValue = totalRevenue.Div(decimal.NewFromInt(int64(totalBookings)))
	}

	// ── Today ─────────────────────────────────────────────────────────────────

	todayFrom, todayTo := todayRange()
	todayRevenue, todayBookings, _, err := s.repo.GetPeriodSummary(ctx, artistID, todayFrom, todayTo)
	if err != nil {
		return nil, fmt.Errorf("get earnings summary: today summary: %w", err)
	}

	// ── This week (Mon–Sun) ───────────────────────────────────────────────────

	weekFrom, weekTo := thisWeekRange()
	weekRevenue, weekBookings, _, err := s.repo.GetPeriodSummary(ctx, artistID, weekFrom, weekTo)
	if err != nil {
		return nil, fmt.Errorf("get earnings summary: week summary: %w", err)
	}

	// ── Daily breakdown (last 7 days) ─────────────────────────────────────────

	last7From := time.Now().UTC().AddDate(0, 0, -6).Truncate(24 * time.Hour)
	last7To := time.Now().UTC().AddDate(0, 0, 1).Truncate(24 * time.Hour)
	dailyRows, err := s.repo.GetDailyBreakdown(ctx, artistID, last7From, last7To)
	if err != nil {
		return nil, fmt.Errorf("get earnings summary: daily breakdown: %w", err)
	}

	// ── Service breakdown ─────────────────────────────────────────────────────

	serviceRows, err := s.repo.GetServiceBreakdown(ctx, artistID, from, to)
	if err != nil {
		return nil, fmt.Errorf("get earnings summary: service breakdown: %w", err)
	}

	// ── Build response ────────────────────────────────────────────────────────

	daily := make([]DailyEarnings, 0, len(dailyRows))
	for _, row := range dailyRows {
		daily = append(daily, DailyEarnings{
			Day:     row.Day,
			Revenue: row.Revenue,
		})
	}

	byService := make([]ServiceEarnings, 0, len(serviceRows))
	for _, row := range serviceRows {
		byService = append(byService, ServiceEarnings{
			ServiceID:     row.ServiceID,
			ServiceName:   row.ServiceName,
			BookingsCount: row.BookingsCount,
			Revenue:       row.Revenue,
		})
	}

	return &EarningsSummaryResponse{
		Period:              Period{From: from, To: to},
		ThisMonth:           PeriodStats{Revenue: totalRevenue, BookingsCount: totalBookings},
		Today:               PeriodStats{Revenue: todayRevenue, BookingsCount: todayBookings},
		ThisWeek:            PeriodStats{Revenue: weekRevenue, BookingsCount: weekBookings},
		TotalRevenue:        totalRevenue,
		TotalBookings:       totalBookings,
		TotalDeposits:       totalDeposits,
		AverageBookingValue: avgBookingValue,
		DailyBreakdown:      daily,
		ByService:           byService,
	}, nil
}

// ── Private helpers ───────────────────────────────────────────────────────────

// parseDateRange parses optional YYYY-MM-DD from/to strings.
// If both are empty, defaults to the current calendar month.
// The returned `to` is exclusive (start of the day after the last day).
func parseDateRange(fromStr, toStr string) (time.Time, time.Time, error) {
	now := time.Now().UTC()

	if fromStr == "" && toStr == "" {
		// Default: current calendar month
		from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
		to := from.AddDate(0, 1, 0) // first of next month (exclusive)
		return from, to, nil
	}

	if fromStr == "" || toStr == "" {
		return time.Time{}, time.Time{}, apperror.BadRequest(
			"INVALID_DATE_RANGE",
			"Both 'from' and 'to' must be provided together (YYYY-MM-DD)",
		)
	}

	from, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		return time.Time{}, time.Time{}, apperror.BadRequest(
			"INVALID_FROM_DATE",
			"'from' must be in YYYY-MM-DD format",
		)
	}

	to, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		return time.Time{}, time.Time{}, apperror.BadRequest(
			"INVALID_TO_DATE",
			"'to' must be in YYYY-MM-DD format",
		)
	}

	if to.Before(from) {
		return time.Time{}, time.Time{}, apperror.BadRequest(
			"INVALID_DATE_RANGE",
			"'to' date must be on or after 'from' date",
		)
	}

	// Make `to` exclusive: start of the day after the requested last day
	toExclusive := to.AddDate(0, 0, 1)

	return from.UTC(), toExclusive.UTC(), nil
}

// todayRange returns the start and exclusive end of today in UTC.
func todayRange() (time.Time, time.Time) {
	now := time.Now().UTC()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 1)
	return from, to
}

// thisWeekRange returns Monday 00:00:00 UTC → next Monday 00:00:00 UTC.
// Lebanon uses Mon–Sun weeks which matches ISO 8601.
func thisWeekRange() (time.Time, time.Time) {
	now := time.Now().UTC()
	weekday := int(now.Weekday()) // Sunday=0 … Saturday=6
	// Adjust so Monday=0
	offset := (weekday - 1 + 7) % 7
	monday := time.Date(now.Year(), now.Month(), now.Day()-offset, 0, 0, 0, 0, time.UTC)
	nextMonday := monday.AddDate(0, 0, 7)
	return monday, nextMonday
}
