// Package artist implements the artist domain for B-Edge.
package artist

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/billing"
)

// subscriptionVisibleCond mirrors discovery.subscriptionVisibleCond exactly —
// an artist is publicly reachable only when their subscription is in a visible
// state (comped, trialing, active, or grace). past_due and suspended artists
// are hidden from Discover AND unreachable via direct artist-domain lookups,
// so there is no gap between search and direct-URL navigation.
//
// Uses billing.GraceDays (not a locally duplicated literal) so this can never
// drift from DeriveStatus's own grace/past_due boundary, for the same reason
// discovery.subscriptionVisibleCond does the same. If either the grace window
// or this condition changes, both usages update from one constant.
//
// The alias used in the EXISTS subquery must match the alias used in whatever
// outer query this is embedded in — both GetArtistByID and the handle/UUID
// queries below alias artists as 'a', so this works without modification.
var subscriptionVisibleCond = fmt.Sprintf(`EXISTS (
	SELECT 1 FROM subscriptions sub
	WHERE sub.artist_id = a.id
	AND (
		sub.cancelled_at IS NOT NULL
		OR sub.plan_code = 'comped'
		OR (sub.trial_ends_at IS NOT NULL AND NOW() < sub.trial_ends_at)
		OR (sub.current_period_end IS NOT NULL AND NOW() < sub.current_period_end + INTERVAL '%d days')
	)
)`, billing.GraceDays)

// uniqueViolationCode is the PostgreSQL error code for unique constraint violations.
const uniqueViolationCode = "23505"

// Repository defines all database operations for the artist domain.
type Repository interface {
	GetArtistByID(ctx context.Context, artistID uuid.UUID) (*ArtistProfile, error)
	GetArtistByUserID(ctx context.Context, userID uuid.UUID) (*ArtistProfile, error)
	// GetArtistIDByHandle resolves a public handle (e.g. "rania") to the
	// artist's real UUID. Returns ErrArtistNotFound if no artist has that
	// handle - deliberately the same error a bad UUID lookup returns, so
	// the caller can't distinguish "handle doesn't exist" from "UUID
	// doesn't exist" and probe for which handles are taken.
	GetArtistIDByHandle(ctx context.Context, handle string) (uuid.UUID, error)
	IsArtistActive(ctx context.Context, artistID uuid.UUID) (bool, error)
	UpdateArtistProfile(ctx context.Context, artistID uuid.UUID, req UpdateProfileRequest) error
	GetStoresByArtist(ctx context.Context, artistID uuid.UUID) ([]*Store, error)
	GetStoresBySalon(ctx context.Context, salonID uuid.UUID) ([]*Store, error)
	GetStoreByID(ctx context.Context, storeID uuid.UUID) (*Store, error)
	// CreateStore inserts a new store and assigns artistID to work there, in
	// a single transaction - a store nobody is assigned to would be
	// invisible to GetStoresByArtist (which is what the booking funnel and
	// the availability algorithm both actually query), making it a
	// dead, unusable row rather than a partial success.
	CreateStore(ctx context.Context, store *Store, artistID uuid.UUID) error
	UpdateStore(ctx context.Context, storeID uuid.UUID, req UpdateStoreRequest) error
	GetServicesBySalon(ctx context.Context, salonID uuid.UUID) ([]*SalonServiceRecord, error)
	GetServiceByID(ctx context.Context, id uuid.UUID) (*SalonServiceRecord, error)
	CreateService(ctx context.Context, s *SalonServiceRecord) error
	UpdateService(ctx context.Context, id uuid.UUID, req UpdateServiceRequest) error
	DeleteService(ctx context.Context, id uuid.UUID) error
	GetBusinessHours(ctx context.Context, storeID uuid.UUID) ([]*BusinessHours, error)
	SetBusinessHours(ctx context.Context, storeID uuid.UUID, req SetBusinessHoursRequest) error
	GetExceptions(ctx context.Context, storeID uuid.UUID) ([]*BusinessHoursException, error)
	CreateException(ctx context.Context, storeID uuid.UUID, req CreateExceptionRequest) error
	DeleteException(ctx context.Context, storeID uuid.UUID, date time.Time) error
}

