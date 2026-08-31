// Package discovery implements the public customer-facing artist discovery
// surface for B-Edge: browsing/searching artists and viewing an artist's public
// profile (with stores and services). It is deliberately separate from the
// artist domain, which serves the authenticated owner's view of their own data.
package discovery

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/openinghours"
)

// newArtistWindow is how recently an artist must have been created to earn the
// "New" badge on a discovery card.
const newArtistWindow = 30 * 24 * time.Hour

// ── Sentinel errors ───────────────────────────────────────────────────────────

var (
	// ErrArtistNotFound is returned when no artist matches the given ID.
	ErrArtistNotFound = errors.New("artist not found")
)

// ── Valid categories ──────────────────────────────────────────────────────────

// ValidCategories is the fixed set of artist primary categories the customer PWA
// filters on. Mirrors the CHECK constraint added in migration 008. Used to reject
// unknown ?category= values with a clear error.
var ValidCategories = map[string]bool{
	"makeup":   true,
	"hair":     true,
	"nails":    true,
	"lashes":   true,
	"skincare": true,
}

// ── Internal row types ────────────────────────────────────────────────────────

// ArtistCardRow is one (artist, city) row from the discovery list query. An
// artist with stores in multiple cities yields one row per city, so they appear
// in each city's section on the discovery screen.
type ArtistCardRow struct {
	ID          uuid.UUID       `db:"id"`
	Handle      *string         `db:"handle"`
	Name        string          `db:"name"`
	Category    *string         `db:"category"`
	Rating      decimal.Decimal `db:"rating"`
	ReviewCount int             `db:"review_count"`
	City        string          `db:"city"`
	IsVerified  bool            `db:"is_verified"`
	CreatedAt   time.Time       `db:"created_at"`
}

// ArtistProfileRow is the core artist row for the public profile aggregate.
type ArtistProfileRow struct {
	ID          uuid.UUID       `db:"id"`
	Name        string          `db:"name"`
	Bio         *string         `db:"bio"`
	Instagram   *string         `db:"instagram"`
	Category    *string         `db:"category"`
	Rating      decimal.Decimal `db:"rating"`
	ReviewCount int             `db:"review_count"`
	IsVerified  bool            `db:"is_verified"`
	SalonID     *uuid.UUID      `db:"salon_id"`
}

// StoreRow is one store in an artist's public profile.
type StoreRow struct {
	ID        uuid.UUID `db:"id"`
	Name      string    `db:"name"`
	City      string    `db:"city"`
	Address   *string   `db:"address"`
	Phone     *string   `db:"phone"`
	Timezone  string    `db:"timezone"`
	Latitude  *float64  `db:"latitude"`
	Longitude *float64  `db:"longitude"`
}

// DayHoursRow is one weekday's regular trading hours for a store.
type DayHoursRow struct {
	StoreID   uuid.UUID `db:"store_id"`
	DayOfWeek int       `db:"day_of_week"` // 0 = Sunday, matching time.Weekday
	IsOpen    bool      `db:"is_open"`
	OpenTime  string    `db:"open_time"`
	CloseTime string    `db:"close_time"`
}

// ExceptionRow is a one-off trading override for a specific date.
type ExceptionRow struct {
	StoreID       uuid.UUID `db:"store_id"`
	ExceptionDate time.Time `db:"exception_date"`
	IsClosed      bool      `db:"is_closed"`
	OpenTime      *string   `db:"open_time"`
	CloseTime     *string   `db:"close_time"`
}

// ServiceRow is one service in an artist's public profile.
type ServiceRow struct {
	ID            uuid.UUID       `db:"id"`
	Name          string          `db:"name"`
	DurationMin   int             `db:"duration_min"`
	Price         decimal.Decimal `db:"price"`
	DepositAmount decimal.Decimal `db:"deposit_amount"`
}

// ── Response structs ──────────────────────────────────────────────────────────

