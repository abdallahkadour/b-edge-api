package booking

// Every change to a booking is written to the salon's activity log, naming
// who made it (2026-10-10). Before this, audit_events held team changes only;
// an approval, a confirmed deposit, a refund, a cancellation or a no-show
// left no trace of who did it.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/audit/audittest"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/caller"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

// signedIn is the request context RequireAuth builds for this user.
func signedIn(user uuid.UUID, role salonrole.Role) context.Context {
	salon := testSalonID
	return caller.With(context.Background(), caller.Caller{
		UserID: user, Role: "artist", SalonID: &salon, SalonRole: role, IP: "10.0.0.9",
	})
}

func onlyEvent(t *testing.T, r *audittest.Recorder) audit.Event {
	t.Helper()
	require.Len(t, r.Events, 1, "exactly one activity row per change")
	return r.Events[0]
}

func bookingAt(status string, artistID uuid.UUID) *Booking {
	return &Booking{
		ID: uuid.New(), SalonID: testSalonID, ArtistID: artistID, CustomerID: uuid.New(),
		Status: status, StartTime: time.Now().UTC().Add(48 * time.Hour),
		FinalPrice: decimal.NewFromInt(120), DepositAmount: decimal.NewFromInt(30),
	}
}

func assertBookingEvent(t *testing.T, e audit.Event, b *Booking, actor uuid.UUID, action, from, to string) {
	t.Helper()
	assert.Equal(t, audit.EntityBooking, e.EntityType)
	assert.Equal(t, b.ID, e.EntityID)
	assert.Equal(t, action, e.Action)
	require.NotNil(t, e.SalonID)
	assert.Equal(t, b.SalonID, *e.SalonID, "the booking's salon, so the owner's feed shows it")
	require.NotNil(t, e.ActorID)
	assert.Equal(t, actor, *e.ActorID)
	assert.Equal(t, map[string]any{"status": from}, e.OldValues)
	facts, ok := e.NewValues.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, to, facts["status"])
	assert.Equal(t, b.ArtistID, facts["artist_id"], "whose booking it was, whoever acted")
}

func TestApproveBooking_RecordsWhoApprovedIt(t *testing.T) {
	user, artistID := uuid.New(), uuid.New()
	b := bookingAt(StatusPending, artistID)
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID, getServiceSvc: defaultService()}).WithAudit(rec)

	_, err := svc.ApproveBooking(signedIn(user, salonrole.Owner), b.ID, user)

	require.NoError(t, err)
	assertBookingEvent(t, onlyEvent(t, rec), b, user, audit.ActionBookingApprove, StatusPending, StatusApproved)
}

func TestApproveBooking_Refused_RecordsNothing(t *testing.T) {
	user := uuid.New()
	b := bookingAt(StatusPending, uuid.New())
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: uuid.New()}).WithAudit(rec)

	_, err := svc.ApproveBooking(signedIn(user, salonrole.Member), b.ID, user)

	require.Error(t, err)
	assert.Empty(t, rec.Events, "nothing happened, so there is nothing to record")
}

func TestApproveBooking_WriteFailed_RecordsNothing(t *testing.T) {
	user, artistID := uuid.New(), uuid.New()
	b := bookingAt(StatusPending, artistID)
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID,
		getServiceSvc: defaultService(), approveBookingErr: errors.New("db down")}).WithAudit(rec)

	_, err := svc.ApproveBooking(signedIn(user, salonrole.Owner), b.ID, user)

	require.Error(t, err)
	assert.Empty(t, rec.Events)
}

func TestConfirmDepositReceived_RecordsTheConfirmation(t *testing.T) {
	user, artistID := uuid.New(), uuid.New()
	b := bookingAt(StatusApproved, artistID)
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}).WithAudit(rec)

	_, err := svc.ConfirmDepositReceived(signedIn(user, salonrole.Owner), b.ID, user, nil, nil)

	require.NoError(t, err)
	assertBookingEvent(t, onlyEvent(t, rec), b, user, audit.ActionBookingConfirm, StatusApproved, StatusConfirmed)
}

func TestMarkDepositReceived_RecordsIt(t *testing.T) {
	user, artistID := uuid.New(), uuid.New()
	b := bookingAt(StatusApproved, artistID)
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}).WithAudit(rec)

	_, err := svc.MarkDepositReceived(signedIn(user, salonrole.Owner), b.ID, user)

	require.NoError(t, err)
	assertBookingEvent(t, onlyEvent(t, rec), b, user, audit.ActionBookingDepositPaid, StatusApproved, StatusDepositPaid)
}

