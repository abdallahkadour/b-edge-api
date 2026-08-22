// Package auth implements the authentication domain for B-Edge,
// including user registration, login, token management, and password flows.
package auth

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

// uniqueViolationCode is the PostgreSQL error code for unique constraint violations.
const uniqueViolationCode = "23505"

// Repository defines all database operations for the auth domain.
// Implementations return sentinel errors (e.g. ErrUserNotFound), never apperror types.
type Repository interface {
	// CreateUser inserts a new user row and populates CreatedAt and UpdatedAt
	// on success. Deliberately does not touch the artists table, even for
	// role "artist" - see this method's own doc comment on the concrete
	// implementation for why. Returns ErrEmailConflict if the email is
	// already registered.
	CreateUser(ctx context.Context, user *User) error

	// GetUserByEmail returns the non-deleted user with the given email.
	// For artist accounts the User.SalonID field is populated via LEFT JOIN on artists.
	// Returns ErrUserNotFound if no match exists.
	GetUserByEmail(ctx context.Context, email string) (*User, error)

	// GetUserByID returns the non-deleted user with the given primary key.
	// For artist accounts the User.SalonID field is populated via LEFT JOIN on artists.
	// Returns ErrUserNotFound if not found.
	GetUserByID(ctx context.Context, id uuid.UUID) (*User, error)

	// UpdatePassword replaces the bcrypt password hash for the given user.
	UpdatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error

	// UpdateUserStatus changes the status field for the given user.
	// Passing StatusDeleted also stamps deleted_at.
	UpdateUserStatus(ctx context.Context, userID uuid.UUID, status string) error

	// CreateRefreshToken stores a new hashed refresh token and populates CreatedAt.
	CreateRefreshToken(ctx context.Context, token *RefreshToken) error

	// GetRefreshTokenByHash fetches a refresh token row by its hash.
	// Returns ErrTokenNotFound if the hash does not exist.
	GetRefreshTokenByHash(ctx context.Context, hash string) (*RefreshToken, error)

	// RevokeRefreshToken stamps revoked_at on a token row so it cannot be reused.
	RevokeRefreshToken(ctx context.Context, hash string) error

	// DeleteUnusedPasswordResets removes all unconsumed reset tokens for a user.
	// Call this before CreatePasswordReset to prevent token accumulation.
	DeleteUnusedPasswordResets(ctx context.Context, userID uuid.UUID) error

	// CreatePasswordReset inserts a new password reset token and populates CreatedAt.
	CreatePasswordReset(ctx context.Context, reset *PasswordReset) error

	// GetPasswordResetByToken fetches a reset token row by its token value.
	// Returns ErrResetTokenNotFound if not found.
	GetPasswordResetByToken(ctx context.Context, token string) (*PasswordReset, error)

	// MarkPasswordResetUsed stamps used_at on the token row to make it one-use.
	MarkPasswordResetUsed(ctx context.Context, token string) error

	// EnqueuePasswordResetNotification queues the reset link for delivery.
	// Own method on this domain's own Repository, not a call into
	// customerauth or booking's enqueue helpers - see this method's
	// implementation for why that's the deliberate, established pattern
	// here, not an oversight.
	EnqueuePasswordResetNotification(ctx context.Context, userID uuid.UUID, message string) error
}

// repo is the concrete PostgreSQL implementation of Repository.
type repo struct {
	db *pgxpool.Pool
}

// NewRepository creates an auth repository backed by the given pgx connection pool.
func NewRepository(db *pgxpool.Pool) Repository {
	return &repo{db: db}
}

// isUniqueViolation reports whether err is a PostgreSQL unique constraint violation.
func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == uniqueViolationCode
}

