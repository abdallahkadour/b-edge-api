// Package earnings implements the earnings domain for B-Edge,
// providing revenue aggregation and breakdown for the artist dashboard.
package earnings

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ── Sentinel errors ───────────────────────────────────────────────────────────

var (
	// ErrArtistNotFound is returned when no artist matches the given user ID.
	ErrArtistNotFound = errors.New("artist not found")
)

// ── Revenue statuses ──────────────────────────────────────────────────────────

// RevenueStatuses are the booking statuses that count as earned revenue.
// completed = service delivered and paid.
// no_show   = customer did not appear but deposit was kept by the artist.
var RevenueStatuses = []string{"completed", "no_show"}

// ── Core DB row structs ───────────────────────────────────────────────────────

// earningsRow is the raw aggregate row returned from the summary query.
type earningsRow struct {
	TotalRevenue  decimal.Decimal `db:"total_revenue"`
	TotalBookings int             `db:"total_bookings"`
	TotalDeposits decimal.Decimal `db:"total_deposits"`
}

// serviceEarningsRow is the raw row returned from the by-service query.
type serviceEarningsRow struct {
	ServiceID     uuid.UUID       `db:"service_id"`
	ServiceName   string          `db:"service_name"`
	BookingsCount int             `db:"bookings_count"`
	Revenue       decimal.Decimal `db:"revenue"`
}

// dailyEarningsRow is the raw row returned from the daily breakdown query.
type dailyEarningsRow struct {
	Day     time.Time       `db:"day"`
	Revenue decimal.Decimal `db:"revenue"`
}

// ── Request structs ───────────────────────────────────────────────────────────

// GetSummaryRequest is the query params for GET /api/v1/earnings/summary.
// If From and To are zero, the current calendar month is used.
type GetSummaryRequest struct {
	From string `query:"from"` // YYYY-MM-DD, optional
	To   string `query:"to"`   // YYYY-MM-DD, optional
}

// ── Response structs ──────────────────────────────────────────────────────────

// PeriodStats holds revenue figures for a single time window.
type PeriodStats struct {
	// Revenue is the total final_price of completed + no_show bookings.
	Revenue decimal.Decimal `json:"revenue"`
	// BookingsCount is the number of completed + no_show bookings.
	BookingsCount int `json:"bookings_count"`
}

// EarningsSummaryResponse is the response for GET /api/v1/earnings/summary.
type EarningsSummaryResponse struct {
	// Period is the date range that was queried.
	Period Period `json:"period"`

	// ThisMonth is the summary for the real current calendar month
	// independent of Period/TotalRevenue below. Does NOT change based on a
	// requested custom range, same as Today and ThisWeek.
	ThisMonth PeriodStats `json:"this_month"`

	// Today is the summary for today.
	Today PeriodStats `json:"today"`

	// ThisWeek is the summary for the current calendar week (Mon–Sun).
	ThisWeek PeriodStats `json:"this_week"`

	// TotalRevenue is the sum of all completed + no_show final_price in the period.
	TotalRevenue decimal.Decimal `json:"total_revenue"`

	// TotalBookings is the count of completed + no_show bookings in the period.
	TotalBookings int `json:"total_bookings"`

	// TotalDeposits is the sum of deposit_amount on completed + no_show bookings in the period.
	TotalDeposits decimal.Decimal `json:"total_deposits"`

	// AverageBookingValue is TotalRevenue / TotalBookings (zero if no bookings).
	AverageBookingValue decimal.Decimal `json:"average_booking_value"`

	// DailyBreakdown is the per-day revenue for the last 7 days.
	DailyBreakdown []DailyEarnings `json:"daily_breakdown"`

	// ByService is the revenue breakdown per service in the period.
	ByService []ServiceEarnings `json:"by_service"`
}

// Period holds the from/to dates of an earnings query.
type Period struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
}

// DailyEarnings holds revenue for a single day.
type DailyEarnings struct {
	// Day is midnight in the business's reporting timezone (Asia/Beirut),
	// expressed as its UTC instant - NOT midnight UTC. See GetDailyBreakdown
	// for why this distinction matters.
	Day time.Time `json:"day"`
	// Revenue is the total earned on that day.
	Revenue decimal.Decimal `json:"revenue"`
}

