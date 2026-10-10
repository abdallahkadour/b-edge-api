package booking

// The salon owner acting on a member's booking (2026-10-10).
//
// salonrole.BookingsAnyWrite has been Owner=true since roles landed and
// guarded nothing: every booking action compared the caller with the
// booking's artist and refused anyone else. Decision: the owner can approve,
// confirm a deposit, refund, cancel, complete and mark a no-show on any
// booking in her salon, and every such action is written to the activity
// log under her name. A member still acts on her own bookings only.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/audit/audittest"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/caller"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

// aMembersBooking is a pending booking of someone else's, in testSalonID;
// the repo resolves the caller to her own, different, artist row.
func aMembersBooking(status string) (*Booking, *mockRepo) {
	b := bookingAt(status, uuid.New())
	return b, &mockRepo{getBookingByIDBooking: b, getArtistIDByUserIDArtistID: uuid.New(), getServiceSvc: defaultService()}
}

func isNotFound(t *testing.T, err error) {
	t.Helper()
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "BOOKING_NOT_FOUND", ae.Code, "refused as if the booking did not exist")
}

func TestApproveBooking_TheOwner_ApprovesAMembersBooking_UnderHerOwnName(t *testing.T) {
	owner := uuid.New()
	b, repo := aMembersBooking(StatusPending)
	rec := &audittest.Recorder{}

	got, err := newTestService(repo).WithAudit(rec).ApproveBooking(signedIn(owner, salonrole.Owner), b.ID, owner)

	require.NoError(t, err)
	assert.Equal(t, StatusApproved, got.Status)
	e := onlyEvent(t, rec)
	assert.Equal(t, owner, *e.ActorID, "the activity row names the owner, not the booking's artist")
	assert.Equal(t, b.ArtistID, e.NewValues.(map[string]any)["artist_id"])
}

func TestApproveBooking_AMember_CannotApproveAColleaguesBooking(t *testing.T) {
	member := uuid.New()
	b, repo := aMembersBooking(StatusPending)

	_, err := newTestService(repo).ApproveBooking(signedIn(member, salonrole.Member), b.ID, member)

	isNotFound(t, err)
}

func TestApproveBooking_TheOwnerOfAnotherSalon_Cannot(t *testing.T) {
	owner := uuid.New()
	b, repo := aMembersBooking(StatusPending)
	elsewhere := uuid.New()
	ctx := caller.With(context.Background(), caller.Caller{UserID: owner, Role: "artist", SalonID: &elsewhere, SalonRole: salonrole.Owner})

	_, err := newTestService(repo).ApproveBooking(ctx, b.ID, owner)

	isNotFound(t, err)
}

func TestApproveBooking_OwnerStandingOfSomeoneElse_DoesNotCarryOver(t *testing.T) {
	// The request's caller is the owner; the action is being taken for a
	// different user id. The owner's standing is hers, not theirs.
	owner, other := uuid.New(), uuid.New()
	b, repo := aMembersBooking(StatusPending)

	_, err := newTestService(repo).ApproveBooking(signedIn(owner, salonrole.Owner), b.ID, other)

	isNotFound(t, err)
}

func TestOwner_CanConfirmRefundCompleteAndNoShow_AMembersBookings(t *testing.T) {
	owner := uuid.New()
	ctx := signedIn(owner, salonrole.Owner)

	b, repo := aMembersBooking(StatusApproved)
	_, err := newTestService(repo).ConfirmDepositReceived(ctx, b.ID, owner, nil, nil)
	require.NoError(t, err, "confirm deposit received")

	b, repo = aMembersBooking(StatusApproved)
	_, err = newTestService(repo).MarkDepositReceived(ctx, b.ID, owner)
	require.NoError(t, err, "mark deposit received")

	b, repo = aMembersBooking(StatusDepositPaid)
	_, err = newTestService(repo).ConfirmDeposit(ctx, b.ID, owner)
	require.NoError(t, err, "confirm deposit")

	b, repo = aMembersBooking(StatusRefundDue)
	ref := "OMT-1"
	_, err = newTestService(repo).MarkRefunded(ctx, b.ID, owner, &ref, true)
	require.NoError(t, err, "mark refunded")

	b, repo = aMembersBooking(StatusConfirmed)
	b.StartTime = time.Now().UTC().Add(-time.Hour)
	repo.completeBookingToken = "tok"
	_, err = newTestService(repo).CompleteBooking(ctx, b.ID, owner)
	require.NoError(t, err, "complete")

	b, repo = aMembersBooking(StatusConfirmed)
	b.StartTime = time.Now().UTC().Add(-time.Hour)
	_, err = newTestService(repo).MarkNoShow(ctx, b.ID, owner)
	require.NoError(t, err, "no-show")
}