// ArtistCard is one card on the discovery screen. No price field - the card shows
// identity, specialty, rating, city, and the New badge only.
type ArtistCard struct {
	ID          uuid.UUID       `json:"id"`
	Handle      *string         `json:"handle,omitempty"`
	Name        string          `json:"name"`
	Category    *string         `json:"category,omitempty"`
	Rating      decimal.Decimal `json:"rating"`
	ReviewCount int             `json:"review_count"`
	City        string          `json:"city"`
	IsVerified  bool            `json:"is_verified"`
	IsNew       bool            `json:"is_new"`
}

// PublicArtistProfile is the full public profile aggregate rendered by the
// customer-facing artist screen: the artist plus their stores and services.
type PublicArtistProfile struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Bio         *string         `json:"bio,omitempty"`
	Instagram   *string         `json:"instagram,omitempty"`
	Category    *string         `json:"category,omitempty"`
	Rating      decimal.Decimal `json:"rating"`
	ReviewCount int             `json:"review_count"`
	IsVerified  bool            `json:"is_verified"`
	Stores      []StoreCard     `json:"stores"`
	Services    []ServiceCard   `json:"services"`
}

// StoreCard is a store entry in the public profile.
//
// Deliberately narrower than artist.Store: that type carries
// weekday_buffer_min, early_bird_fee, same_day_notice_hours and salon_id,
// none of which are a customer's business. is_active is likewise absent -
// the public query filters inactive stores out entirely, so every card a
// customer sees is active by construction and a field saying so would only
// invite someone to render a badge that is always the same.
type StoreCard struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	City    string    `json:"city"`
	Address *string   `json:"address,omitempty"`
	Phone   *string   `json:"phone,omitempty"`
	// Latitude and Longitude are the artist-dropped map pin (migration
	// 027). Both nil means no pin was set; consumers omit the map rather
	// than rendering a default location.
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
	// OpenStatus is computed per request, never stored - see OpenStatus.
	OpenStatus OpenStatus `json:"open_status"`
}

// OpenReason explains why a store is or isn't currently trading, so the UI
// can say something more useful than "Closed".
type OpenReason string

// The reasons a store's door is in the state it's in.
const (
	// ReasonOpen - trading right now.
	ReasonOpen OpenReason = "open"
	// ReasonOutsideHours - trades today, but not at this hour.
	ReasonOutsideHours OpenReason = "outside_hours"
	// ReasonClosedToday - does not trade this weekday at all.
	ReasonClosedToday OpenReason = "closed_today"
	// ReasonHoliday - a dated exception closes the store.
	ReasonHoliday OpenReason = "holiday"
	// ReasonUnknown - hours are not configured, or could not be resolved.
	// Rendered as no badge at all rather than as "Closed": telling a
	// customer a salon is shut when the truth is that nobody filled in the
	// hours would cost the artist real bookings.
	ReasonUnknown OpenReason = "unknown"
)

// OpenStatus is a store's trading state at the moment of the request.
//
// Derived, never stored. Storing it would need a scheduler to keep it true,
// and this codebase deliberately has none - the same reasoning that keeps
// subscription status derived (see internal/pkg/subscription).
type OpenStatus struct {
	IsOpen bool       `json:"is_open"`
	Reason OpenReason `json:"reason"`
	// ClosesAt is set when currently open, so the UI can warn "closes in
	// 30 minutes" rather than only "open".
	ClosesAt *time.Time `json:"closes_at,omitempty"`
	// OpensAt is set only when the store opens LATER TODAY. A store that
	// has already closed for the day leaves this nil rather than pointing
	// at tomorrow - answering "when do you next open" properly needs the
	// whole week's hours plus exception lookahead, which is a bigger
	// feature than the badge this exists to render.
	OpensAt *time.Time `json:"opens_at,omitempty"`
}

// ServiceCard is a service entry in the public profile.
type ServiceCard struct {
	ID            uuid.UUID       `json:"id"`
	Name          string          `json:"name"`
	DurationMin   int             `json:"duration_min"`
	Price         decimal.Decimal `json:"price"`
	DepositAmount decimal.Decimal `json:"deposit_amount"`
}

// ── Converters ────────────────────────────────────────────────────────────────

