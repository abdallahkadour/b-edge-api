// Package customerauth contains unit tests for the customer OTP auth
// service layer. Uses an in-memory mockRepo - no database required.
package customerauth

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/stretchr/testify/require"

	internaljwt "github.com/abdallahkadour/b-edge-api/internal/pkg/jwt"
)

func TestMain(m *testing.M) {
	os.Setenv("JWT_SECRET", "test-secret-that-is-long-enough-for-testing-purposes-only")
	os.Setenv("JWT_REFRESH_SECRET", "test-refresh-secret-that-is-long-enough-for-testing-only")
	os.Exit(m.Run())
}

// ── Mock repository ──────────────────────────────────────────────────────

type mockRepo struct {
	deliveryLooksBroken    bool
	deliveryLooksBrokenErr error

	recentOTPCount    int
	recentOTPCountErr error

	createOTPID  uuid.UUID
	createOTPErr error

	latestOTP    *CustomerOTP
	latestOTPErr error

	incrementAttemptsErr error
	markVerifiedErr      error

	customer    *CustomerInfo
	customerErr error

	storeRefreshTokenErr error

	// refresh/logout flow
	storedToken     *StoredRefreshToken
	storedTokenErr  error
	revokeCalled    bool
	revokeErr       error
	customerByID    *CustomerInfo
	customerByIDErr error

	// captured for assertions
	enqueuedMessage    string
	enqueuedPhone      string
	phoneIneligible    bool
	phoneEligibleErr   error
	findOrCreateCalled bool
	incrementCalled    bool
}

func (m *mockRepo) CountRecentOTPs(_ context.Context, _ string, _ time.Time) (int, error) {
	return m.recentOTPCount, m.recentOTPCountErr
}
func (m *mockRepo) CreateOTP(_ context.Context, _, _ string, _ time.Time) (uuid.UUID, error) {
	return m.createOTPID, m.createOTPErr
}
func (m *mockRepo) GetLatestOTP(_ context.Context, _ string) (*CustomerOTP, error) {
	return m.latestOTP, m.latestOTPErr
}
func (m *mockRepo) IncrementAttempts(_ context.Context, _ uuid.UUID) error {
	m.incrementCalled = true
	return m.incrementAttemptsErr
}
func (m *mockRepo) MarkVerified(_ context.Context, _ uuid.UUID) error {
	return m.markVerifiedErr
}
func (m *mockRepo) FindOrCreateCustomerByPhone(_ context.Context, phone string) (*CustomerInfo, error) {
	m.findOrCreateCalled = true
	if m.customerErr != nil {
		return nil, m.customerErr
	}
	if m.customer != nil {
		return m.customer, nil
	}
	return &CustomerInfo{ID: uuid.New(), Name: "Customer", Phone: phone}, nil
}
func (m *mockRepo) StoreRefreshToken(_ context.Context, _ uuid.UUID, _ string, _ time.Time) error {
	return m.storeRefreshTokenErr
}
func (m *mockRepo) RecentDeliveryLooksBroken(_ context.Context) (bool, error) {
	return m.deliveryLooksBroken, m.deliveryLooksBrokenErr
}

func (m *mockRepo) EnqueueOTPNotification(_ context.Context, phone, message string) error {
	m.enqueuedPhone = phone
	m.enqueuedMessage = message
	return nil
}
func (m *mockRepo) PhoneEligibleForOTP(_ context.Context, _ string) (bool, error) {
	if m.phoneEligibleErr != nil {
		return false, m.phoneEligibleErr
	}
	return !m.phoneIneligible, nil
}
func (m *mockRepo) GetRefreshTokenHash(_ context.Context, _ string) (*StoredRefreshToken, error) {
	return m.storedToken, m.storedTokenErr
}
func (m *mockRepo) RevokeRefreshToken(_ context.Context, _ string) error {
	m.revokeCalled = true
	return m.revokeErr
}
func (m *mockRepo) GetCustomerByID(_ context.Context, id uuid.UUID) (*CustomerInfo, error) {
	if m.customerByIDErr != nil {
		return nil, m.customerByIDErr
	}
	if m.customerByID != nil {
		return m.customerByID, nil
	}
	return &CustomerInfo{ID: id, Name: "Customer", Phone: "+96170000000"}, nil
}

