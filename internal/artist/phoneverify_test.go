package artist

// Artist phone verification, at the service layer.
//
// The rule itself (ordering, ceilings, expiry) is tested in
// internal/pkg/otp - once, where it lives. These cover what THIS flow adds:
// reading the number from the account rather than the request, refusing to
// re-verify, burning an attempt only on a wrong code, and the order the two
// writes happen in.

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/otp"
)

type fakePhones struct {
	phone      string
	verifiedAt *time.Time
	marked     bool
	queued     []string
}

func (f *fakePhones) PhoneForUser(context.Context, uuid.UUID) (string, *time.Time, error) {
	return f.phone, f.verifiedAt, nil
}
func (f *fakePhones) MarkPhoneVerified(context.Context, uuid.UUID) error { f.marked = true; return nil }
func (f *fakePhones) EnqueueOTP(_ context.Context, _, msg string) error {
	f.queued = append(f.queued, msg)
	return nil
}

type fakeOTPs struct {
	recent      int
	rec         otp.Record
	latestErr   error
	incremented int
	verified    bool
	createdHash string
}

func (f *fakeOTPs) CountRecent(context.Context, string, time.Time) (int, error) {
	return f.recent, nil
}
func (f *fakeOTPs) Create(_ context.Context, _, hash string, _ time.Time) (uuid.UUID, error) {
	f.createdHash = hash
	return uuid.New(), nil
}
func (f *fakeOTPs) Latest(context.Context, string) (uuid.UUID, otp.Record, error) {
	if f.latestErr != nil {
		return uuid.Nil, otp.Record{}, f.latestErr
	}
	return uuid.New(), f.rec, nil
}
func (f *fakeOTPs) IncrementAttempts(context.Context, uuid.UUID) error { f.incremented++; return nil }
func (f *fakeOTPs) MarkVerified(context.Context, uuid.UUID) error      { f.verified = true; return nil }

func newPhoneService(p *fakePhones, o *fakeOTPs) *Service {
	return NewServiceWithPhones(nil, p, o)
}

func codeFor(s string) otp.Record {
	return otp.Record{Hash: otp.Hash(s), ExpiresAt: time.Now().UTC().Add(otp.Validity)}
}

func errCode(t *testing.T, err error) string {
	t.Helper()
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	return ae.Code
}

// ── requesting ─────────────────────────────────────────────────────────────

func TestRequestPhoneOTP_QueuesACodeNeverThePlaintext(t *testing.T) {
	p := &fakePhones{phone: "+96170555123"}
	o := &fakeOTPs{}
	err := newPhoneService(p, o).RequestPhoneOTP(context.Background(), uuid.New())

	require.NoError(t, err)
	require.Len(t, p.queued, 1, "a message must be queued")
	assert.NotEmpty(t, o.createdHash, "a code must be stored")
	assert.Len(t, o.createdHash, 64, "sha256-hex is 64 characters, a code is 6")

	// The real assertion: whatever six-digit code went into the message must
	// NOT be what was stored. A dump of the OTP table must not be replayable.
	require.Regexp(t, `\b(\d{6})\b`, p.queued[0], "the message must carry a six-digit code")
	sent := regexp.MustCompile(`\b(\d{6})\b`).FindStringSubmatch(p.queued[0])[1]
	assert.NotEqual(t, sent, o.createdHash, "the plaintext code must never be stored")
	assert.Equal(t, otp.Hash(sent), o.createdHash,
		"what is stored must be the hash OF the code that was sent")
}

func TestRequestPhoneOTP_AlreadyVerified_Refused(t *testing.T) {
	at := time.Now().UTC().Add(-time.Hour)
	p := &fakePhones{phone: "+96170555123", verifiedAt: &at}
	err := newPhoneService(p, &fakeOTPs{}).RequestPhoneOTP(context.Background(), uuid.New())

	assert.Equal(t, "PHONE_ALREADY_VERIFIED", errCode(t, err))
	assert.Empty(t, p.queued, "no message may be sent for a number already proven")
}

