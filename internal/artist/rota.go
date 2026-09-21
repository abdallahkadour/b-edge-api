package artist

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// rota.go: an artist's own working hours, per store.
//
// business_hours says when a STORE is open; this says when a particular
// artist works inside that window. Slot generation intersects the two - see
// internal/pkg/schedule, which owns the intersection and its one load-bearing
// rule: an artist with no rows here is available for the WHOLE store window,
// because absence means "no personal restriction", not "never available".
//
// That default is why introducing this table changes nothing for the five
// artists currently on the platform, none of whom will have rows.

// ArtistSchedule is one weekday's working hours at one store.
type ArtistSchedule struct {
	ID        uuid.UUID `db:"id"           json:"id"`
	ArtistID  uuid.UUID `db:"artist_id"    json:"artist_id"`
	StoreID   uuid.UUID `db:"store_id"     json:"store_id"`
	DayOfWeek int       `db:"day_of_week"  json:"day_of_week"`
	StartTime string    `db:"start_time"   json:"start_time"`
	EndTime   string    `db:"end_time"     json:"end_time"`
	IsWorking bool      `db:"is_working"   json:"is_working"`
}

// ArtistScheduleException is a one-off personal override for a date:
// a holiday, illness, or a different shift.
type ArtistScheduleException struct {
	ID            uuid.UUID  `db:"id"              json:"id"`
	ArtistID      uuid.UUID  `db:"artist_id"       json:"artist_id"`
	StoreID       *uuid.UUID `db:"store_id"        json:"store_id,omitempty"`
	ExceptionDate time.Time  `db:"exception_date"  json:"exception_date"`
	IsUnavailable bool       `db:"is_unavailable"  json:"is_unavailable"`
	StartTime     *string    `db:"start_time"      json:"start_time,omitempty"`
	EndTime       *string    `db:"end_time"        json:"end_time,omitempty"`
	Reason        *string    `db:"reason"          json:"reason,omitempty"`
}

// SetRotaRequest replaces an artist's whole rota at one store.
//
// Replace rather than add/remove, deliberately. A weekly rota is edited as a
// grid - seven rows the artist adjusts together - and per-row endpoints turn
// one edit into seven requests that can interleave, leaving a half-applied
// week. Idempotent replacement has no such state. Same reasoning as the
// portfolio's PUT /media/:id/services.
type SetRotaRequest struct {
	StoreID uuid.UUID     `json:"store_id" validate:"required"`
	Days    []RotaDayItem `json:"days"     validate:"required,dive"`
}

// RotaDayItem is one day of the submitted grid. A day the artist does not
// work is sent with is_working false rather than omitted, so that "I do not
// work Mondays" is a statement rather than an absence - absence already
// means something else here.
type RotaDayItem struct {
	DayOfWeek int    `json:"day_of_week" validate:"min=0,max=6"`
	StartTime string `json:"start_time"  validate:"required"`
	EndTime   string `json:"end_time"    validate:"required"`
	IsWorking bool   `json:"is_working"`
}

// CreateScheduleExceptionRequest adds a personal date override.
type CreateScheduleExceptionRequest struct {
	ExceptionDate string     `json:"exception_date" validate:"required"`
	StoreID       *uuid.UUID `json:"store_id"`
	IsUnavailable bool       `json:"is_unavailable"`
	StartTime     *string    `json:"start_time"`
	EndTime       *string    `json:"end_time"`
	Reason        *string    `json:"reason" validate:"omitempty,max=255"`
}

var (
	// ErrNotAnArtist means the caller has no artist row - a customer or an
	// admin reaching an artist-only route.
	ErrNotAnArtist = errors.New("no artist profile for this account")

	// ErrStoreNotLinked means the artist does not work at that store.
	// Writing a rota for a store you are not attached to is meaningless,
	// and permitting it would let one salon's member write rows keyed to
	// another salon's store.
	ErrStoreNotLinked = errors.New("you are not linked to that store")
)

// ── Repository ────────────────────────────────────────────────────────────

