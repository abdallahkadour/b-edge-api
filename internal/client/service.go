// Package client implements the artist-facing CRM for B-Edge.
package client

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// Service handles all client CRM business logic.
type Service struct {
	repo     Repository
	validate *validator.Validate
}

// NewService creates a new client Service.
func NewService(repo Repository) *Service {
	return &Service{
		repo:     repo,
		validate: validator.New(),
	}
}

// ListClients returns the authenticated artist's clients, optionally filtered by
// a search string on name or service. requesterUserID is the JWT user_id, which
// is resolved to the artist's id before querying.
func (s *Service) ListClients(ctx context.Context, requesterUserID uuid.UUID, q string) ([]*ClientCard, error) {
	artistID, err := s.resolveArtist(ctx, requesterUserID)
	if err != nil {
		return nil, err
	}

	rows, err := s.repo.ListClients(ctx, artistID, q)
	if err != nil {
		return nil, fmt.Errorf("list clients: %w", err)
	}

	result := make([]*ClientCard, 0, len(rows))
	for _, r := range rows {
		result = append(result, toClientCard(r))
	}
	return result, nil
}

// GetClient returns one client's full profile (metrics, note, history) for the
// authenticated artist. Returns NOT_FOUND if the customer is not their client.
func (s *Service) GetClient(ctx context.Context, requesterUserID, customerID uuid.UUID) (*ClientProfile, error) {
	artistID, err := s.resolveArtist(ctx, requesterUserID)
	if err != nil {
		return nil, err
	}

	row, err := s.repo.GetClient(ctx, artistID, customerID)
	if err != nil {
		if errors.Is(err, ErrClientNotFound) {
			return nil, apperror.NotFound("CLIENT_NOT_FOUND", "This customer is not one of your clients")
		}
		return nil, fmt.Errorf("get client: %w", err)
	}

	historyRows, err := s.repo.GetClientHistory(ctx, artistID, customerID)
	if err != nil {
		return nil, fmt.Errorf("get client: history: %w", err)
	}
	history := make([]BookingHistory, 0, len(historyRows))
	for _, h := range historyRows {
		history = append(history, toBookingHistory(h))
	}

	return &ClientProfile{
		CustomerID:    row.CustomerID,
		Name:          row.Name,
		Phone:         row.Phone,
		BookingsCount: row.BookingsCount,
		TotalSpent:    row.TotalSpent,
		AverageRating: row.AverageRating,
		IsVIP:         isVIP(row.BookingsCount, row.TotalSpent),
		Note:          row.NoteContent,
		History:       history,
	}, nil
}

// UpsertNote creates or updates the artist's private note for a customer. The
// customer must already be the artist's client (have a completed booking),
// preventing notes on arbitrary users.
func (s *Service) UpsertNote(ctx context.Context, requesterUserID, customerID uuid.UUID, req UpsertNoteRequest) (*NoteResponse, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	artistID, err := s.resolveArtist(ctx, requesterUserID)
	if err != nil {
		return nil, err
	}

	// Ensure the customer is actually this artist's client before storing a note.
	if _, err := s.repo.GetClient(ctx, artistID, customerID); err != nil {
		if errors.Is(err, ErrClientNotFound) {
			return nil, apperror.NotFound("CLIENT_NOT_FOUND", "This customer is not one of your clients")
		}
		return nil, fmt.Errorf("upsert note: verify client: %w", err)
	}

	salonID, err := s.repo.GetArtistSalonID(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("upsert note: salon: %w", err)
	}

	content, updatedAt, err := s.repo.UpsertNote(ctx, salonID, artistID, customerID, req.Content)
	if err != nil {
		return nil, fmt.Errorf("upsert note: %w", err)
	}

	return &NoteResponse{
		CustomerID: customerID,
		Content:    content,
		UpdatedAt:  updatedAt,
	}, nil
}

// resolveArtist turns the JWT user_id into the caller's artists.id, mapping the
// "not an artist" case to a forbidden error.
func (s *Service) resolveArtist(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	artistID, err := s.repo.GetArtistIDByUserID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrArtistNotFound) {
			return uuid.Nil, apperror.Forbidden("NOT_AN_ARTIST", "Only artists can access client records")
		}
		return uuid.Nil, fmt.Errorf("resolve artist: %w", err)
	}
	return artistID, nil
}

// ── Private helpers ───────────────────────────────────────────────────────────

func mapValidationError(err error) error {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return apperror.BadRequest("VALIDATION_ERROR", err.Error())
	}
	details := make([]apperror.FieldError, 0, len(ve))
	for _, fe := range ve {
		details = append(details, apperror.FieldError{
			Field:   fe.Field(),
			Message: fe.Field() + " is invalid",
		})
	}
	return apperror.UnprocessableEntity("VALIDATION_ERROR", details)
}
