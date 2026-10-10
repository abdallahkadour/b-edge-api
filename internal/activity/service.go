package activity

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// Service is the activity feed's logic.
type Service struct{ repo Repository }

// NewService creates the activity service.
func NewService(repo Repository) *Service { return &Service{repo: repo} }

// List is one page of salonID's activity, newest first.
//
// salonID is the caller's salon from the token; the route is guarded by
// salonrole.ActivitySalonRead, so only the owner reaches this. kind and
// actor are optional filters. hasMore reports another page.
func (s *Service) List(ctx context.Context, callerID, salonID uuid.UUID, kind, actor string, cursor time.Time, limit int) ([]Entry, bool, error) {
	var f Filter
	if kind != "" {
		if _, ok := kinds[kind]; !ok {
			return nil, false, apperror.BadRequest("INVALID_KIND",
				"kind must be one of bookings, payments, menu, shop, team")
		}
		f.Kind = kind
	}
	if actor != "" {
		id, err := uuid.Parse(actor)
		if err != nil {
			return nil, false, apperror.BadRequest("INVALID_ACTOR", "actor must be a user id")
		}
		f.ActorID = &id
	}

	rows, err := s.repo.List(ctx, salonID, f, cursor, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list activity: %w", err)
	}
	hasMore := len(rows) > limit
	if hasMore {
		rows = rows[:limit]
	}
	out := make([]Entry, 0, len(rows))
	for _, e := range rows {
		e.Actor.IsYou = e.Actor.ID != nil && *e.Actor.ID == callerID
		out = append(out, e)
	}
	return out, hasMore, nil
}