// CreateUser inserts a new user row and populates CreatedAt and UpdatedAt from the DB.
//
// Deliberately does NOT create an artists row here, even for role "artist" -
// it used to (an earlier version of this function auto-provisioned one, from
// before the self-service onboarding flow existed), and that turned out to
// be a real, live bug once a UI path to /auth/register finally existed to
// exercise it: artists.status defaults to 'active' at the DB level, so a
// freshly registered artist skipped onboarding and admin review entirely,
// landing straight in the full dashboard. It also broke onboarding itself -
// internal/onboarding's own Complete() expects NO artists row to exist yet
// (its idempotency check treats a pre-existing row as ErrAlreadyOnboarded),
// so the very first onboarding submission for any newly registered artist
// would have failed outright. onboarding.Complete() is now the ONLY place
// an artists row gets created, always with status='pending', which is what
// GetStatus's ErrNotOnboarded (no row found) → 404 → "show the onboarding
// form" logic in the frontend actually depends on.
//
// Returns ErrEmailConflict if the email is already taken.
func (r *repo) CreateUser(ctx context.Context, user *User) error {
	const insertUser = `
		INSERT INTO users (id, name, email, password_hash, role, phone, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at`

	err := r.db.QueryRow(ctx, insertUser,
		user.ID, user.Name, user.Email, user.PasswordHash,
		user.Role, user.Phone, user.Status,
	).Scan(&user.CreatedAt, &user.UpdatedAt)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrEmailConflict
		}
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// GetUserByEmail returns the user matching the given email, excluding soft-deleted rows.
//
// For artist accounts, salon_id is fetched from the artists table via LEFT JOIN so
// the caller (auth service) can embed it in the JWT access token without a second
// database round-trip. For customers and admins, artists.salon_id will be NULL and
// User.SalonID will be nil.
//
// Returns ErrUserNotFound if no match exists.
func (r *repo) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	const q = `
		SELECT u.id, u.name, u.email, u.password_hash, u.role, u.phone, u.status,
		       u.created_at, u.updated_at, u.deleted_at,
		       a.salon_id
		FROM   users u
		LEFT   JOIN artists a ON a.user_id = u.id
		WHERE  u.email = $1 AND u.deleted_at IS NULL`

	u := &User{}
	err := r.db.QueryRow(ctx, q, email).Scan(
		&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role,
		&u.Phone, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
		&u.SalonID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return u, nil
}

// GetUserByID returns the user with the given primary key, excluding soft-deleted rows.
//
// For artist accounts, salon_id is fetched from the artists table via LEFT JOIN so
// the caller (auth service) can embed it in the JWT access token without a second
// database round-trip. For customers and admins, artists.salon_id will be NULL and
// User.SalonID will be nil.
//
// Returns ErrUserNotFound if not found.
func (r *repo) GetUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	const q = `
		SELECT u.id, u.name, u.email, u.password_hash, u.role, u.phone, u.status,
		       u.created_at, u.updated_at, u.deleted_at,
		       a.salon_id
		FROM   users u
		LEFT   JOIN artists a ON a.user_id = u.id
		WHERE  u.id = $1 AND u.deleted_at IS NULL`

	u := &User{}
	err := r.db.QueryRow(ctx, q, id).Scan(
		&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role,
		&u.Phone, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
		&u.SalonID,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrUserNotFound
		}
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return u, nil
}

// UpdatePassword replaces the password hash for the given user.
func (r *repo) UpdatePassword(ctx context.Context, userID uuid.UUID, passwordHash string) error {
	const q = `
		UPDATE users
		SET password_hash = $1, updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL`

	_, err := r.db.Exec(ctx, q, passwordHash, userID)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	return nil
}

// UpdateUserStatus changes the status for the given user.
// When status is StatusDeleted, deleted_at is also stamped to soft-delete the row.
// UpdateUserStatus was, until this fix, unusable for every caller
// (FreezeAccount, UnfreezeAccount, DeleteAccount alike) - reusing $1 both
// as a direct assignment (`status = $1`) and inside a comparison
// (`CASE WHEN $1 = 'deleted'`) left Postgres unable to deduce a single
// consistent type for that parameter across the two different syntactic
// contexts, failing every call with SQLSTATE 42P08 ("inconsistent types
// deduced for parameter $1"), regardless of which status was passed. This
// had no test coverage that actually executed the real SQL against a real
// database (the mock-based service tests all pass a fake repository), so
// it shipped and stayed broken until a live UI call to /auth/freeze-account
// finally exercised the real query. Fixed by giving the CASE its own
// placeholder for the same value instead of reusing $1.
func (r *repo) UpdateUserStatus(ctx context.Context, userID uuid.UUID, status string) error {
	const q = `
		UPDATE users
		SET status     = $1,
		    deleted_at = CASE WHEN $3 = 'deleted' THEN NOW() ELSE deleted_at END,
		    updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL`

	_, err := r.db.Exec(ctx, q, status, userID, status)
	if err != nil {
		return fmt.Errorf("update user status: %w", err)
	}
	return nil
}

// CreateRefreshToken stores a new hashed refresh token entry and populates CreatedAt.
func (r *repo) CreateRefreshToken(ctx context.Context, token *RefreshToken) error {
	const q = `
		INSERT INTO refresh_tokens (id, user_id, token_hash, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at`

	err := r.db.QueryRow(ctx, q,
		token.ID, token.UserID, token.TokenHash, token.ExpiresAt,
	).Scan(&token.CreatedAt)
	if err != nil {
		return fmt.Errorf("create refresh token: %w", err)
	}
	return nil
}