func TestConfirmDeposit_RecordsIt(t *testing.T) {
	user, artistID := uuid.New(), uuid.New()
	b := bookingAt(StatusDepositPaid, artistID)
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}).WithAudit(rec)

	_, err := svc.ConfirmDeposit(signedIn(user, salonrole.Owner), b.ID, user)

	require.NoError(t, err)
	assertBookingEvent(t, onlyEvent(t, rec), b, user, audit.ActionBookingConfirm, StatusDepositPaid, StatusConfirmed)
}

func TestMarkRefunded_RecordsWhoSentTheMoneyBack(t *testing.T) {
	user, artistID := uuid.New(), uuid.New()
	b := bookingAt(StatusRefundDue, artistID)
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}).WithAudit(rec)
	ref := "OMT-12345"

	_, err := svc.MarkRefunded(signedIn(user, salonrole.Owner), b.ID, user, &ref, true)

	require.NoError(t, err)
	assertBookingEvent(t, onlyEvent(t, rec), b, user, audit.ActionBookingRefunded, StatusRefundDue, StatusRefunded)
}

func TestCancelBooking_ByTheArtist_RecordsTheRefundOwed(t *testing.T) {
	artistID := uuid.New()
	paid := time.Now().Add(-24 * time.Hour)
	b := bookingAt(StatusConfirmed, artistID)
	b.DepositPaidAt = &paid
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}).WithAudit(rec)

	_, err := svc.CancelBooking(signedIn(artistID, salonrole.Owner), b.ID, artistID, RoleArtist, CancelBookingRequest{})

	require.NoError(t, err)
	assertBookingEvent(t, onlyEvent(t, rec), b, artistID, audit.ActionBookingCancel, StatusConfirmed, StatusRefundDue)
}

func TestCancelBooking_ByTheCustomer_IsRecordedAsTheCustomer(t *testing.T) {
	b := bookingAt(StatusApproved, uuid.New())
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getBookingByIDBooking: b}).WithAudit(rec)
	ctx := caller.With(context.Background(), caller.Caller{UserID: b.CustomerID, Role: "customer"})

	_, err := svc.CancelBooking(ctx, b.ID, b.CustomerID, RoleCustomer, CancelBookingRequest{})

	require.NoError(t, err)
	e := onlyEvent(t, rec)
	assert.Equal(t, "customer", e.ActorRole)
	assertBookingEvent(t, e, b, b.CustomerID, audit.ActionBookingCancel, StatusApproved, StatusCancelled)
}

func TestCompleteBooking_RecordsIt(t *testing.T) {
	user, artistID := uuid.New(), uuid.New()
	b := bookingAt(StatusConfirmed, artistID)
	b.StartTime = time.Now().UTC().Add(-2 * time.Hour)
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID, completeBookingToken: "tok"}).WithAudit(rec)

	_, err := svc.CompleteBooking(signedIn(user, salonrole.Owner), b.ID, user)

	require.NoError(t, err)
	assertBookingEvent(t, onlyEvent(t, rec), b, user, audit.ActionBookingComplete, StatusConfirmed, StatusCompleted)
}

func TestMarkNoShow_RecordsIt(t *testing.T) {
	user, artistID := uuid.New(), uuid.New()
	b := bookingAt(StatusConfirmed, artistID)
	b.StartTime = time.Now().UTC().Add(-2 * time.Hour)
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID}).WithAudit(rec)

	_, err := svc.MarkNoShow(signedIn(user, salonrole.Owner), b.ID, user)

	require.NoError(t, err)
	assertBookingEvent(t, onlyEvent(t, rec), b, user, audit.ActionBookingNoShow, StatusConfirmed, StatusNoShow)
}

func TestShiftDay_RecordsOneRowForTheDay(t *testing.T) {
	repo, storeID := shiftRepo(t)
	req := shiftReq(30)
	req.StoreID = storeID.String()
	repo.enrichedForDay[0].StoreID = storeID
	rec := &audittest.Recorder{}
	user := uuid.New()
	svc := newTestService(repo).WithAudit(rec)

	_, err := svc.ShiftDay(signedIn(user, salonrole.Owner), user, req)

	require.NoError(t, err)
	e := onlyEvent(t, rec)
	assert.Equal(t, audit.ActionBookingShiftDay, e.Action)
	assert.Equal(t, audit.EntityArtist, e.EntityType)
	assert.Equal(t, repo.getArtistIDByUserIDArtistID, e.EntityID)
	assert.Equal(t, map[string]any{"date": "2027-03-01", "minutes": 30, "moved": 1}, e.NewValues)
}

func TestTransitions_WithoutAnAuditLog_StillWork(t *testing.T) {
	// Every existing test builds the service without one.
	user, artistID := uuid.New(), uuid.New()
	b := bookingAt(StatusPending, artistID)
	svc := newTestService(&mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: artistID, getServiceSvc: defaultService()})

	_, err := svc.ApproveBooking(signedIn(user, salonrole.Owner), b.ID, user)

	require.NoError(t, err)
}
