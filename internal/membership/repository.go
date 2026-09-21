package membership

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

const pgUniqueViolation = "23505"

// Repository is the membership domain's database surface.
//
// This is one of the few files allowed to read salons.owner_id - it is on
// the allowlist in internal/pkg/salonrole's TestNoInlineOwnerChecks, because
// transferring ownership is by definition writing that column. It does not
// make authorisation DECISIONS from it; salonrole.Can does that.
type Repository interface {
	CreateInvitation(ctx context.Context, inv *Invitation) error
	InvitationByTokenHash(ctx context.Context, hash string) (*Invitation, error)
	InvitationByID(ctx context.Context, salonID, id uuid.UUID) (*Invitation, error)
	LiveInvitationForContact(ctx context.Context, salonID uuid.UUID, phone, email *string) (*Invitation, error)
	ListInvitations(ctx context.Context, salonID uuid.UUID) ([]*Invitation, error)
	SetInvitationStatus(ctx context.Context, id uuid.UUID, st InvitationStatus, acceptedBy *uuid.UUID) error

	ListMembers(ctx context.Context, salonID uuid.UUID, from time.Time) ([]*Member, error)
	ActiveMemberCount(ctx context.Context, salonID uuid.UUID) (int, error)
	MemberByArtistID(ctx context.Context, salonID, artistID uuid.UUID, from time.Time) (*Member, error)
	ArtistIDForUser(ctx context.Context, userID uuid.UUID) (uuid.UUID, *uuid.UUID, error)
	DetachArtist(ctx context.Context, salonID, artistID uuid.UUID) error

	SalonOwnerUserID(ctx context.Context, salonID uuid.UUID) (uuid.UUID, error)
	SalonName(ctx context.Context, salonID uuid.UUID) (string, error)
	UserDisplayName(ctx context.Context, userID uuid.UUID) (string, error)
	TransferOwnership(ctx context.Context, salonID, toUserID uuid.UUID) error

	UserIDByContact(ctx context.Context, phone, email *string) (*uuid.UUID, error)
}

type pgRepo struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) Repository { return &pgRepo{db: db} }

const invitationCols = `id, salon_id, invited_by, phone, email, token_hash,
	status, expires_at, accepted_by, accepted_at, created_at, updated_at`

func scanInvitation(row pgx.Row) (*Invitation, error) {
	i := &Invitation{}
	err := row.Scan(&i.ID, &i.SalonID, &i.InvitedBy, &i.Phone, &i.Email,
		&i.TokenHash, &i.Status, &i.ExpiresAt, &i.AcceptedBy, &i.AcceptedAt,
		&i.CreatedAt, &i.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return i, nil
}

func (r *pgRepo) CreateInvitation(ctx context.Context, inv *Invitation) error {
	err := r.db.QueryRow(ctx, `
		INSERT INTO salon_invitations
		    (salon_id, invited_by, phone, email, token_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING `+invitationCols,
		inv.SalonID, inv.InvitedBy, inv.Phone, inv.Email, inv.TokenHash, inv.ExpiresAt,
	).Scan(&inv.ID, &inv.SalonID, &inv.InvitedBy, &inv.Phone, &inv.Email,
		&inv.TokenHash, &inv.Status, &inv.ExpiresAt, &inv.AcceptedBy,
		&inv.AcceptedAt, &inv.CreatedAt, &inv.UpdatedAt)
	if err != nil {
		// The partial unique indexes on (salon_id, phone) and
		// (salon_id, lower(email)) WHERE status='pending' are the real
		// guarantee that one contact has at most one live invitation. The
		// service checks first for a clean error; this catches the race.
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			return ErrLiveInvitationExists
		}
		return fmt.Errorf("create invitation: %w", err)
	}
	return nil
}

func (r *pgRepo) InvitationByTokenHash(ctx context.Context, hash string) (*Invitation, error) {
	return scanInvitation(r.db.QueryRow(ctx,
		`SELECT `+invitationCols+` FROM salon_invitations WHERE token_hash = $1`, hash))
}

