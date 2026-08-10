// Package customerauth implements phone-based WhatsApp OTP login for
// customer accounts - deliberately separate from internal/domain/auth
// (artist email+password login), since the mechanism is genuinely
// different, not just a variant of the same flow.
//
// Guest booking remains fully available and unaffected by any of this - an
// account is optional, useful mainly for viewing booking history across
// visits. This mirrors Fresha's own model (guest checkout OR an account),
// validated before building rather than assumed.
package customerauth

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var (
	// ErrOTPNotFound is returned when no active OTP exists for a phone
	// either none was ever requested, or the most recent one already
	// expired/was already used.
	ErrOTPNotFound = errors.New("no active verification code for this phone number")

	// ErrOTPExpired is returned when the OTP exists but its expiry has passed.
	ErrOTPExpired = errors.New("verification code has expired")

	// ErrOTPAlreadyUsed is returned when attempting to verify a code that
	// already succeeded once - codes are single-use.
	ErrOTPAlreadyUsed = errors.New("verification code has already been used")

	// ErrTooManyAttempts is returned once a single OTP has been guessed
	// wrong 5 times - that code is permanently dead, a new one must be
	// requested. Prevents brute-forcing a 6-digit space.
	ErrTooManyAttempts = errors.New("too many incorrect attempts - request a new code")

	// ErrInvalidCode is returned when the submitted code doesn't match.
	ErrInvalidCode = errors.New("incorrect verification code")

	// ErrRateLimited is returned when a phone number has already requested
	// 3 codes within the last 5 minutes.
	ErrRateLimited = errors.New("too many codes requested - please wait a few minutes and try again")

	// ErrRefreshTokenNotFound is returned when a refresh token's hash
	// doesn't match any stored row - either it's invalid, or it belongs to
	// a different session type (e.g. an artist's cookie sent by mistake).
	ErrRefreshTokenNotFound = errors.New("session not found")

	// ErrPhoneNotEligible is returned when a phone number is already held by
	// a non-customer account (an artist). Callers must treat this as a
	// silent no-op that still returns the normal success response - telling
	// the caller "that number belongs to an artist" would turn this
	// endpoint into a phone-enumeration oracle.
	ErrPhoneNotEligible = errors.New("phone number is not eligible for customer login")

	// ErrCustomerNotFound is returned when a customer ID from a valid JWT
	// no longer resolves to an active user row (e.g. the account was
	// deleted between token issuance and this request).
	ErrCustomerNotFound = errors.New("customer not found")
)

// StoredRefreshToken is the subset of a refresh_tokens row needed to
// validate a refresh request - same table internal/domain/auth already
// uses, generic across roles.
type StoredRefreshToken struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	RevokedAt *time.Time
}

// otpLength is the number of digits in a generated code. 6 digits matches
// standard consumer-app practice (validated: 4-6 digit range is typical;
// 6 is the unambiguous middle choice, same length Gmail/WhatsApp itself uses).
const otpLength = 6

// otpValidity is how long a generated code remains usable.
const otpValidity = 5 * time.Minute

// otpRateLimitWindow + otpRateLimitMax together enforce "3 requests per
// phone per 5 minutes" - validated as standard OTP practice, not invented.
const otpRateLimitWindow = 5 * time.Minute
const otpRateLimitMax = 3

// otpMaxAttempts is how many wrong guesses a single code tolerates before
// it's permanently dead - validated as standard practice (5 attempts).
const otpMaxAttempts = 5

// CustomerOTP mirrors a row in customer_otps.
type CustomerOTP struct {
	ID         uuid.UUID
	Phone      string
	OTPHash    string
	ExpiresAt  time.Time
	Attempts   int
	VerifiedAt *time.Time
	CreatedAt  time.Time
}

// RequestOTPRequest is the body for POST /customer-auth/request-otp.
type RequestOTPRequest struct {
	Phone string `json:"phone" validate:"required,min=7,max=20"`
}

// VerifyOTPRequest is the body for POST /customer-auth/verify-otp.
type VerifyOTPRequest struct {
	Phone string `json:"phone" validate:"required,min=7,max=20"`
	Code  string `json:"code"  validate:"required,len=6,numeric"`
}

// CustomerInfo is the safe subset of a customer's user row returned to the client.
type CustomerInfo struct {
	ID    uuid.UUID `json:"id"`
	Name  string    `json:"name"`
	Phone string    `json:"phone"`
}

// VerifyOTPResult is the response for a successful verify - mirrors the
// shape internal/domain/auth's LoginResult already returns, so the
// frontend's token-handling code can work identically for both.
type VerifyOTPResult struct {
	AccessToken  string       `json:"access_token"`
	RefreshToken string       `json:"-"` // never serialized - set as an httpOnly cookie only
	Customer     CustomerInfo `json:"customer"`
}
