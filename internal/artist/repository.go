// Package artist implements the artist domain for B-Edge.
package artist

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Repository defines all database operations for the artist domain.
type Repository interface {
	GetArtistByID(ctx context.Context, artistID uuid.UUID) (*ArtistProfile, error)
	GetArtistByUserID(ctx context.Context, userID uuid.UUID) (*ArtistProfile, error)
	UpdateArtistProfile(ctx context.Context, artistID uuid.UUID, req UpdateProfileRequest) error
	GetStoresByArtist(ctx context.Context, artistID uuid.UUID) ([]*Store, error)
	GetStoresBySalon(ctx context.Context, salonID uuid.UUID) ([]*Store, error)
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

// ── Artist profile ────────────────────────────────────────────────────────────

func (r *pgRepo) GetArtistByID(ctx context.Context, artistID uuid.UUID) (*ArtistProfile, error) {
	p := &ArtistProfile{}
	err := r.db.QueryRow(ctx, `
		SELECT a.id, a.user_id, a.salon_id,
		       u.name, u.email, u.phone,
		       a.bio, a.bio_ar, a.instagram,
		       a.rating, a.review_count, a.is_verified,
		       a.created_at, a.updated_at
		FROM artists a
		JOIN users u ON u.id = a.user_id
		WHERE a.id = $1
		AND u.deleted_at IS NULL`,
		artistID,
	).Scan(
		&p.ID, &p.UserID, &p.SalonID,
		&p.Name, &p.Email, &p.Phone,
		&p.Bio, &p.BioAr, &p.Instagram,
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

func (r *pgRepo) GetArtistByUserID(ctx context.Context, userID uuid.UUID) (*ArtistProfile, error) {
	p := &ArtistProfile{}
	err := r.db.QueryRow(ctx, `
		SELECT a.id, a.user_id, a.salon_id,
		       u.name, u.email, u.phone,
		       a.bio, a.bio_ar, a.instagram,
		       a.rating, a.review_count, a.is_verified,
		       a.created_at, a.updated_at
		FROM artists a
		JOIN users u ON u.id = a.user_id
		WHERE a.user_id = $1
		AND u.deleted_at IS NULL`,
		userID,
	).Scan(
		&p.ID, &p.UserID, &p.SalonID,
		&p.Name, &p.Email, &p.Phone,
		&p.Bio, &p.BioAr, &p.Instagram,
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
		SET bio        = COALESCE($1, bio),
		    bio_ar     = COALESCE($2, bio_ar),
		    instagram  = COALESCE($3, instagram),
		    updated_at = NOW()
		WHERE id = $4`,
		req.Bio, req.BioAr, req.Instagram, artistID,
	)
	if err != nil {
		return fmt.Errorf("update artist profile: %w", err)
	}
	return nil
}

// ── Stores ────────────────────────────────────────────────────────────────────

func (r *pgRepo) GetStoresByArtist(ctx context.Context, artistID uuid.UUID) ([]*Store, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.id, s.salon_id, s.name, s.name_ar, s.address,
		       s.city, s.country, s.phone,
		       s.same_day_notice_hours, s.early_bird_cutoff, s.early_bird_fee,
		       s.weekday_buffer_min, s.weekend_buffer_min,
		       s.is_active, s.created_at, s.updated_at
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

func (r *pgRepo) GetStoresBySalon(ctx context.Context, salonID uuid.UUID) ([]*Store, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, salon_id, name, name_ar, address,
		       city, country, phone,
		       same_day_notice_hours, early_bird_cutoff, early_bird_fee,
		       weekday_buffer_min, weekend_buffer_min,
		       is_active, created_at, updated_at
		FROM stores
		WHERE salon_id = $1
		AND is_active = TRUE
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
			&s.IsActive, &s.CreatedAt, &s.UpdatedAt,
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
