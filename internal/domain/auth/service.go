// Package auth implements the authentication domain for B-Edge,
// including user registration, login, token management, and password flows.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/hash"
	internaljwt "github.com/abdallahkadour/b-edge-api/internal/pkg/jwt"
)

// resetTokenLength is the number of random bytes used to generate a password reset token.
// 32 bytes = 64 hex characters — cryptographically strong.
const resetTokenLength = 32

// resetTokenExpiry is how long a password reset token remains valid.
const resetTokenExpiry = 60 * time.Minute

// refreshTokenExpiry is how long a refresh token remains valid.
const refreshTokenExpiry = 7 * 24 * time.Hour

// Service handles all auth business logic.
// It sits between the handler (HTTP) and the repository (SQL).
// It knows nothing about HTTP — no fiber.Ctx, no status codes.
type Service struct {
	repo     Repository
	validate *validator.Validate
}

// NewService creates a new auth Service with the given repository.
func NewService(repo Repository) *Service {
	return &Service{
		repo:     repo,
		validate: validator.New(),
	}
}

// RegisterResult is returned after a successful registration.
type RegisterResult struct {
	AccessToken  string
	RefreshToken string
	User         UserInfo
}

// LoginResult is returned after a successful login.
type LoginResult struct {
	AccessToken  string
	RefreshToken string
	User         UserInfo
}

// Register creates a new user account.
// Steps: validate → check email not taken → hash password → create user → generate tokens.
func (s *Service) Register(ctx context.Context, req RegisterRequest) (*RegisterResult, error) {
	// Step 1: Validate request fields
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	// Step 2: Check email is not already registered
	_, err := s.repo.GetUserByEmail(ctx, req.Email)
	if err == nil {
		// GetUserByEmail succeeded — user exists
		return nil, apperror.Conflict("EMAIL_TAKEN", "An account with this email already exists")
	}
	if !errors.Is(err, ErrUserNotFound) {
		// Unexpected database error
		return nil, fmt.Errorf("register: check email: %w", err)
	}

	// Step 3: Hash the password — bcrypt, cost 10 in production
	passwordHash, err := hash.Password(req.Password)
	if err != nil {
		return nil, fmt.Errorf("register: hash password: %w", err)
	}

	// Step 4: Build and persist the user
	user := &User{
		ID:           uuid.New(),
		Name:         req.Name,
		Email:        req.Email,
		PasswordHash: passwordHash,
		Role:         req.Role,
		Phone:        req.Phone,
		Status:       StatusActive,
	}

	if err := s.repo.CreateUser(ctx, user); err != nil {
		if errors.Is(err, ErrEmailConflict) {
			return nil, apperror.Conflict("EMAIL_TAKEN", "An account with this email already exists")
		}
		return nil, fmt.Errorf("register: create user: %w", err)
	}

	// Step 5: Generate access + refresh tokens
	tokens, err := s.generateAndStoreTokens(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("register: generate tokens: %w", err)
	}

	return &RegisterResult{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		User:         toUserInfo(user),
	}, nil
}

// Login authenticates a user with email and password.
// Steps: find user → check status → verify password → generate tokens.
func (s *Service) Login(ctx context.Context, req LoginRequest) (*LoginResult, error) {
	// Step 1: Validate request fields
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	// Step 2: Find user by email
	user, err := s.repo.GetUserByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			// Return same error as wrong password — never reveal if email exists
			return nil, apperror.Unauthorized("INVALID_CREDENTIALS", "Email or password is incorrect")
		}
		return nil, fmt.Errorf("login: get user: %w", err)
	}

	// Step 3: Check account is active
	if user.Status == StatusFrozen {
		return nil, apperror.Forbidden("ACCOUNT_FROZEN", "Your account has been temporarily frozen")
	}
	if user.Status == StatusSuspended {
		return nil, apperror.Forbidden("ACCOUNT_SUSPENDED", "Your account has been suspended")
	}
	if user.Status == StatusDeleted {
		return nil, apperror.Unauthorized("INVALID_CREDENTIALS", "Email or password is incorrect")
	}

	// Step 4: Verify password
	if err := hash.VerifyPassword(req.Password, user.PasswordHash); err != nil {
		// Same message as user not found — never leak which one failed
		return nil, apperror.Unauthorized("INVALID_CREDENTIALS", "Email or password is incorrect")
	}

	// Step 5: Generate access + refresh tokens
	tokens, err := s.generateAndStoreTokens(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("login: generate tokens: %w", err)
	}

	return &LoginResult{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		User:         toUserInfo(user),
	}, nil
}

