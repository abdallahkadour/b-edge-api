package offering

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/money"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/optional"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// UpdateRequest is the body of both PUT endpoints.
//
// Offered is REQUIRED (a pointer so absent and false differ): as a plain bool
// an omitted key read as false, and false deletes her row and her custom
// price - so a body meant only to change the deposit switched the service
// off. Absent or null is a 422 on "offered".
//
// Price and DepositAmount are optional.Field: absent keeps her override,
// null clears it to the salon's value, a string sets it (PP-2).
type UpdateRequest struct {
	Offered       *bool                  `json:"offered" validate:"required"`
	Price         optional.Field[string] `json:"price"`
	DepositAmount optional.Field[string] `json:"deposit_amount"`
}

type auditLogger interface {
	Log(ctx context.Context, e audit.Event) error
}

type Service struct {
	repo     Repository
	audit    auditLogger
	validate *validator.Validate
}

func NewService(repo Repository, a auditLogger) *Service {
	return &Service{repo: repo, audit: a, validate: validation.New()}
}

func errServiceNotFound() error {
	return apperror.NotFound("SERVICE_NOT_FOUND", "Service not found or no longer available")
}
func errMemberNotFound() error { return apperror.NotFound("MEMBER_NOT_FOUND", "Artist not found") }

func (s *Service) ListMine(ctx context.Context, salonID, userID uuid.UUID) ([]*Offering, error) {
	artistID, err := s.repo.ArtistIDForUser(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil, errMemberNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("list mine: %w", err)
	}
	// salon_id and salon_role come off the access token, which stays valid
	// until it expires even after membership.Leave revokes refresh tokens.
	// Confirm she is STILL in the token's salon, not only that the token
	// says so (ruling 9).
	ok, err := s.repo.ArtistInSalon(ctx, artistID, salonID)
	if err != nil {
		return nil, fmt.Errorf("list mine: %w", err)
	}
	if !ok {
		return nil, errMemberNotFound()
	}
	return s.repo.List(ctx, salonID, artistID)
}

func (s *Service) ListForMember(ctx context.Context, salonID, memberArtistID uuid.UUID) ([]*Offering, error) {
	ok, err := s.repo.ArtistInSalon(ctx, memberArtistID, salonID)
	if err != nil {
		return nil, fmt.Errorf("list for member: %w", err)
	}
	if !ok {
		return nil, errMemberNotFound()
	}
	return s.repo.List(ctx, salonID, memberArtistID)
}

func (s *Service) UpdateMine(ctx context.Context, salonID, userID, serviceID uuid.UUID,
	req UpdateRequest, ip string) (*Offering, error) {
	artistID, err := s.repo.ArtistIDForUser(ctx, userID)
	if errors.Is(err, ErrNotFound) {
		return nil, errMemberNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("update mine: %w", err)
	}
	// salon_id and salon_role come off the access token, which stays valid
	// until it expires even after membership.Leave revokes refresh tokens.
	// Without this, a since-revoked artist could still write
	// artist_services rows for her FORMER salon in that window - rows that
	// can never be booked, but would silently switch services back on if
	// she later rejoins (breaking PP-7: "joining a salon: all switches
	// off"). Confirm she is STILL in the token's salon (ruling 9).
	ok, err := s.repo.ArtistInSalon(ctx, artistID, salonID)
	if err != nil {
		return nil, fmt.Errorf("update mine: %w", err)
	}
	if !ok {
		return nil, errMemberNotFound()
	}
	return s.update(ctx, salonID, artistID, serviceID, userID, req, ip)
}

// UpdateForMember is the owner acting on a member's behalf (PP-3). The member
// must be in the caller's salon; otherwise 404, indistinguishable from absent.
func (s *Service) UpdateForMember(ctx context.Context, salonID, actorUserID, memberArtistID,
	serviceID uuid.UUID, req UpdateRequest, ip string) (*Offering, error) {
	ok, err := s.repo.ArtistInSalon(ctx, memberArtistID, salonID)
	if err != nil {
		return nil, fmt.Errorf("update for member: %w", err)
	}
	if !ok {
		return nil, errMemberNotFound()
	}
	return s.update(ctx, salonID, memberArtistID, serviceID, actorUserID, req, ip)
}