// pgRepo is the PostgreSQL implementation of Repository.
type pgRepo struct {
	db *pgxpool.Pool
}

// NewRepository creates an artist repository backed by the given pool.
func NewRepository(db *pgxpool.Pool) Repository {
	return &pgRepo{db: db}
}

// CreateStore inserts a new store row and, in the same transaction, assigns
// artistID to work there via artist_stores. Everything past salon/name/
// city/address/phone is left to the stores table's own column defaults
// (same_day_notice_hours, buffers, timezone) - the RETURNING clause reads
// them back so the caller gets a complete Store, matching every other
// create-then-return method in this codebase.
func (r *pgRepo) CreateStore(ctx context.Context, store *Store, artistID uuid.UUID) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("create store: begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit

	err = tx.QueryRow(ctx, `
		INSERT INTO stores (salon_id, name, name_ar, address, city, phone)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, country, same_day_notice_hours, early_bird_cutoff, early_bird_fee,
		          weekday_buffer_min, weekend_buffer_min, timezone, is_active,
		          created_at, updated_at`,
		store.SalonID, store.Name, store.NameAr, store.Address, store.City, store.Phone,
	).Scan(
		&store.ID, &store.Country, &store.SameDayNoticeHours, &store.EarlyBirdCutoff, &store.EarlyBirdFee,
		&store.WeekdayBufferMin, &store.WeekendBufferMin, &store.Timezone, &store.IsActive,
		&store.CreatedAt, &store.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create store: insert store: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO artist_stores (artist_id, store_id)
		VALUES ($1, $2)`,
		artistID, store.ID,
	); err != nil {
		return fmt.Errorf("create store: assign artist: %w", err)
	}

	return tx.Commit(ctx)
}

// UpdateStore applies a partial update to a store's settings.
//
// COALESCE($n, col) preserves the existing value when a field is omitted.
// early_bird_cutoff is the exception: it is nullable, and COALESCE alone
// cannot clear a column - passing NULL preserves rather than clears. The
// CASE branch lets an explicit empty string mean "remove the cutoff".
func (r *pgRepo) UpdateStore(ctx context.Context, storeID uuid.UUID, req UpdateStoreRequest) error {
	tag, err := r.db.Exec(ctx, `
		UPDATE stores SET
			name                  = COALESCE($2, name),
			name_ar               = COALESCE($3, name_ar),
			address               = COALESCE($4, address),
			phone                 = COALESCE($5, phone),
			same_day_notice_hours = COALESCE($6, same_day_notice_hours),
			early_bird_cutoff     = CASE WHEN $7 = '' THEN NULL ELSE COALESCE($7::TIME, early_bird_cutoff) END,
			early_bird_fee        = COALESCE($8::NUMERIC, early_bird_fee),
			weekday_buffer_min    = COALESCE($9, weekday_buffer_min),
			weekend_buffer_min    = COALESCE($10, weekend_buffer_min),
			timezone              = COALESCE($11, timezone),
			is_active             = COALESCE($12, is_active),
			updated_at            = NOW()
		WHERE id = $1`,
		storeID,
		req.Name, req.NameAr, req.Address, req.Phone,
		req.SameDayNoticeHours, req.EarlyBirdCutoff, req.EarlyBirdFee,
		req.WeekdayBufferMin, req.WeekendBufferMin,
		req.Timezone, req.IsActive,
	)
	if err != nil {
		return fmt.Errorf("update store: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrStoreNotFound
	}
	return nil
}

// ── Artist profile ────────────────────────────────────────────────────────────

func (r *pgRepo) GetArtistByID(ctx context.Context, artistID uuid.UUID) (*ArtistProfile, error) {
	p := &ArtistProfile{}
	err := r.db.QueryRow(ctx, `
		SELECT a.id, a.user_id, a.salon_id, a.handle,
		       u.name, u.email, u.phone,
		       a.bio, a.bio_ar, a.instagram, a.avatar_url,
		       a.rating, a.review_count, a.is_verified,
		       a.created_at, a.updated_at
		FROM artists a
		JOIN users u ON u.id = a.user_id
		WHERE a.id = $1
		AND a.status = 'active'
		AND u.deleted_at IS NULL
		AND `+subscriptionVisibleCond,
		artistID,
	).Scan(
		&p.ID, &p.UserID, &p.SalonID, &p.Handle,
		&p.Name, &p.Email, &p.Phone,
		&p.Bio, &p.BioAr, &p.Instagram, &p.AvatarURL,
		&p.Rating, &p.ReviewCount, &p.IsVerified,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArtistNotFound
		}
		return nil, fmt.Errorf("get artist by id: %w", err)
	}
	return p, nil
}

// GetArtistIDByHandle resolves a public handle to the artist's UUID.
func (r *pgRepo) GetArtistIDByHandle(ctx context.Context, handle string) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx,
		`SELECT a.id FROM artists a WHERE a.handle = $1 AND a.status = 'active' AND `+subscriptionVisibleCond,
		handle,
	).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrArtistNotFound
		}
		return uuid.Nil, fmt.Errorf("get artist id by handle: %w", err)
	}
	return id, nil
}

// IsArtistActive reports whether an artist ID resolves to an active,
// reviewed profile - the check used to close the OTHER half of
// ResolveArtistID: a caller who already has a pending artist's raw UUID
// (an old shared link, or simply guessing) bypasses the handle lookup
// entirely, since a valid UUID needs no database round-trip on its own to
// parse. Every public, handle-or-UUID entry point must go through this,
// not just the handle path.
func (r *pgRepo) IsArtistActive(ctx context.Context, artistID uuid.UUID) (bool, error) {
	var exists bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM artists a WHERE a.id = $1 AND a.status = 'active' AND `+subscriptionVisibleCond+`)`,
		artistID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("is artist active: %w", err)
	}
	return exists, nil
}

