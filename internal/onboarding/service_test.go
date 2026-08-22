// Package onboarding tests. In-memory mock repository, no database needed -
// matching every other domain in this codebase.
package onboarding

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockRepo struct {
	completeArtistID uuid.UUID
	completeErr      error
	completeCalled   bool

	statusResult *OnboardingStatus
	statusErr    error
}

func (m *mockRepo) Complete(_ context.Context, _ uuid.UUID, _ CompleteOnboardingRequest) (uuid.UUID, error) {
	m.completeCalled = true
	return m.completeArtistID, m.completeErr
}

func (m *mockRepo) GetStatus(_ context.Context, _ uuid.UUID) (*OnboardingStatus, error) {
	return m.statusResult, m.statusErr
}

func newTestService(repo Repository) *Service { return NewService(repo) }

// validRequest is a complete, passing submission - individual tests copy
// and mutate it to isolate the one field they're testing.
func validRequest() CompleteOnboardingRequest {
	return CompleteOnboardingRequest{
		Handle:             "sarah-beauty",
		Category:           "makeup",
		SalonName:          "Sarah Beauty Studio",
		StoreName:          "Downtown",
		City:               "Beirut",
		ServiceName:        "Bridal Makeup",
		ServiceDurationMin: 90,
		ServicePrice:       "150.00",
	}
}

// ── Complete: success ────────────────────────────────────────────────────────

func TestComplete_ValidRequest_Success(t *testing.T) {
	artistID := uuid.New()
	repo := &mockRepo{completeArtistID: artistID}
	svc := newTestService(repo)

	result, err := svc.Complete(context.Background(), uuid.New(), validRequest())

	require.NoError(t, err)
	assert.Equal(t, artistID, result.ArtistID)
	assert.Equal(t, "pending", result.Status, "a new submission must never start anywhere but pending")
	assert.True(t, repo.completeCalled)
}

// ── Complete: the two real backend errors ───────────────────────────────────

func TestComplete_AlreadyOnboarded_Conflict(t *testing.T) {
	repo := &mockRepo{completeErr: ErrAlreadyOnboarded}
	svc := newTestService(repo)

	_, err := svc.Complete(context.Background(), uuid.New(), validRequest())

	require.Error(t, err, "a second submission from the same account must be rejected")
}

func TestComplete_HandleTaken_Conflict(t *testing.T) {
	repo := &mockRepo{completeErr: ErrHandleTaken}
	svc := newTestService(repo)

	_, err := svc.Complete(context.Background(), uuid.New(), validRequest())

	require.Error(t, err)
}

func TestComplete_UnexpectedRepoError_Propagates(t *testing.T) {
	repo := &mockRepo{completeErr: errors.New("db down")}
	svc := newTestService(repo)

	_, err := svc.Complete(context.Background(), uuid.New(), validRequest())

	require.Error(t, err)
}

// ── Complete: validation (the checks that mirror database CHECK constraints) ─

func TestComplete_InvalidHandle_Rejected(t *testing.T) {
	// Uppercase, spaces, and a leading hyphen all violate the same regex
	// the database enforces (migration 012) - each must be rejected here,
	// as a clean 400, before ever reaching Postgres.
	cases := []string{"Sarah-Beauty", "sarah beauty", "-sarah", "sarah-", "ab"}
	repo := &mockRepo{}
	svc := newTestService(repo)

	for _, handle := range cases {
		req := validRequest()
		req.Handle = handle

		_, err := svc.Complete(context.Background(), uuid.New(), req)

		require.Error(t, err, "handle %q must be rejected", handle)
		assert.False(t, repo.completeCalled, "an invalid handle must never reach the repository")
		repo.completeCalled = false
	}
}

func TestComplete_InvalidCategory_Rejected(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)
	req := validRequest()
	req.Category = "photography" // not one of the five real categories

	_, err := svc.Complete(context.Background(), uuid.New(), req)

	require.Error(t, err)
	assert.False(t, repo.completeCalled)
}

func TestComplete_MissingRequiredField_Rejected(t *testing.T) {
	repo := &mockRepo{}
	svc := newTestService(repo)
	req := validRequest()
	req.SalonName = ""

	_, err := svc.Complete(context.Background(), uuid.New(), req)

	require.Error(t, err)
	assert.False(t, repo.completeCalled)
}

func TestComplete_ServiceDurationOutOfRange_Rejected(t *testing.T) {
	// Mirrors the database CHECK on services.duration_min (15-480) exactly
	// - an onboarding-specific bug here would otherwise surface as a raw
	// Postgres constraint violation instead of a clean field error.
	repo := &mockRepo{}
	svc := newTestService(repo)
	req := validRequest()
	req.ServiceDurationMin = 5

	_, err := svc.Complete(context.Background(), uuid.New(), req)

	require.Error(t, err)
	assert.False(t, repo.completeCalled)
}

// ── GetStatus ────────────────────────────────────────────────────────────────

func TestGetStatus_NotOnboarded_ReturnsNotFound(t *testing.T) {
	repo := &mockRepo{statusErr: ErrNotOnboarded}
	svc := newTestService(repo)

	_, err := svc.GetStatus(context.Background(), uuid.New())

	require.Error(t, err, "an account that never onboarded must not resolve to a status")
}

func TestGetStatus_Pending_ReturnsPending(t *testing.T) {
	repo := &mockRepo{statusResult: &OnboardingStatus{Status: "pending"}}
	svc := newTestService(repo)

	result, err := svc.GetStatus(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "pending", result.Status)
}

func TestGetStatus_Active_ReturnsActive(t *testing.T) {
	repo := &mockRepo{statusResult: &OnboardingStatus{Status: "active"}}
	svc := newTestService(repo)

	result, err := svc.GetStatus(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "active", result.Status)
}
