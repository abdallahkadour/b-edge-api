package booking

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Rescheduling exists to remove a trap: before it, a client who needed a
// different time had to cancel and rebook, losing the slot and - inside 24
// hours - the deposit. These tests pin the rules that keep it from becoming a
// different trap.

func rescheduleFixture(status string, count int) *Booking {
	start := time.Now().UTC().Add(72 * time.Hour).Truncate(time.Hour)
	return &Booking{
		ID:              uuid.New(),
		ArtistID:        uuid.New(),
		StoreID:         uuid.New(),
		ServiceID:       uuid.New(),
		CustomerID:      uuid.New(),
		Status:          status,
		StartTime:       start,
		EndTime:         start.Add(time.Hour),
		BlockedUntil:    start.Add(90 * time.Minute),
		RescheduleCount: count,
	}
}

func TestReschedule_ForeignBookingReadsAsNotFound(t *testing.T) {
	b := rescheduleFixture(StatusConfirmed, 0)
	repo := &mockRepo{getBookingByIDBooking: b}
	svc := newTestService(repo)

	// A different user entirely.
	_, err := svc.RescheduleBooking(context.Background(), b.ID, uuid.New(),
		RescheduleBookingRequest{StartTime: time.Now().Add(96 * time.Hour).Format(time.RFC3339)})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found",
		"someone else's booking must be indistinguishable from a missing one")
	assert.Zero(t, repo.rescheduleCalls)
}

// A slot moved repeatedly is a slot nobody else can book, with no deposit ever
// at risk. Two is enough for the honest case and short of a pattern.
func TestReschedule_RefusesPastTheLimit(t *testing.T) {
	b := rescheduleFixture(StatusConfirmed, maxReschedules)
	repo := &mockRepo{getBookingByIDBooking: b}
	svc := newTestService(repo)

	_, err := svc.RescheduleBooking(context.Background(), b.ID, b.CustomerID,
		RescheduleBookingRequest{StartTime: time.Now().Add(96 * time.Hour).Format(time.RFC3339)})

	require.Error(t, err)
	assert.Zero(t, repo.rescheduleCalls, "the limit must be refused before any write")
}

// HELD is an unconfirmed guest hold that expires on its own. Moving one would
// extend a reservation nobody has committed to.
func TestReschedule_RefusesANonMovableStatus(t *testing.T) {
	for _, status := range []string{StatusHeld, StatusCancelled, StatusCompleted, StatusNoShow} {
		b := rescheduleFixture(status, 0)
		repo := &mockRepo{getBookingByIDBooking: b}
		svc := newTestService(repo)

		_, err := svc.RescheduleBooking(context.Background(), b.ID, b.CustomerID,
			RescheduleBookingRequest{StartTime: time.Now().Add(96 * time.Hour).Format(time.RFC3339)})

		require.Error(t, err, "status %s must not be movable", status)
		assert.Zero(t, repo.rescheduleCalls)
	}
}

func TestReschedule_RefusesATimeInThePast(t *testing.T) {
	b := rescheduleFixture(StatusConfirmed, 0)
	repo := &mockRepo{getBookingByIDBooking: b}
	svc := newTestService(repo)

	_, err := svc.RescheduleBooking(context.Background(), b.ID, b.CustomerID,
		RescheduleBookingRequest{StartTime: time.Now().Add(-time.Hour).Format(time.RFC3339)})

	require.Error(t, err)
	assert.Zero(t, repo.rescheduleCalls)
}

func TestReschedule_RefusesAMalformedTime(t *testing.T) {
	b := rescheduleFixture(StatusConfirmed, 0)
	svc := newTestService(&mockRepo{getBookingByIDBooking: b})

	_, err := svc.RescheduleBooking(context.Background(), b.ID, b.CustomerID,
		RescheduleBookingRequest{StartTime: "next tuesday"})

	assert.Error(t, err)
}

// ── The rules that are easy to get wrong ──────────────────────────────────────

// Exact equality against the generated grid, not a window. Accepting "close
// enough" is how an appointment ends up starting at 10:03.
func TestSlotOffered_RequiresAnExactMatch(t *testing.T) {
	start := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	slots := []*TimeSlot{{StartTime: start}}

	assert.True(t, slotOffered(slots, start))
	assert.False(t, slotOffered(slots, start.Add(time.Minute)))
	assert.False(t, slotOffered(slots, start.Add(-time.Minute)))
	assert.False(t, slotOffered(nil, start))
}

// The same instant expressed in another zone is the same slot. Comparing
// wall-clock rather than instants would refuse a legitimate move from a client
// whose device reports +03:00.
func TestSlotOffered_ComparesInstantsNotZones(t *testing.T) {
	utc := time.Date(2026, 10, 1, 10, 0, 0, 0, time.UTC)
	beirut := utc.In(time.FixedZone("EEST", 3*60*60))

	assert.True(t, slotOffered([]*TimeSlot{{StartTime: utc}}, beirut))
}

func TestIsMovableStatus(t *testing.T) {
	for _, s := range []string{StatusPending, StatusApproved, StatusDepositPaid, StatusConfirmed} {
		assert.True(t, isMovableStatus(s), s)
	}
	for _, s := range []string{StatusHeld, StatusCancelled, StatusCompleted, StatusNoShow, ""} {
		assert.False(t, isMovableStatus(s), s)
	}
}

func TestMaxReschedules_IsBoundedAndSmall(t *testing.T) {
	assert.Positive(t, maxReschedules)
	assert.LessOrEqual(t, maxReschedules, 3,
		"a high cap makes a held-and-moved slot indistinguishable from an unlimited one")
}