func (r *pgRepo) InvitationByID(ctx context.Context, salonID, id uuid.UUID) (*Invitation, error) {
	// Scoped by salon_id in the WHERE clause, so an invitation belonging to
	// another salon is simply not found - no separate ownership branch that
	// could return a different shape.
	return scanInvitation(r.db.QueryRow(ctx,
		`SELECT `+invitationCols+` FROM salon_invitations WHERE id = $1 AND salon_id = $2`,
		id, salonID))
}

func (r *pgRepo) LiveInvitationForContact(ctx context.Context, salonID uuid.UUID,
	phone, email *string) (*Invitation, error) {

	return scanInvitation(r.db.QueryRow(ctx, `
		SELECT `+invitationCols+`
		  FROM salon_invitations
		 WHERE salon_id = $1
		   AND status = 'pending'
		   AND ( ($2::text IS NOT NULL AND phone = $2)
		      OR ($3::text IS NOT NULL AND lower(email) = lower($3)) )
		 LIMIT 1`, salonID, phone, email))
}

func (r *pgRepo) ListInvitations(ctx context.Context, salonID uuid.UUID) ([]*Invitation, error) {
	rows, err := r.db.Query(ctx, `
		SELECT `+invitationCols+`
		  FROM salon_invitations WHERE salon_id = $1
		 ORDER BY created_at DESC`, salonID)
	if err != nil {
		return nil, fmt.Errorf("list invitations: %w", err)
	}
	defer rows.Close()

	// make(...) not var: a nil slice marshals to JSON null, which no
	// Angular @for can iterate. CLAUDE.md, enforced convention.
	out := make([]*Invitation, 0)
	for rows.Next() {
		i, err := scanInvitation(rows)
		if err != nil {
			return nil, fmt.Errorf("list invitations: scan: %w", err)
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

func (r *pgRepo) SetInvitationStatus(ctx context.Context, id uuid.UUID,
	st InvitationStatus, acceptedBy *uuid.UUID) error {

	var acceptedAt *time.Time
	if st == StatusAccepted {
		now := time.Now()
		acceptedAt = &now
	}
	ct, err := r.db.Exec(ctx, `
		UPDATE salon_invitations
		   SET status = $2, accepted_by = $3, accepted_at = $4, updated_at = now()
		 WHERE id = $1`, id, st, acceptedBy, acceptedAt)
	if err != nil {
		return fmt.Errorf("set invitation status: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// memberSelect is one query so that IsOwner and FutureBookings can never be
// computed two different ways for the list and the single-member read.
const memberSelect = `
	SELECT a.id, a.user_id, u.name, a.handle, a.avatar_url, a.category,
	       a.status, (s.owner_id = a.user_id) AS is_owner, a.created_at,
	       (SELECT count(*) FROM bookings b
	         WHERE b.artist_id = a.id
	           AND b.start_time >= $2
	           AND b.status IN ('pending', 'confirmed')) AS future_bookings
	  FROM artists a
	  JOIN users  u ON u.id = a.user_id
	  JOIN salons s ON s.id = a.salon_id
	 WHERE a.salon_id = $1`

func scanMember(row pgx.Row) (*Member, error) {
	m := &Member{}
	err := row.Scan(&m.ArtistID, &m.UserID, &m.DisplayName, &m.Handle,
		&m.AvatarURL, &m.Category, &m.Status, &m.IsOwner, &m.JoinedAt,
		&m.FutureBookings)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return m, nil
}

func (r *pgRepo) ListMembers(ctx context.Context, salonID uuid.UUID, from time.Time) ([]*Member, error) {
	rows, err := r.db.Query(ctx, memberSelect+
		` ORDER BY (s.owner_id = a.user_id) DESC, u.name`, salonID, from)
	if err != nil {
		return nil, fmt.Errorf("list members: %w", err)
	}
	defer rows.Close()

	out := make([]*Member, 0)
	for rows.Next() {
		m, err := scanMember(rows)
		if err != nil {
			return nil, fmt.Errorf("list members: scan: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

func (r *pgRepo) MemberByArtistID(ctx context.Context, salonID, artistID uuid.UUID,
	from time.Time) (*Member, error) {
	return scanMember(r.db.QueryRow(ctx, memberSelect+` AND a.id = $3`, salonID, from, artistID))
}

func (r *pgRepo) ActiveMemberCount(ctx context.Context, salonID uuid.UUID) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM artists WHERE salon_id = $1 AND status = 'active'`,
		salonID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count active members: %w", err)
	}
	return n, nil
}

// ArtistIDForUser returns the caller's artist row and the salon it is
// attached to. A nil salon means the artist exists but belongs to nowhere.
func (r *pgRepo) ArtistIDForUser(ctx context.Context, userID uuid.UUID) (uuid.UUID, *uuid.UUID, error) {
	var id uuid.UUID
	var salonID *uuid.UUID
	err := r.db.QueryRow(ctx,
		`SELECT id, salon_id FROM artists WHERE user_id = $1`, userID).Scan(&id, &salonID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, nil, ErrNotFound
		}
		return uuid.Nil, nil, fmt.Errorf("artist for user: %w", err)
	}
	return id, salonID, nil
}

// DetachArtist removes a member from a salon WITHOUT deleting anything they
// did (BR-4). Their bookings, reviews, earnings and client notes all keep
// pointing at the artist row; only the salon link goes.
func (r *pgRepo) DetachArtist(ctx context.Context, salonID, artistID uuid.UUID) error {
	ct, err := r.db.Exec(ctx,
		`UPDATE artists SET salon_id = NULL, updated_at = now()
		  WHERE id = $1 AND salon_id = $2`, artistID, salonID)
	if err != nil {
		return fmt.Errorf("detach artist: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *pgRepo) SalonOwnerUserID(ctx context.Context, salonID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx,
		`SELECT owner_id FROM salons WHERE id = $1 AND deleted_at IS NULL`, salonID).Scan(&id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return uuid.Nil, ErrNotFound
		}
		return uuid.Nil, fmt.Errorf("salon owner: %w", err)
	}
	return id, nil
}

func (r *pgRepo) SalonName(ctx context.Context, salonID uuid.UUID) (string, error) {
	var name string
	err := r.db.QueryRow(ctx,
		`SELECT name FROM salons WHERE id = $1 AND deleted_at IS NULL`, salonID).Scan(&name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("salon name: %w", err)
	}
	return name, nil
}

func (r *pgRepo) UserDisplayName(ctx context.Context, userID uuid.UUID) (string, error) {
	var name string
	err := r.db.QueryRow(ctx, `SELECT name FROM users WHERE id = $1`, userID).Scan(&name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("user name: %w", err)
	}
	return name, nil
}

// TransferOwnership moves salons.owner_id, which moves every owner
// capability with it. The caller invalidates both parties' refresh tokens
// afterwards: the role is baked into the access token at issue, so without
// that the former owner keeps owner capabilities until their token expires.
func (r *pgRepo) TransferOwnership(ctx context.Context, salonID, toUserID uuid.UUID) error {
	ct, err := r.db.Exec(ctx,
		`UPDATE salons SET owner_id = $2, updated_at = now()
		  WHERE id = $1 AND deleted_at IS NULL`, salonID, toUserID)
	if err != nil {
		return fmt.Errorf("transfer ownership: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// UserIDByContact finds an existing account by phone or email. Returns
// (nil, nil) when nobody matches - that is an ordinary outcome, not an
// error: inviting someone with no B-Edge account is the common case.
func (r *pgRepo) UserIDByContact(ctx context.Context, phone, email *string) (*uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `
		SELECT id FROM users
		 WHERE deleted_at IS NULL
		   AND ( ($1::text IS NOT NULL AND phone = $1)
		      OR ($2::text IS NOT NULL AND lower(email) = lower($2)) )
		 LIMIT 1`, phone, email).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("user by contact: %w", err)
	}
	return &id, nil
}
