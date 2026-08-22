package customerauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/go-playground/validator/v10"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	internaljwt "github.com/abdallahkadour/b-edge-api/internal/pkg/jwt"
)

// devBypassOTPCode lets any phone number skip real OTP verification, in
// development only. Added so customer-auth-gated screens (My Bookings, My
// Orders) can be reached and tested without a live Twilio account to
// receive a real WhatsApp code. Works even for a phone number that never
// called RequestOTP at all - it short-circuits before the OTP lookup.
//
// MUST NEVER be reachable in production: gated on APP_ENV, the same
// variable and same fail-CLOSED convention already used for stack traces
// in internal/middleware/register.go. If APP_ENV is unset, empty, or
// misspelled, this path stays closed, not open - the inverse would mean a
// misconfigured deploy silently ships a universal customer-login bypass.
const devBypassOTPCode = "326321"

func isDevBypassCode(code string) bool {
	return os.Getenv("APP_ENV") == "development" && code == devBypassOTPCode
}

// refreshTokenValidity mirrors internal/domain/auth's refresh token
// lifetime exactly - customer sessions use the same 7-day window as
// artist sessions, no reason for the two to diverge.
const refreshTokenValidity = 7 * 24 * time.Hour

// Service handles all customer OTP auth business logic.
type Service struct {
	repo     Repository
	validate *validator.Validate
	log      *zap.Logger
}

// NewService constructs a Service.
// Variadic logger - see the booking domain's NewService for why this is
// optional rather than required (avoids churning every test call site for
// an observability addition).
func NewService(repo Repository, log ...*zap.Logger) *Service {
	l := zap.NewNop()
	if len(log) > 0 && log[0] != nil {
		l = log[0]
	}
	return &Service{repo: repo, validate: validator.New(), log: l}
}

// RequestOTP generates and queues a WhatsApp login code for a phone number.
//
// Resolves (or creates) the customer's identity by phone BEFORE generating
// the code - not after verification - because the notification needs a
// real user_id to attach to (notifications.user_id is NOT NULL), and
// because "requesting to log in with this phone" is exactly the same
// legitimate reason to establish an identity that a guest booking already
// is. The account isn't meaningfully "theirs" until they complete
// verification, but the row existing early is harmless - migration 014's
// phone-uniqueness fix means this can never create a duplicate.
func (s *Service) RequestOTP(ctx context.Context, req RequestOTPRequest) error {
	if err := s.validate.Struct(req); err != nil {
		return mapValidationError(err)
	}

	count, err := s.repo.CountRecentOTPs(ctx, req.Phone, time.Now().Add(-otpRateLimitWindow))
	if err != nil {
		return fmt.Errorf("request otp: rate limit check: %w", err)
	}
	if count >= otpRateLimitMax {
		return apperror.BadRequest("RATE_LIMITED", ErrRateLimited.Error())
	}

	// Read-only eligibility check. Deliberately does NOT create a users row:
	// this endpoint is unauthenticated, so creating an account for whatever
	// phone number a stranger submits let anyone pre-register numbers that
	// never signed up (security audit, Aug 2026). The customer's row is now
	// created in VerifyOTP, once they've actually proven control of it.
	eligible, err := s.repo.PhoneEligibleForOTP(ctx, req.Phone)
	if err != nil {
		return fmt.Errorf("request otp: check eligibility: %w", err)
	}
	if !eligible {
		// The number belongs to an artist account, which authenticates by
		// email+password only. Return the SAME success response as any
		// other request - revealing the difference would turn this endpoint
		// into a phone-enumeration oracle. No code is sent.
		return nil
	}

	code, err := generateOTPCode()
	if err != nil {
		return fmt.Errorf("request otp: generate code: %w", err)
	}

	if _, err := s.repo.CreateOTP(ctx, req.Phone, hashOTP(code), time.Now().Add(otpValidity)); err != nil {
		return fmt.Errorf("request otp: store code: %w", err)
	}

	// Best-effort, matching the exact same pattern booking notifications
	// use - a queuing failure must never fail the request itself, and this
	// genuinely does nothing until Twilio credentials exist (see the
	// worker's own "not configured - log and skip" behavior).
	message := fmt.Sprintf(
		"Your B-Edge verification code is %s. It expires in %d minutes. Don't share this code with anyone.",
		code, int(otpValidity.Minutes()),
	)
	// Best-effort, but LOGGED. This is the most consequential of the
	// swallowed-error sites: RequestOTP returns 200 "Verification code
	// sent" regardless, so without this line a broken queue leaves the
	// person staring at a code entry screen for a code that was never
	// queued, with nothing anywhere to say why.
	if err := s.repo.EnqueueOTPNotification(ctx, req.Phone, message); err != nil {
		s.log.Error("failed to enqueue OTP notification - user was told a code was sent, but none was queued",
			zap.Error(err),
		)
	}

	return nil
}