// toArtistCard converts a list row to its client card, computing the New badge
// from the artist's creation time.
func toArtistCard(r *ArtistCardRow, now time.Time) *ArtistCard {
	return &ArtistCard{
		ID:          r.ID,
		Handle:      r.Handle,
		Name:        r.Name,
		Category:    r.Category,
		Rating:      r.Rating,
		ReviewCount: r.ReviewCount,
		City:        r.City,
		IsVerified:  r.IsVerified,
		IsNew:       now.Sub(r.CreatedAt) < newArtistWindow,
	}
}

// toStoreCard converts a store row to its client representation, computing
// its current trading state from the supplied hours and exceptions.
//
// days is the store's full week of regular hours and excs its dated
// overrides; both may be empty, which yields ReasonUnknown rather than a
// misleading "Closed".
func toStoreCard(r *StoreRow, days []*DayHoursRow, excs []*ExceptionRow, now time.Time) StoreCard {
	return StoreCard{
		ID:         r.ID,
		Name:       r.Name,
		City:       r.City,
		Address:    r.Address,
		Phone:      r.Phone,
		Latitude:   r.Latitude,
		Longitude:  r.Longitude,
		OpenStatus: deriveOpenStatus(r, days, excs, now),
	}
}

// deriveOpenStatus resolves a store's trading state at now.
//
// Everything is evaluated in the STORE's own timezone, not the server's and
// not UTC. Which weekday it is, and which date an exception applies to, both
// depend on where the store physically is - at 23:00 UTC it is already
// tomorrow in Beirut, so a UTC-based weekday lookup would read the wrong
// row for two hours every night. That exact bug existed in booking's
// isToday() and is documented in internal/pkg/openinghours.
func deriveOpenStatus(r *StoreRow, days []*DayHoursRow, excs []*ExceptionRow, now time.Time) OpenStatus {
	loc := openinghours.Location(r.Timezone)
	local := now.In(loc)
	today := openinghours.LocalDate(local, loc)

	var day *openinghours.DayHours
	for _, d := range days {
		if d.DayOfWeek == int(local.Weekday()) {
			day = &openinghours.DayHours{IsOpen: d.IsOpen, OpenTime: d.OpenTime, CloseTime: d.CloseTime}
			break
		}
	}

	var exc *openinghours.Exception
	for _, e := range excs {
		ed := e.ExceptionDate.In(loc)
		if ed.Year() == local.Year() && ed.Month() == local.Month() && ed.Day() == local.Day() {
			exc = &openinghours.Exception{IsClosed: e.IsClosed, OpenTime: e.OpenTime, CloseTime: e.CloseTime}
			break
		}
	}

	// No hours row for today at all means nobody configured this store's
	// week - report unknown so the UI shows no badge, rather than claiming
	// the salon is shut.
	if day == nil {
		return OpenStatus{IsOpen: false, Reason: ReasonUnknown}
	}

	window, open, err := openinghours.Resolve(r.Timezone, today, day, exc)
	if err != nil {
		// Malformed TIME data. Same reasoning as above: say nothing rather
		// than something wrong.
		return OpenStatus{IsOpen: false, Reason: ReasonUnknown}
	}
	if !open {
		if exc != nil && exc.IsClosed {
			return OpenStatus{IsOpen: false, Reason: ReasonHoliday}
		}
		return OpenStatus{IsOpen: false, Reason: ReasonClosedToday}
	}

	if window.IsOpenAt(now) {
		closesAt := window.CloseAt
		return OpenStatus{IsOpen: true, Reason: ReasonOpen, ClosesAt: &closesAt}
	}

	// Trades today, but not right now. Offer the opening time only if it is
	// still ahead of us.
	st := OpenStatus{IsOpen: false, Reason: ReasonOutsideHours}
	if now.Before(window.OpenAt) {
		opensAt := window.OpenAt
		st.OpensAt = &opensAt
	}
	return st
}

// toServiceCard converts a service row to its client representation.
func toServiceCard(r *ServiceRow) ServiceCard {
	return ServiceCard{
		ID:            r.ID,
		Name:          r.Name,
		DurationMin:   r.DurationMin,
		Price:         r.Price,
		DepositAmount: r.DepositAmount,
	}
}