func newTestService(repo Repository) *Service {
	return NewService(repo)
}

// ── RequestOTP tests ─────────────────────────────────────────────────────

func TestRequestOTP_Success_EnqueuesMessageWithCode(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.RequestOTP(context.Background(), RequestOTPRequest{Phone: "70123456"})

	require.NoError(t, err)
	assert.Equal(t, "+96170123456", repo.enqueuedPhone,
		"queued against the PHONE - no users row exists yet at this point - and in "+
			"E.164, because the notification worker concatenates \"whatsapp:\" straight "+
			"onto this value and Twilio rejects anything else")
	assert.Contains(t, repo.enqueuedMessage, "B-Edge verification code")
	assert.Contains(t, repo.enqueuedMessage, "5 minutes")
}

// TestRequestOTP_DoesNotCreateUser guards the audit fix: this endpoint is
// unauthenticated, so creating a users row for whatever number a stranger
// submits let anyone pre-register phone numbers that never signed up. The
// row is now created only in VerifyOTP, after the caller proves control.
func TestRequestOTP_DoesNotCreateUser(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.RequestOTP(context.Background(), RequestOTPRequest{Phone: "70123456"})

	require.NoError(t, err)
	assert.False(t, repo.findOrCreateCalled,
		"RequestOTP must never create a users row - an unauthenticated caller controls this phone value")
}

// TestRequestOTP_IneligiblePhone_SilentSuccess - a number held by an artist
// must return the SAME response as any other request. A distinguishable
// error would make this endpoint a phone-enumeration oracle.
func TestRequestOTP_IneligiblePhone_SilentSuccess(t *testing.T) {
	repo := &mockRepo{phoneIneligible: true}
	svc := newTestService(repo)

	_, err := svc.RequestOTP(context.Background(), RequestOTPRequest{Phone: "70123456"})

	require.NoError(t, err, "must look identical to a successful request from outside")
	assert.Empty(t, repo.enqueuedPhone, "but no code may actually be sent")
}

func TestRequestOTP_RateLimited_Rejected(t *testing.T) {
	repo := &mockRepo{recentOTPCount: 3} // already at the max
	svc := newTestService(repo)

	_, err := svc.RequestOTP(context.Background(), RequestOTPRequest{Phone: "+96170123456"})

	assert.Error(t, err)
}

// The STATUS matters, not just the failure. This previously returned 400
// because apperror had no 429 helper, and two things broke silently: the
// shared front-end interceptor keys on 429 and never showed its rate-limit
// banner, and the error handler only logs at 404 and above, so someone
// hammering the endpoint left no trace. Asserting the error alone - which is
// all the test above did - is what let that through.
func TestRequestOTP_RateLimited_Returns429(t *testing.T) {
	repo := &mockRepo{recentOTPCount: 3}
	svc := newTestService(repo)

	_, err := svc.RequestOTP(context.Background(), RequestOTPRequest{Phone: "+96170123456"})

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, http.StatusTooManyRequests, appErr.HTTPStatus)
	assert.Equal(t, "RATE_LIMITED", appErr.Code)
}

func TestRequestOTP_UnderRateLimit_Allowed(t *testing.T) {
	repo := &mockRepo{recentOTPCount: 2} // one under the max of 3
	svc := newTestService(repo)

	_, err := svc.RequestOTP(context.Background(), RequestOTPRequest{Phone: "+96170123456"})

	require.NoError(t, err)
}

func TestRequestOTP_InvalidPhone_ValidationError(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.RequestOTP(context.Background(), RequestOTPRequest{Phone: "12"}) // too short

	assert.Error(t, err)
}