// VerifyOTP checks a submitted code and, on success, issues a session
// same access+refresh token pair shape internal/domain/auth issues for
// artists, so the frontend's token handling works identically for both.
func (s *Service) VerifyOTP(ctx context.Context, req VerifyOTPRequest) (*VerifyOTPResult, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, mapValidationError(err)
	}

	if isDevBypassCode(req.Code) {
		return s.issueSession(ctx, req.Phone)
	}

	otp, err := s.repo.GetLatestOTP(ctx, req.Phone)
	if err != nil {
		if errors.Is(err, ErrOTPNotFound) {
			return nil, apperror.BadRequest("OTP_NOT_FOUND", ErrOTPNotFound.Error())
		}
		return nil, fmt.Errorf("verify otp: get latest: %w", err)
	}

	if otp.VerifiedAt != nil {
		return nil, apperror.BadRequest("OTP_ALREADY_USED", ErrOTPAlreadyUsed.Error())
	}
	if time.Now().After(otp.ExpiresAt) {
		return nil, apperror.BadRequest("OTP_EXPIRED", ErrOTPExpired.Error())
	}
	if otp.Attempts >= otpMaxAttempts {
		return nil, apperror.BadRequest("OTP_TOO_MANY_ATTEMPTS", ErrTooManyAttempts.Error())
	}

	if hashOTP(req.Code) != otp.OTPHash {
		if incErr := s.repo.IncrementAttempts(ctx, otp.ID); incErr != nil {
			return nil, fmt.Errorf("verify otp: increment attempts: %w", incErr)
		}
		return nil, apperror.BadRequest("OTP_INVALID", ErrInvalidCode.Error())
	}

	if err := s.repo.MarkVerified(ctx, otp.ID); err != nil {
		return nil, fmt.Errorf("verify otp: mark verified: %w", err)
	}

	return s.issueSession(ctx, req.Phone)
}

// issueSession resolves (or creates) the customer by phone and mints a
// fresh access+refresh pair. Shared by the real OTP path (after
// MarkVerified) and the dev-only bypass path above, which has no OTP
// record to mark verified in the first place.
func (s *Service) issueSession(ctx context.Context, phone string) (*VerifyOTPResult, error) {
	customer, err := s.repo.FindOrCreateCustomerByPhone(ctx, phone)
	if err != nil {
		return nil, fmt.Errorf("verify otp: resolve customer: %w", err)
	}

	accessToken, err := internaljwt.GenerateAccessToken(customer.ID, nil, "customer")
	if err != nil {
		return nil, fmt.Errorf("verify otp: generate access token: %w", err)
	}
	refreshToken, err := internaljwt.GenerateRefreshToken(customer.ID)
	if err != nil {
		return nil, fmt.Errorf("verify otp: generate refresh token: %w", err)
	}

	if err := s.repo.StoreRefreshToken(ctx, customer.ID, hashOTP(refreshToken), time.Now().Add(refreshTokenValidity)); err != nil {
		return nil, fmt.Errorf("verify otp: store refresh token: %w", err)
	}

	return &VerifyOTPResult{
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		Customer:     *customer,
	}, nil
}

