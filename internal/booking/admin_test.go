package booking

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The admin's view of bookings across every salon (feature, 2026-10-09).
// Read-only by design: an admin who could also cancel or confirm would hold
// every artist's money flow, and nothing has asked for that. What the admin
// needs is to SEE - above all the refunds owed across the platform, which no
// screen showed before (the PRD's launch list asked for it; the admin page
// had approvals, artists, plans, billing and reports, but no bookings).

func TestAdminListBookings_UnknownStatus_Refused(t *testing.T) {
	svc := newTestService(&mockRepo{})

	_, _, err := svc.AdminListBookings(context.Background(), "refunded_maybe", "", time.Now(), 20)

	assert.Equal(t, "INVALID_STATUS", appErrCode(t, err))
}

func TestAdminListBookings_MalformedSalon_Refused(t *testing.T) {
	svc := newTestService(&mockRepo{})

	_, _, err := svc.AdminListBookings(context.Background(), "", "not-a-uuid", time.Now(), 20)

	assert.Equal(t, "INVALID_SALON_ID", appErrCode(t, err))
}

func TestAdminListBookings_PassesTheFiltersAndPagesByOneExtra(t *testing.T) {
	salon := uuid.New()
	rows := make([]*AdminBooking, 3)
	for i := range rows {
		rows[i] = &AdminBooking{EnrichedBooking: EnrichedBooking{Booking: Booking{ID: uuid.New(), Status: StatusRefundDue,
			StartTime: time.Now().Add(time.Hour), EndTime: time.Now().Add(2 * time.Hour)}}, SalonName: "Rania Studio"}
	}
	repo := &mockRepo{adminBookings: rows}
	svc := newTestService(repo)

	out, more, err := svc.AdminListBookings(context.Background(), StatusRefundDue, salon.String(), time.Now(), 2)

	require.NoError(t, err)
	assert.Equal(t, StatusRefundDue, repo.adminStatus)
	require.NotNil(t, repo.adminSalon)
	assert.Equal(t, salon, *repo.adminSalon)
	assert.Equal(t, 2, repo.adminLimit, "the repository is asked for limit, and returns one extra to say there is more")
	assert.Len(t, out, 2)
	assert.True(t, more)
	assert.Equal(t, "Rania Studio", out[0].SalonName)
}

func TestAdminBookingSummary_PassesThrough(t *testing.T) {
	repo := &mockRepo{adminSummary: &AdminBookingSummary{RefundsOwed: 2, RefundsOwedAmount: decimal.NewFromInt(60)}}
	svc := newTestService(repo)

	s, err := svc.AdminBookingSummary(context.Background())

	require.NoError(t, err)
	assert.Equal(t, 2, s.RefundsOwed)
	assert.True(t, s.RefundsOwedAmount.Equal(decimal.NewFromInt(60)))
}