func (r *pgRepo) GetArtistByUserID(ctx context.Context, userID uuid.UUID) (*ArtistProfile, error) {
	p := &ArtistProfile{}
	err := r.db.QueryRow(ctx, `
		SELECT a.id, a.user_id, a.salon_id, a.handle,
		       u.name, u.email, u.phone,
		       a.bio, a.bio_ar, a.instagram, a.avatar_url,
		       a.rating, a.review_count, a.is_verified,
		       a.created_at, a.updated_at
		FROM artists a
		JOIN users u ON u.id = a.user_id
		WHERE a.user_id = $1
		AND u.deleted_at IS NULL`,
		userID,
	).Scan(
		&p.ID, &p.UserID, &p.SalonID, &p.Handle,
		&p.Name, &p.Email, &p.Phone,
		&p.Bio, &p.BioAr, &p.Instagram, &p.AvatarURL,
		&p.Rating, &p.ReviewCount, &p.IsVerified,
		&p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrArtistNotFound
		}
		return nil, fmt.Errorf("get artist by user id: %w", err)
	}
	return p, nil
}

func (r *pgRepo) UpdateArtistProfile(ctx context.Context, artistID uuid.UUID, req UpdateProfileRequest) error {
	_, err := r.db.Exec(ctx, `
		UPDATE artists
		SET handle     = COALESCE($1, handle),
		    bio        = COALESCE($2, bio),
		    bio_ar     = COALESCE($3, bio_ar),
		    instagram  = COALESCE($4, instagram),
		    avatar_url = CASE WHEN $5 = '' THEN NULL ELSE COALESCE($5, avatar_url) END,
		    updated_at = NOW()
		WHERE id = $6`,
		req.Handle, req.Bio, req.BioAr, req.Instagram, req.AvatarURL, artistID,
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode {
			return ErrHandleTaken
		}
		return fmt.Errorf("update artist profile: %w", err)
	}
	return nil
}

// ── Stores ────────────────────────────────────────────────────────────────────