// Refresh issues a new access token using a valid refresh token.
// The old refresh token is revoked and a new one is issued (token rotation).
func (s *Service) Refresh(ctx context.Context, rawRefreshToken string) (*LoginResult, error) {
	// Step 1: Verify the JWT signature and expiry
	userID, err := internaljwt.VerifyRefreshToken(rawRefreshToken)
	if err != nil {
		return nil, apperror.Unauthorized("TOKEN_INVALID", "Authentication failed")
	}

	// Step 2: Check the token exists in the database and is not revoked
	tokenHash := hashToken(rawRefreshToken)
	storedToken, err := s.repo.GetRefreshTokenByHash(ctx, tokenHash)
	if err != nil {
		if errors.Is(err, ErrTokenNotFound) {
			return nil, apperror.Unauthorized("TOKEN_INVALID", "Authentication failed")
		}
		return nil, fmt.Errorf("refresh: get token: %w", err)
	}

	// Step 3: Reject if already revoked (replay attack detection)
	if storedToken.RevokedAt != nil {
		return nil, apperror.Unauthorized("TOKEN_INVALID", "Authentication failed")
	}

	// Step 4: Revoke the used refresh token (rotation — one use only)
	if err := s.repo.RevokeRefreshToken(ctx, tokenHash); err != nil {
		return nil, fmt.Errorf("refresh: revoke token: %w", err)
	}

	// Step 5: Fetch the user
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return nil, apperror.Unauthorized("TOKEN_INVALID", "Authentication failed")
		}
		return nil, fmt.Errorf("refresh: get user: %w", err)
	}

	// Step 6: Issue new token pair
	tokens, err := s.generateAndStoreTokens(ctx, user)
	if err != nil {
		return nil, fmt.Errorf("refresh: generate tokens: %w", err)
	}

	return &LoginResult{
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		User:         toUserInfo(user),
	}, nil
}

// Logout revokes the refresh token so it cannot be used again.
func (s *Service) Logout(ctx context.Context, rawRefreshToken string) error {
	tokenHash := hashToken(rawRefreshToken)
	if err := s.repo.RevokeRefreshToken(ctx, tokenHash); err != nil {
		return fmt.Errorf("logout: revoke token: %w", err)
	}
	return nil
}

// ForgotPassword generates a password reset token and stores it.
// Always returns nil — never reveal if the email exists or not.
func (s *Service) ForgotPassword(ctx context.Context, req ForgotPasswordRequest) error {
	if err := s.validate.Struct(req); err != nil {
		return nil // silent — do not reveal email existence
	}

	user, err := s.repo.GetUserByEmail(ctx, req.Email)
	if err != nil {
		return nil // silent — do not reveal if email is registered
	}

	// Remove any existing unused tokens before creating a new one
	if err := s.repo.DeleteUnusedPasswordResets(ctx, user.ID); err != nil {
		return fmt.Errorf("forgot password: delete old tokens: %w", err)
	}

	// Generate a cryptographically secure random token
	token, err := generateSecureToken()
	if err != nil {
		return fmt.Errorf("forgot password: generate token: %w", err)
	}

	reset := &PasswordReset{
		ID:        uuid.New(),
		UserID:    user.ID,
		Token:     token,
		ExpiresAt: time.Now().Add(resetTokenExpiry),
	}

	if err := s.repo.CreatePasswordReset(ctx, reset); err != nil {
		return fmt.Errorf("forgot password: store token: %w", err)
	}

	// TODO: send WhatsApp message with reset token when notification domain is built
	// notification.SendPasswordReset(ctx, user.Phone, token)

	return nil
}

// ResetPassword sets a new password using a valid reset token.
func (s *Service) ResetPassword(ctx context.Context, req ResetPasswordRequest) error {
	if err := s.validate.Struct(req); err != nil {
		return mapValidationError(err)
	}

	// Step 1: Find the reset token
	reset, err := s.repo.GetPasswordResetByToken(ctx, req.Token)
	if err != nil {
		if errors.Is(err, ErrResetTokenNotFound) {
			return apperror.BadRequest("INVALID_RESET_TOKEN", "Password reset token is invalid or expired")
		}
		return fmt.Errorf("reset password: get token: %w", err)
	}

	// Step 2: Check it has not been used
	if reset.UsedAt != nil {
		return apperror.BadRequest("INVALID_RESET_TOKEN", "Password reset token is invalid or expired")
	}

	// Step 3: Check it has not expired
	if time.Now().After(reset.ExpiresAt) {
		return apperror.BadRequest("INVALID_RESET_TOKEN", "Password reset token is invalid or expired")
	}

	// Step 4: Hash the new password
	passwordHash, err := hash.Password(req.NewPassword)
	if err != nil {
		return fmt.Errorf("reset password: hash password: %w", err)
	}

	// Step 5: Update the password
	if err := s.repo.UpdatePassword(ctx, reset.UserID, passwordHash); err != nil {
		return fmt.Errorf("reset password: update password: %w", err)
	}

	// Step 6: Mark token as used — cannot be reused
	if err := s.repo.MarkPasswordResetUsed(ctx, req.Token); err != nil {
		return fmt.Errorf("reset password: mark used: %w", err)
	}

	return nil
}

