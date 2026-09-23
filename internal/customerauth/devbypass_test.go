//go:build devbypass

package customerauth

// The dev OTP bypass, exercised in the build that actually contains it.
//
// These moved out of service_test.go when the bypass became
// internal/pkg/devbypass behind a build tag. They cannot live in the untagged
// file any more, and that is the point: in a production build there is no
// bypass constant to reference, because there is no bypass.
//
// The production-build counterpart is
// internal/pkg/devbypass/devbypass_disabled_test.go, which asserts that no
// code and no APP_ENV value opens this gate at all.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/devbypass"
)

func TestVerifyOTP_DevBypassCode_OutsideDevelopment_TreatedAsAnOrdinaryWrongCode(t *testing.T) {
	cases := []string{"production", "staging", ""}
	for _, env := range cases {
		t.Setenv("APP_ENV", env)

		repo := &mockRepo{latestOTPErr: ErrOTPNotFound}
		svc := newTestService(repo)

		_, err := svc.VerifyOTP(context.Background(), VerifyOTPRequest{
			Phone: "+96170199999", // never requested a real code
			Code:  devbypass.Code,
		})

		require.Error(t, err, "APP_ENV=%q must not activate the bypass", env)
		assert.False(t, repo.findOrCreateCalled, "APP_ENV=%q must not reach customer resolution", env)
	}
}

func TestVerifyOTP_DevBypassCode_DoesNotWeakenRealCodeChecking(t *testing.T) {
	// Even in development, a WRONG code that happens to differ from the
	// bypass must still fail normally - the bypass is one specific extra
	// value accepted, not a general relaxation of verification.
	t.Setenv("APP_ENV", "development")

	otpID := uuid.New()
	repo := &mockRepo{
		latestOTP: &CustomerOTP{
			ID:        otpID,
			Phone:     "+96170123456",
			OTPHash:   hashOTP("654321"),
			ExpiresAt: time.Now().Add(2 * time.Minute),
		},
	}
	svc := newTestService(repo)

	_, err := svc.VerifyOTP(context.Background(), VerifyOTPRequest{
		Phone: "+96170123456",
		Code:  "111111", // neither the real code nor the bypass code
	})

	require.Error(t, err)
	assert.True(t, repo.incrementCalled, "a genuinely wrong code must still count against the attempt limit")
}

func TestVerifyOTP_DevBypassCode_InDevelopment_IssuesSessionWithNoOTPRecord(t *testing.T) {
	t.Setenv("APP_ENV", "development")

	customerID := uuid.New()
	repo := &mockRepo{
		// Deliberately no latestOTP set, and latestOTPErr left nil rather
		// than ErrOTPNotFound - if the bypass ever fell through to the real
		// lookup, GetLatestOTP would be called and this test's mock would
		// return a zero-value CustomerOTP instead of erroring, masking the
		// bug. Asserting findOrCreateCalled below is the real proof.
		customer: &CustomerInfo{ID: customerID, Name: "Bypass Test", Phone: "+96170123456"},
	}
	svc := newTestService(repo)

	result, err := svc.VerifyOTP(context.Background(), VerifyOTPRequest{
		Phone: "+96170123456",
		Code:  devbypass.Code,
	})

	require.NoError(t, err)
	assert.True(t, repo.findOrCreateCalled, "bypass must still issue a real session via the normal customer-resolution path")
	assert.NotEmpty(t, result.AccessToken)
	assert.NotEmpty(t, result.RefreshToken)
	assert.Equal(t, customerID, result.Customer.ID)
}
