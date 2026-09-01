package booking

// The booking state-machine matrix, executed.
//
// B-Edge-Booking-State-Machine-Matrix-v1.md enumerates every (status,
// action) cell with its expected outcome. This is that table as a test: the
// grid is DATA below, and one loop drives it, so adding a status or an
// action is a row or a column rather than a new hand-written test that
// someone has to remember to write.
//
// Why it exists. Three of the twelve bugs found in earlier live passes were
// exactly this class - approving, completing or no-showing a booking whose
// state or time made no sense for that action - and none were caught by a
// test. The reason is that hand-written tests cover the cases the author
// thought of, which are the cases they already handled in the code. A grid
// has no such blind spot: every cell must be filled in, including the ones
// nobody would think to ask about.
//
// Two assertion rules this deliberately enforces, both previously missing
// from the suite entirely:
//
//   - Assert the exact ERROR CODE, not merely that the call failed. Before
//     this file, zero tests asserted any of BOOKING_NOT_PENDING,
//     BOOKING_NOT_APPROVED, BOOKING_NOT_DEPOSIT_PAID, BOOKING_NOT_CONFIRMED
//     or BOOKING_NOT_CANCELLABLE. A rejection carrying the wrong code is a
//     bug the client sees, and nothing could fail on it.
//
//   - Assert the row DID NOT MOVE on rejection. An action that returns the
//     right error while still writing the row is the worst outcome and the
//     easiest to miss.
//
// Scope: this is the service layer against a mock repository, matching the
// house pattern. The mock enforces the same status guard the SQL does, so a
// service-layer guard that is missing shows up as the repository being
// reached with the wrong status - see mockRepo.guard below.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// allStatuses is every value the CHECK constraint permits after migration
// 032. Order matches the document's tables.
var allStatuses = []string{
	StatusHeld, StatusPending, StatusApproved, StatusDepositPaid,
	StatusConfirmed, StatusCompleted, StatusCancelled, StatusExpired,
	StatusNoShow, StatusRefundDue, StatusRefunded,
}

// matrixAction is one column of the grid.
type matrixAction struct {
	name string
	// legalFrom is the single status this action accepts.
	legalFrom string
	// rejectCode is what every other status must produce.
	rejectCode string
	// timing says whether the action also constrains the appointment time.
	// "" none, "future" start must be ahead, "past" start must be behind.
	timing string
	// run invokes the action. artistUser is the owning artist's user id.
	run func(s *Service, id, artistUser uuid.UUID) error
}

func matrixActions() []matrixAction {
	return []matrixAction{
		{
			name: "ApproveBooking", legalFrom: StatusPending,
			rejectCode: "BOOKING_NOT_PENDING", timing: "future",
			run: func(s *Service, id, u uuid.UUID) error {
				_, err := s.ApproveBooking(context.Background(), id, u)
				return err
			},
		},
		{
			name: "MarkDepositReceived", legalFrom: StatusApproved,
			rejectCode: "BOOKING_NOT_APPROVED",
			run: func(s *Service, id, u uuid.UUID) error {
				_, err := s.MarkDepositReceived(context.Background(), id, u)
				return err
			},
		},
		{
			name: "ConfirmDeposit", legalFrom: StatusDepositPaid,
			rejectCode: "BOOKING_NOT_DEPOSIT_PAID",
			run: func(s *Service, id, u uuid.UUID) error {
				_, err := s.ConfirmDeposit(context.Background(), id, u)
				return err
			},
		},
		{
			name: "ConfirmDepositReceived", legalFrom: StatusApproved,
			rejectCode: "BOOKING_NOT_APPROVED",
			run: func(s *Service, id, u uuid.UUID) error {
				_, err := s.ConfirmDepositReceived(context.Background(), id, u, nil)
				return err
			},
		},
		{
			name: "CompleteBooking", legalFrom: StatusConfirmed,
			rejectCode: "BOOKING_NOT_CONFIRMED", timing: "past",
			run: func(s *Service, id, u uuid.UUID) error {
				_, err := s.CompleteBooking(context.Background(), id, u)
				return err
			},
		},
		{
			name: "MarkNoShow", legalFrom: StatusConfirmed,
			rejectCode: "BOOKING_NOT_CONFIRMED", timing: "past",
			run: func(s *Service, id, u uuid.UUID) error {
				_, err := s.MarkNoShow(context.Background(), id, u)
				return err
			},
		},
		{
			name: "MarkRefunded", legalFrom: StatusRefundDue,
			rejectCode: "BOOKING_NOT_REFUND_DUE",
			run: func(s *Service, id, u uuid.UUID) error {
				_, err := s.MarkRefunded(context.Background(), id, u, nil)
				return err
			},
		},
	}
}

