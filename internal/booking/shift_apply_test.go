package booking

// Tests for applying a whole-day shift.
//
// The evaluation itself is covered by shift_test.go against the pure
// evaluateShift. These cover the write: what gets refused, what gets moved,
// and the two columns that must travel with the times.

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

func shiftReq(mins int) ShiftPreviewRequest {
	m := mins
	return ShiftPreviewRequest{
		StoreID:      uuid.New().String(),
		Date:         "2027-03-01",
		ShiftMinutes: &m,
	}
}

// shiftRepo builds a day with one movable booking inside trading hours.
func shiftRepo(t *testing.T) (*mockRepo, uuid.UUID) {
	t.Helper()
	artistID := uuid.New()
	storeID := uuid.New()
	start := beirutTime(2027, time.March, 1, 11, 0)
	return &mockRepo{
		getArtistIDByUserIDArtistID: artistID,
		getStoreStore:               defaultStore(),
		getBusinessHoursBH:          &BusinessHours{IsOpen: true, OpenTime: "09:00:00", CloseTime: "18:00:00"},
		enrichedForDay: []*EnrichedBooking{{
			Booking: Booking{
				ID: uuid.New(), ArtistID: artistID, StoreID: storeID,
				Status: StatusConfirmed, StartTime: start, EndTime: start.Add(time.Hour),
			},
			CustomerName:  "Layla",
			CustomerPhone: strPtrB("+96170555444"),
			ServiceName:   "Bridal makeup",
		}},
	}, storeID
}

// The happy path. The shift is applied and the exact booking IDs the plan
// listed are the ones handed to the repository.
func TestShiftDay_AppliesTheMovableSet(t *testing.T) {
	repo, storeID := shiftRepo(t)
	req := shiftReq(30)
	req.StoreID = storeID.String()
	repo.enrichedForDay[0].StoreID = storeID
	svc := newTestService(repo)

	plan, err := svc.ShiftDay(context.Background(), uuid.New(), req)

	require.NoError(t, err)
	require.True(t, plan.CanApply)
	require.Len(t, repo.shiftedIDs, len(plan.Movable))
	assert.Equal(t, plan.Movable[0].BookingID, repo.shiftedIDs[0])
	assert.Equal(t, 30, repo.shiftedMinutes)
}

// All-or-nothing. A blocked day must write NOTHING - a partial shift leaves
// an arrangement the artist never asked for.
func TestShiftDay_BlockedDayWritesNothing(t *testing.T) {
	repo, storeID := shiftRepo(t)
	// -240 moves the 11:00 booking to 07:00, before the 09:00 open. Within
	// the field's valid range (max 240), so it reaches the blocker logic
	// rather than being rejected as validation.
	req := shiftReq(-240)
	req.StoreID = storeID.String()
	repo.enrichedForDay[0].StoreID = storeID
	svc := newTestService(repo)

	plan, err := svc.ShiftDay(context.Background(), uuid.New(), req)

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "SHIFT_NOT_APPLICABLE", appErr.Code)
	assert.Nil(t, repo.shiftedIDs, "nothing may be written when anything blocks")
	require.NotNil(t, plan, "the refusal still carries the plan so the caller sees WHY")
	assert.NotEmpty(t, plan.Blockers)
}

// The constraint disagreeing with the evaluation means the day changed
// between resolving and writing. That is a conflict to retry, not a 500.
func TestShiftDay_ConstraintViolationIsAConflict(t *testing.T) {
	repo, storeID := shiftRepo(t)
	req := shiftReq(30)
	req.StoreID = storeID.String()
	repo.enrichedForDay[0].StoreID = storeID
	repo.shiftBookingsErr = ErrSlotUnavailable
	svc := newTestService(repo)

	_, err := svc.ShiftDay(context.Background(), uuid.New(), req)

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "SHIFT_CONFLICT", appErr.Code)
}

// A moved customer is told in WORDS. The calendar link cannot carry the
// change on its own: someone who added the appointment and never opens the
// updated link keeps the old time.
func TestShiftDay_NotifiesMovedCustomersWithTheNewTime(t *testing.T) {
	repo, storeID := shiftRepo(t)
	req := shiftReq(30)
	req.StoreID = storeID.String()
	repo.enrichedForDay[0].StoreID = storeID
	svc := newTestService(repo)

	_, err := svc.ShiftDay(context.Background(), uuid.New(), req)

	require.NoError(t, err)
	require.Len(t, repo.enqueuedNotifications, 1)
	n := repo.enqueuedNotifications[0]
	assert.Equal(t, "booking_rescheduled", n.TemplateName)
	assert.Contains(t, n.Message, "Layla")
	assert.Contains(t, n.Message, "moved to")
}

// No phone means no message, and it must not be an error - the preview
// already surfaced that this customer needs a call.
func TestShiftDay_CustomerWithoutAPhoneIsSkippedNotFailed(t *testing.T) {
	repo, storeID := shiftRepo(t)
	req := shiftReq(30)
	req.StoreID = storeID.String()
	repo.enrichedForDay[0].StoreID = storeID
	repo.enrichedForDay[0].CustomerPhone = nil
	svc := newTestService(repo)

	_, err := svc.ShiftDay(context.Background(), uuid.New(), req)

	require.NoError(t, err, "the shift still applied")
	assert.Empty(t, repo.enqueuedNotifications)
}

// A notification failure must not undo a committed shift.
func TestShiftDay_NotificationFailureDoesNotUndoTheShift(t *testing.T) {
	repo, storeID := shiftRepo(t)
	req := shiftReq(30)
	req.StoreID = storeID.String()
	repo.enrichedForDay[0].StoreID = storeID
	repo.enqueueNotificationErr = errors.New("queue down")
	svc := newTestService(repo)

	_, err := svc.ShiftDay(context.Background(), uuid.New(), req)

	require.NoError(t, err)
	assert.NotNil(t, repo.shiftedIDs, "the bookings still moved")
}

// Zero is rejected with its own code rather than falling out of `required`,
// whose message would contradict the min/max printed on the same field.
func TestShiftDay_ZeroMinutesRejected(t *testing.T) {
	repo, _ := shiftRepo(t)
	req := shiftReq(0)
	svc := newTestService(repo)

	_, err := svc.ShiftDay(context.Background(), uuid.New(), req)

	require.Error(t, err)
	var appErr *apperror.AppError
	require.ErrorAs(t, err, &appErr)
	assert.Equal(t, "SHIFT_MINUTES_ZERO", appErr.Code)
}