// GetRefreshTokenByHash fetches the refresh token row matching the given hash.
// Returns ErrTokenNotFound if no row exists. Does not filter by revocation status
// callers must inspect RevokedAt to detect replayed tokens.
func (r *repo) GetRefreshTokenByHash(ctx context.Context, hash string) (*RefreshToken, error) {
	const q = `
		SELECT id, user_id, token_hash, expires_at, revoked_at, created_at
		FROM refresh_tokens
		WHERE token_hash = $1`

	rt := &RefreshToken{}
	err := r.db.QueryRow(ctx, q, hash).Scan(
		&rt.ID, &rt.UserID, &rt.TokenHash,
		&rt.ExpiresAt, &rt.RevokedAt, &rt.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrTokenNotFound
		}
		return nil, fmt.Errorf("get refresh token by hash: %w", err)
	}
	return rt, nil
}

// RevokeRefreshToken stamps revoked_at on the token row to invalidate it immediately.
// No-ops if the token is already revoked.
func (r *repo) RevokeRefreshToken(ctx context.Context, hash string) error {
	const q = `
		UPDATE refresh_tokens
		SET revoked_at = NOW()
		WHERE token_hash = $1 AND revoked_at IS NULL`

	_, err := r.db.Exec(ctx, q, hash)
	if err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

// DeleteUnusedPasswordResets removes all unconsumed reset tokens for the given user.
func (r *repo) DeleteUnusedPasswordResets(ctx context.Context, userID uuid.UUID) error {
	const q = `DELETE FROM password_resets WHERE user_id = $1 AND used_at IS NULL`

	_, err := r.db.Exec(ctx, q, userID)
	if err != nil {
		return fmt.Errorf("delete unused password resets: %w", err)
	}
	return nil
}

// CreatePasswordReset inserts a new password reset token entry and populates CreatedAt.
func (r *repo) CreatePasswordReset(ctx context.Context, reset *PasswordReset) error {
	const q = `
		INSERT INTO password_resets (id, user_id, token, expires_at)
		VALUES ($1, $2, $3, $4)
		RETURNING created_at`

	err := r.db.QueryRow(ctx, q,
		reset.ID, reset.UserID, reset.Token, reset.ExpiresAt,
	).Scan(&reset.CreatedAt)
	if err != nil {
		return fmt.Errorf("create password reset: %w", err)
	}
	return nil
}

// GetPasswordResetByToken fetches the reset token row matching the given token string.
// Returns ErrResetTokenNotFound if no row exists. Callers must check ExpiresAt and UsedAt.
func (r *repo) GetPasswordResetByToken(ctx context.Context, token string) (*PasswordReset, error) {
	const q = `
		SELECT id, user_id, token, expires_at, used_at, created_at
		FROM password_resets
		WHERE token = $1`

	pr := &PasswordReset{}
	err := r.db.QueryRow(ctx, q, token).Scan(
		&pr.ID, &pr.UserID, &pr.Token,
		&pr.ExpiresAt, &pr.UsedAt, &pr.CreatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrResetTokenNotFound
		}
		return nil, fmt.Errorf("get password reset by token: %w", err)
	}
	return pr, nil
}

// MarkPasswordResetUsed stamps used_at on the token row, making it one-use.
// No-ops if the token is already consumed.
func (r *repo) MarkPasswordResetUsed(ctx context.Context, token string) error {
	const q = `
		UPDATE password_resets
		SET used_at = NOW()
		WHERE token = $1 AND used_at IS NULL`

	_, err := r.db.Exec(ctx, q, token)
	if err != nil {
		return fmt.Errorf("mark password reset used: %w", err)
	}
	return nil
}

// EnqueuePasswordResetNotification queues the reset-link message onto the
// same notifications table internal/notification's worker already polls
// and delivers via WhatsApp (or logs/skips if unconfigured).
//
// This does NOT call into customerauth's EnqueueOTPNotification or
// booking's enqueueNotification - both exist, and it would build, but it
// would be the wrong move architecturally. customerauth's variant is
// phone-keyed specifically for pre-verification users with no `users` row
// yet (see its own comment in customerauth/repository.go); booking's
// enqueueNotification is unexported and can't be called from outside that
// package at all. Every domain in this codebase owns its own enqueue
// method on its own Repository - this one does too, following that
// pattern rather than reaching across packages for it. Before this
// existed, ForgotPassword created and stored a real reset token, then a
// bare `// TODO: send WhatsApp message` comment did nothing - the token
// was created but never reached the person who requested it, in every
// environment, always.
func (r *repo) EnqueuePasswordResetNotification(ctx context.Context, userID uuid.UUID, message string) error {
	const q = `
		INSERT INTO notifications (user_id, template_name, channel, payload)
		VALUES ($1, 'password_reset', 'whatsapp', jsonb_build_object('message', $2::text))`

	if _, err := r.db.Exec(ctx, q, userID, message); err != nil {
		return fmt.Errorf("enqueue password reset notification: %w", err)
	}
	return nil
}
