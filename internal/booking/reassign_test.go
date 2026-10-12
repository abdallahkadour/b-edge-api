package booking

// Moving a booking to another artist of the salon (2026-10-10, audit §2).
//
// When an artist is off sick, the owner's only path was to cancel - a
// refund owed, the client sent back to the funnel, the slot perhaps gone.
// Now the owner moves it. The new artist must be able to take it exactly as
// if the client had booked her: she offers the service, works at that
// store, is approved and paid up, and is free at that time with travel
// counted. The client keeps the price she agreed (founder, 2026-10-10); the
// deposit stays where it was paid, the salon's account; only who does the
// work, and whose earnings it counts under, changes.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/audit/audittest"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

// team is a salon of three: Rania owns it and has the booking; Maya and Lina
// are members. The booking is Monday 10:00, an open day.
type team struct {
	repo                          *mockRepo
	booking                       *Booking
	owner                         uuid.UUID // users.id
	rania, maya, lina             uuid.UUID // artists.id
	raniaUser, mayaUser, linaUser uuid.UUID
}

func newTeam() *team {
	t := &team{owner: uuid.New(), rania: uuid.New(), maya: uuid.New(), lina: uuid.New(), mayaUser: uuid.New(), linaUser: uuid.New()}
	t.raniaUser = t.owner
	t.booking = &Booking{
		ID: uuid.New(), SalonID: testSalonID, StoreID: uuid.New(), ServiceID: uuid.New(), ArtistID: t.rania,
		CustomerID: uuid.New(), Status: StatusConfirmed,
		StartTime: beirutTime(2027, 3, 1, 10, 0), EndTime: beirutTime(2027, 3, 1, 11, 0), BlockedUntil: beirutTime(2027, 3, 1, 11, 0),
		OriginalPrice: decimal.NewFromInt(150), FinalPrice: decimal.NewFromInt(150), DepositAmount: decimal.NewFromInt(30),
	}
	t.repo = &mockRepo{
		getBookingByIDBooking: t.booking,
		getStoreStore:         defaultStore(),
		getBusinessHoursBH:    defaultBusinessHours(),
		getServiceSvc:         defaultService(),
		bookingsByArtist:      map[uuid.UUID][]*Booking{},
		salonArtists: []SalonArtist{
			{ArtistID: t.rania, UserID: t.raniaUser, Name: "Rania"},
			{ArtistID: t.maya, UserID: t.mayaUser, Name: "Maya"},
			{ArtistID: t.lina, UserID: t.linaUser, Name: "Lina"},
		},
		notificationContextCustomer: "Sara",
		notificationContextService:  "Bridal makeup",
	}
	return t
}

func (t *team) asOwner() context.Context { return signedIn(t.owner, salonrole.Owner) }

func reasonOf(cs []ReassignCandidate, artistID uuid.UUID) (ReassignCandidate, bool) {
	for _, c := range cs {
		if c.ArtistID == artistID {
			return c, true
		}
	}
	return ReassignCandidate{}, false
}

func codeOf(t *testing.T, err error) string {
	t.Helper()
	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	return ae.Code
}

// ── who could take it ─────────────────────────────────────────────────────────

func TestReassignCandidates_EveryOtherArtist_EligibleOrWithTheReason(t *testing.T) {
	tm := newTeam()
	tm.repo.notOffered = map[uuid.UUID]bool{tm.lina: true}
	mayaPrice := defaultService()
	mayaPrice.Price = decimal.NewFromInt(220)
	tm.repo.serviceByArtist = map[uuid.UUID]*SalonService{tm.maya: mayaPrice}

	got, err := newTestService(tm.repo).ReassignCandidates(tm.asOwner(), tm.booking.ID, tm.owner)

	require.NoError(t, err)
	assert.Equal(t, testSalonID, tm.repo.salonArtistsAskedFor, "the booking's salon's roster")
	_, listed := reasonOf(got, tm.rania)
	assert.False(t, listed, "not the artist who already has it")
	maya, _ := reasonOf(got, tm.maya)
	assert.True(t, maya.Eligible)
	assert.True(t, maya.Price.Equal(decimal.NewFromInt(220)), "her own price, shown so the owner sees the difference")
	lina, _ := reasonOf(got, tm.lina)
	assert.False(t, lina.Eligible)
	assert.Equal(t, ReasonNotOffered, lina.Reason)
}

