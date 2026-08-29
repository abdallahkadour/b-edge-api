package admin

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// defaultTrialPlanCode is the plan a newly-approved artist's trial
// subscription starts on. 'starter' - the entry paid tier - matching how
// competitor booking platforms (Fresha, Vagaro, Booksy) default a new
// trial to their lowest paid tier rather than their top one.
const defaultTrialPlanCode = "starter"

// defaultTrialDays is the length of a new artist's trial, starting at
// admin approval rather than at signup - see
// B-Edge-Monetization-Implementation-Spec-v1.md section 7.3 ("Starting the
// trial at approval is the recommendation") and section 8's worked example,
// which uses 30 days throughout. Section 11 still lists exact trial length
// as an open founder decision for launch - this is a provisional default
// closing a real gap (artists approved with no subscription row at all
// read as permanently past_due), not a final answer to that question.
const defaultTrialDays = 30

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

// Approve activates a pending artist AND creates their initial trial
// subscription in the same transaction - see ApproveWithTrialSubscription's
// doc comment on the Repository interface for why: without this, an artist
// approved after Aug 29, 2026 got no subscriptions row at all, which
// billing.DeriveStatus reads as permanently past_due with no way to
// recover short of an admin manually creating one via the Artists tab.
func (s *service) Approve(ctx context.Context, artistID, adminID uuid.UUID, ip string) error {
	trialEndsAt := time.Now().AddDate(0, 0, defaultTrialDays)

	rows, err := s.repo.ApproveWithTrialSubscription(ctx, artistID, defaultTrialPlanCode, trialEndsAt)
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
