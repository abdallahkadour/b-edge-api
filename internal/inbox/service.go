package inbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// Service handles notification-centre business logic.
type Service struct {
	repo Repository
}

// NewService creates an inbox Service.
func NewService(repo Repository) *Service {
	return &Service{repo: repo}
}

// GetFeed returns a user's notification centre contents.
//
// The unread count is returned alongside the list so the bell badge and the
// panel cannot disagree - fetching them separately would let a read that
// lands between the two calls show an empty list under a non-zero badge.
func (s *Service) GetFeed(ctx context.Context, userID uuid.UUID, unreadOnly bool, limit int) (*FeedResponse, error) {
	items, err := s.repo.ListFeed(ctx, userID, unreadOnly, limit)
	if err != nil {
		return nil, fmt.Errorf("get feed: %w", err)
	}
	count, err := s.repo.CountUnread(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get feed: count: %w", err)
	}
	return &FeedResponse{Notifications: items, UnreadCount: count}, nil
}

// GetUnreadCount backs the badge on its own, for polling.
func (s *Service) GetUnreadCount(ctx context.Context, userID uuid.UUID) (*UnreadCountResponse, error) {
	count, err := s.repo.CountUnread(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get unread count: %w", err)
	}
	return &UnreadCountResponse{UnreadCount: count}, nil
}

// MarkRead marks one notification read.
//
// A notification belonging to someone else returns 404, not 403 - the same
// posture as billing invoices and media tags, so IDs cannot be probed by
// watching the status code.
func (s *Service) MarkRead(ctx context.Context, userID, notificationID uuid.UUID) error {
	if err := s.repo.MarkRead(ctx, userID, notificationID); err != nil {
		if errors.Is(err, ErrNotificationNotFound) {
			return apperror.NotFound("NOTIFICATION_NOT_FOUND", "Notification not found")
		}
		return fmt.Errorf("mark read: %w", err)
	}
	return nil
}

// MarkAllRead clears the badge.
func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	if _, err := s.repo.MarkAllRead(ctx, userID); err != nil {
		return fmt.Errorf("mark all read: %w", err)
	}
	return nil
}

// Archive dismisses a notification from the feed.
func (s *Service) Archive(ctx context.Context, userID, notificationID uuid.UUID) error {
	if err := s.repo.Archive(ctx, userID, notificationID); err != nil {
		if errors.Is(err, ErrNotificationNotFound) {
			return apperror.NotFound("NOTIFICATION_NOT_FOUND", "Notification not found")
		}
		return fmt.Errorf("archive: %w", err)
	}
	return nil
}

// Notify files a notification for a user.
//
// The entry point other domains call. Exported on the service rather than
// having callers reach the repository directly, so producers depend on one
// narrow surface and defaults (level) are applied in one place.
//
// Deliberately best-effort at every call site: filing a notification must
// never fail the action it describes. A refund that happened but was not
// announced is recoverable; a refund that was rolled back because the
// announcement failed is not.
func (s *Service) Notify(ctx context.Context, p CreateParams) error {
	if p.UserID == uuid.Nil || p.Kind == "" || p.Title == "" {
		return fmt.Errorf("notify: user, kind and title are required")
	}
	if err := s.repo.Create(ctx, p); err != nil {
		return fmt.Errorf("notify: %w", err)
	}
	return nil
}