func (s *Service) update(ctx context.Context, salonID, artistID, serviceID, actorUserID uuid.UUID,
	req UpdateRequest, ip string) (*Offering, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, validation.MapError(err)
	}
	current, err := s.repo.Get(ctx, salonID, artistID, serviceID)
	if errors.Is(err, ErrNotFound) {
		return nil, errServiceNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("update offering: %w", err)
	}

	// Switching off deletes the row, INCLUDING her custom price - "no row"
	// is what "not offered" means (spec §5). Existing bookings are untouched.
	if !*req.Offered {
		if current.Offered {
			if err := s.repo.Delete(ctx, artistID, serviceID); err != nil {
				return nil, err
			}
			s.log(ctx, auditEntry{salonID: salonID, actor: actorUserID, ip: ip, action: "offering.off",
				old: snapshot(artistID, serviceID, true, current.OwnPrice, current.OwnDeposit),
				new: snapshot(artistID, serviceID, false, nil, nil)})
		}
		return s.repo.Get(ctx, salonID, artistID, serviceID)
	}

	p := UpsertParams{ArtistID: artistID, ServiceID: serviceID, ActorUserID: actorUserID}
	newPrice, newDeposit := current.OwnPrice, current.OwnDeposit
	if req.Price.IsSet() {
		p.PriceSet = true
		if !req.Price.IsNull() {
			v, _ := req.Price.Get()
			d, err := money.Parse(v, "price")
			if err != nil {
				return nil, err
			}
			p.Price = &d
		}
		newPrice = p.Price
	}
	if req.DepositAmount.IsSet() {
		p.DepositSet = true
		if !req.DepositAmount.IsNull() {
			v, _ := req.DepositAmount.Get()
			d, err := money.Parse(v, "deposit_amount")
			if err != nil {
				return nil, err
			}
			p.Deposit = &d
		}
		newDeposit = p.Deposit
	}

	// Her OWN deposit above the price she will charge is refused. A BLANK
	// deposit follows the salon's and is capped when read (PP-6).
	effective := current.SalonPrice
	if newPrice != nil {
		effective = *newPrice
	}
	if newDeposit != nil && newDeposit.GreaterThan(effective) {
		return nil, apperror.UnprocessableEntity("VALIDATION_ERROR", []apperror.FieldError{{
			Field:   "deposit_amount",
			Message: "The deposit can't be more than the price ($" + effective.StringFixed(2) + ")",
		}})
	}

	if err := s.repo.Upsert(ctx, p); err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, errServiceNotFound()
		}
		return nil, err
	}
	s.log(ctx, auditEntry{salonID: salonID, actor: actorUserID, ip: ip, action: "offering.update",
		old: snapshot(artistID, serviceID, current.Offered, current.OwnPrice, current.OwnDeposit),
		new: snapshot(artistID, serviceID, true, newPrice, newDeposit)})
	return s.repo.Get(ctx, salonID, artistID, serviceID)
}

// offeringSnapshot is one side of an artist_service audit row. artist_id is
// the point: every artist in the salon can have a row for the same service,
// so "service X changed" alone cannot say WHOSE price it was - and when the
// owner changes a member's price, the actor is the owner, not her.
type offeringSnapshot struct {
	ArtistID      uuid.UUID        `json:"artist_id"`
	ServiceID     uuid.UUID        `json:"service_id"`
	Offered       bool             `json:"offered"`
	Price         *decimal.Decimal `json:"price"`          // her override; null = the salon's
	DepositAmount *decimal.Decimal `json:"deposit_amount"` // her override; null = the salon's
}

func snapshot(artistID, serviceID uuid.UUID, offered bool, price, deposit *decimal.Decimal) offeringSnapshot {
	return offeringSnapshot{ArtistID: artistID, ServiceID: serviceID, Offered: offered,
		Price: price, DepositAmount: deposit}
}

type auditEntry struct {
	salonID, actor uuid.UUID
	ip, action     string
	old, new       offeringSnapshot
}

// log records the change. ActorRole is "artist" because only an artist
// holding a salon capability reaches these routes (an admin token carries no
// salon and is refused NO_SALON first) - the same value internal/membership
// records for the same actors. The IP is threaded from the handler
// (clientip.From), as membership does.
func (s *Service) log(ctx context.Context, e auditEntry) {
	_ = s.audit.Log(ctx, audit.Event{
		SalonID: &e.salonID, ActorID: &e.actor, ActorRole: "artist", EntityType: "artist_service",
		EntityID: e.new.ServiceID, Action: e.action, OldValues: e.old, NewValues: e.new,
		IPAddress: e.ip,
	})
}
