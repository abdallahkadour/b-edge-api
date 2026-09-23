package artist

// Artist phone verification.
//
// An artist proves the number on their account is theirs. That timestamp is
// what membership.Invite reads when REQUIRE_VERIFIED_PHONE_FOR_INVITE is on:
// a salon may only invite a registered artist whose number is verified
// (migration 051), so without this the invitation feature has no way to ever
// be switched on.
//
// ── What is reused, and what is not ──────────────────────────────────────
//
// The RULE is internal/pkg/otp: code generation, hashing, the ceilings, and
// the exact order a submission is judged in. Customer login applies the
// identical rule to reach a completely different outcome - a session rather
// than a timestamp - and two copies of a security check is the defect class
// this codebase keeps producing.
//
// DELIVERY is internal/notification's queue, which sendVia drives: WhatsApp
// first, SMS fallback the moment TWILIO_SMS_FROM is set. Building against the
// queue rather than a Twilio client means SMS works later with no change
// here.
//
// ── The dev bypass ───────────────────────────────────────────────────────
//
// devbypass.Allows is consulted first, exactly as customer login does. In a
// production build that function is `return false` and the code is not in the
// binary - see internal/pkg/devbypass. It exists so these flows can be
// exercised while outbound delivery does not work.

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/devbypass"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/otp"
)

// PhoneVerifyPort is what this flow needs from storage.
//
// Narrow on purpose: the artist Repository is large, and a flow that only
// needs four things should say so rather than accept everything.
type PhoneVerifyPort interface {
	// PhoneForUser returns the user's stored number and whether it is already
	// verified.
	PhoneForUser(ctx context.Context, userID uuid.UUID) (phone string, verifiedAt *time.Time, err error)

	// MarkPhoneVerified stamps users.phone_verified_at.
	MarkPhoneVerified(ctx context.Context, userID uuid.UUID) error

	// EnqueueOTP queues the code for delivery. Queue, not send: the worker
	// owns transport and retries.
	EnqueueOTP(ctx context.Context, phone, message string) error
}

// RequestPhoneOTP sends a verification code to the artist's own number.
//
// The number comes from the ACCOUNT, never from the request body. Accepting a
// caller-supplied number here would let an artist verify somebody else's
// phone onto their own account, which is the whole thing this is meant to
// establish.
func (s *Service) RequestPhoneOTP(ctx context.Context, userID uuid.UUID) error {
	phoneNumber, verifiedAt, err := s.phones.PhoneForUser(ctx, userID)
	if err != nil {
		return err
	}
	if phoneNumber == "" {
		// Registration requires a number for artists, so this is an account
		// predating that rule rather than a normal state.
		return apperror.Conflict("NO_PHONE_ON_ACCOUNT",
			"Add a phone number to your profile before verifying it")
	}
	if verifiedAt != nil {
		return apperror.Conflict("PHONE_ALREADY_VERIFIED",
			"This number is already verified")
	}

	// Rate limited on the NUMBER, not the account, and deliberately: the cost
	// being protected is messages sent to a handset, which does not care which
	// account asked.
	count, err := s.otps.CountRecent(ctx, phoneNumber, time.Now().Add(-otp.RateLimitWindow))
	if err != nil {
		return err
	}
	if count >= otp.RateLimitMax {
		return apperror.TooManyRequests("RATE_LIMITED",
			"Too many codes requested. Try again in a few minutes.")
	}

	code, err := otp.Generate()
	if err != nil {
		return fmt.Errorf("request phone otp: %w", err)
	}
	if _, err := s.otps.Create(ctx, phoneNumber, otp.Hash(code), time.Now().Add(otp.Validity)); err != nil {
		return err
	}

	// Queued, not sent. If delivery is broken the code still exists, so a
	// later fix makes previously-requested codes work rather than stranding
	// them - and the caller is not held open waiting on Twilio.
	msg := fmt.Sprintf("Your B-Edge verification code is %s. It expires in %d minutes.",
		code, int(otp.Validity.Minutes()))
	if err := s.phones.EnqueueOTP(ctx, phoneNumber, msg); err != nil {
		return err
	}
	return nil
}

// VerifyPhoneOTP checks a submitted code and, on success, stamps
// users.phone_verified_at.
func (s *Service) VerifyPhoneOTP(ctx context.Context, userID uuid.UUID, code string) error {
	phoneNumber, verifiedAt, err := s.phones.PhoneForUser(ctx, userID)
	if err != nil {
		return err
	}
	if phoneNumber == "" {
		return apperror.Conflict("NO_PHONE_ON_ACCOUNT",
			"Add a phone number to your profile before verifying it")
	}
	if verifiedAt != nil {
		// Idempotent rather than an error: a double-submit from a flaky
		// connection should not read as a failure to the artist.
		return nil
	}

	// Same order as customer login: the bypass is consulted before the
	// lookup, so it works for a number that never requested a code at all.
	// In a production build this is `return false`.
	if devbypass.Allows(code) {
		return s.phones.MarkPhoneVerified(ctx, userID)
	}

	id, rec, err := s.otps.Latest(ctx, phoneNumber)
	if err != nil {
		if errors.Is(err, otp.ErrNotFound) {
			return apperror.BadRequest("OTP_NOT_FOUND",
				"No code has been sent to this number. Request one first.")
		}
		return err
	}

	switch otp.Check(rec, code, time.Now()) {
	case otp.AlreadyUsed:
		return apperror.BadRequest("OTP_ALREADY_USED", "That code has already been used")
	case otp.Expired:
		return apperror.BadRequest("OTP_EXPIRED", "That code has expired. Request a new one.")
	case otp.TooManyAttempts:
		return apperror.BadRequest("OTP_TOO_MANY_ATTEMPTS",
			"Too many incorrect attempts. Request a new code.")
	case otp.WrongCode:
		// Only a wrong code burns an attempt.
		if incErr := s.otps.IncrementAttempts(ctx, id); incErr != nil {
			return incErr
		}
		return apperror.BadRequest("OTP_INVALID", "That code is not correct")
	}

	// Mark the CODE used before the account, so a failure between the two
	// cannot leave a code that still verifies. Verified-but-code-unmarked is
	// recoverable; code-unmarked-but-verified is a replayable code.
	if err := s.otps.MarkVerified(ctx, id); err != nil {
		return err
	}
	return s.phones.MarkPhoneVerified(ctx, userID)
}
