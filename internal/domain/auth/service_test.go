// Package auth contains unit tests for the auth service layer.
// These tests use mock repositories - no database required.
package auth

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// spyAuditRepo records every event it's asked to log, so tests can assert
// on what was written without a real database.
type spyAuditRepo struct {
	events []audit.Event
}

func (s *spyAuditRepo) Log(_ context.Context, e audit.Event) error {
	s.events = append(s.events, e)
	return nil
}

func adminUser() *User {
	h, _ := hashPassword("AdminPass123")
	return &User{
		ID:           uuid.New(),
		Name:         "Admin",
		Email:        "admin@bedge.com",
		PasswordHash: h,
		Role:         RoleAdmin,
		Status:       StatusActive,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

// ── Mock repository ──────────────────────────────────────────────────────────
// mockRepo implements the Repository interface with controllable behaviour.
// Each field holds the value the method will return when called.

type mockRepo struct {
	// GetUserByEmail
	getUserByEmailUser *User
	getUserByEmailErr  error

	// GetUserByID
	getUserByIDUser *User
	getUserByIDErr  error

	// CreateUser
	createUserErr error

	// UpdatePassword
	updatePasswordErr error

	// UpdateUserStatus
	updateUserStatusErr error

	// CreateRefreshToken
	createRefreshTokenErr error

	// GetRefreshTokenByHash
	getRefreshTokenByHashToken *RefreshToken
	getRefreshTokenByHashErr   error

	// RevokeRefreshToken
	revokeRefreshTokenErr error

	// DeleteUnusedPasswordResets
	deleteUnusedPasswordResetsErr error

	// CreatePasswordReset
	createPasswordResetErr error

	// GetPasswordResetByToken
	getPasswordResetByTokenReset *PasswordReset
	getPasswordResetByTokenErr   error

	// MarkPasswordResetUsed
	markPasswordResetUsedErr error

	// EnqueuePasswordResetNotification
	enqueuePasswordResetNotificationErr error
	enqueuePasswordResetNotificationMsg string
}

// TestMain runs before all tests in this package.
// It sets the environment variables required by the service.
func TestMain(m *testing.M) {
	os.Setenv("JWT_SECRET", "test-secret-that-is-long-enough-for-testing-purposes-only")
	os.Setenv("JWT_REFRESH_SECRET", "test-refresh-secret-that-is-long-enough-for-testing-only")
	os.Setenv("APP_ENV", "test")
	os.Exit(m.Run())
}

func (m *mockRepo) CreateUser(_ context.Context, user *User) error {
	user.CreatedAt = time.Now()
	user.UpdatedAt = time.Now()
	return m.createUserErr
}
func (m *mockRepo) GetUserByEmail(_ context.Context, _ string) (*User, error) {
	return m.getUserByEmailUser, m.getUserByEmailErr
}
func (m *mockRepo) GetUserByID(_ context.Context, _ uuid.UUID) (*User, error) {
	return m.getUserByIDUser, m.getUserByIDErr
}
func (m *mockRepo) UpdatePassword(_ context.Context, _ uuid.UUID, _ string) error {
	return m.updatePasswordErr
}
func (m *mockRepo) UpdateUserStatus(_ context.Context, _ uuid.UUID, _ string) error {
	return m.updateUserStatusErr
}
func (m *mockRepo) CreateRefreshToken(_ context.Context, token *RefreshToken) error {
	token.CreatedAt = time.Now()
	return m.createRefreshTokenErr
}
func (m *mockRepo) GetRefreshTokenByHash(_ context.Context, _ string) (*RefreshToken, error) {
	return m.getRefreshTokenByHashToken, m.getRefreshTokenByHashErr
}
func (m *mockRepo) RevokeRefreshToken(_ context.Context, _ string) error {
	return m.revokeRefreshTokenErr
}
func (m *mockRepo) DeleteUnusedPasswordResets(_ context.Context, _ uuid.UUID) error {
	return m.deleteUnusedPasswordResetsErr
}
func (m *mockRepo) CreatePasswordReset(_ context.Context, reset *PasswordReset) error {
	reset.CreatedAt = time.Now()
	return m.createPasswordResetErr
}
func (m *mockRepo) GetPasswordResetByToken(_ context.Context, _ string) (*PasswordReset, error) {
	return m.getPasswordResetByTokenReset, m.getPasswordResetByTokenErr
}
func (m *mockRepo) MarkPasswordResetUsed(_ context.Context, _ string) error {
	return m.markPasswordResetUsedErr
}
func (m *mockRepo) EnqueuePasswordResetNotification(_ context.Context, _ uuid.UUID, message string) error {
	m.enqueuePasswordResetNotificationMsg = message
	return m.enqueuePasswordResetNotificationErr
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// newTestService creates a Service backed by the given mock repository.
func newTestService(repo Repository) *Service {
	return NewService(repo)
}

// validRegisterReq returns a valid registration request for use in tests.
func validRegisterReq() RegisterRequest {
	phone := "+96170123456"
	return RegisterRequest{
		Name:     "Lara",
		Email:    "lara@example.com",
		Password: "Secret123",
		Role:     RoleCustomer,
		Phone:    &phone,
	}
}

// existingUser returns a User as if already stored in the database.
func existingUser() *User {
	phone := "+96170123456"
	h, _ := hashPassword("Secret123")
	return &User{
		ID:           uuid.New(),
		Name:         "Lara",
		Email:        "lara@example.com",
		PasswordHash: h,
		Role:         RoleCustomer,
		Phone:        &phone,
		Status:       StatusActive,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
}

// hashPassword is a test helper to produce a bcrypt hash.
func hashPassword(plain string) (string, error) {
	return hashPwd(plain)
}

// ── Register tests ────────────────────────────────────────────────────────────

func TestRegister_Success(t *testing.T) {
	repo := &mockRepo{
		getUserByEmailErr: ErrUserNotFound, // email not taken
		createUserErr:     nil,
	}
	svc := newTestService(repo)

	result, err := svc.Register(context.Background(), validRegisterReq())

	require.NoError(t, err)
	assert.NotEmpty(t, result.AccessToken)
	assert.NotEmpty(t, result.RefreshToken)
	assert.Equal(t, "lara@example.com", result.User.Email)
	assert.Equal(t, RoleCustomer, result.User.Role)
	assert.Empty(t, result.User.ID == uuid.Nil)
}

func TestRegister_EmailTaken(t *testing.T) {
	repo := &mockRepo{
		getUserByEmailUser: existingUser(), // email exists
		getUserByEmailErr:  nil,
	}
	svc := newTestService(repo)

	result, err := svc.Register(context.Background(), validRegisterReq())

	require.Error(t, err)
	assert.Nil(t, result)

	var appErr *apperror.AppError
	isAppErr := errors.As(err, &appErr)
	assert.True(t, isAppErr)
	assert.Equal(t, "EMAIL_TAKEN", appErr.Code)
}

func TestRegister_InvalidEmail(t *testing.T) {
	repo := &mockRepo{getUserByEmailErr: ErrUserNotFound}
	svc := newTestService(repo)

	req := validRegisterReq()
	req.Email = "not-an-email"

	result, err := svc.Register(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestRegister_PasswordTooShort(t *testing.T) {
	repo := &mockRepo{getUserByEmailErr: ErrUserNotFound}
	svc := newTestService(repo)

	req := validRegisterReq()
	req.Password = "123"

	result, err := svc.Register(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, result)
}

func TestRegister_InvalidRole(t *testing.T) {
	repo := &mockRepo{getUserByEmailErr: ErrUserNotFound}
	svc := newTestService(repo)

	req := validRegisterReq()
	req.Role = "admin" // admin cannot self-register

	result, err := svc.Register(context.Background(), req)

	require.Error(t, err)
	assert.Nil(t, result)
}

// ── Login tests ───────────────────────────────────────────────────────────────

func TestLogin_Success(t *testing.T) {
	repo := &mockRepo{
		getUserByEmailUser: existingUser(),
		getUserByEmailErr:  nil,
	}
	svc := newTestService(repo)

	result, err := svc.Login(context.Background(), LoginRequest{
		Email:    "lara@example.com",
		Password: "Secret123",
	})

	require.NoError(t, err)
	assert.NotEmpty(t, result.AccessToken)
	assert.NotEmpty(t, result.RefreshToken)
	assert.Equal(t, "lara@example.com", result.User.Email)
}

func TestLogin_WrongPassword(t *testing.T) {
	repo := &mockRepo{
		getUserByEmailUser: existingUser(),
		getUserByEmailErr:  nil,
	}
	svc := newTestService(repo)

	result, err := svc.Login(context.Background(), LoginRequest{
		Email:    "lara@example.com",
		Password: "WrongPassword",
	})

	require.Error(t, err)
	assert.Nil(t, result)

	var appErr *apperror.AppError
	errors.As(err, &appErr)
	assert.Equal(t, "INVALID_CREDENTIALS", appErr.Code)
}

func TestLogin_UserNotFound(t *testing.T) {
	repo := &mockRepo{
		getUserByEmailErr: ErrUserNotFound,
	}
	svc := newTestService(repo)

	result, err := svc.Login(context.Background(), LoginRequest{
		Email:    "nobody@example.com",
		Password: "Secret123",
	})

	require.Error(t, err)
	assert.Nil(t, result)

	// Same error as wrong password - never reveal which one failed
	var appErr *apperror.AppError
	errors.As(err, &appErr)
	assert.Equal(t, "INVALID_CREDENTIALS", appErr.Code)
}

func TestLogin_FrozenAccount(t *testing.T) {
	user := existingUser()
	user.Status = StatusFrozen

	repo := &mockRepo{
		getUserByEmailUser: user,
		getUserByEmailErr:  nil,
	}
	svc := newTestService(repo)

	result, err := svc.Login(context.Background(), LoginRequest{
		Email:    "lara@example.com",
		Password: "Secret123",
	})

	require.Error(t, err)
	assert.Nil(t, result)

	var appErr *apperror.AppError
	errors.As(err, &appErr)
	assert.Equal(t, "ACCOUNT_FROZEN", appErr.Code)
}

// ── ChangePassword tests ──────────────────────────────────────────────────────

func TestChangePassword_Success(t *testing.T) {
	repo := &mockRepo{
		getUserByIDUser:   existingUser(),
		getUserByIDErr:    nil,
		updatePasswordErr: nil,
	}
	svc := newTestService(repo)

	err := svc.ChangePassword(context.Background(), uuid.New(), ChangePasswordRequest{
		CurrentPassword: "Secret123",
		NewPassword:     "NewSecret456",
	})

	require.NoError(t, err)
}

func TestChangePassword_WrongCurrentPassword(t *testing.T) {
	repo := &mockRepo{
		getUserByIDUser: existingUser(),
		getUserByIDErr:  nil,
	}
	svc := newTestService(repo)

	err := svc.ChangePassword(context.Background(), uuid.New(), ChangePasswordRequest{
		CurrentPassword: "WrongPassword",
		NewPassword:     "NewSecret456",
	})

	require.Error(t, err)

	var appErr *apperror.AppError
	errors.As(err, &appErr)
	assert.Equal(t, "INVALID_CREDENTIALS", appErr.Code)
}

// ── ForgotPassword tests ─────────────────────────────────────────────────────

// TestForgotPassword_ExistingUser_EnqueuesNotification is the test that
// actually matters here: this used to create and store a real token, then
// do nothing to deliver it - the enqueue call never happened at all. A
// test that only asserted "returns nil" would have passed against that
// exact bug.
func TestForgotPassword_ExistingUser_EnqueuesNotification(t *testing.T) {
	repo := &mockRepo{getUserByEmailUser: existingUser(), getUserByEmailErr: nil}
	svc := newTestService(repo)

	err := svc.ForgotPassword(context.Background(), ForgotPasswordRequest{Email: "existing@example.com"})

	require.NoError(t, err)
	assert.NotEmpty(t, repo.enqueuePasswordResetNotificationMsg, "must actually enqueue a delivery, not just store the token")
	assert.Contains(t, repo.enqueuePasswordResetNotificationMsg, "/reset-password?token=")
}

// TestForgotPassword_UnknownEmail_SilentSuccess guards the whole point of
// this endpoint's design: revealing whether an email is registered turns
// password reset into an account-enumeration oracle.
func TestForgotPassword_UnknownEmail_SilentSuccess(t *testing.T) {
	repo := &mockRepo{getUserByEmailErr: ErrUserNotFound}
	svc := newTestService(repo)

	err := svc.ForgotPassword(context.Background(), ForgotPasswordRequest{Email: "nobody@example.com"})

	require.NoError(t, err)
	assert.Empty(t, repo.enqueuePasswordResetNotificationMsg, "must never enqueue anything for an email that isn't registered")
}

func TestForgotPassword_EnqueueFails_ReturnsError(t *testing.T) {
	repo := &mockRepo{
		getUserByEmailUser:                  existingUser(),
		enqueuePasswordResetNotificationErr: errors.New("db down"),
	}
	svc := newTestService(repo)

	err := svc.ForgotPassword(context.Background(), ForgotPasswordRequest{Email: "existing@example.com"})

	require.Error(t, err)
}

// ── FreezeAccount / UnfreezeAccount tests ────────────────────────────────────

func TestFreezeAccount_Success(t *testing.T) {
	repo := &mockRepo{updateUserStatusErr: nil}
	svc := newTestService(repo)

	err := svc.FreezeAccount(context.Background(), uuid.New())
	require.NoError(t, err)
}

func TestUnfreezeAccount_Success(t *testing.T) {
	repo := &mockRepo{updateUserStatusErr: nil}
	svc := newTestService(repo)

	err := svc.UnfreezeAccount(context.Background(), uuid.New())
	require.NoError(t, err)
}

// ── DeleteAccount tests ───────────────────────────────────────────────────────

func TestDeleteAccount_Success(t *testing.T) {
	repo := &mockRepo{updateUserStatusErr: nil}
	svc := newTestService(repo)

	err := svc.DeleteAccount(context.Background(), uuid.New())
	require.NoError(t, err)
}

// ── Admin login audit logging ────────────────────────────────────────────────
//
// These guard the actual point of this feature: a small, deliberately
// powerful set of accounts, and every action they take recorded. A test
// only asserting "login succeeds" would miss the whole reason this exists.

func TestLogin_Admin_Success_WritesAuditEvent(t *testing.T) {
	admin := adminUser()
	repo := &mockRepo{getUserByEmailUser: admin}
	spy := &spyAuditRepo{}
	svc := NewService(repo, spy)

	_, err := svc.Login(context.Background(), LoginRequest{
		Email:    admin.Email,
		Password: "AdminPass123",
	}, "203.0.113.7")

	require.NoError(t, err)
	require.Len(t, spy.events, 1)
	assert.Equal(t, "login_success", spy.events[0].Action)
	assert.Equal(t, admin.ID, *spy.events[0].ActorID)
	assert.Equal(t, "admin_session", spy.events[0].EntityType)
	assert.Equal(t, "203.0.113.7", spy.events[0].IPAddress)
}

func TestLogin_Admin_WrongPassword_WritesFailedAuditEvent(t *testing.T) {
	admin := adminUser()
	repo := &mockRepo{getUserByEmailUser: admin}
	spy := &spyAuditRepo{}
	svc := NewService(repo, spy)

	_, err := svc.Login(context.Background(), LoginRequest{
		Email:    admin.Email,
		Password: "WrongPassword",
	}, "203.0.113.7")

	require.Error(t, err, "a wrong password must still fail login")
	require.Len(t, spy.events, 1, "a failed admin login attempt must still be recorded")
	assert.Equal(t, "login_failed", spy.events[0].Action)
}

// TestLogin_NonAdmin_Success_NoAuditEvent guards the other half: logging
// every customer and artist login would be noise, not a control. Only
// admin-role logins are audited.
func TestLogin_NonAdmin_Success_NoAuditEvent(t *testing.T) {
	repo := &mockRepo{getUserByEmailUser: existingUser()}
	spy := &spyAuditRepo{}
	svc := NewService(repo, spy)

	_, err := svc.Login(context.Background(), LoginRequest{
		Email:    "lara@example.com",
		Password: "Secret123",
	}, "203.0.113.7")

	require.NoError(t, err)
	assert.Empty(t, spy.events, "a non-admin login must not write to the audit log")
}

// TestLogin_UnknownEmail_NoAuditEvent guards the deliberate choice not to
// log an attempt against an email that matched no user at all - there is
// no real admin identity to attach it to.
func TestLogin_UnknownEmail_NoAuditEvent(t *testing.T) {
	repo := &mockRepo{getUserByEmailErr: ErrUserNotFound}
	spy := &spyAuditRepo{}
	svc := NewService(repo, spy)

	_, err := svc.Login(context.Background(), LoginRequest{
		Email:    "nobody@example.com",
		Password: "whatever",
	}, "203.0.113.7")

	require.Error(t, err)
	assert.Empty(t, spy.events)
}

// TestLogin_WithoutIPArgument_StillCompilesAndWorks guards backward
// compatibility for the variadic signature change - every one of this
// file's four pre-existing Login() calls omits the IP argument entirely.
func TestLogin_WithoutIPArgument_StillCompilesAndWorks(t *testing.T) {
	repo := &mockRepo{getUserByEmailUser: existingUser()}
	svc := newTestService(repo)

	result, err := svc.Login(context.Background(), LoginRequest{
		Email:    "lara@example.com",
		Password: "Secret123",
	})

	require.NoError(t, err)
	assert.NotEmpty(t, result.AccessToken)
}
