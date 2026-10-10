// Package caller carries who is making a request from the auth middleware to
// the services.
//
// Two things need it below the handler, and neither is any one domain's
// business:
//
//   - The activity log (internal/audit). An owner's Activity screen answers
//     "who did what", so every recorded change has to name the person who
//     made it. Most services were never told: payout.Upsert takes a salon and
//     a request, not a person. Threading an actor parameter through every
//     write method in seven packages - and every test that calls them - to
//     reach one audit row each is the change this package avoids.
//   - The owner acting on a member's booking (salonrole.BookingsAnyWrite).
//     The booking guards compare the caller with the booking's artist; the
//     owner's standing is what lets them pass for someone else's booking.
//
// RequireAuth fills it once per request, from the same values it puts in
// Fiber Locals - the database's answer for salon and salon role
// (middleware.Standing, D28), not the token's.
//
// A leaf package, importing nothing of this codebase but salonrole, so that
// middleware, audit and every domain can import it without a cycle.
package caller

import (
	"context"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

// Caller is the signed-in person behind a request.
type Caller struct {
	UserID uuid.UUID
	// Role is users.role: customer, artist or admin.
	Role string
	// SalonID and SalonRole are what the database says now.
	SalonID   *uuid.UUID
	SalonRole salonrole.Role
	IP        string
}

type key struct{}

// With returns ctx carrying c.
func With(ctx context.Context, c Caller) context.Context {
	return context.WithValue(ctx, key{}, c)
}

// From returns the request's caller. ok is false when there is none - a
// background worker, a public route, a unit test - and the zero Caller then
// holds nothing: no user, no salon, no capability.
func From(ctx context.Context) (Caller, bool) {
	c, ok := ctx.Value(key{}).(Caller)
	return c, ok
}

// InSalon reports whether the caller belongs to salonID.
func (c Caller) InSalon(salonID uuid.UUID) bool {
	return c.SalonID != nil && *c.SalonID == salonID
}