func TestReassignCandidates_TheReasons(t *testing.T) {
	tm := newTeam()
	tm.repo.notAtStore = map[uuid.UUID]bool{tm.maya: true}
	tm.repo.unapproved = map[uuid.UUID]bool{tm.lina: true}

	got, err := newTestService(tm.repo).ReassignCandidates(tm.asOwner(), tm.booking.ID, tm.owner)

	require.NoError(t, err)
	maya, _ := reasonOf(got, tm.maya)
	lina, _ := reasonOf(got, tm.lina)
	assert.Equal(t, ReasonNotAtStore, maya.Reason)
	assert.Equal(t, ReasonNotAccepting, lina.Reason)
}

func TestReassignCandidates_SomeoneAlreadyBookedThen_IsNotFree(t *testing.T) {
	tm := newTeam()
	busy := existingBooking(beirutTime(2027, 3, 1, 9, 30), beirutTime(2027, 3, 1, 10, 30))
	busy.ArtistID = tm.maya
	tm.repo.bookingsByArtist[tm.maya] = []*Booking{busy}

	got, err := newTestService(tm.repo).ReassignCandidates(tm.asOwner(), tm.booking.ID, tm.owner)

	require.NoError(t, err)
	maya, _ := reasonOf(got, tm.maya)
	assert.False(t, maya.Eligible)
	assert.Equal(t, ReasonNotFree, maya.Reason)
}

func TestReassignCandidates_AMember_IsTreatedAsIfTheBookingDidNotExist(t *testing.T) {
	tm := newTeam()

	_, err := newTestService(tm.repo).ReassignCandidates(signedIn(tm.mayaUser, salonrole.Member), tm.booking.ID, tm.mayaUser)

	assert.Equal(t, "BOOKING_NOT_FOUND", codeOf(t, err))
}

// ── moving it ─────────────────────────────────────────────────────────────────

func TestReassignBooking_MovesIt_KeepsThePrice_RecordsIt_TellsEveryone(t *testing.T) {
	tm := newTeam()
	tm.booking.ArtistID = tm.lina // Lina is off sick; the owner moves her client to Maya
	dearer := defaultService()
	dearer.Price = decimal.NewFromInt(220)
	tm.repo.serviceByArtist = map[uuid.UUID]*SalonService{tm.maya: dearer}
	rec := &audittest.Recorder{}

	got, err := newTestService(tm.repo).WithAudit(rec).ReassignBooking(tm.asOwner(), tm.booking.ID, tm.owner, ReassignRequest{ArtistID: tm.maya.String()})

	require.NoError(t, err)
	require.Len(t, tm.repo.reassigned, 1)
	assert.Equal(t, reassignCall{ID: tm.booking.ID, From: tm.lina, To: tm.maya, Travel: map[uuid.UUID]int{}}, tm.repo.reassigned[0])
	assert.Equal(t, tm.maya, got.ArtistID)
	assert.True(t, got.FinalPrice.Equal(decimal.NewFromInt(150)), "she keeps the price she agreed, not Maya's 220")
	assert.True(t, got.DepositAmount.Equal(decimal.NewFromInt(30)))

	e := onlyEvent(t, rec)
	assert.Equal(t, audit.ActionBookingReassign, e.Action)
	assert.Equal(t, tm.owner, *e.ActorID)
	assert.Equal(t, map[string]any{"artist_id": tm.lina, "artist_name": "Lina"}, e.OldValues)
	assert.Equal(t, "Maya", e.NewValues.(map[string]any)["artist_name"])

	to := map[uuid.UUID]string{}
	for _, n := range tm.repo.enqueuedNotifications {
		to[n.UserID] = n.Message
	}
	assert.Contains(t, to[tm.booking.CustomerID], "will now be with Maya", "the client is told")
	assert.Contains(t, to[tm.mayaUser], "Sara", "the new artist is told whose it is")
	assert.Contains(t, to[tm.linaUser], "Maya", "and the one who had it, where it went")
	_, toldHerself := to[tm.owner]
	assert.False(t, toldHerself, "the owner is not told what she just did")
}

