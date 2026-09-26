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

// ── One network, at most 2 unfinished holds per artist (migration 053) ────

func TestHoldGuestSlot_NetworkAlreadyHoldsTheLimit_429(t *testing.T) {
	repo := &mockRepo{getServiceSvc: defaultService(), getStoreStore: defaultStore(),
		createGuestHoldErr: ErrTooManyHolds}
	req := holdReq()
	req.ClientIP = "203.0.113.7"

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), req)

	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "TOO_MANY_HOLDS", appErr.Code)
	assert.Equal(t, 429, appErr.HTTPStatus)
}

func TestHoldGuestSlot_LimitsByAKeyedHashOfTheAddress(t *testing.T) {
	// The limit is 2 per artist per network, and the network is recorded as
	// a keyed hash - a plain hash of an IPv4 address is reversible by trying
	// all four billion.
	hold := func(ip string) *mockRepo {
		repo := &mockRepo{getServiceSvc: defaultService(), getStoreStore: defaultStore()}
		req := holdReq()
		req.ClientIP = ip
		_, err := newTestService(repo).HoldGuestSlot(context.Background(), req)
		require.NoError(t, err)
		return repo
	}
	a1, a2, b := hold("203.0.113.7"), hold("203.0.113.7"), hold("198.51.100.9")

	assert.Equal(t, 2, a1.createGuestHoldMax)
	assert.Len(t, a1.createGuestHoldClient, 32)
	assert.NotContains(t, a1.createGuestHoldClient, "203.0.113.7", "never the address itself")
	assert.Equal(t, a1.createGuestHoldClient, a2.createGuestHoldClient, "one network, one key")
	assert.NotEqual(t, a1.createGuestHoldClient, b.createGuestHoldClient, "another network, another key")
}