// ChangePassword updates the password for an authenticated user.
func (s *Service) ChangePassword(ctx context.Context, userID uuid.UUID, req ChangePasswordRequest) error {
	if err := s.validate.Struct(req); err != nil {
		return mapValidationError(err)
	}

	// Step 1: Fetch the user
	user, err := s.repo.GetUserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrUserNotFound) {
			return apperror.NotFound("USER_NOT_FOUND", "User not found")
		}
		return fmt.Errorf("change password: get user: %w", err)
	}

	// Step 2: Verify current password
	if err := hash.VerifyPassword(req.CurrentPassword, user.PasswordHash); err != nil {
		return apperror.Unauthorized("INVALID_CREDENTIALS", "Current password is incorrect")
	}

	// Step 3: Hash and store new password
	passwordHash, err := hash.Password(req.NewPassword)
	if err != nil {
		return fmt.Errorf("change password: hash: %w", err)
	}

	if err := s.repo.UpdatePassword(ctx, userID, passwordHash); err != nil {
		return fmt.Errorf("change password: update: %w", err)
	}

	return nil
}

// FreezeAccount sets the user status to frozen.
// A frozen user cannot log in until unfrozen.
func (s *Service) FreezeAccount(ctx context.Context, userID uuid.UUID) error {
	if err := s.repo.UpdateUserStatus(ctx, userID, StatusFrozen); err != nil {
		return fmt.Errorf("freeze account: %w", err)
	}
	return nil
}

// UnfreezeAccount restores a frozen account to active.
func (s *Service) UnfreezeAccount(ctx context.Context, userID uuid.UUID) error {
	if err := s.repo.UpdateUserStatus(ctx, userID, StatusActive); err != nil {
		return fmt.Errorf("unfreeze account: %w", err)
	}
	return nil
}

// DeleteAccount soft-deletes a user account.
// The record is never physically removed — deleted_at is stamped instead.
func (s *Service) DeleteAccount(ctx context.Context, userID uuid.UUID) error {
	if err := s.repo.UpdateUserStatus(ctx, userID, StatusDeleted); err != nil {
		return fmt.Errorf("delete account: %w", err)
	}
	return nil
}

// ── Private helpers ──────────────────────────────────────────────────────────

// tokenPair holds a raw access token and a raw refresh token.
type tokenPair struct {
	AccessToken  string
	RefreshToken string
}

// generateAndStoreTokens creates a new JWT pair and stores the refresh token hash.
func (s *Service) generateAndStoreTokens(ctx context.Context, user *User) (*tokenPair, error) {
	accessToken, err := internaljwt.GenerateAccessToken(user.ID, nil, user.Role)
	if err != nil {
		return nil, fmt.Errorf("generate access token: %w", err)
	}

	refreshToken, err := internaljwt.GenerateRefreshToken(user.ID)
	if err != nil {
		return nil, fmt.Errorf("generate refresh token: %w", err)
	}

	rt := &RefreshToken{
		ID:        uuid.New(),
		UserID:    user.ID,
		TokenHash: hashToken(refreshToken),
		ExpiresAt: time.Now().Add(refreshTokenExpiry),
	}

	if err := s.repo.CreateRefreshToken(ctx, rt); err != nil {
		return nil, fmt.Errorf("store refresh token: %w", err)
	}

	return &tokenPair{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
	}, nil
}

// hashToken returns the SHA-256 hex hash of a token string.
// Only the hash is stored in the database — never the raw token.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// generateSecureToken returns a cryptographically secure random hex string.
func generateSecureToken() (string, error) {
	b := make([]byte, resetTokenLength)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate secure token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// toUserInfo converts a User to the safe UserInfo subset returned to clients.
// PasswordHash is never included.
func toUserInfo(u *User) UserInfo {
	return UserInfo{
		ID:    u.ID,
		Name:  u.Name,
		Email: u.Email,
		Role:  u.Role,
		Phone: u.Phone,
	}
}

// mapValidationError converts go-playground/validator errors
// into a structured apperror with per-field details.
func mapValidationError(err error) error {
	var ve validator.ValidationErrors
	if !errors.As(err, &ve) {
		return apperror.BadRequest("VALIDATION_ERROR", err.Error())
	}

	details := make([]apperror.FieldError, 0, len(ve))
	for _, fe := range ve {
		details = append(details, apperror.FieldError{
			Field:   fe.Field(),
			Message: validationMessage(fe),
		})
	}

	return apperror.UnprocessableEntity("VALIDATION_ERROR", details)
}

// validationMessage returns a human-readable message for a validation failure.
func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + " is required"
	case "email":
		return "Invalid email format"
	case "min":
		return fe.Field() + " must be at least " + fe.Param() + " characters"
	case "max":
		return fe.Field() + " must be at most " + fe.Param() + " characters"
	case "oneof":
		return fe.Field() + " must be one of: " + fe.Param()
	case "e164":
		return "Phone number must be in international format e.g. +96170123456"
	default:
		return fe.Field() + " is invalid"
	}
}

// hashPwd wraps hash.Password so test files in the same package
// can hash passwords without importing the hash package directly.
func hashPwd(plain string) (string, error) {
	return hash.Password(plain)
}