// Refresh exchanges a valid, unrevoked refresh token for a new session
// mirrors internal/domain/auth's Refresh exactly: verify signature, check
// not revoked, rotate (revoke the old one, issue + store a new pair).
// Called once at app bootstrap by the frontend's AuthStore to restore a
// session after a page reload, since the access token itself only ever
// lives in memory.
func (s *Service) Refresh(ctx context.Context, rawRefreshToken string) (*VerifyOTPResult, error) {
	userID, err := internaljwt.VerifyRefreshToken(rawRefreshToken)
	if err != nil {
		return nil, apperror.Unauthorized("TOKEN_INVALID", "Authentication failed")
	}

	oldHash := hashOTP(rawRefreshToken)
	stored, err := s.repo.GetRefreshTokenHash(ctx, oldHash)
	if err != nil {
		if errors.Is(err, ErrRefreshTokenNotFound) {
			return nil, apperror.Unauthorized("TOKEN_INVALID", "Authentication failed")
		}
		return nil, fmt.Errorf("refresh: get token: %w", err)
	}
	if stored.RevokedAt != nil {
		return nil, apperror.Unauthorized("TOKEN_INVALID", "Authentication failed")
	}

	if err := s.repo.RevokeRefreshToken(ctx, oldHash); err != nil {
		return nil, fmt.Errorf("refresh: revoke old token: %w", err)
	}

	customer, err := s.repo.GetCustomerByID(ctx, userID)
	if err != nil {
		if errors.Is(err, ErrCustomerNotFound) {
			return nil, apperror.Unauthorized("TOKEN_INVALID", "Authentication failed")
		}
		return nil, fmt.Errorf("refresh: get customer: %w", err)
	}

	accessToken, err := internaljwt.GenerateAccessToken(customer.ID, nil, "customer")
	if err != nil {
		return nil, fmt.Errorf("refresh: generate access token: %w", err)
	}
	newRefreshToken, err := internaljwt.GenerateRefreshToken(customer.ID)
	if err != nil {
		return nil, fmt.Errorf("refresh: generate refresh token: %w", err)
	}
	if err := s.repo.StoreRefreshToken(ctx, customer.ID, hashOTP(newRefreshToken), time.Now().Add(refreshTokenValidity)); err != nil {
		return nil, fmt.Errorf("refresh: store new token: %w", err)
	}

	return &VerifyOTPResult{
		AccessToken:  accessToken,
		RefreshToken: newRefreshToken,
		Customer:     *customer,
	}, nil
}

// Logout revokes a refresh token so it can never be used again - same
// one-way revocation as internal/domain/auth's Logout.
func (s *Service) Logout(ctx context.Context, rawRefreshToken string) error {
	if err := s.repo.RevokeRefreshToken(ctx, hashOTP(rawRefreshToken)); err != nil {
		return fmt.Errorf("logout: revoke token: %w", err)
	}
	return nil
}

// generateOTPCode produces a cryptographically random N-digit numeric
// string (zero-padded - "004821" is a valid code, not "4821").
func generateOTPCode() (string, error) {
	max := big.NewInt(1)
	for i := 0; i < otpLength; i++ {
		max.Mul(max, big.NewInt(10))
	}
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return "", fmt.Errorf("generate otp code: %w", err)
	}
	return fmt.Sprintf("%0*d", otpLength, n), nil
}

// hashOTP hashes a code (or a refresh token) for storage - same sha256-hex
// convention already used for refresh tokens in internal/domain/auth's
// hashToken. Deliberately reused here rather than bcrypt: both OTPs and
// refresh tokens are high-entropy-enough-or-rate-limited-enough that
// bcrypt's deliberate slowness buys nothing, matching the existing
// codebase's own choice for the same trade-off.
func hashOTP(value string) string {
	h := sha256.Sum256([]byte(value))
	return hex.EncodeToString(h[:])
}

// mapValidationError converts a validator error into a proper
// UnprocessableEntity with field-level details - matching the exact
// pattern already established in the review domain, not a stripped-down
// generic message. This detail is what the frontend's
// extractApiErrorMessage helper (built earlier this session) actually
// surfaces to the person filling in the form; a generic message here would
// silently regress that fix for this one domain.
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

func validationMessage(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return fe.Field() + " is required"
	case "min":
		return fe.Field() + " must be at least " + fe.Param()
	case "max":
		return fe.Field() + " must be at most " + fe.Param() + " characters"
	case "len":
		return fe.Field() + " must be exactly " + fe.Param() + " characters"
	case "numeric":
		return fe.Field() + " must contain only numbers"
	default:
		return fe.Field() + " is invalid"
	}
}
