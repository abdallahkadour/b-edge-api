package report

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// Service applies the rules around raising and handling reports.
type Service struct {
	repo     Repository
	audit    audit.Repository
	validate *validator.Validate
}

// NewService creates a report service.
func NewService(repo Repository, auditRepo audit.Repository) *Service {
	a := audit.Repository(audit.NopRepository{})
	if auditRepo != nil {
		a = auditRepo
	}
	return &Service{repo: repo, audit: a, validate: validation.New()}
}

// Create raises a problem.
//
// The reporter must be party to the booking they are reporting. That check is
// what stops someone filing reports about strangers' appointments, and it is
// the reason reports require an account at all: a client already signs in with
// the phone they booked with to see the booking, so this costs them nothing.
func (s *Service) Create(ctx context.Context, userID uuid.UUID, role string, req CreateReportRequest) (*Response, error) {
	req.Description = strings.TrimSpace(req.Description)
	if err := s.validate.Struct(req); err != nil {
		return nil, validation.MapError(err)
	}
	if !Category(req.Category).Valid() {
		return nil, apperror.BadRequest("INVALID_CATEGORY", "Choose one of the listed reasons")
	}
	if req.BookingID == nil && req.ArtistID == nil {
		return nil, apperror.BadRequest("SUBJECT_REQUIRED", "Tell us which booking or artist this is about")
	}

	rep := &Report{
		ReporterUserID: userID,
		ReporterRole:   role,
		Category:       Category(req.Category),
		Description:    req.Description,
	}

	if req.BookingID != nil {
		bookingID, err := uuid.Parse(*req.BookingID)
		if err != nil {
			return nil, apperror.BadRequest("INVALID_BOOKING_ID", "Invalid booking ID")
		}
		owns, err := s.repo.BookingBelongsTo(ctx, bookingID, userID)
		if err != nil {
			return nil, err
		}
		if !owns {
			// Same answer whether the booking is someone else's or does not
			// exist. Anything else lets a caller confirm a real booking id.
			return nil, apperror.NotFound("BOOKING_NOT_FOUND", "Booking not found")
		}
		rep.BookingID = &bookingID

		// Attribute the report to the artist even when the reporter only gave
		// a booking, so "how many payment-details reports against this artist"
		// is answerable - which is how an impersonation campaign gets caught.
		if artistID, err := s.repo.ArtistIDForBooking(ctx, bookingID); err == nil {
			rep.ArtistID = &artistID
		}
	}

	if req.ArtistID != nil && rep.ArtistID == nil {
		artistID, err := uuid.Parse(*req.ArtistID)
		if err != nil {
			return nil, apperror.BadRequest("INVALID_ARTIST_ID", "Invalid artist ID")
		}
		rep.ArtistID = &artistID
	}

	if err := s.repo.Create(ctx, rep); err != nil {
		if errors.Is(err, ErrDuplicateOpen) {
			return nil, apperror.Conflict("REPORT_ALREADY_OPEN",
				"You have already reported this booking. We are looking into it.")
		}
		return nil, err
	}

	out := toResponse(rep)
	return &out, nil
}

// ListMine returns the caller's own reports so they can see what happened.
func (s *Service) ListMine(ctx context.Context, userID uuid.UUID) ([]Response, error) {
	rows, err := s.repo.ListForReporter(ctx, userID)
	if err != nil {
		return nil, err
	}
	out := make([]Response, 0, len(rows))
	for _, r := range rows {
		out = append(out, toResponse(r))
	}
	return out, nil
}

// ListQueue returns the admin queue.
func (s *Service) ListQueue(ctx context.Context, includeResolved bool) ([]*AdminResponse, error) {
	return s.repo.ListQueue(ctx, includeResolved)
}

// Resolve moves a report along.
func (s *Service) Resolve(ctx context.Context, id, adminID uuid.UUID, req ResolveReportRequest, ip string) error {
	if !req.Status.Valid() {
		return apperror.BadRequest("INVALID_STATUS", "Unknown status")
	}
	// Closing a report without saying what was done leaves the next person
	// looking at the pattern with nothing. Moving it to 'reviewing' does not
	// need one - nothing has been decided yet.
	if req.Status.IsTerminal() && (req.Note == nil || strings.TrimSpace(*req.Note) == "") {
		return apperror.BadRequest("NOTE_REQUIRED", "Record what was done about this")
	}

	rows, err := s.repo.Resolve(ctx, id, adminID, req.Status, req.Note)
	if err != nil {
		return fmt.Errorf("resolve report: %w", err)
	}
	if rows == 0 {
		return apperror.Conflict("REPORT_NOT_OPEN", "This report has already been dealt with")
	}

	_ = s.audit.Log(ctx, audit.Event{
		ActorID:    &adminID,
		ActorRole:  "admin",
		EntityType: "report",
		EntityID:   id,
		Action:     "report_" + string(req.Status),
		NewValues:  map[string]any{"status": string(req.Status), "note": req.Note},
		IPAddress:  ip,
	})
	return nil
}