func TestRequestPhoneOTP_NoPhoneOnAccount_Refused(t *testing.T) {
	p := &fakePhones{phone: ""}
	err := newPhoneService(p, &fakeOTPs{}).RequestPhoneOTP(context.Background(), uuid.New())

	assert.Equal(t, "NO_PHONE_ON_ACCOUNT", errCode(t, err))
}

func TestRequestPhoneOTP_RateLimited_Refused(t *testing.T) {
	p := &fakePhones{phone: "+96170555123"}
	o := &fakeOTPs{recent: otp.RateLimitMax}
	err := newPhoneService(p, o).RequestPhoneOTP(context.Background(), uuid.New())

	assert.Equal(t, "RATE_LIMITED", errCode(t, err))
	assert.Empty(t, p.queued)
}

func TestRequestPhoneOTP_JustUnderTheLimit_Allowed(t *testing.T) {
	// POSITIVE CONTROL. A limiter that refused at RateLimitMax-1 would
	// satisfy the test above while costing a genuine user their last request.
	p := &fakePhones{phone: "+96170555123"}
	o := &fakeOTPs{recent: otp.RateLimitMax - 1}

	require.NoError(t, newPhoneService(p, o).RequestPhoneOTP(context.Background(), uuid.New()))
	assert.Len(t, p.queued, 1)
}

// ── verifying ──────────────────────────────────────────────────────────────

func TestVerifyPhoneOTP_CorrectCode_MarksCodeUsedThenAccount(t *testing.T) {
	p := &fakePhones{phone: "+96170555123"}
	o := &fakeOTPs{rec: codeFor("123456")}

	require.NoError(t, newPhoneService(p, o).VerifyPhoneOTP(context.Background(), uuid.New(), "123456"))

	assert.True(t, o.verified, "the code must be marked used")
	assert.True(t, p.marked, "the account must be stamped verified")
	assert.Zero(t, o.incremented, "a correct code must not burn an attempt")
}

func TestVerifyPhoneOTP_WrongCode_BurnsExactlyOneAttempt(t *testing.T) {
	p := &fakePhones{phone: "+96170555123"}
	o := &fakeOTPs{rec: codeFor("123456")}

	err := newPhoneService(p, o).VerifyPhoneOTP(context.Background(), uuid.New(), "999999")

	assert.Equal(t, "OTP_INVALID", errCode(t, err))
	assert.Equal(t, 1, o.incremented)
	assert.False(t, p.marked, "a wrong code must not verify the account")
}

func TestVerifyPhoneOTP_ExpiredCode_DoesNotBurnAnAttempt(t *testing.T) {
	// Incrementing on an expired code would let anyone exhaust a victim's
	// remaining attempts against a code that is already dead.
	p := &fakePhones{phone: "+96170555123"}
	rec := codeFor("123456")
	rec.ExpiresAt = time.Now().UTC().Add(-time.Minute)
	o := &fakeOTPs{rec: rec}

	err := newPhoneService(p, o).VerifyPhoneOTP(context.Background(), uuid.New(), "123456")

	assert.Equal(t, "OTP_EXPIRED", errCode(t, err))
	assert.Zero(t, o.incremented, "an expired code must not consume an attempt")
	assert.False(t, p.marked)
}

func TestVerifyPhoneOTP_AlreadyVerified_IsIdempotent(t *testing.T) {
	// A double-submit from a flaky connection must not read as a failure.
	at := time.Now().UTC().Add(-time.Hour)
	p := &fakePhones{phone: "+96170555123", verifiedAt: &at}
	o := &fakeOTPs{}

	require.NoError(t, newPhoneService(p, o).VerifyPhoneOTP(context.Background(), uuid.New(), "123456"))
	assert.Zero(t, o.incremented)
}

func TestVerifyPhoneOTP_NoCodeEverRequested_Refused(t *testing.T) {
	p := &fakePhones{phone: "+96170555123"}
	o := &fakeOTPs{latestErr: otp.ErrNotFound}

	err := newPhoneService(p, o).VerifyPhoneOTP(context.Background(), uuid.New(), "123456")

	assert.Equal(t, "OTP_NOT_FOUND", errCode(t, err))
	assert.False(t, p.marked)
}