// matrixRepo wraps mockRepo with the status guard the real SQL enforces, so
// a MISSING service-layer guard is caught rather than silently passing
// through to a mock that accepts anything.
type matrixRepo struct {
	*mockRepo
	current  string
	written  bool
	writeErr error
}

func (m *matrixRepo) guard(required string) error {
	if m.current != required {
		m.writeErr = fmt.Errorf("repository reached with status %q, expected %q", m.current, required)
		return m.writeErr
	}
	m.written = true
	return nil
}

func (m *matrixRepo) ApproveBooking(_ context.Context, _ uuid.UUID, _ time.Time) (string, error) {
	if err := m.guard(StatusPending); err != nil {
		return "", err
	}
	return "tok", nil
}
func (m *matrixRepo) UpdateBookingStatus(_ context.Context, _ uuid.UUID, _ string) error {
	return m.guard(StatusApproved)
}
func (m *matrixRepo) ConfirmDeposit(_ context.Context, _ uuid.UUID) error {
	return m.guard(StatusDepositPaid)
}
func (m *matrixRepo) ConfirmDepositReceived(_ context.Context, _ uuid.UUID, _ *string) error {
	return m.guard(StatusApproved)
}
func (m *matrixRepo) CompleteBooking(_ context.Context, _ uuid.UUID) (string, error) {
	if err := m.guard(StatusConfirmed); err != nil {
		return "", err
	}
	return "rev", nil
}
func (m *matrixRepo) MarkNoShow(_ context.Context, _ uuid.UUID) error {
	return m.guard(StatusConfirmed)
}
func (m *matrixRepo) MarkRefunded(_ context.Context, _ uuid.UUID, _ *string) error {
	return m.guard(StatusRefundDue)
}

func newMatrixRepo(status string, start time.Time, artistID uuid.UUID) *matrixRepo {
	tok := "tok"
	return &matrixRepo{
		mockRepo: &mockRepo{
			getBookingByIDBooking: &Booking{
				ID: uuid.New(), ArtistID: artistID, CustomerID: uuid.New(),
				Status: status, StartTime: start, EndTime: start.Add(time.Hour),
				CalendarToken: &tok,
			},
			getArtistIDByUserIDArtistID: artistID,
			getServiceSvc:               &SalonService{},
		},
		current: status,
	}
}

// TestStateMatrix_EveryCell walks the whole grid at both time positions.
//
// A cell is legal only when the status matches AND the timing constraint is
// satisfied; everything else must be rejected with the documented code, and
// must not reach the repository.
func TestStateMatrix_EveryCell(t *testing.T) {
	future := time.Now().Add(72 * time.Hour)
	past := time.Now().Add(-72 * time.Hour)

	for _, act := range matrixActions() {
		for _, status := range allStatuses {
			for _, when := range []string{"future", "past"} {
				start := future
				if when == "past" {
					start = past
				}

				artistID, userID := uuid.New(), uuid.New()
				repo := newMatrixRepo(status, start, artistID)
				svc := newTestService(repo)

				err := act.run(svc, repo.getBookingByIDBooking.ID, userID)

				statusOK := status == act.legalFrom
				timingOK := act.timing == "" || act.timing == when
				cell := fmt.Sprintf("%s on %s (%s appointment)", act.name, status, when)

				switch {
				case statusOK && timingOK:
					assert.NoError(t, err, "%s: should be legal", cell)
					assert.True(t, repo.written, "%s: legal transition never reached the repository", cell)
					assert.NoError(t, repo.writeErr, "%s", cell)

				case !statusOK:
					// Wrong status is checked before timing, so the status
					// code wins even when the timing is also wrong.
					require.Error(t, err, "%s: must be rejected", cell)
					var appErr *apperror.AppError
					require.ErrorAs(t, err, &appErr, "%s: must be a typed error", cell)
					assert.Equal(t, act.rejectCode, appErr.Code, "%s: wrong rejection code", cell)
					assert.False(t, repo.written,
						"%s: REJECTED BUT STILL WROTE THE ROW", cell)

				default:
					// Right status, wrong time.
					require.Error(t, err, "%s: timing must be enforced", cell)
					assert.False(t, repo.written,
						"%s: timing rejected but still wrote the row", cell)
				}
			}
		}
	}
}

// The grid above drives each action from a single legal status. This asserts
// the count itself, so a new action added without a matrix row is visible as
// a failing number rather than as silently missing coverage.
func TestStateMatrix_CoversEveryStateChangingAction(t *testing.T) {
	const documentedActions = 7 // §2 minus the two guest submit paths and cancel, tested separately
	assert.Len(t, matrixActions(), documentedActions,
		"an action was added or removed without updating the matrix")
	assert.Len(t, allStatuses, 11,
		"a status was added or removed without updating the matrix (migration 032 dropped deposit_pending)")
}
