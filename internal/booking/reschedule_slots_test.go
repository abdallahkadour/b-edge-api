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

// The times a customer is shown for moving her appointment must be exactly the
// times RescheduleBooking will accept (E2E 28.10, the reschedule screen,
// 2026-10-09). The public slots endpoint counts the booking being moved as
// occupied, so it hides every time that overlaps her own appointment - an hour
// later, say - which the move itself would allow. RescheduleSlots answers from
// the same guards and the same exclusion as the move.

func TestRescheduleSlots_SomeoneElsesBooking_ReadsAsNotFound(t *testing.T) {
	b := rescheduleFixture(StatusConfirmed, 0)
	svc := newTestService(&mockRepo{getBookingByIDBooking: b})

	_, err := svc.RescheduleSlots(context.Background(), b.ID, uuid.New(), b.StartTime.Format("2006-01-02"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestRescheduleSlots_PastTheLimit_SaysSoBeforeShowingTimes(t *testing.T) {
	b := rescheduleFixture(StatusConfirmed, maxReschedules)
	svc := newTestService(&mockRepo{getBookingByIDBooking: b})

	_, err := svc.RescheduleSlots(context.Background(), b.ID, b.CustomerID, b.StartTime.Format("2006-01-02"))

	assert.Equal(t, "RESCHEDULE_LIMIT", appErrCode(t, err))
}

func TestRescheduleSlots_NotMovable_SaysSo(t *testing.T) {
	b := rescheduleFixture(StatusCompleted, 0)
	svc := newTestService(&mockRepo{getBookingByIDBooking: b})

	_, err := svc.RescheduleSlots(context.Background(), b.ID, b.CustomerID, b.StartTime.Format("2006-01-02"))

	assert.Equal(t, "NOT_MOVABLE", appErrCode(t, err))
}

// The detail screen decides whether to offer "Change time" from these, so the
// rule lives once, beside the move it describes.
func enrichedWith(status string, count int, start time.Time) *EnrichedBookingResponse {
	return toEnrichedResponse(&EnrichedBooking{Booking: Booking{ID: uuid.New(), Status: status,
		StartTime: start, EndTime: start.Add(time.Hour), RescheduleCount: count}})
}

func TestEnrichedResponse_NeverMoved_CanBeMovedTwice(t *testing.T) {
	r := enrichedWith(StatusConfirmed, 0, time.Now().UTC().Add(48*time.Hour))
	assert.True(t, r.CanReschedule)
	assert.Equal(t, 2, r.ReschedulesLeft)
}

func TestEnrichedResponse_MovedTwice_CannotBeMovedAgain(t *testing.T) {
	r := enrichedWith(StatusConfirmed, maxReschedules, time.Now().UTC().Add(48*time.Hour))
	assert.False(t, r.CanReschedule)
	assert.Equal(t, 0, r.ReschedulesLeft)
}

func TestEnrichedResponse_AlreadyStarted_CannotBeMoved(t *testing.T) {
	r := enrichedWith(StatusConfirmed, 0, time.Now().UTC().Add(-2*time.Hour))
	assert.False(t, r.CanReschedule)
}

func TestEnrichedResponse_CancelledOrHeld_CannotBeMoved(t *testing.T) {
	future := time.Now().UTC().Add(48 * time.Hour)
	assert.False(t, enrichedWith(StatusCancelled, 0, future).CanReschedule)
	assert.False(t, enrichedWith(StatusHeld, 0, future).CanReschedule, "an unsubmitted hold is not hers to move")
}

func appErrCode(t *testing.T, err error) string {
	t.Helper()
	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr), "want an AppError, got %v", err)
	return appErr.Code
}
