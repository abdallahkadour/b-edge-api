package booking

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// A guest releasing her own hold is a guest path, so like the two guest
// submit paths it is tested here rather than as a column of the state matrix
// (statematrix_test.go covers the artist's actions).

func TestReleaseGuestHold_UnsubmittedHold_FreesTheSlotAndTellsTheWaitlist(t *testing.T) {
	// She went back from the last funnel screen to choose another time. Her
	// hold must end now rather than ten minutes from now, and whoever waits
	// for that slot must hear about it exactly as they would from the timer.
	slot := FreedSlot{ArtistID: uuid.New(), StoreID: uuid.New(), ServiceID: uuid.New(),
		StartTime: time.Now().Add(48 * time.Hour)}
	repo := &mockRepo{releaseGuestHoldFreed: []FreedSlot{slot}}
	id := uuid.New()

	err := newTestService(repo).ReleaseGuestHold(context.Background(), id)

	require.NoError(t, err)
	assert.Equal(t, id, repo.releaseGuestHoldID, "the hold she named is the one released")
	assert.Equal(t, 1, repo.notifyNextWaitlistCount, "the waitlist is told the slot opened")
	assert.Equal(t, slot.ArtistID, repo.notifyNextWaitlistArtistID)
}

func TestReleaseGuestHold_NothingReleased_NotFound(t *testing.T) {
	// An unknown id, a hold that already ended, and a booking she has
	// SUBMITTED are all released by nobody - the SQL guard matches none of
	// them - and all answer the same 404, so this public route cannot be
	// used to learn whether a booking exists.
	repo := &mockRepo{}

	err := newTestService(repo).ReleaseGuestHold(context.Background(), uuid.New())

	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "BOOKING_NOT_FOUND", appErr.Code)
	assert.False(t, repo.notifyNextWaitlistCalled, "nothing freed, nobody told")
}

func TestReleaseGuestHold_DatabaseError_IsNotA404(t *testing.T) {
	// Breaks if every error is mapped to "not found": a database outage
	// would then tell the guest her hold does not exist.
	repo := &mockRepo{releaseGuestHoldErr: errors.New("connection reset")}

	err := newTestService(repo).ReleaseGuestHold(context.Background(), uuid.New())

	require.Error(t, err)
	var appErr *apperror.AppError
	assert.False(t, errors.As(err, &appErr), "a database error must surface as an internal error, got %v", err)
}