func TestMember_CannotConfirmOrCompleteAColleaguesBooking(t *testing.T) {
	member := uuid.New()
	ctx := signedIn(member, salonrole.Member)

	b, repo := aMembersBooking(StatusApproved)
	_, err := newTestService(repo).ConfirmDepositReceived(ctx, b.ID, member, nil, nil)
	isNotFound(t, err)

	b, repo = aMembersBooking(StatusConfirmed)
	b.StartTime = time.Now().UTC().Add(-time.Hour)
	_, err = newTestService(repo).CompleteBooking(ctx, b.ID, member)
	isNotFound(t, err)
}

func TestCancelBooking_TheOwner_CancelsAMembersBooking_AsTheSalon(t *testing.T) {
	// The salon calling off an appointment is blameless for the customer:
	// a deposit that arrived is owed back, whichever of the salon's people
	// pressed the button.
	owner := uuid.New()
	paid := time.Now().Add(-24 * time.Hour)
	b, repo := aMembersBooking(StatusConfirmed)
	b.DepositPaidAt = &paid

	got, err := newTestService(repo).CancelBooking(signedIn(owner, salonrole.Owner), b.ID, owner, RoleArtist, CancelBookingRequest{})

	require.NoError(t, err)
	assert.Equal(t, StatusRefundDue, got.Status)
}

func TestCancelBooking_AMember_CannotCancelAColleaguesBooking(t *testing.T) {
	member := uuid.New()
	b, repo := aMembersBooking(StatusConfirmed)

	_, err := newTestService(repo).CancelBooking(signedIn(member, salonrole.Member), b.ID, member, RoleArtist, CancelBookingRequest{})

	isNotFound(t, err)
}

func TestGetEnrichedBookingByID_TheOwner_ReadsAMembersBooking(t *testing.T) {
	owner, member := uuid.New(), uuid.New()
	e := &EnrichedBooking{Booking: *bookingAt(StatusConfirmed, uuid.New())}
	repo := &mockRepo{getEnrichedBookingByIDBooking: e, getArtistIDByUserIDArtistID: uuid.New()}

	_, err := newTestService(repo).GetEnrichedBookingByID(signedIn(owner, salonrole.Owner), e.ID, owner, RoleArtist)
	require.NoError(t, err)

	_, err = newTestService(repo).GetEnrichedBookingByID(signedIn(member, salonrole.Member), e.ID, member, RoleArtist)
	isNotFound(t, err)
}

// ── The whole salon's bookings, for the owner ────────────────────────────────

func TestListSalonBookings_PassesTheSalonAndFilters(t *testing.T) {
	artist := uuid.New()
	repo := &mockRepo{}

	_, _, err := newTestService(repo).ListSalonBookings(context.Background(), testSalonID, artist.String(), StatusPending, time.Now(), 20)

	require.NoError(t, err)
	assert.Equal(t, testSalonID, repo.salonListSalon)
	require.NotNil(t, repo.salonListArtist)
	assert.Equal(t, artist, *repo.salonListArtist)
	assert.Equal(t, StatusPending, repo.salonListStatus)
	assert.Equal(t, 20, repo.salonListLimit, "the repository asks for one more itself, as the artist list does")
}

func TestListSalonBookings_EveryoneWhenNoArtist(t *testing.T) {
	repo := &mockRepo{}

	_, _, err := newTestService(repo).ListSalonBookings(context.Background(), testSalonID, "", "", time.Now(), 20)

	require.NoError(t, err)
	assert.Nil(t, repo.salonListArtist)
}

func TestListSalonBookings_BadFilters_AreRefused(t *testing.T) {
	svc := newTestService(&mockRepo{})

	_, _, err := svc.ListSalonBookings(context.Background(), testSalonID, "nope", "", time.Now(), 20)
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "INVALID_ARTIST_ID", ae.Code)

	_, _, err = svc.ListSalonBookings(context.Background(), testSalonID, "", "lost", time.Now(), 20)
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "INVALID_STATUS", ae.Code)
}

func TestListSalonWeek_IsTheSalonsWeek(t *testing.T) {
	repo := &mockRepo{}
	week := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

	_, err := newTestService(repo).ListSalonWeek(context.Background(), testSalonID, "", week)

	require.NoError(t, err)
	assert.Equal(t, testSalonID, repo.salonListSalon)
	assert.True(t, repo.salonWeekStart.Equal(week))
}
