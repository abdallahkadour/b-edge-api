// Package share serves crawlable link previews for shared artist profiles.
//
// Both PWAs are client-rendered, and WhatsApp/Instagram/Facebook crawlers do
// not execute JavaScript, so Open Graph tags written at runtime by Angular
// are invisible to them. Every artist link shared today renders as a bare
// URL with no title, description or image - on the channel that is B-Edge's
// primary distribution.
//
// This package returns a small static HTML document carrying those tags,
// which humans are immediately redirected out of. See
// project-docs/B-Edge-Share-Previews-Decision-v1.md for why this rather than
// Angular SSR.
package share

import (
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ErrArtistNotFound is returned when no publicly visible artist matches the
// given handle or ID. Deliberately the same for "no such artist" and "that
// artist is hidden" - a suspended artist's link must not be distinguishable
// from a nonexistent one.
var ErrArtistNotFound = errors.New("artist not found")

// ogImageTransform is the Cloudinary transformation that produces a
// correctly-proportioned link-preview card.
//
// 1200x630 is the size Facebook, WhatsApp and Twitter all crop toward;
// anything else gets centre-cropped unpredictably by each of them
// differently. c_fill keeps the subject filling the frame rather than
// letterboxing, and g_auto lets Cloudinary pick the crop centre, which for
// portfolio photos means a face rather than the middle of a torso.
const ogImageTransform = "c_fill,g_auto,w_1200,h_630"

// cloudinaryUploadMarker is the path segment after which a transformation
// can be inserted into a Cloudinary delivery URL.
const cloudinaryUploadMarker = "/upload/"

// ArtistPreview is everything the preview card needs about one artist.
type ArtistPreview struct {
	ID          uuid.UUID
	Handle      *string
	Name        string
	Bio         *string
	Category    *string
	City        *string
	Rating      decimal.Decimal
	ReviewCount int
	// CoverURL is the artist's first portfolio photo, if any.
	CoverURL *string
}

// OGImageURL returns the image to advertise on the preview card.
//
// Returns ("", false) when the artist has no portfolio photo, leaving the
// caller to fall back - a card with a title and description but no image is
// still far better than no card, so a missing photo must not suppress the
// tags entirely.
//
// A non-Cloudinary URL is returned unchanged rather than rejected: the
// transformation is an optimisation, and serving a correctly-sized image is
// less important than serving one at all.
func (a ArtistPreview) OGImageURL() (string, bool) {
	if a.CoverURL == nil || *a.CoverURL == "" {
		return "", false
	}
	raw := *a.CoverURL
	idx := strings.Index(raw, cloudinaryUploadMarker)
	if idx == -1 {
		return raw, true
	}
	head := raw[:idx+len(cloudinaryUploadMarker)]
	tail := raw[idx+len(cloudinaryUploadMarker):]
	return head + ogImageTransform + "/" + tail, true
}

// ShareSlug is the identifier to use in the canonical share URL - the
// handle when the artist has one, otherwise the UUID.
func (a ArtistPreview) ShareSlug() string {
	if a.Handle != nil && *a.Handle != "" {
		return *a.Handle
	}
	return a.ID.String()
}

// Description is the card's subtitle.
//
// Prefers the artist's own bio. Falls back to a composed line from category
// and city, because an empty description renders as a blank second line in
// WhatsApp rather than collapsing - worse-looking than a generic one.
func (a ArtistPreview) Description() string {
	if a.Bio != nil {
		if b := strings.TrimSpace(*a.Bio); b != "" {
			return b
		}
	}

	var parts []string
	if a.Category != nil && *a.Category != "" {
		parts = append(parts, capitalise(*a.Category))
	}
	if a.City != nil && *a.City != "" {
		parts = append(parts, "in "+*a.City)
	}
	if len(parts) == 0 {
		return "Book an appointment on B-Edge."
	}
	return strings.Join(parts, " ") + " · Book on B-Edge."
}

// capitalise upper-cases the first letter.
//
// The artist categories are a fixed set of ASCII slugs validated by the
// artist domain (makeup, hair, nails, lashes, skincare), so this does not
// need the Unicode-aware casing rules that strings.Title was deprecated
// for failing to apply.
func capitalise(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
