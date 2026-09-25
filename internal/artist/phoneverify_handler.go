package artist

import (
	"github.com/gofiber/fiber/v2"

	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// VerifyPhoneRequest is the body for POST /artists/me/phone/verify.
type VerifyPhoneRequest struct {
	// len=6 matches a real code exactly. It is also why the dev bypass is
	// 000000 rather than 0000 - a shorter code is rejected here and never
	// reaches verification at all.
	Code string `json:"code" validate:"required,len=6,numeric"`
}

// RequestPhoneOTP godoc
//
//	@Summary     Send a verification code to your own phone
//	@Description Sends a one-time code to the number on the authenticated
//	@Description artist's account. The number is read from the account, never
//	@Description from the request - so this cannot be used to verify somebody
//	@Description else's phone onto your own profile.
//	@Tags        artists
//	@Produce     json
//	@Success     202 {object} response.Body
//	@Failure     409 {object} response.ErrorBody "NO_PHONE_ON_ACCOUNT or PHONE_ALREADY_VERIFIED"
//	@Failure     429 {object} response.ErrorBody "RATE_LIMITED"
//	@Security    BearerAuth
//	@Router      /artists/me/phone/request-otp [post]
func (h *Handler) RequestPhoneOTP(c *fiber.Ctx) error {
	// The auth middleware guarantees this local is present; every other
	// handler in this file reads it the same way.
	userID := middleware.UserIDFromContext(c)

	if err := h.svc.RequestPhoneOTP(c.UserContext(), userID); err != nil {
		return err
	}

	// 202: queued, not delivered. Claiming 200 would assert something this
	// endpoint cannot know - the worker owns delivery, and on this platform
	// delivery has never once succeeded.
	return response.Accepted(c, fiber.Map{
		"message": "A verification code has been sent to your phone",
	})
}

// VerifyPhone godoc
//
//	@Summary     Confirm the code and verify your phone number
//	@Description On success, stamps the account's phone as verified. That is
//	@Description what a salon owner's invitation checks against.
//	@Tags        artists
//	@Accept      json
//	@Produce     json
//	@Param       body body VerifyPhoneRequest true "The six-digit code"
//	@Success     200 {object} response.Body
//	@Failure     400 {object} response.ErrorBody "OTP_NOT_FOUND, OTP_EXPIRED, OTP_INVALID, OTP_ALREADY_USED, OTP_TOO_MANY_ATTEMPTS"
//	@Security    BearerAuth
//	@Router      /artists/me/phone/verify [post]
func (h *Handler) VerifyPhone(c *fiber.Ctx) error {
	userID := middleware.UserIDFromContext(c)

	var req VerifyPhoneRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	if err := validation.New().Struct(req); err != nil {
		return validation.MapError(err)
	}

	if err := h.svc.VerifyPhoneOTP(c.UserContext(), userID, req.Code); err != nil {
		return err
	}

	return response.OK(c, fiber.Map{
		"message":  "Your phone number is verified",
		"verified": true,
	})
}
