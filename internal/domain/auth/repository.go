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
	// CreateUser inserts a new user row and populates CreatedAt and UpdatedAt on success.
	// Returns ErrEmailConflict if the email is already registered.
	CreateUser(ctx context.Context, user *User) error

	// GetUserByEmail returns the non-deleted user with the given email.
	// Returns ErrUserNotFound if no match exists.
	GetUserByEmail(ctx context.Context, email string) (*User, error)

	// GetUserByID returns the non-deleted user with the given primary key.
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
// Returns ErrEmailConflict if the email is already taken.
func (r *repo) CreateUser(ctx context.Context, user *User) error {
	const q = `
		INSERT INTO users (id, name, email, password_hash, role, phone, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING created_at, updated_at`

	err := r.db.QueryRow(ctx, q,
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
// Returns ErrUserNotFound if no match exists.
func (r *repo) GetUserByEmail(ctx context.Context, email string) (*User, error) {
	const q = `
		SELECT id, name, email, password_hash, role, phone, status,
		       created_at, updated_at, deleted_at
		FROM users
		WHERE email = $1 AND deleted_at IS NULL`

	u := &User{}
	err := r.db.QueryRow(ctx, q, email).Scan(
		&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role,
		&u.Phone, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
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
// Returns ErrUserNotFound if not found.
func (r *repo) GetUserByID(ctx context.Context, id uuid.UUID) (*User, error) {
	const q = `
		SELECT id, name, email, password_hash, role, phone, status,
		       created_at, updated_at, deleted_at
		FROM users
		WHERE id = $1 AND deleted_at IS NULL`

	u := &User{}
	err := r.db.QueryRow(ctx, q, id).Scan(
		&u.ID, &u.Name, &u.Email, &u.PasswordHash, &u.Role,
		&u.Phone, &u.Status, &u.CreatedAt, &u.UpdatedAt, &u.DeletedAt,
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
func (r *repo) UpdateUserStatus(ctx context.Context, userID uuid.UUID, status string) error {
	const q = `
		UPDATE users
		SET status     = $1,
		    deleted_at = CASE WHEN $1 = 'deleted' THEN NOW() ELSE deleted_at END,
		    updated_at = NOW()
		WHERE id = $2 AND deleted_at IS NULL`

	_, err := r.db.Exec(ctx, q, status, userID)
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
// Returns ErrTokenNotFound if no row exists. Does not filter by revocation status —
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
