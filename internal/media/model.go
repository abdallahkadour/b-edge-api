// Package media implements the media/portfolio domain for B-Edge,
// providing portfolio photo management for artist profiles.
package media

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// ── Constants ─────────────────────────────────────────────────────────────────

// MaxPortfolioPhotos is the maximum number of photos an artist can have.
const MaxPortfolioPhotos = 20

// OwnerTypeArtist is the owner_type value for artist media.
const OwnerTypeArtist = "artist"

// MediaTypePhoto is the type value for photo media.
const MediaTypePhoto = "photo"

// ── Sentinel errors ───────────────────────────────────────────────────────────

var (
	// ErrMediaNotFound is returned when no media item matches the given criteria.
	ErrMediaNotFound = errors.New("media not found")

	// ErrNotMediaOwner is returned when a user tries to modify media they do not own.
	ErrNotMediaOwner = errors.New("not authorised to modify this media item")

	// ErrPortfolioFull is returned when the artist has reached the 20-photo limit.
	ErrPortfolioFull = errors.New("portfolio is full - maximum 20 photos allowed")

	// ErrArtistNotFound is returned when no artist profile matches the given user ID.
	ErrArtistNotFound = errors.New("artist not found")
)

// ── Core structs ──────────────────────────────────────────────────────────────

// MediaItem represents a single row from the media table.
type MediaItem struct {
	ID           uuid.UUID `db:"id"            json:"id"`
	OwnerType    string    `db:"owner_type"    json:"owner_type"`
	OwnerID      uuid.UUID `db:"owner_id"      json:"owner_id"`
	URL          string    `db:"url"           json:"url"`
	CloudinaryID *string   `db:"cloudinary_id" json:"cloudinary_id,omitempty"`
	Type         string    `db:"type"          json:"type"`
	DisplayOrder int       `db:"display_order" json:"display_order"`
	CreatedAt    time.Time `db:"created_at"    json:"created_at"`
}

// ── Request structs ───────────────────────────────────────────────────────────

// AddMediaRequest is the request body for POST /api/v1/media.
type AddMediaRequest struct {
	// URL is the publicly accessible media URL (e.g. from Cloudinary).
	URL string `json:"url" validate:"required,url,max=500"`
	// CloudinaryID is the Cloudinary public_id for deletion later.
	CloudinaryID *string `json:"cloudinary_id" validate:"omitempty,max=255"`
}

// ReorderRequest is the request body for PATCH /api/v1/media/reorder.
type ReorderRequest struct {
	// IDs is the desired order - all media IDs for this artist, ordered 0..N.
	IDs []string `json:"ids" validate:"required,min=1"`
}

// SetCoverRequest is the request body for PATCH /api/v1/media/:id/cover.
// (No body needed - the ID is in the path. Struct kept for Swagger docs.)
type SetCoverRequest struct{}

// ── Response structs ──────────────────────────────────────────────────────────

// MediaResponse is the safe representation of a media item returned to clients.
type MediaResponse struct {
	ID           uuid.UUID `json:"id"`
	URL          string    `json:"url"`
	CloudinaryID *string   `json:"cloudinary_id,omitempty"`
	Type         string    `json:"type"`
	DisplayOrder int       `json:"display_order"`
	CreatedAt    time.Time `json:"created_at"`
}

// PortfolioResponse is the response for GET /api/v1/media/portfolio/:artist_id.
type PortfolioResponse struct {
	// ArtistID is the artist whose portfolio is returned.
	ArtistID uuid.UUID `json:"artist_id"`
	// Photos is the list of media items ordered by display_order ASC.
	Photos []MediaResponse `json:"photos"`
	// TotalCount is the number of photos in the portfolio.
	TotalCount int `json:"total_count"`
	// MaxAllowed is the platform limit (always 20).
	MaxAllowed int `json:"max_allowed"`
}

// ── Converters ────────────────────────────────────────────────────────────────

// toMediaResponse converts a MediaItem to a MediaResponse.
func toMediaResponse(m *MediaItem) MediaResponse {
	return MediaResponse{
		ID:           m.ID,
		URL:          m.URL,
		CloudinaryID: m.CloudinaryID,
		Type:         m.Type,
		DisplayOrder: m.DisplayOrder,
		CreatedAt:    m.CreatedAt,
	}
}
