// Package auth implements the authentication domain for B-Edge,
// including user registration, login, token management, and password flows.
package auth

import (
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
)

// refreshTokenCookie is the name of the httpOnly cookie used to store the refresh token.
const refreshTokenCookie = "refresh_token"

// refreshTokenCookieDuration is how long the refresh token cookie lives in the browser.
const refreshTokenCookieDuration = 7 * 24 * time.Hour

// Handler handles all HTTP requests for the auth domain.
// It knows about HTTP — fiber.Ctx, cookies, status codes.
// It knows nothing about SQL or business rules — those belong to Service.
type Handler struct {
	svc *Service
	log *zap.Logger
}

// NewHandler creates a new auth Handler with the given service and logger.
func NewHandler(svc *Service, log *zap.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// RegisterRoutes attaches all auth routes to the Fiber app.
// Called once from cmd/main.go during server startup.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo)
	handler := NewHandler(svc, log)

	auth := app.Group("/api/v1/auth")

	// Public routes — no token required
	auth.Post("/register", handler.Register)
	auth.Post("/login", handler.Login)
	auth.Post("/refresh", handler.Refresh)
	auth.Post("/forgot-password", handler.ForgotPassword)
	auth.Post("/reset-password", handler.ResetPassword)

	// Protected routes — valid JWT required
	auth.Post("/logout", middleware.RequireAuth(), handler.Logout)
	auth.Patch("/change-password", middleware.RequireAuth(), handler.ChangePassword)
	auth.Patch("/freeze-account", middleware.RequireAuth(), handler.FreezeAccount)
	auth.Patch("/unfreeze-account", middleware.RequireAuth(), handler.UnfreezeAccount)
	auth.Delete("/delete-account", middleware.RequireAuth(), handler.DeleteAccount)
}

// Register godoc
// @Summary      Register a new account
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body RegisterRequest true "Registration details"
// @Success      201  {object} response.Body{data=RegisterResult}
// @Failure      409  {object} response.ErrorBody
// @Failure      422  {object} response.ErrorBody
// @Router       /auth/register [post]
func (h *Handler) Register(c *fiber.Ctx) error {
	var req RegisterRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	result, err := h.svc.Register(c.Context(), req)
	if err != nil {
		return err
	}

	setRefreshTokenCookie(c, result.RefreshToken)

	return response.Created(c, fiber.Map{
		"access_token": result.AccessToken,
		"user":         result.User,
	})
}

// Login godoc
// @Summary      Login with email and password
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body LoginRequest true "Login credentials"
// @Success      200  {object} response.Body{data=LoginResult}
// @Failure      401  {object} response.ErrorBody
// @Router       /auth/login [post]
func (h *Handler) Login(c *fiber.Ctx) error {
	var req LoginRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	result, err := h.svc.Login(c.Context(), req)
	if err != nil {
		return err
	}

	setRefreshTokenCookie(c, result.RefreshToken)

	return response.OK(c, fiber.Map{
		"access_token": result.AccessToken,
		"user":         result.User,
	})
}

// Refresh godoc
// @Summary      Refresh access token using refresh token cookie
// @Tags         auth
// @Produce      json
// @Success      200  {object} response.Body{data=LoginResult}
// @Failure      401  {object} response.ErrorBody
// @Router       /auth/refresh [post]
func (h *Handler) Refresh(c *fiber.Ctx) error {
	// Read refresh token from httpOnly cookie
	rawToken := c.Cookies(refreshTokenCookie)
	if rawToken == "" {
		return apperror.Unauthorized("TOKEN_MISSING", "Authentication required")
	}

	result, err := h.svc.Refresh(c.Context(), rawToken)
	if err != nil {
		return err
	}

	// Rotate the cookie — old token revoked, new token set
	setRefreshTokenCookie(c, result.RefreshToken)

	return response.OK(c, fiber.Map{
		"access_token": result.AccessToken,
		"user":         result.User,
	})
}

// Logout godoc
// @Summary      Logout and revoke refresh token
// @Tags         auth
// @Security     BearerAuth
// @Produce      json
// @Success      204
// @Failure      401  {object} response.ErrorBody
// @Router       /auth/logout [post]
func (h *Handler) Logout(c *fiber.Ctx) error {
	rawToken := c.Cookies(refreshTokenCookie)
	if rawToken != "" {
		// Best effort — do not fail logout if token not found
		if err := h.svc.Logout(c.Context(), rawToken); err != nil {
			h.log.Warn("logout: failed to revoke token", zap.Error(err))
		}
	}

	// Clear the cookie regardless
	clearRefreshTokenCookie(c)

	return response.NoContent(c)
}

