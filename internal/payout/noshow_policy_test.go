package payout

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// The owner's setting for the no-show deposit rule (decision D29): after how
// many missed appointments at her salon a customer pays a deposit. 0 is off.

func intp(v int) *int { return &v }

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr), "want an AppError, got %v", err)
	return appErr.Code
}

func TestSetNoShowPolicy_ZeroTurnsItOff(t *testing.T) {
	repo := &mockRepo{}
	salon := uuid.New()

	out, err := newTestService(repo).SetNoShowPolicy(context.Background(), salon, SetNoShowPolicyRequest{After: intp(0)})

	require.NoError(t, err)
	assert.Equal(t, 0, out.After)
	assert.Equal(t, salon, repo.noShowSalon)
	assert.Equal(t, 0, repo.noShowAfter)
}

func TestSetNoShowPolicy_OutOfRange_Refused(t *testing.T) {
	for _, v := range []int{-1, 11} {
		repo := &mockRepo{}
		_, err := newTestService(repo).SetNoShowPolicy(context.Background(), uuid.New(), SetNoShowPolicyRequest{After: intp(v)})
		assert.Equal(t, "VALIDATION_ERROR", codeOf(t, err), "after=%d", v)
		assert.Zero(t, repo.noShowWrites, "nothing written for %d", v)
	}
}

func TestSetNoShowPolicy_Missing_Refused(t *testing.T) {
	repo := &mockRepo{}
	_, err := newTestService(repo).SetNoShowPolicy(context.Background(), uuid.New(), SetNoShowPolicyRequest{})
	assert.Equal(t, "VALIDATION_ERROR", codeOf(t, err), "an absent value must not silently become 0, which is 'off'")
	assert.Zero(t, repo.noShowWrites)
}

func TestNoShowPolicy_ReadsTheSalonsSetting(t *testing.T) {
	repo := &mockRepo{noShowAfter: 3}
	out, err := newTestService(repo).NoShowPolicy(context.Background(), uuid.New())
	require.NoError(t, err)
	assert.Equal(t, 3, out.After)
}