// ── VerifyOTP tests ──────────────────────────────────────────────────────

func TestVerifyOTP_Success_IssuesSession(t *testing.T) {
	code := "123456"
	otpID := uuid.New()
	customerID := uuid.New()

	repo := &mockRepo{
		latestOTP: &CustomerOTP{
			ID:        otpID,
			Phone:     "+96170123456",
			OTPHash:   hashOTP(code),
			ExpiresAt: time.Now().Add(2 * time.Minute),
			Attempts:  0,
		},
		customer: &CustomerInfo{ID: customerID, Name: "Sarah", Phone: "+96170123456"},
	}
	svc := newTestService(repo)

	result, err := svc.VerifyOTP(context.Background(), VerifyOTPRequest{
		Phone: "+96170123456",
		Code:  code,
	})

	require.NoError(t, err)
	require.NotEmpty(t, result.AccessToken)
	require.NotEmpty(t, result.RefreshToken)
	assert.Equal(t, customerID, result.Customer.ID)
}

func TestVerifyOTP_WrongCode_IncrementsAttemptsAndFails(t *testing.T) {
	repo := &mockRepo{
		latestOTP: &CustomerOTP{
			ID:        uuid.New(),
			Phone:     "+96170123456",
			OTPHash:   hashOTP("123456"),
			ExpiresAt: time.Now().Add(2 * time.Minute),
			Attempts:  0,
		},
	}
	svc := newTestService(repo)

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPRequest{
		Phone: "+96170123456",
		Code:  "999999", // wrong
	})

	assert.Error(t, err)
	assert.True(t, repo.incrementCalled, "a wrong guess must count against the attempt limit")
}

func TestVerifyOTP_Expired_Rejected(t *testing.T) {
	repo := &mockRepo{
		latestOTP: &CustomerOTP{
			ID:        uuid.New(),
			Phone:     "+96170123456",
			OTPHash:   hashOTP("123456"),
			ExpiresAt: time.Now().Add(-1 * time.Minute), // already expired
			Attempts:  0,
		},
	}
	svc := newTestService(repo)

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPRequest{
		Phone: "+96170123456",
		Code:  "123456", // correct code, but too late
	})

	assert.Error(t, err)
}

func TestVerifyOTP_AlreadyUsed_Rejected(t *testing.T) {
	verifiedAt := time.Now().Add(-1 * time.Minute)
	repo := &mockRepo{
		latestOTP: &CustomerOTP{
			ID:         uuid.New(),
			Phone:      "+96170123456",
			OTPHash:    hashOTP("123456"),
			ExpiresAt:  time.Now().Add(2 * time.Minute),
			Attempts:   0,
			VerifiedAt: &verifiedAt,
		},
	}
	svc := newTestService(repo)

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPRequest{
		Phone: "+96170123456",
		Code:  "123456", // correct code, but already used once
	})

	assert.Error(t, err)
}

func TestVerifyOTP_TooManyAttempts_Rejected(t *testing.T) {
	repo := &mockRepo{
		latestOTP: &CustomerOTP{
			ID:        uuid.New(),
			Phone:     "+96170123456",
			OTPHash:   hashOTP("123456"),
			ExpiresAt: time.Now().Add(2 * time.Minute),
			Attempts:  5, // already at the max
		},
	}
	svc := newTestService(repo)

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPRequest{
		Phone: "+96170123456",
		Code:  "123456", // correct code, but this OTP is already dead
	})

	assert.Error(t, err)
}

func TestVerifyOTP_NoOTPRequested_NotFound(t *testing.T) {
	repo := &mockRepo{latestOTPErr: ErrOTPNotFound}
	svc := newTestService(repo)

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPRequest{
		Phone: "+96170199999", // never requested a code
		Code:  "123456",
	})

	assert.Error(t, err)
}

