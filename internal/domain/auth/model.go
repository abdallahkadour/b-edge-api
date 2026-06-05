// Package auth implements the authentication domain for B-Edge,
// including user registration, login, token management, and password flows.
package auth

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Role constants for the users table.
const (
	// RoleCustomer is the default role for clients booking services.
	RoleCustomer = "customer"

	// RoleArtist is the role for beauty artists providing services.
	RoleArtist = "artist"

	// RoleAdmin is the role for platform administrators. Never assigned via API.
	RoleAdmin = "admin"
)

// Status constants for the users table.
const (
	// StatusActive is the default status for newly registered users.
	StatusActive = "active"

	// StatusFrozen is set by an admin to temporarily block a user from booking.
	StatusFrozen = "frozen"

	// StatusSuspended is set by an admin for policy violations.
	StatusSuspended = "suspended"

	// StatusDeleted is set when a user requests account deletion (soft delete).
	StatusDeleted = "deleted"
)

// Sentinel errors returned by the auth repository.
// Services convert these into apperror types — never return apperror from a repository.
var (
	// ErrUserNotFound is returned when no user matches the given criteria.
	ErrUserNotFound = errors.New("user not found")

	// ErrEmailConflict is returned when a user with the same email already exists.
	ErrEmailConflict = errors.New("email already registered")

	// ErrTokenNotFound is returned when no matching refresh token exists in the DB.
	ErrTokenNotFound = errors.New("refresh token not found")

	// ErrTokenRevoked is returned when a refresh token exists but has been revoked.
	ErrTokenRevoked = errors.New("refresh token has been revoked")

	// ErrResetTokenNotFound is returned when no matching password reset token exists.
	ErrResetTokenNotFound = errors.New("password reset token not found")

	// ErrResetTokenExpired is returned when a password reset token has passed its expiry.
	ErrResetTokenExpired = errors.New("password reset token has expired")

	// ErrResetTokenUsed is returned when a password reset token has already been consumed.
	ErrResetTokenUsed = errors.New("password reset token has already been used")
)

// User represents a registered B-Edge user from the users table.
type User struct {
	ID           uuid.UUID  `db:"id"`
	Name         string     `db:"name"`
	Email        string     `db:"email"`
	PasswordHash string     `db:"password_hash"`
	Role         string     `db:"role"`
	Phone        *string    `db:"phone"`
	Status       string     `db:"status"`
	CreatedAt    time.Time  `db:"created_at"`
	UpdatedAt    time.Time  `db:"updated_at"`
	DeletedAt    *time.Time `db:"deleted_at"`
}

// RefreshToken represents a stored hashed refresh token entry in the refresh_tokens table.
// The raw token is never persisted — only its SHA-256 hash is stored.
type RefreshToken struct {
	ID        uuid.UUID  `db:"id"`
	UserID    uuid.UUID  `db:"user_id"`
	TokenHash string     `db:"token_hash"`
	ExpiresAt time.Time  `db:"expires_at"`
	RevokedAt *time.Time `db:"revoked_at"`
	CreatedAt time.Time  `db:"created_at"`
}

// PasswordReset represents a one-use token entry in the password_resets table.
// Used in the forgot-password / reset-password flow.
type PasswordReset struct {
	ID        uuid.UUID  `db:"id"`
	UserID    uuid.UUID  `db:"user_id"`
	Token     string     `db:"token"`
	ExpiresAt time.Time  `db:"expires_at"`
	UsedAt    *time.Time `db:"used_at"`
	CreatedAt time.Time  `db:"created_at"`
}

// RegisterRequest is the request body for POST /api/v1/auth/register.
type RegisterRequest struct {
	Name     string  `json:"name"     validate:"required,min=2,max=100"`
	Email    string  `json:"email"    validate:"required,email"`
	Password string  `json:"password" validate:"required,min=8"`
	Role     string  `json:"role"     validate:"required,oneof=customer artist"`
	Phone    *string `json:"phone"    validate:"omitempty,e164"`
}

// LoginRequest is the request body for POST /api/v1/auth/login.
type LoginRequest struct {
	Email    string `json:"email"    validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

// RefreshRequest is the request body for POST /api/v1/auth/refresh.
type RefreshRequest struct {
	RefreshToken string `json:"refresh_token" validate:"required"`
}

// ForgotPasswordRequest is the request body for POST /api/v1/auth/forgot-password.
type ForgotPasswordRequest struct {
	Email string `json:"email" validate:"required,email"`
}

// ResetPasswordRequest is the request body for POST /api/v1/auth/reset-password.
type ResetPasswordRequest struct {
	Token       string `json:"token"        validate:"required"`
	NewPassword string `json:"new_password" validate:"required,min=8"`
}

// ChangePasswordRequest is the request body for PATCH /api/v1/auth/change-password.
type ChangePasswordRequest struct {
	CurrentPassword string `json:"current_password" validate:"required"`
	NewPassword     string `json:"new_password"     validate:"required,min=8"`
}

// UserInfo is the safe subset of User fields returned to the client.
// PasswordHash is never included in any response.
type UserInfo struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Email string    `json:"email"`
	Role  string    `json:"role"`
	Phone *string   `json:"phone,omitempty"`
}
