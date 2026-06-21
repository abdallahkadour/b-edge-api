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
	ID      uuid.UUID `db:"id"`
	Name    string    `db:"name"`
	City    string    `db:"city"`
	Address *string   `db:"address"`
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

// ArtistCard is one card on the discovery screen. No price field — the card shows
// identity, specialty, rating, city, and the New badge only.
type ArtistCard struct {
	ID          uuid.UUID       `json:"id"`
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
type StoreCard struct {
	ID      uuid.UUID `json:"id"`
	Name    string    `json:"name"`
	City    string    `json:"city"`
	Address *string   `json:"address,omitempty"`
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
		Name:        r.Name,
		Category:    r.Category,
		Rating:      r.Rating,
		ReviewCount: r.ReviewCount,
		City:        r.City,
		IsVerified:  r.IsVerified,
		IsNew:       now.Sub(r.CreatedAt) < newArtistWindow,
	}
}

// toStoreCard converts a store row to its client representation.
func toStoreCard(r *StoreRow) StoreCard {
	return StoreCard{ID: r.ID, Name: r.Name, City: r.City, Address: r.Address}
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