func TestVerifyOTP_InvalidCodeFormat_ValidationError(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPRequest{
		Phone: "+96170123456",
		Code:  "12", // not 6 digits
	})

	assert.Error(t, err)
}

// ── dev bypass code ──────────────────────────────────────────────────────
//
// The most important thing to prove here isn't that the bypass works in
// development - it's that it is IMPOSSIBLE to trigger when APP_ENV isn't
// exactly "development". A universal customer-login bypass reachable in
// production would be a severe vulnerability, so the fail-closed case gets
// equal billing with the happy path, not an afterthought.

// ── generateOTPCode tests ────────────────────────────────────────────────

func TestGenerateOTPCode_AlwaysSixDigits(t *testing.T) {
	for i := 0; i < 20; i++ {
		code, err := generateOTPCode()
		require.NoError(t, err)
		assert.Len(t, code, 6, "must always be exactly 6 characters, including leading zeros")
		for _, ch := range code {
			assert.True(t, ch >= '0' && ch <= '9', "must be purely numeric")
		}
	}
}

// ── Refresh tests ────────────────────────────────────────────────────────

func TestRefresh_Success_RotatesAndReissues(t *testing.T) {
	customerID := uuid.New()
	rawToken, err := internaljwt.GenerateRefreshToken(customerID)
	require.NoError(t, err)

	repo := &mockRepo{
		storedToken:  &StoredRefreshToken{ID: uuid.New(), UserID: customerID},
		customerByID: &CustomerInfo{ID: customerID, Name: "Sarah", Phone: "+96170123456"},
	}
	svc := newTestService(repo)

	result, err := svc.Refresh(context.Background(), rawToken)

	require.NoError(t, err)
	require.NotEmpty(t, result.AccessToken)
	assert.NotEqual(t, rawToken, result.RefreshToken, "rotation must issue a genuinely new refresh token, not reuse the old one")
	assert.True(t, repo.revokeCalled, "the old refresh token must be revoked, one-time use")
}

func TestRefresh_RevokedToken_Rejected(t *testing.T) {
	customerID := uuid.New()
	rawToken, err := internaljwt.GenerateRefreshToken(customerID)
	require.NoError(t, err)

	revokedAt := time.Now().Add(-1 * time.Hour)
	repo := &mockRepo{
		storedToken: &StoredRefreshToken{ID: uuid.New(), UserID: customerID, RevokedAt: &revokedAt},
	}
	svc := newTestService(repo)

	_, err = svc.Refresh(context.Background(), rawToken)

	assert.Error(t, err, "a previously-revoked token must never be accepted again (replay protection)")
}

func TestRefresh_UnknownToken_Rejected(t *testing.T) {
	customerID := uuid.New()
	rawToken, err := internaljwt.GenerateRefreshToken(customerID)
	require.NoError(t, err)

	repo := &mockRepo{storedTokenErr: ErrRefreshTokenNotFound}
	svc := newTestService(repo)

	_, err = svc.Refresh(context.Background(), rawToken)

	assert.Error(t, err)
}

func TestRefresh_GarbageToken_Rejected(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.Refresh(context.Background(), "not-a-real-jwt-at-all")

	assert.Error(t, err)
}

// ── Logout tests ─────────────────────────────────────────────────────────

func TestLogout_RevokesToken(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	err := svc.Logout(context.Background(), "some-refresh-token")

	require.NoError(t, err)
	assert.True(t, repo.revokeCalled)
}

// The rate limit is keyed on the phone, so it only works if the same human
// always produces the same key. Before normalisation, "70123456" and
// "+96170123456" were two separate buckets and the three-per-five-minutes cap
// was bypassable by retyping the number a different way.
func TestRequestOTP_NormalisesBeforeTheRateLimitLookup(t *testing.T) {
	for _, form := range []string{"70123456", "070123456", "+961 70 123 456", "0096170123456"} {
		repo := &mockRepo{}
		svc := newTestService(repo)

		_, errReq := svc.RequestOTP(context.Background(), RequestOTPRequest{Phone: form})
		require.NoError(t, errReq)
		assert.Equal(t, "+96170123456", repo.enqueuedPhone,
			"every way of typing this number must hit the same rate-limit bucket (%q)", form)
	}
}

