// Package artist implements the artist domain for B-Edge,
// including profile management, store assignment, service catalogue,
// and business hours configuration.
package artist

import (
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// ── Sentinel errors ───────────────────────────────────────────────────────────

var (
	// ErrArtistNotFound is returned when no artist matches the given criteria.
	ErrArtistNotFound = errors.New("artist not found")

	// ErrStoreNotFound is returned when no store matches the given criteria.
	ErrStoreNotFound = errors.New("store not found")

	// ErrServiceNotFound is returned when no service matches the given criteria.
	ErrServiceNotFound = errors.New("service not found")

	// ErrNotArtistOwner is returned when a user tries to modify another artist's profile.
	ErrNotArtistOwner = errors.New("not authorised to modify this artist profile")

	// ErrDuplicateStore is returned when an artist is already assigned to a store.
	ErrDuplicateStore = errors.New("artist already assigned to this store")
)

// ── Core structs ──────────────────────────────────────────────────────────────

// Artist represents a beauty professional's profile from the artists table.
type Artist struct {
	ID          uuid.UUID       `db:"id"`
	UserID      uuid.UUID       `db:"user_id"`
	SalonID     *uuid.UUID      `db:"salon_id"`
	Bio         *string         `db:"bio"`
	BioAr       *string         `db:"bio_ar"`
	Instagram   *string         `db:"instagram"`
	Rating      decimal.Decimal `db:"rating"`
	ReviewCount int             `db:"review_count"`
	IsVerified  bool            `db:"is_verified"`
	CreatedAt   time.Time       `db:"created_at"`
	UpdatedAt   time.Time       `db:"updated_at"`
}

// ArtistProfile is the full public profile returned to clients.
// Combines artist fields with user fields (name, phone).
type ArtistProfile struct {
	ID          uuid.UUID       `db:"id"`
	UserID      uuid.UUID       `db:"user_id"`
	SalonID     *uuid.UUID      `db:"salon_id"`
	Name        string          `db:"name"`
	Email       string          `db:"email"`
	Phone       *string         `db:"phone"`
	Bio         *string         `db:"bio"`
	BioAr       *string         `db:"bio_ar"`
	Instagram   *string         `db:"instagram"`
	Rating      decimal.Decimal `db:"rating"`
	ReviewCount int             `db:"review_count"`
	IsVerified  bool            `db:"is_verified"`
	CreatedAt   time.Time       `db:"created_at"`
	UpdatedAt   time.Time       `db:"updated_at"`
}

// Store represents a physical salon location from the stores table.
type Store struct {
	ID                 uuid.UUID       `db:"id"`
	SalonID            uuid.UUID       `db:"salon_id"`
	Name               string          `db:"name"`
	NameAr             *string         `db:"name_ar"`
	Address            *string         `db:"address"`
	City               string          `db:"city"`
	Country            string          `db:"country"`
	Phone              *string         `db:"phone"`
	SameDayNoticeHours int             `db:"same_day_notice_hours"`
	EarlyBirdCutoff    *string         `db:"early_bird_cutoff"`
	EarlyBirdFee       decimal.Decimal `db:"early_bird_fee"`
	WeekdayBufferMin   int             `db:"weekday_buffer_min"`
	WeekendBufferMin   int             `db:"weekend_buffer_min"`
	IsActive           bool            `db:"is_active"`
	CreatedAt          time.Time       `db:"created_at"`
	UpdatedAt          time.Time       `db:"updated_at"`
}

// Service represents a service offered by a salon.
type SalonServiceRecord struct {
	ID                   uuid.UUID       `db:"id"`
	SalonID              uuid.UUID       `db:"salon_id"`
	CategoryID           *uuid.UUID      `db:"category_id"`
	Name                 string          `db:"name"`
	NameAr               *string         `db:"name_ar"`
	Description          *string         `db:"description"`
	DurationMin          int             `db:"duration_min"`
	ActiveDurationMin    *int            `db:"active_duration_min"`
	Price                decimal.Decimal `db:"price"`
	DepositAmount        decimal.Decimal `db:"deposit_amount"`
	DepositDeadlineHours int             `db:"deposit_deadline_hours"`
	IsActive             bool            `db:"is_active"`
	IsCustom             bool            `db:"is_custom"`
	CreatedAt            time.Time       `db:"created_at"`
	UpdatedAt            time.Time       `db:"updated_at"`
}

// BusinessHours represents working hours for a store on a specific day.
type BusinessHours struct {
	ID        uuid.UUID `db:"id"`
	StoreID   uuid.UUID `db:"store_id"`
	DayOfWeek int       `db:"day_of_week"`
	OpenTime  string    `db:"open_time"`
	CloseTime string    `db:"close_time"`
	IsOpen    bool      `db:"is_open"`
	CreatedAt time.Time `db:"created_at"`
}

// BusinessHoursException overrides regular hours for a specific date.
type BusinessHoursException struct {
	ID            uuid.UUID `db:"id"`
	StoreID       uuid.UUID `db:"store_id"`
	ExceptionDate time.Time `db:"exception_date"`
	IsClosed      bool      `db:"is_closed"`
	OpenTime      *string   `db:"open_time"`
	CloseTime     *string   `db:"close_time"`
	Reason        *string   `db:"reason"`
	CreatedAt     time.Time `db:"created_at"`
}

// ── Request structs ───────────────────────────────────────────────────────────

// UpdateProfileRequest is the request body for PATCH /api/v1/artists/:id.
type UpdateProfileRequest struct {
	Bio       *string `json:"bio"       validate:"omitempty,max=500"`
	BioAr     *string `json:"bio_ar"    validate:"omitempty,max=500"`
	Instagram *string `json:"instagram" validate:"omitempty,max=255"`
}

// CreateServiceRequest is the request body for POST /api/v1/artists/services.
type CreateServiceRequest struct {
	Name                 string  `json:"name"                   validate:"required,min=2,max=200"`
	NameAr               *string `json:"name_ar"                validate:"omitempty,max=200"`
	Description          *string `json:"description"`
	DurationMin          int     `json:"duration_min"           validate:"required,min=15,max=480"`
	ActiveDurationMin    *int    `json:"active_duration_min"    validate:"omitempty,min=15"`
	Price                string  `json:"price"                  validate:"required"`
	DepositAmount        string  `json:"deposit_amount"         validate:"required"`
	DepositDeadlineHours int     `json:"deposit_deadline_hours" validate:"required,min=1,max=168"`
	CategoryID           *string `json:"category_id"            validate:"omitempty,uuid"`
}

// UpdateServiceRequest is the request body for PATCH /api/v1/artists/services/:id.
type UpdateServiceRequest struct {
	Name                 *string `json:"name"                   validate:"omitempty,min=2,max=200"`
	NameAr               *string `json:"name_ar"                validate:"omitempty,max=200"`
	Description          *string `json:"description"`
	DurationMin          *int    `json:"duration_min"           validate:"omitempty,min=15,max=480"`
	Price                *string `json:"price"                  validate:"omitempty"`
	DepositAmount        *string `json:"deposit_amount"         validate:"omitempty"`
	DepositDeadlineHours *int    `json:"deposit_deadline_hours" validate:"omitempty,min=1"`
	IsActive             *bool   `json:"is_active"`
}

// SetBusinessHoursRequest sets working hours for a store on a specific day.
type SetBusinessHoursRequest struct {
	DayOfWeek int    `json:"day_of_week" validate:"required,min=0,max=6"`
	OpenTime  string `json:"open_time"   validate:"required"`
	CloseTime string `json:"close_time"  validate:"required"`
	IsOpen    bool   `json:"is_open"`
}

// CreateExceptionRequest creates a holiday or special-hours exception.
type CreateExceptionRequest struct {
	ExceptionDate string  `json:"exception_date" validate:"required"`
	IsClosed      bool    `json:"is_closed"`
	OpenTime      *string `json:"open_time"      validate:"omitempty"`
	CloseTime     *string `json:"close_time"     validate:"omitempty"`
	Reason        *string `json:"reason"         validate:"omitempty,max=255"`
}

// ── Response structs ──────────────────────────────────────────────────────────

// ArtistResponse is the safe public representation of an artist.
type ArtistResponse struct {
	ID          uuid.UUID       `json:"id"`
	Name        string          `json:"name"`
	Bio         *string         `json:"bio,omitempty"`
	BioAr       *string         `json:"bio_ar,omitempty"`
	Instagram   *string         `json:"instagram,omitempty"`
	Rating      decimal.Decimal `json:"rating"`
	ReviewCount int             `json:"review_count"`
	IsVerified  bool            `json:"is_verified"`
}

// ServiceResponse is the safe representation of a service.
type ServiceResponse struct {
	ID                   uuid.UUID       `json:"id"`
	SalonID              uuid.UUID       `json:"salon_id"`
	Name                 string          `json:"name"`
	NameAr               *string         `json:"name_ar,omitempty"`
	Description          *string         `json:"description,omitempty"`
	DurationMin          int             `json:"duration_min"`
	ActiveDurationMin    *int            `json:"active_duration_min,omitempty"`
	Price                decimal.Decimal `json:"price"`
	DepositAmount        decimal.Decimal `json:"deposit_amount"`
	DepositDeadlineHours int             `json:"deposit_deadline_hours"`
	IsActive             bool            `json:"is_active"`
}
