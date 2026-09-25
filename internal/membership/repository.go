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
	CountLiveInvitations(ctx context.Context, salonID uuid.UUID) (int, error)
	CountInvitationsSince(ctx context.Context, salonID uuid.UUID, since time.Time) (int, error)
	SetInvitationStatus(ctx context.Context, id uuid.UUID, st InvitationStatus, acceptedBy *uuid.UUID) error

	ListMembers(ctx context.Context, salonID uuid.UUID, from time.Time) ([]*Member, error)
	ActiveMemberCount(ctx context.Context, salonID uuid.UUID) (int, error)

	// ArtistCeiling returns the most artists this salon's plan permits, and
	// the plan's code for the upgrade message. 0 means no plan was found,
	// which is treated as unlimited rather than as zero - see Invite.
	ArtistCeiling(ctx context.Context, salonID uuid.UUID) (int, string, error)
	MemberByArtistID(ctx context.Context, salonID, artistID uuid.UUID, from time.Time) (*Member, error)
	ArtistIDForUser(ctx context.Context, userID uuid.UUID) (uuid.UUID, *uuid.UUID, error)
	DetachArtist(ctx context.Context, salonID, artistID uuid.UUID) error

	SalonOwnerUserID(ctx context.Context, salonID uuid.UUID) (uuid.UUID, error)
	SalonName(ctx context.Context, salonID uuid.UUID) (string, error)
	UserDisplayName(ctx context.Context, userID uuid.UUID) (string, error)
	TransferOwnership(ctx context.Context, salonID, toUserID uuid.UUID) error

	UserIDByContact(ctx context.Context, phone, email *string) (*uuid.UUID, error)

	// InviteeByContact resolves a contact to the registered user behind it,
	// with their artist row and verification state. ErrNotFound means nobody
	// is registered under that contact - which since migration 051 is itself
	// a refusal, not a blank slate.
	InviteeByContact(ctx context.Context, phone, email *string) (*Invitee, error)
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

func (r *pgRepo) CountLiveInvitations(ctx context.Context, salonID uuid.UUID) (int, error) {
	var n int
	err := r.db.QueryRow(ctx, `
		SELECT count(*) FROM salon_invitations
		 WHERE salon_id = $1 AND status = 'pending' AND expires_at > NOW()`,
		salonID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count live invitations: %w", err)
	}
	return n, nil
}

func (r *pgRepo) CountInvitationsSince(ctx context.Context, salonID uuid.UUID,
	since time.Time) (int, error) {
	var n int
	err := r.db.QueryRow(ctx,
		`SELECT count(*) FROM salon_invitations WHERE salon_id = $1 AND created_at >= $2`,
		salonID, since).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count invitations since: %w", err)
	}
	return n, nil
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

// ArtistCeiling reads plans.included_seats for the salon's live
// subscription.
//
// Joined through the OWNER's artist row, because billing is still keyed on
// artists.id - it moves to salons.id when the subscription regrain lands.
// Written as one query so the ceiling and the plan code can never disagree.
func (r *pgRepo) ArtistCeiling(ctx context.Context, salonID uuid.UUID) (int, string, error) {
	var ceiling int
	var code string
	err := r.db.QueryRow(ctx, `
		SELECT p.included_seats, p.code
		  FROM salons s
		  JOIN artists a  ON a.user_id = s.owner_id AND a.salon_id = s.id
		  JOIN subscriptions sub ON sub.artist_id = a.id AND sub.cancelled_at IS NULL
		  JOIN plans p ON p.code = sub.plan_code
		 WHERE s.id = $1
		 LIMIT 1`, salonID).Scan(&ceiling, &code)
	if errors.Is(err, pgx.ErrNoRows) {
		// No live subscription, or an owner who is not an artist in their
		// own salon. Treated as unlimited, deliberately: refusing here would
		// mean a billing lookup failure silently stops a salon hiring, and
		// an under-charged salon is a far better failure than a blocked one.
		return 0, "", nil
	}
	if err != nil {
		return 0, "", fmt.Errorf("artist ceiling: %w", err)
	}
	return ceiling, code, nil
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
	ct, err := r.db.Exec(ctx, `
		WITH gone AS (
			-- She no longer belongs to the salon, so she no longer offers its
			-- services. Same statement as the detach, so neither can happen alone.
			DELETE FROM artist_services os
			 USING services s
			 WHERE os.service_id = s.id AND os.artist_id = $1 AND s.salon_id = $2
		)
		UPDATE artists SET salon_id = NULL, updated_at = now()
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
// InviteeByContact resolves a contact to everything Invite needs to decide,
// in one query.
//
// One query rather than three because the three facts are read together and
// must agree: a user who exists, an artist row that may or may not hang off
// it, and a verification timestamp. Fetching them separately invites the
// classic drift where the account is found but the artist lookup is skipped
// on some branch.
//
// LEFT JOIN, not JOIN: a registered CUSTOMER is a different refusal from an
// unregistered number, and the owner needs to be told which.
func (r *pgRepo) InviteeByContact(ctx context.Context, phone, email *string) (*Invitee, error) {
	var iv Invitee
	err := r.db.QueryRow(ctx, `
		SELECT u.id, u.role, a.id, a.salon_id, a.category, u.phone_verified_at
		  FROM users u
		  LEFT JOIN artists a ON a.user_id = u.id
		 WHERE u.deleted_at IS NULL
		   AND ( ($1::text IS NOT NULL AND u.phone = $1)
		      OR ($2::text IS NOT NULL AND lower(u.email) = lower($2)) )
		 LIMIT 1`, phone, email,
	).Scan(&iv.UserID, &iv.Role, &iv.ArtistID, &iv.SalonID, &iv.Category, &iv.PhoneVerifiedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("invitee by contact: %w", err)
	}
	return &iv, nil
}

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
