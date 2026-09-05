package customerauth

import (
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// refreshTokenCookie is the cookie name for the customer's refresh token
// deliberately distinct from internal/domain/auth's artist cookie name, so
// a browser that's simultaneously testing both an artist dashboard session
// and a customer session (e.g. Edge, during development) never collides.
const refreshTokenCookie = "customer_refresh_token"

// Handler handles all HTTP requests for customer OTP auth.
type Handler struct {
	svc *Service
}

// RegisterRoutes attaches customer-auth routes to the Fiber app. Both
// routes are deliberately public - a guest has no session to authenticate
// with yet, that's the entire point of this domain.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, log *zap.Logger) {
	repo := NewRepository(pool)
	svc := NewService(repo, log)
	handler := &Handler{svc: svc}

	g := app.Group("/api/v1/customer-auth")
	g.Post("/request-otp", handler.RequestOTP)
	g.Post("/verify-otp", handler.VerifyOTP)
	g.Post("/refresh", handler.Refresh)
	g.Post("/logout", handler.Logout)
}

// RequestOTP godoc
// @Summary      Request a WhatsApp login code (public)
// @Description  Sends a 6-digit code to the given phone number via WhatsApp.
// @Description  Rate limited to 3 requests per phone per 5 minutes. Always
// @Description  returns 200 regardless of whether this phone has booked
// @Description  before - the response never reveals whether a phone number
// @Description  is already known, matching standard OTP-flow practice.
// @Tags         customer-auth
// @Accept       json
// @Param        body body RequestOTPRequest true "Phone number"
// @Success      200
// @Router       /customer-auth/request-otp [post]
func (h *Handler) RequestOTP(c *fiber.Ctx) error {
	var req RequestOTPRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	if err := h.svc.RequestOTP(c.Context(), req); err != nil {
		return err
	}

	return response.OK(c, fiber.Map{"message": "Verification code sent"})
}

// VerifyOTP godoc
// @Summary      Verify a WhatsApp login code and start a session (public)
// @Description  On success, issues an access token in the response body and
// @Description  sets a refresh token as an httpOnly cookie - same session
// @Description  model as artist login.
// @Tags         customer-auth
// @Accept       json
// @Produce      json
// @Param        body body VerifyOTPRequest true "Phone and code"
// @Success      200 {object} response.Body{data=VerifyOTPResult}
// @Failure      400 {object} response.ErrorBody
// @Router       /customer-auth/verify-otp [post]
func (h *Handler) VerifyOTP(c *fiber.Ctx) error {
	var req VerifyOTPRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}

	result, err := h.svc.VerifyOTP(c.Context(), req)
	if err != nil {
		return err
	}

	setRefreshTokenCookie(c, result.RefreshToken)

	return response.OK(c, fiber.Map{
		"access_token": result.AccessToken,
		"customer":     result.Customer,
	})
}

// Refresh godoc
// @Summary      Refresh an access token using the refresh cookie (public)
// @Description  Called at app bootstrap to restore a session after a page
// @Description  reload, since the access token only ever lives in memory
// @Description  on the frontend. Rotates the refresh token - one use only.
// @Tags         customer-auth
// @Produce      json
// @Success      200 {object} response.Body{data=VerifyOTPResult}
// @Failure      401 {object} response.ErrorBody
// @Router       /customer-auth/refresh [post]
func (h *Handler) Refresh(c *fiber.Ctx) error {
	rawToken := c.Cookies(refreshTokenCookie)
	if rawToken == "" {
		return apperror.Unauthorized("TOKEN_MISSING", "Authentication required")
	}

	result, err := h.svc.Refresh(c.Context(), rawToken)
	if err != nil {
		return err
	}

	setRefreshTokenCookie(c, result.RefreshToken)

	return response.OK(c, fiber.Map{
		"access_token": result.AccessToken,
		"customer":     result.Customer,
	})
}

// Logout godoc
// @Summary      Revoke the current session (public)
// @Tags         customer-auth
// @Success      204
// @Router       /customer-auth/logout [post]
func (h *Handler) Logout(c *fiber.Ctx) error {
	rawToken := c.Cookies(refreshTokenCookie)
	if rawToken != "" {
		_ = h.svc.Logout(c.Context(), rawToken)
	}
	clearRefreshTokenCookie(c)
	return c.SendStatus(fiber.StatusNoContent)
}

// setRefreshTokenCookie sets the httpOnly refresh cookie - same settings
// (Secure, SameSite=Strict, MaxAge, Path="/") as internal/domain/auth's
// artist cookie, matched exactly rather than guessed, so both behave
// identically on local HTTP dev.
func setRefreshTokenCookie(c *fiber.Ctx, token string) {
	c.Cookie(&fiber.Cookie{
		Name:     refreshTokenCookie,
		Value:    token,
		HTTPOnly: true,
		Secure:   true,
		SameSite: "Strict",
		MaxAge:   int(refreshTokenValidity.Seconds()),
		Path:     "/",
	})
}

// clearRefreshTokenCookie expires the cookie immediately - called on logout.
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