func TestReassignBooking_ToSomeoneWhoDoesNotOfferIt_IsRefusedAndNothingChanges(t *testing.T) {
	tm := newTeam()
	tm.repo.notOffered = map[uuid.UUID]bool{tm.lina: true}

	_, err := newTestService(tm.repo).ReassignBooking(tm.asOwner(), tm.booking.ID, tm.owner, ReassignRequest{ArtistID: tm.lina.String()})

	assert.Equal(t, "ARTIST_NOT_ELIGIBLE", codeOf(t, err))
	assert.Empty(t, tm.repo.reassigned)
}

func TestReassignBooking_ToSomeoneBusyThen_IsRefused(t *testing.T) {
	tm := newTeam()
	busy := existingBooking(beirutTime(2027, 3, 1, 9, 30), beirutTime(2027, 3, 1, 10, 30))
	busy.ArtistID = tm.maya
	tm.repo.bookingsByArtist[tm.maya] = []*Booking{busy}

	_, err := newTestService(tm.repo).ReassignBooking(tm.asOwner(), tm.booking.ID, tm.owner, ReassignRequest{ArtistID: tm.maya.String()})

	assert.Equal(t, "ARTIST_NOT_FREE", codeOf(t, err))
	assert.Empty(t, tm.repo.reassigned)
}

func TestReassignBooking_ToAnArtistOutsideTheSalon_IsRefused(t *testing.T) {
	tm := newTeam()

	_, err := newTestService(tm.repo).ReassignBooking(tm.asOwner(), tm.booking.ID, tm.owner, ReassignRequest{ArtistID: uuid.New().String()})

	assert.Equal(t, "ARTIST_NOT_ELIGIBLE", codeOf(t, err))
	assert.Empty(t, tm.repo.reassigned)
}

func TestReassignBooking_AMember_CannotMoveAnyonesBooking(t *testing.T) {
	tm := newTeam()

	_, err := newTestService(tm.repo).ReassignBooking(signedIn(tm.mayaUser, salonrole.Member), tm.booking.ID, tm.mayaUser,
		ReassignRequest{ArtistID: tm.maya.String()})

	assert.Equal(t, "BOOKING_NOT_FOUND", codeOf(t, err))
	assert.Empty(t, tm.repo.reassigned)
}

func TestReassignBooking_APastOrFinishedBooking_StaysPut(t *testing.T) {
	tm := newTeam()
	tm.booking.Status = StatusCompleted

	_, err := newTestService(tm.repo).ReassignBooking(tm.asOwner(), tm.booking.ID, tm.owner, ReassignRequest{ArtistID: tm.maya.String()})
	assert.Equal(t, "BOOKING_NOT_REASSIGNABLE", codeOf(t, err))

	tm.booking.Status = StatusConfirmed
	tm.booking.StartTime = time.Now().Add(-time.Hour)
	_, err = newTestService(tm.repo).ReassignBooking(tm.asOwner(), tm.booking.ID, tm.owner, ReassignRequest{ArtistID: tm.maya.String()})
	assert.Equal(t, "BOOKING_NOT_REASSIGNABLE", codeOf(t, err))
	assert.Empty(t, tm.repo.reassigned)
}

func TestReassignBooking_ToTheSameArtist_IsABadRequest(t *testing.T) {
	tm := newTeam()

	_, err := newTestService(tm.repo).ReassignBooking(tm.asOwner(), tm.booking.ID, tm.owner, ReassignRequest{ArtistID: tm.rania.String()})

	assert.Equal(t, "SAME_ARTIST", codeOf(t, err))
}

func TestReassignBooking_LostTheRaceInTheDatabase_IsNotFreeAndNotRecorded(t *testing.T) {
	tm := newTeam()
	tm.repo.reassignErr = ErrSlotUnavailable
	rec := &audittest.Recorder{}

	_, err := newTestService(tm.repo).WithAudit(rec).ReassignBooking(tm.asOwner(), tm.booking.ID, tm.owner, ReassignRequest{ArtistID: tm.maya.String()})

	assert.Equal(t, "ARTIST_NOT_FREE", codeOf(t, err))
	assert.Empty(t, rec.Events)
	assert.Empty(t, tm.repo.enqueuedNotifications)
}