// GetStoreByID returns a single store by ID, regardless of active state.
// Used for ownership checks before an update, where an inactive store must
// still resolve.
func (r *pgRepo) GetStoreByID(ctx context.Context, storeID uuid.UUID) (*Store, error) {
	s := &Store{}
	err := r.db.QueryRow(ctx, `
		SELECT id, salon_id, name, name_ar, address, city, country, phone,
		       same_day_notice_hours, early_bird_cutoff, early_bird_fee,
		       weekday_buffer_min, weekend_buffer_min,
		       timezone, is_active, created_at, updated_at
		FROM stores
		WHERE id = $1`,
		storeID,
	).Scan(
		&s.ID, &s.SalonID, &s.Name, &s.NameAr, &s.Address, &s.City, &s.Country, &s.Phone,
		&s.SameDayNoticeHours, &s.EarlyBirdCutoff, &s.EarlyBirdFee,
		&s.WeekdayBufferMin, &s.WeekendBufferMin,
		&s.Timezone, &s.IsActive, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrStoreNotFound
		}
		return nil, fmt.Errorf("get store by id: %w", err)
	}
	return s, nil
}

func (r *pgRepo) GetStoresByArtist(ctx context.Context, artistID uuid.UUID) ([]*Store, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.id, s.salon_id, s.name, s.name_ar, s.address,
		       s.city, s.country, s.phone,
		       s.same_day_notice_hours, s.early_bird_cutoff, s.early_bird_fee,
		       s.weekday_buffer_min, s.weekend_buffer_min,
		       s.timezone, s.is_active, s.created_at, s.updated_at
		FROM stores s
		JOIN artist_stores ast ON ast.store_id = s.id
		WHERE ast.artist_id = $1
		AND s.is_active = TRUE
		ORDER BY s.name ASC`,
		artistID,
	)
	if err != nil {
		return nil, fmt.Errorf("get stores by artist: %w", err)
	}
	defer rows.Close()
	return scanStores(rows)
}

// GetStoresBySalon returns every store owned by this salon, active or not -
// deliberately unfiltered, unlike GetStoresByArtist below. This is the
// artist-only route (GET /artists/salon/stores, RequireRole("artist")) that
// backs their own Hours screen; if it hid inactive stores the same way the
// customer-facing GetStoresByArtist correctly does, an artist who
// deactivated a store (see UpdateStore) would lose all access to it -
// nothing left to click to reactivate. An owner should always see
// everything they own; a customer should only ever see what's bookable.
func (r *pgRepo) GetStoresBySalon(ctx context.Context, salonID uuid.UUID) ([]*Store, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, salon_id, name, name_ar, address,
		       city, country, phone,
		       same_day_notice_hours, early_bird_cutoff, early_bird_fee,
		       weekday_buffer_min, weekend_buffer_min,
		       timezone, is_active, created_at, updated_at
		FROM stores
		WHERE salon_id = $1
		ORDER BY name ASC`,
		salonID,
	)
	if err != nil {
		return nil, fmt.Errorf("get stores by salon: %w", err)
	}
	defer rows.Close()
	return scanStores(rows)
}

// ── Services ──────────────────────────────────────────────────────────────────

