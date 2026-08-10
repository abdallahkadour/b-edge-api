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

// businessLocation is the timezone B-Edge's reporting calendar runs on.
//
// Unlike slot generation - which resolves each store's own IANA zone from
// stores.timezone, because a slot happens at one physical location - earnings
// are aggregated per ARTIST, and an artist can work across stores. "This
// month" therefore needs one reporting calendar, not one per store, or the
// same booking could land in different months depending on which store it
// was at.
//
// Every store is currently in Lebanon, so Asia/Beirut is both the reporting
// calendar and every store's zone. Once B-Edge operates across offsets (UAE
// is UTC+4 and observes no DST), this becomes a per-salon reporting-timezone
// setting rather than a package constant - tracked as a pre-expansion item.
//
// Loaded once at init. Falls back to UTC only if the IANA database is
// genuinely unavailable - cmd/main.go blank-imports time/tzdata to embed it
// in the binary, so this should be unreachable - and warns rather than
// failing silently, since a silent fallback shifts every reporting boundary
// by the offset with nothing to show for it.
var businessLocation = func() *time.Location {
	loc, err := time.LoadLocation("Asia/Beirut")
	if err != nil {
		fmt.Printf("WARNING: could not load Asia/Beirut timezone, falling back to UTC - 'today'/'this month' boundaries will be wrong near midnight Beirut time: %v\n", err)
		return time.UTC
	}
	return loc
}()

// beirutNow returns the current instant expressed in the reporting timezone.
// "Today", "this week", and "this month" must be computed against the
// artist's local calendar, not the server's UTC clock - Beirut is UTC+3 (+2
// in winter), so using raw UTC creates a multi-hour daily window where these
// boundaries still report the previous day or month.
func beirutNow() time.Time {
	return time.Now().In(businessLocation)
}

// Service handles all earnings business logic.
// It knows nothing about HTTP - no fiber.Ctx, no status codes.
// It knows nothing about SQL - all DB access goes through Repository.
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
// first day of next month 00:00:00 UTC - exclusive upper bound).
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

	// ── This month (real calendar month - independent of the requested range) ─
	//
	// Deliberately a SEPARATE query from the main period summary above, even
	// though they're identical whenever the caller didn't pass from/to (the
	// common case). Before this fix they were the same value: ThisMonth just
	// aliased totalRevenue/totalBookings, which was invisible as long as the
	// API always defaulted to the current month - but the moment a caller
	// requests an explicit custom range (e.g. a date-range picker), "this
	// month" would silently report that unrelated range instead of the real
	// current month, contradicting the "This month" label the UI shows next
	// to Today/This week - both of which are correctly already independent.
	monthFrom, monthTo := currentMonthRange()
	monthRevenue, monthBookings, _, err := s.repo.GetPeriodSummary(ctx, artistID, monthFrom, monthTo)
	if err != nil {
		return nil, fmt.Errorf("get earnings summary: this month summary: %w", err)
	}

	// ── Daily breakdown ────────────────────────────────────────────────────────
	// Uses the same period as the main summary (default: current calendar
	// month; or the caller's from/to). Previously this was hardcoded to a
	// fixed last-7-days window regardless of the requested period, so a
	// custom from/to range would show correct totals but an empty chart
	// the daily breakdown must reflect whatever period the caller asked for.

	dailyRows, err := s.repo.GetDailyBreakdown(ctx, artistID, from, to)
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
		ThisMonth:           PeriodStats{Revenue: monthRevenue, BookingsCount: monthBookings},
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
	if fromStr == "" && toStr == "" {
		// Default: current calendar month, from the artist's local perspective.
		from, to := currentMonthRange()
		return from, to, nil
	}

	if fromStr == "" || toStr == "" {
		return time.Time{}, time.Time{}, apperror.BadRequest(
			"INVALID_DATE_RANGE",
			"Both 'from' and 'to' must be provided together (YYYY-MM-DD)",
		)
	}

	// time.Parse gives us the calendar date (year/month/day) with no
	// timezone information attached - reinterpret those same numbers in the
	// business's local timezone rather than accepting Go's UTC default.
	// Otherwise this branch disagrees with the no-params default above:
	// a custom range for "this month" would return different boundaries
	// than requesting no range at all, off by the Beirut/UTC offset.
	fromParsed, err := time.Parse("2006-01-02", fromStr)
	if err != nil {
		return time.Time{}, time.Time{}, apperror.BadRequest(
			"INVALID_FROM_DATE",
			"'from' must be in YYYY-MM-DD format",
		)
	}
	from := time.Date(fromParsed.Year(), fromParsed.Month(), fromParsed.Day(), 0, 0, 0, 0, businessLocation)

	toParsed, err := time.Parse("2006-01-02", toStr)
	if err != nil {
		return time.Time{}, time.Time{}, apperror.BadRequest(
			"INVALID_TO_DATE",
			"'to' must be in YYYY-MM-DD format",
		)
	}
	to := time.Date(toParsed.Year(), toParsed.Month(), toParsed.Day(), 0, 0, 0, 0, businessLocation)

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

// currentMonthRange returns the start of the current calendar month through
// the start of next month (exclusive), in the business's local timezone,
// expressed as UTC instants for the DB query. Shared by parseDateRange's
// no-params default AND GetSummary's independent "this month" stat - the
// latter must use the SAME boundary logic so a request for an unrelated
// custom range still reports the real current month, not the requested one.
func currentMonthRange() (time.Time, time.Time) {
	now := beirutNow()
	from := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, businessLocation)
	to := from.AddDate(0, 1, 0)
	return from.UTC(), to.UTC()
}

// todayRange returns the start and end of "today" in the business's local
// timezone, expressed as UTC instants for the DB query.
func todayRange() (time.Time, time.Time) {
	now := beirutNow()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, businessLocation)
	to := from.AddDate(0, 0, 1)
	return from.UTC(), to.UTC()
}

// thisWeekRange returns Monday 00:00:00 → next Monday 00:00:00, in the
// business's local timezone, expressed as UTC instants for the DB query.
// Lebanon uses Mon–Sun weeks which matches ISO 8601.
func thisWeekRange() (time.Time, time.Time) {
	now := beirutNow()
	weekday := int(now.Weekday()) // Sunday=0 … Saturday=6
	// Adjust so Monday=0
	offset := (weekday - 1 + 7) % 7
	monday := time.Date(now.Year(), now.Month(), now.Day()-offset, 0, 0, 0, 0, businessLocation)
	nextMonday := monday.AddDate(0, 0, 7)
	return monday.UTC(), nextMonday.UTC()
}
