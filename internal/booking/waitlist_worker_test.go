package booking

// Tests for the stalled-queue sweep.
//
// The worker's whole reason to exist is the one case lazy cascading cannot
// reach, so these cover the sweep rather than the ticker: the loop itself is
// the same shape as the notification worker's and is supervised identically.

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

type fakeSweepRepo struct {
	groups     []WaitlistGroup
	findErr    error
	cascaded   []WaitlistGroup
	cascadeErr map[uuid.UUID]error
	nowSeen    time.Time
}

func (f *fakeSweepRepo) FindStaleWaitlistGroups(_ context.Context, now time.Time) ([]WaitlistGroup, error) {
	f.nowSeen = now
	return f.groups, f.findErr
}

func (f *fakeSweepRepo) NotifyNextWaitlistEntry(_ context.Context, artistID, storeID, serviceID uuid.UUID, date time.Time) error {
	if err, ok := f.cascadeErr[artistID]; ok {
		return err
	}
	f.cascaded = append(f.cascaded, WaitlistGroup{
		ArtistID: artistID, StoreID: storeID, ServiceID: serviceID, Date: date,
	})
	return nil
}

func newSweepWorker(repo *fakeSweepRepo, now time.Time) *WaitlistWorker {
	return &WaitlistWorker{repo: repo, log: zap.NewNop(), now: func() time.Time { return now }}
}

func group() WaitlistGroup {
	return WaitlistGroup{
		ArtistID: uuid.New(), StoreID: uuid.New(), ServiceID: uuid.New(),
		Date: time.Date(2027, time.March, 1, 0, 0, 0, 0, time.UTC),
	}
}

// The happy path: every stalled queue gets handed to the same cascade the
// lazy path uses.
func TestWaitlistWorker_CascadesEveryStalledQueue(t *testing.T) {
	a, b := group(), group()
	repo := &fakeSweepRepo{groups: []WaitlistGroup{a, b}}

	newSweepWorker(repo, time.Now()).sweep(context.Background())

	require.Len(t, repo.cascaded, 2)
	assert.Equal(t, a.ArtistID, repo.cascaded[0].ArtistID)
	assert.Equal(t, b.ServiceID, repo.cascaded[1].ServiceID)
}

// Nothing stalled is the overwhelmingly common case - the sweep runs every
// five minutes forever - and must do no work at all.
func TestWaitlistWorker_NothingStalledDoesNothing(t *testing.T) {
	repo := &fakeSweepRepo{}

	newSweepWorker(repo, time.Now()).sweep(context.Background())

	assert.Empty(t, repo.cascaded)
}

// One bad group must not freeze every other stalled queue - which is exactly
// the failure this worker exists to prevent, so causing it here would be
// self-defeating.
func TestWaitlistWorker_OneFailureDoesNotStopTheRest(t *testing.T) {
	bad, good := group(), group()
	repo := &fakeSweepRepo{
		groups:     []WaitlistGroup{bad, good},
		cascadeErr: map[uuid.UUID]error{bad.ArtistID: errors.New("artist deleted")},
	}

	newSweepWorker(repo, time.Now()).sweep(context.Background())

	require.Len(t, repo.cascaded, 1, "the healthy queue must still be unstalled")
	assert.Equal(t, good.ArtistID, repo.cascaded[0].ArtistID)
}

// A lookup failure is logged and the sweep returns; the next tick retries.
// It must not panic or cascade a partial result.
func TestWaitlistWorker_FindFailureIsSurvivable(t *testing.T) {
	repo := &fakeSweepRepo{findErr: errors.New("db down")}

	newSweepWorker(repo, time.Now()).sweep(context.Background())

	assert.Empty(t, repo.cascaded)
}

// The clock is injected, not read from the wall, so the deadline comparison
// is testable and the sweep is deterministic.
func TestWaitlistWorker_UsesTheInjectedClock(t *testing.T) {
	fixed := time.Date(2027, time.March, 1, 12, 0, 0, 0, time.UTC)
	repo := &fakeSweepRepo{}

	newSweepWorker(repo, fixed).sweep(context.Background())

	assert.Equal(t, fixed, repo.nowSeen)
}

// The sweep interval must stay comfortably inside the confirm window, or a
// stalled queue waits longer than the window it was already waiting through.
func TestWaitlistWorker_SweepIntervalFitsInsideTheConfirmWindow(t *testing.T) {
	assert.Less(t, waitlistSweepInterval, waitlistConfirmWindow,
		"a sweep slower than the confirm window would let stalls outlive their own deadline")
}