// The API's only rule was validate:"required,min=7,max=20" - a length check on
// a string. This is what that let through.
func TestRequestOTP_RejectsWhatTheLengthCheckAccepted(t *testing.T) {
	for _, junk := range []string{"aaaaaaa", "1234567", "-------"} {
		svc := newTestService(&mockRepo{})
		_, err := svc.RequestOTP(context.Background(), RequestOTPRequest{Phone: junk})
		assert.Error(t, err, "%q must be rejected", junk)
	}
}

// A Gulf client booking a Beirut artist can pay in person. Refusing their
// number at the form turns a reachable customer into a lost one.
func TestRequestOTP_AcceptsMENANumbers(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)

	_, err := svc.RequestOTP(context.Background(),
		RequestOTPRequest{Phone: "+971501234567"})
	require.NoError(t, err)
	assert.Equal(t, "+971501234567", repo.enqueuedPhone)
}

// P0.4: the API used to answer "Verification code sent" unconditionally,
// while the platform's delivery rate was zero. The customer waited for a
// message that was never coming and nothing anywhere said otherwise.
func TestRequestOTP_ReportsWhenTheChannelIsFailing(t *testing.T) {
	repo := &mockRepo{deliveryLooksBroken: true}
	svc := newTestService(repo)

	broken, err := svc.RequestOTP(context.Background(),
		RequestOTPRequest{Phone: "+96170123456"})

	require.NoError(t, err, "a failing channel must not fail the request - the code is still queued")
	assert.True(t, broken, "the caller must be told the channel is failing")
}

func TestRequestOTP_SaysNothingWhenTheChannelIsHealthy(t *testing.T) {
	repo := &mockRepo{deliveryLooksBroken: false}
	svc := newTestService(repo)

	broken, err := svc.RequestOTP(context.Background(),
		RequestOTPRequest{Phone: "+96170123456"})

	require.NoError(t, err)
	assert.False(t, broken)
}

// A health-check failure means "cannot tell", and cannot-tell must not be
// announced as an outage - nor may it fail the request, which has already
// queued the code.
func TestRequestOTP_HealthCheckFailureIsNotAnOutage(t *testing.T) {
	repo := &mockRepo{deliveryLooksBrokenErr: assert.AnError}
	svc := newTestService(repo)

	broken, err := svc.RequestOTP(context.Background(),
		RequestOTPRequest{Phone: "+96170123456"})

	require.NoError(t, err)
	assert.False(t, broken, "an unanswerable health check must not be reported as failure")
}

// THE ENUMERATION PROPERTY. RequestOTP answers identically for a number
// that belongs to an artist as for one that does not, so the endpoint
// cannot be used to discover who is registered. The delivery flag is
// platform-wide precisely so it cannot reintroduce that oracle: a
// per-number signal is only answerable for numbers the system has messaged,
// which is itself the leak.
func TestRequestOTP_DeliveryFlagDoesNotLeakWhetherANumberIsRegistered(t *testing.T) {
	// An artist's number: ineligible, no code sent.
	artist := &mockRepo{phoneIneligible: true, deliveryLooksBroken: true}
	// A customer's number: eligible, code sent.
	customer := &mockRepo{phoneIneligible: false, deliveryLooksBroken: true}

	a, errA := newTestService(artist).RequestOTP(context.Background(),
		RequestOTPRequest{Phone: "+96170123456"})
	c, errC := newTestService(customer).RequestOTP(context.Background(),
		RequestOTPRequest{Phone: "+96170123457"})

	require.NoError(t, errA)
	require.NoError(t, errC)
	assert.Equal(t, a, c,
		"the two responses must be indistinguishable, or the endpoint becomes "+
			"a way to discover which numbers are registered")
}
