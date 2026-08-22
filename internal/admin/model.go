// Package admin implements the review actions on top of self-service
// onboarding - listing pending artists and approving or rejecting them.
//
// Deliberately its own small package rather than living inside the artist
// domain. Approve/reject is not "an artist managing their own profile" -
// it's a different actor, with a different trust level, doing something to
// someone else's profile. Keeping it separate also means the admin route
// group (RequireRole("admin"), audit-logged) never has to be threaded
// through a package whose other 90% of routes are artist-only.
package admin

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// ErrNotPending is returned when trying to approve or reject an artist
// whose status isn't 'pending' - already decided once, or never submitted
// at all. A decision should not be re-appliable to an artist who is
// already live or already rejected.
var ErrNotPending = errors.New("this artist is not awaiting review")

// PendingArtist is one row in the admin review queue - just enough to make
// a decision without opening the full dashboard: who they are, how to
// reach them, what they submitted, and when.
type PendingArtist struct {
	ArtistID    uuid.UUID `json:"artist_id"`
	Name        string    `json:"name"`
	Email       string    `json:"email"`
	Handle      *string   `json:"handle"`
	Category    *string   `json:"category"`
	Bio         *string   `json:"bio"`
	SalonName   string    `json:"salon_name"`
	StoreName   string    `json:"store_name"`
	City        string    `json:"city"`
	ServiceName string    `json:"service_name"`
	SubmittedAt time.Time `json:"submitted_at"`
}

// DecisionRequest is the body for both approve and reject - a reason is
// only meaningful for a rejection, but keeping one shape for both actions
// keeps the handler and audit-logging code identical for either outcome.
type DecisionRequest struct {
	Reason *string `json:"reason" validate:"omitempty,max=1000"`
}

type Service interface {
	ListPending(ctx context.Context) ([]*PendingArtist, error)
	Approve(ctx context.Context, artistID, adminID uuid.UUID, ip string) error
	Reject(ctx context.Context, artistID, adminID uuid.UUID, req DecisionRequest, ip string) error
}