// ServiceEarnings holds revenue broken down by service.
type ServiceEarnings struct {
	// ServiceID is the service UUID.
	ServiceID uuid.UUID `json:"service_id"`
	// ServiceName is the human-readable service name.
	ServiceName string `json:"service_name"`
	// BookingsCount is the number of completed + no_show bookings for this service.
	BookingsCount int `json:"bookings_count"`
	// Revenue is the total final_price for this service.
	Revenue decimal.Decimal `json:"revenue"`
}

// ── Salon overview (owner only) ───────────────────────────────────────────────

// CancelledStatuses are the bookings that were called off after being made:
// cancelled outright, or cancelled with a deposit to return (refund_due) or
// already returned (refunded). expired is not here - a request nobody
// answered, or a checkout hold that lapsed, was never a booking.
var CancelledStatuses = []string{"cancelled", "refund_due", "refunded"}

// SalonTotals are the salon's figures over one window.
type SalonTotals struct {
	// Earned is the same figure each artist's own Earnings page shows
	// (EarnedCond: completed + no_show at final_price), summed across the
	// salon. B-Edge holds no money, so this is booked value, not cash.
	Earned decimal.Decimal `json:"earned"`
	// EarnedBookings is how many bookings Earned is made of.
	EarnedBookings int `json:"earned_bookings"`
	// Deposits is deposit_amount on those same bookings.
	Deposits  decimal.Decimal `json:"deposits"`
	Completed int             `json:"completed"`
	NoShows   int             `json:"no_shows"`
	// Cancelled counts CancelledStatuses.
	Cancelled int `json:"cancelled"`
	// OrdersDelivered and OrdersValue are shop orders delivered in the
	// window, by delivered_at, at what the customer was charged (after any
	// discount code).
	OrdersDelivered int             `json:"orders_delivered"`
	OrdersValue     decimal.Decimal `json:"orders_value"`
}

// SalonWaiting is what is waiting on someone in the salon right now. Not
// tied to the period: a refund owed from last month is still owed.
type SalonWaiting struct {
	AwaitingApproval int `json:"awaiting_approval"`
	// DepositsToCheck is approved (deposit not yet in) and deposit_paid
	// (customer says paid, not yet confirmed) - the Deposits screen's queue.
	DepositsToCheck   int             `json:"deposits_to_check"`
	RefundsOwed       int             `json:"refunds_owed"`
	RefundsOwedAmount decimal.Decimal `json:"refunds_owed_amount"`
}

// ArtistOverview is one artist's row in the owner's breakdown.
type ArtistOverview struct {
	ArtistID uuid.UUID `json:"artist_id"`
	Name     string    `json:"name"`
	// IsYou marks the caller's own row.
	IsYou bool `json:"is_you"`
	// IsMember is false for an artist who has left the salon but had
	// bookings here in the period. Those bookings stay the salon's.
	IsMember       bool            `json:"is_member"`
	Earned         decimal.Decimal `json:"earned"`
	EarnedBookings int             `json:"earned_bookings"`
	Completed      int             `json:"completed"`
	NoShows        int             `json:"no_shows"`
	Cancelled      int             `json:"cancelled"`
	// Rating and ReviewCount are the artist's all-time figures, the ones
	// customers see on her profile.
	Rating      decimal.Decimal `json:"rating"`
	ReviewCount int             `json:"review_count"`

	userID uuid.UUID
}

// SalonOverviewResponse is the response for GET /api/v1/earnings/salon.
type SalonOverviewResponse struct {
	Period Period `json:"period"`
	// PreviousPeriod is the window Previous was counted over: the period
	// before, cut to the same point if the current one is still running, so
	// the 10th of the month is compared with the 10th of the last.
	PreviousPeriod Period           `json:"previous_period"`
	Totals         SalonTotals      `json:"totals"`
	Previous       SalonTotals      `json:"previous"`
	Waiting        SalonWaiting     `json:"waiting"`
	ByArtist       []ArtistOverview `json:"by_artist"`
}
