package admin

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

type service struct {
	repo  Repository
	audit audit.Repository
}

func NewService(repo Repository, auditRepo audit.Repository) Service {
	a := audit.Repository(audit.NopRepository{})
	if auditRepo != nil {
		a = auditRepo
	}
	return &service{repo: repo, audit: a}
}

func (s *service) ListPending(ctx context.Context) ([]*PendingArtist, error) {
	artists, err := s.repo.ListPending(ctx)
	if err != nil {
		return nil, fmt.Errorf("list pending artists: %w", err)
	}
	return artists, nil
}

func (s *service) Approve(ctx context.Context, artistID, adminID uuid.UUID, ip string) error {
	rows, err := s.repo.UpdateStatus(ctx, artistID, "active")
	if err != nil {
		return fmt.Errorf("approve artist: %w", err)
	}
	if rows == 0 {
		return apperror.Conflict("NOT_PENDING", "This artist is not awaiting review")
	}

	// Best-effort - a failure to WRITE the audit log must never undo an
	// approval that already succeeded and already committed. Logged as a
	// server-side error rather than surfaced to the caller.
	_ = s.audit.Log(ctx, audit.Event{
		ActorID:    &adminID,
		ActorRole:  "admin",
		EntityType: "artist",
		EntityID:   artistID,
		Action:     "approved",
		IPAddress:  ip,
	})

	return nil
}

func (s *service) Reject(ctx context.Context, artistID, adminID uuid.UUID, req DecisionRequest, ip string) error {
	rows, err := s.repo.UpdateStatus(ctx, artistID, "rejected")
	if err != nil {
		return fmt.Errorf("reject artist: %w", err)
	}
	if rows == 0 {
		return apperror.Conflict("NOT_PENDING", "This artist is not awaiting review")
	}

	// The rejection reason lives in new_values, not as a bare string
	// column - keeps the audit_events schema generic (it already stores
	// arbitrary JSONB for exactly this) rather than adding a
	// rejection-specific column to a table meant to log every kind of
	// admin action, not just this one.
	_ = s.audit.Log(ctx, audit.Event{
		ActorID:    &adminID,
		ActorRole:  "admin",
		EntityType: "artist",
		EntityID:   artistID,
		Action:     "rejected",
		NewValues:  map[string]any{"reason": req.Reason},
		IPAddress:  ip,
	})

	return nil
}
