package offering

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// Offering is one row of the settings screen: a service on the salon's menu,
// whether this artist has switched it on, the salon's price and deposit, her
// own override if she set one, and what actually applies.
type Offering struct {
	ServiceID        uuid.UUID        `json:"service_id"`
	ServiceName      string           `json:"service_name"`
	DurationMin      int              `json:"duration_min"`
	Offered          bool             `json:"offered"`
	SalonPrice       decimal.Decimal  `json:"salon_price"`
	SalonDeposit     decimal.Decimal  `json:"salon_deposit"`
	OwnPrice         *decimal.Decimal `json:"own_price"`
	OwnDeposit       *decimal.Decimal `json:"own_deposit"`
	EffectivePrice   decimal.Decimal  `json:"effective_price"`
	EffectiveDeposit decimal.Decimal  `json:"effective_deposit"`
	DepositCapped    bool             `json:"deposit_capped"`
	UpdatedByName    *string          `json:"updated_by_name,omitempty"`
}

// Repository is the settings-screen contract: list her whole menu with the
// offered flag, look up one service, switch a service on with optional price
// and deposit overrides, and switch one off.
type Repository interface {
	ArtistIDForUser(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
	ArtistInSalon(ctx context.Context, artistID, salonID uuid.UUID) (bool, error)
	List(ctx context.Context, salonID, artistID uuid.UUID) ([]*Offering, error)
	Get(ctx context.Context, salonID, artistID, serviceID uuid.UUID) (*Offering, error) // ErrNotFound
	Upsert(ctx context.Context, p UpsertParams) error
	Delete(ctx context.Context, artistID, serviceID uuid.UUID) error
}

// UpsertParams carries the presence/value pair for each clearable field: Set
// false leaves the stored value alone, Set true with a nil pointer clears it
// back to the salon's, and Set true with a value applies it.
type UpsertParams struct {
	ArtistID, ServiceID, ActorUserID uuid.UUID
	PriceSet                         bool
	Price                            *decimal.Decimal // nil with PriceSet = clear to the salon's
	DepositSet                       bool
	Deposit                          *decimal.Decimal
}

// ErrNotFound is returned by Get when the service does not belong to the
// given salon, or does not exist.
var ErrNotFound = errors.New("offering: not found")