// ForgotPassword godoc
// @Summary      Request a password reset token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body ForgotPasswordRequest true "Email address"
// @Success      204
// @Router       /auth/forgot-password [post]
func (h *Handler) ForgotPassword(c *fiber.Ctx) error {
	var req ForgotPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	// Service is always silent — never reveals if email exists
	h.svc.ForgotPassword(c.Context(), req) //nolint:errcheck

	// Always return 204 — never reveal if the email is registered
	return response.NoContent(c)
}

// ResetPassword godoc
// @Summary      Reset password using a reset token
// @Tags         auth
// @Accept       json
// @Produce      json
// @Param        body body ResetPasswordRequest true "Reset token and new password"
// @Success      204
// @Failure      400  {object} response.ErrorBody
// @Router       /auth/reset-password [post]
func (h *Handler) ResetPassword(c *fiber.Ctx) error {
	var req ResetPasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	if err := h.svc.ResetPassword(c.Context(), req); err != nil {
		return err
	}

	return response.NoContent(c)
}

// ChangePassword godoc
// @Summary      Change password while authenticated
// @Tags         auth
// @Security     BearerAuth
// @Accept       json
// @Produce      json
// @Param        body body ChangePasswordRequest true "Current and new password"
// @Success      204
// @Failure      401  {object} response.ErrorBody
// @Router       /auth/change-password [patch]
func (h *Handler) ChangePassword(c *fiber.Ctx) error {
	var req ChangePasswordRequest
	if err := c.BodyParser(&req); err != nil {
		return apperror.BadRequest("INVALID_BODY", "Request body is invalid")
	}

	userID := middleware.UserIDFromContext(c)

	if err := h.svc.ChangePassword(c.Context(), userID, req); err != nil {
		return err
	}

	return response.NoContent(c)
}

// FreezeAccount godoc
// @Summary      Freeze own account
// @Tags         auth
// @Security     BearerAuth
// @Produce      json
// @Success      204
// @Failure      401  {object} response.ErrorBody
// @Router       /auth/freeze-account [patch]
func (h *Handler) FreezeAccount(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	if err := h.svc.FreezeAccount(c.Context(), userID); err != nil {
		return err
	}

	return response.NoContent(c)
}

// UnfreezeAccount godoc
// @Summary      Unfreeze own account
// @Tags         auth
// @Security     BearerAuth
// @Produce      json
// @Success      204
// @Failure      401  {object} response.ErrorBody
// @Router       /auth/unfreeze-account [patch]
func (h *Handler) UnfreezeAccount(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	if err := h.svc.UnfreezeAccount(c.Context(), userID); err != nil {
		return err
	}

	return response.NoContent(c)
}

// DeleteAccount godoc
// @Summary      Soft delete own account
// @Tags         auth
// @Security     BearerAuth
// @Produce      json
// @Success      204
// @Failure      401  {object} response.ErrorBody
// @Router       /auth/delete-account [delete]
func (h *Handler) DeleteAccount(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	if err := h.svc.DeleteAccount(c.Context(), userID); err != nil {
		return err
	}

	clearRefreshTokenCookie(c)

	return response.NoContent(c)
}

// ── Cookie helpers ───────────────────────────────────────────────────────────

// setRefreshTokenCookie writes the refresh token as a secure httpOnly cookie.
// JavaScript cannot read this cookie — only the browser and server can see it.
func setRefreshTokenCookie(c *fiber.Ctx, token string) {
	c.Cookie(&fiber.Cookie{
		Name:     refreshTokenCookie,
		Value:    token,
		HTTPOnly: true,
		Secure:   true,
		SameSite: "Strict",
		MaxAge:   int(refreshTokenCookieDuration.Seconds()),
		Path:     "/",
	})
}

// clearRefreshTokenCookie expires the refresh token cookie immediately.
// Called on logout and account deletion.
func clearRefreshTokenCookie(c *fiber.Ctx) {
	c.Cookie(&fiber.Cookie{
		Name:     refreshTokenCookie,
		Value:    "",
		HTTPOnly: true,
		Secure:   true,
		SameSite: "Strict",
		MaxAge:   -1,
		Path:     "/",
	})
}