func (r *pgRepo) GetServicesBySalon(ctx context.Context, salonID uuid.UUID) ([]*SalonServiceRecord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, salon_id, category_id, name, name_ar, description,
		       duration_min, active_duration_min, price,
		       deposit_amount, deposit_deadline_hours,
		       is_active, is_custom, created_at, updated_at
		FROM services
		WHERE salon_id = $1
		ORDER BY is_active DESC, name ASC`,
		salonID,
	)
	if err != nil {
		return nil, fmt.Errorf("get services by salon: %w", err)
	}
	defer rows.Close()
	return scanServices(rows)
}

func (r *pgRepo) GetServiceByID(ctx context.Context, id uuid.UUID) (*SalonServiceRecord, error) {
	s := &SalonServiceRecord{}
	err := r.db.QueryRow(ctx, `
		SELECT id, salon_id, category_id, name, name_ar, description,
		       duration_min, active_duration_min, price,
		       deposit_amount, deposit_deadline_hours,
		       is_active, is_custom, created_at, updated_at
		FROM services
		WHERE id = $1`,
		id,
	).Scan(
		&s.ID, &s.SalonID, &s.CategoryID, &s.Name, &s.NameAr, &s.Description,
		&s.DurationMin, &s.ActiveDurationMin, &s.Price,
		&s.DepositAmount, &s.DepositDeadlineHours,
		&s.IsActive, &s.IsCustom, &s.CreatedAt, &s.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrServiceNotFound
		}
		return nil, fmt.Errorf("get service by id: %w", err)
	}
	return s, nil
}

func (r *pgRepo) CreateService(ctx context.Context, s *SalonServiceRecord) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO services (
			id, salon_id, category_id, name, name_ar, description,
			duration_min, active_duration_min, price,
			deposit_amount, deposit_deadline_hours,
			is_active, is_custom
		) VALUES (
			$1, $2, $3, $4, $5, $6,
			$7, $8, $9,
			$10, $11,
			$12, $13
		)
		RETURNING created_at, updated_at`,
		s.ID, s.SalonID, s.CategoryID, s.Name, s.NameAr, s.Description,
		s.DurationMin, s.ActiveDurationMin, s.Price,
		s.DepositAmount, s.DepositDeadlineHours,
		s.IsActive, s.IsCustom,
	).Scan(&s.CreatedAt, &s.UpdatedAt)
	if err != nil {
		return fmt.Errorf("create service: %w", err)
	}
	return nil
}

func (r *pgRepo) UpdateService(ctx context.Context, id uuid.UUID, req UpdateServiceRequest) error {
	_, err := r.db.Exec(ctx, `
		UPDATE services
		SET name           = COALESCE($1, name),
		    name_ar        = COALESCE($2, name_ar),
		    description    = COALESCE($3, description),
		    duration_min   = COALESCE($4, duration_min),
		    price          = COALESCE($5, price),
		    deposit_amount = COALESCE($6, deposit_amount),
		    is_active      = COALESCE($7, is_active),
		    updated_at     = NOW()
		WHERE id = $8`,
		req.Name, req.NameAr, req.Description, req.DurationMin,
		req.Price, req.DepositAmount, req.IsActive, id,
	)
	if err != nil {
		return fmt.Errorf("update service: %w", err)
	}
	return nil
}

func (r *pgRepo) DeleteService(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.Exec(ctx, `
		UPDATE services
		SET is_active  = FALSE,
		    updated_at = NOW()
		WHERE id = $1`,
		id,
	)
	if err != nil {
		return fmt.Errorf("delete service: %w", err)
	}
	return nil
}

// ── Business hours ────────────────────────────────────────────────────────────