// ArtistIDForUser resolves the caller's artist row.
func (r *pgRepo) ArtistIDForUser(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT id FROM artists WHERE user_id = $1`, userID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotAnArtist
		}
		return uuid.Nil, fmt.Errorf("artist for user: %w", err)
	}
	return id, nil
}

// ArtistWorksAtStore is the authorisation check for every rota write. It
// reads artist_stores, which is the same table discovery and the booking
// funnel use to decide where an artist is bookable.
func (r *pgRepo) ArtistWorksAtStore(ctx context.Context, artistID, storeID uuid.UUID) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT true FROM artist_stores WHERE artist_id = $1 AND store_id = $2`,
		artistID, storeID).Scan(&ok)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("artist works at store: %w", err)
	}
	return true, nil
}

func (r *pgRepo) GetRota(ctx context.Context, artistID uuid.UUID) ([]*ArtistSchedule, error) {
	rows, err := r.db.Query(ctx, `
		SELECT id, artist_id, store_id, day_of_week,
		       to_char(start_time, 'HH24:MI'), to_char(end_time, 'HH24:MI'), is_working
		  FROM artist_schedules WHERE artist_id = $1
		 ORDER BY store_id, day_of_week`, artistID)
	if err != nil {
		return nil, fmt.Errorf("get rota: %w", err)
	}
	defer rows.Close()

	out := make([]*ArtistSchedule, 0)
	for rows.Next() {
		s := &ArtistSchedule{}
		if err := rows.Scan(&s.ID, &s.ArtistID, &s.StoreID, &s.DayOfWeek,
			&s.StartTime, &s.EndTime, &s.IsWorking); err != nil {
			return nil, fmt.Errorf("get rota: scan: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// SetRota replaces the artist's rota at one store, in a transaction.
//
// Delete-then-insert rather than upsert: the submission IS the whole week,
// so a day the artist removed must disappear. An upsert would leave it
// behind, and the artist would see a day they deleted still blocking their
// calendar with no way to understand why.
func (r *pgRepo) SetRota(ctx context.Context, artistID uuid.UUID, req SetRotaRequest) error {
	tx, err := r.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("set rota: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit

	if _, err := tx.Exec(ctx,
		`DELETE FROM artist_schedules WHERE artist_id = $1 AND store_id = $2`,
		artistID, req.StoreID); err != nil {
		return fmt.Errorf("set rota: clear: %w", err)
	}

	for _, d := range req.Days {
		if _, err := tx.Exec(ctx, `
			INSERT INTO artist_schedules
			    (artist_id, store_id, day_of_week, start_time, end_time, is_working)
			VALUES ($1, $2, $3, $4::time, $5::time, $6)`,
			artistID, req.StoreID, d.DayOfWeek, d.StartTime, d.EndTime, d.IsWorking,
		); err != nil {
			return fmt.Errorf("set rota: day %d: %w", d.DayOfWeek, err)
		}
	}
	return tx.Commit(ctx)
}

func (r *pgRepo) GetScheduleExceptions(ctx context.Context, artistID uuid.UUID,
	from time.Time) ([]*ArtistScheduleException, error) {

	rows, err := r.db.Query(ctx, `
		SELECT id, artist_id, store_id, exception_date, is_unavailable,
		       to_char(start_time, 'HH24:MI'), to_char(end_time, 'HH24:MI'), reason
		  FROM artist_schedule_exceptions
		 WHERE artist_id = $1 AND exception_date >= $2
		 ORDER BY exception_date`, artistID, from)
	if err != nil {
		return nil, fmt.Errorf("get schedule exceptions: %w", err)
	}
	defer rows.Close()

	out := make([]*ArtistScheduleException, 0)
	for rows.Next() {
		e := &ArtistScheduleException{}
		if err := rows.Scan(&e.ID, &e.ArtistID, &e.StoreID, &e.ExceptionDate,
			&e.IsUnavailable, &e.StartTime, &e.EndTime, &e.Reason); err != nil {
			return nil, fmt.Errorf("get schedule exceptions: scan: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (r *pgRepo) UpsertScheduleException(ctx context.Context, artistID uuid.UUID,
	req CreateScheduleExceptionRequest) error {

	_, err := r.db.Exec(ctx, `
		INSERT INTO artist_schedule_exceptions
		    (artist_id, store_id, exception_date, is_unavailable, start_time, end_time, reason)
		VALUES ($1, $2, $3::date, $4, $5::time, $6::time, $7)
		ON CONFLICT (artist_id, store_id, exception_date) DO UPDATE
		   SET is_unavailable = EXCLUDED.is_unavailable,
		       start_time     = EXCLUDED.start_time,
		       end_time       = EXCLUDED.end_time,
		       reason         = EXCLUDED.reason`,
		artistID, req.StoreID, req.ExceptionDate, req.IsUnavailable,
		req.StartTime, req.EndTime, req.Reason)
	if err != nil {
		return fmt.Errorf("upsert schedule exception: %w", err)
	}
	return nil
}

func (r *pgRepo) DeleteScheduleException(ctx context.Context, artistID, id uuid.UUID) error {
	ct, err := r.db.Exec(ctx,
		`DELETE FROM artist_schedule_exceptions WHERE id = $1 AND artist_id = $2`,
		id, artistID)
	if err != nil {
		return fmt.Errorf("delete schedule exception: %w", err)
	}
	if ct.RowsAffected() == 0 {
		// Scoped by artist_id, so another artist's exception is simply not
		// found - no separate ownership branch to diverge.
		return pgx.ErrNoRows
	}
	return nil
}

// ── Service ───────────────────────────────────────────────────────────────

// GetMyRota returns the caller's declared working hours across every store.
// An empty list is the normal state and means "available whenever the store
// is open", not "never available".
func (s *Service) GetMyRota(ctx context.Context, userID uuid.UUID) ([]*ArtistSchedule, error) {
	artistID, err := s.repo.ArtistIDForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.repo.GetRota(ctx, artistID)
}

// SetMyRota replaces the caller's rota at one store.
func (s *Service) SetMyRota(ctx context.Context, userID uuid.UUID, req SetRotaRequest) error {
	artistID, err := s.repo.ArtistIDForUser(ctx, userID)
	if err != nil {
		return err
	}

	// An artist may only write a rota for a store they actually work at.
	// Without this a salon member could write rows keyed to another
	// salon's store, which artist_stores is exactly the record of.
	linked, err := s.repo.ArtistWorksAtStore(ctx, artistID, req.StoreID)
	if err != nil {
		return err
	}
	if !linked {
		return ErrStoreNotLinked
	}

	// Validate every day before writing any of them. The database enforces
	// start < end and one row per weekday, but a partial write that then
	// hits a constraint would leave the artist's week half-applied - and
	// the transaction in SetRota only protects against that if nothing
	// invalid gets as far as the INSERT loop.
	seen := make(map[int]bool, len(req.Days))
	for _, d := range req.Days {
		if seen[d.DayOfWeek] {
			return fmt.Errorf("day %d appears twice in one submission", d.DayOfWeek)
		}
		seen[d.DayOfWeek] = true
		if d.StartTime >= d.EndTime {
			return fmt.Errorf("day %d: start time must be before end time", d.DayOfWeek)
		}
	}
	return s.repo.SetRota(ctx, artistID, req)
}

// GetMyScheduleExceptions returns personal date overrides from today
// forward. Past exceptions are history and are not useful on a planning
// screen.
func (s *Service) GetMyScheduleExceptions(ctx context.Context, userID uuid.UUID,
) ([]*ArtistScheduleException, error) {
	artistID, err := s.repo.ArtistIDForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	return s.repo.GetScheduleExceptions(ctx, artistID, time.Now())
}

func (s *Service) SetMyScheduleException(ctx context.Context, userID uuid.UUID,
	req CreateScheduleExceptionRequest) error {

	artistID, err := s.repo.ArtistIDForUser(ctx, userID)
	if err != nil {
		return err
	}
	if req.StoreID != nil {
		linked, err := s.repo.ArtistWorksAtStore(ctx, artistID, *req.StoreID)
		if err != nil {
			return err
		}
		if !linked {
			return ErrStoreNotLinked
		}
	}

	// Mirror the table's CHECK in the service so the caller gets a usable
	// message rather than a constraint name. Away for the day carries no
	// times; different hours carry both.
	if req.IsUnavailable {
		req.StartTime, req.EndTime = nil, nil
	} else if req.StartTime == nil || req.EndTime == nil {
		return errors.New("an exception that is not a day off needs a start and end time")
	} else if *req.StartTime >= *req.EndTime {
		return errors.New("start time must be before end time")
	}

	return s.repo.UpsertScheduleException(ctx, artistID, req)
}

func (s *Service) DeleteMyScheduleException(ctx context.Context, userID, id uuid.UUID) error {
	artistID, err := s.repo.ArtistIDForUser(ctx, userID)
	if err != nil {
		return err
	}
	return s.repo.DeleteScheduleException(ctx, artistID, id)
}
