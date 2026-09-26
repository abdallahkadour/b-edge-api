package booking

// Tests for expiring abandoned holds and lapsed-deposit approvals.
//
// Until 2026-09-26 the only thing that expired them was the availability
// read path, and it called the REPOSITORY sweeps directly - so the slots it
// freed were thrown away and nobody on the waitlist was ever told. The
// service-level sweeps that do tell them existed, with comments saying a
// background job called them every minute; no job did. Both halves are
// pinned here: the read path cascades, and a worker runs the sweeps on a
// clock so a slot does not stay blocked until someone happens to look.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

func freedSlot() FreedSlot {
	return FreedSlot{ArtistID: uuid.New(), StoreID: uuid.New(), ServiceID: uuid.New(),
		StartTime: time.Now().Add(48 * time.Hour)}
}

func TestGetAvailableSlots_FreesAnExpiredHold_TellsTheWaitlist(t *testing.T) {
	repo := goldenRepo()
	repo.releaseExpiredHoldsFreed = []FreedSlot{freedSlot()}

	_, err := newTestService(repo).GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	assert.Equal(t, 1, repo.notifyNextWaitlistCount,
		"a hold the read path expires opens a slot; whoever waits for it must be told")
}

func TestGetAvailableSlots_ExpiresALapsedDeposit_TellsTheWaitlist(t *testing.T) {
	repo := goldenRepo()
	repo.expireDeadlineBookingsFreed = []FreedSlot{freedSlot()}

	_, err := newTestService(repo).GetAvailableSlots(context.Background(), goldenReq())

	require.NoError(t, err)
	assert.Equal(t, 1, repo.notifyNextWaitlistCount)
}

type fakeExpirySweeper struct {
	holdsCalls, deadlineCalls int
	holdsErr, deadlineErr     error
}

func (f *fakeExpirySweeper) ReleaseExpiredHolds(context.Context) (int64, error) {
	f.holdsCalls++
	return 0, f.holdsErr
}

func (f *fakeExpirySweeper) ExpireDeadlineBookings(context.Context) (int64, error) {
	f.deadlineCalls++
	return 0, f.deadlineErr
}

func TestExpiryWorker_SweepsHoldsAndLapsedDeposits(t *testing.T) {
	f := &fakeExpirySweeper{}
	w := &ExpiryWorker{svc: f, log: zap.NewNop()}

	w.sweep(context.Background())

	assert.Equal(t, 1, f.holdsCalls)
	assert.Equal(t, 1, f.deadlineCalls)
}

func TestExpiryWorker_OneSweepFailing_DoesNotStopTheOther(t *testing.T) {
	// A failed hold sweep must not leave lapsed-deposit approvals blocking
	// their slots too, and the reverse.
	f := &fakeExpirySweeper{holdsErr: errors.New("connection reset")}
	w := &ExpiryWorker{svc: f, log: zap.NewNop()}

	w.sweep(context.Background())

	assert.Equal(t, 1, f.deadlineCalls)
}

func TestExpiryWorker_IntervalIsShorterThanAHold(t *testing.T) {
	// A hold lasts SlotHoldDuration. Sweeping less often than that would let
	// an abandoned hold outlive its own window by more than its length.
	assert.Less(t, expirySweepInterval, SlotHoldDuration)
}