func (r *pgRepo) GetBusinessHours(ctx context.Context, storeID uuid.UUID) ([]*BusinessHours, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, store_id, day_of_week, open_time, close_time, is_open, created_at
		FROM business_hours
		WHERE store_id = $1
		ORDER BY day_of_week ASC`,
		storeID,
	)
	if err != nil {
		return nil, fmt.Errorf("get business hours: %w", err)
	}
	defer rows.Close()

	var result []*BusinessHours
	for rows.Next() {
		bh := &BusinessHours{}
		if err := rows.Scan(
			&bh.ID, &bh.StoreID, &bh.DayOfWeek,
			&bh.OpenTime, &bh.CloseTime, &bh.IsOpen, &bh.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan business hours: %w", err)
		}
		result = append(result, bh)
	}
	return result, rows.Err()
}

func (r *pgRepo) SetBusinessHours(ctx context.Context, storeID uuid.UUID, req SetBusinessHoursRequest) error {
	_, err := r.db.Exec(ctx, `
		INSERT INTO business_hours (id, store_id, day_of_week, open_time, close_time, is_open)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5)
		ON CONFLICT (store_id, day_of_week)
		DO UPDATE SET
			open_time  = EXCLUDED.open_time,
			close_time = EXCLUDED.close_time,
			is_open    = EXCLUDED.is_open`,
		storeID, req.DayOfWeek, req.OpenTime, req.CloseTime, req.IsOpen,
	)
	if err != nil {
		return fmt.Errorf("set business hours: %w", err)
	}
	return nil
}

func (r *pgRepo) GetExceptions(ctx context.Context, storeID uuid.UUID) ([]*BusinessHoursException, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, store_id, exception_date, is_closed,
		       open_time, close_time, reason, created_at
		FROM business_hours_exceptions
		WHERE store_id = $1
		ORDER BY exception_date ASC`,
		storeID,
	)
	if err != nil {
		return nil, fmt.Errorf("get exceptions: %w", err)
	}
	defer rows.Close()

	var result []*BusinessHoursException
	for rows.Next() {
		ex := &BusinessHoursException{}
		if err := rows.Scan(
			&ex.ID, &ex.StoreID, &ex.ExceptionDate, &ex.IsClosed,
			&ex.OpenTime, &ex.CloseTime, &ex.Reason, &ex.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan exception: %w", err)
		}
		result = append(result, ex)
	}
	return result, rows.Err()
}

func (r *pgRepo) CreateException(ctx context.Context, storeID uuid.UUID, req CreateExceptionRequest) error {
	date, err := time.Parse("2006-01-02", req.ExceptionDate)
	if err != nil {
		return fmt.Errorf("parse exception date: %w", err)
	}
	_, err = r.db.Exec(ctx, `
		INSERT INTO business_hours_exceptions
			(id, store_id, exception_date, is_closed, open_time, close_time, reason)
		VALUES (gen_random_uuid(), $1, $2, $3, $4, $5, $6)
		ON CONFLICT (store_id, exception_date)
		DO UPDATE SET
			is_closed  = EXCLUDED.is_closed,
			open_time  = EXCLUDED.open_time,
			close_time = EXCLUDED.close_time,
			reason     = EXCLUDED.reason`,
		storeID, date, req.IsClosed, req.OpenTime, req.CloseTime, req.Reason,
	)
	if err != nil {
		return fmt.Errorf("create exception: %w", err)
	}
	return nil
}

func (r *pgRepo) DeleteException(ctx context.Context, storeID uuid.UUID, date time.Time) error {
	_, err := r.db.Exec(ctx, `
		DELETE FROM business_hours_exceptions
		WHERE store_id = $1 AND exception_date = $2::date`,
		storeID, date,
	)
	if err != nil {
		return fmt.Errorf("delete exception: %w", err)
	}
	return nil
}

// ── Scan helpers ──────────────────────────────────────────────────────────────

func scanStores(rows pgx.Rows) ([]*Store, error) {
	var result []*Store
	for rows.Next() {
		s := &Store{}
		if err := rows.Scan(
			&s.ID, &s.SalonID, &s.Name, &s.NameAr, &s.Address,
			&s.City, &s.Country, &s.Phone,
			&s.SameDayNoticeHours, &s.EarlyBirdCutoff, &s.EarlyBirdFee,
			&s.WeekdayBufferMin, &s.WeekendBufferMin,
			&s.Timezone, &s.IsActive, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan store: %w", err)
		}
		result = append(result, s)
	}
	return result, rows.Err()
}

func scanServices(rows pgx.Rows) ([]*SalonServiceRecord, error) {
	var result []*SalonServiceRecord
	for rows.Next() {
		s := &SalonServiceRecord{}
		if err := rows.Scan(
			&s.ID, &s.SalonID, &s.CategoryID, &s.Name, &s.NameAr, &s.Description,
			&s.DurationMin, &s.ActiveDurationMin, &s.Price,
			&s.DepositAmount, &s.DepositDeadlineHours,
			&s.IsActive, &s.IsCustom, &s.CreatedAt, &s.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan service: %w", err)
		}
		result = append(result, s)
	}
	return result, rows.Err()
}
