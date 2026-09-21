package artist

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock methods for the rota surface ────────────────────────────────────
//
// Defined here rather than in service_test.go so the rota work stays
// together. The backing fields sit on mockRepo itself - Go lets another file
// add methods to a type, not fields.

func (m *mockRepo) ArtistIDForUser(context.Context, uuid.UUID) (uuid.UUID, error) {
	return m.rotaArtistID, m.rotaArtistErr
}
func (m *mockRepo) ArtistWorksAtStore(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return m.rotaLinked, m.rotaLinkedErr
}
func (m *mockRepo) GetRota(context.Context, uuid.UUID) ([]*ArtistSchedule, error) {
	return m.rotaRows, nil
}
func (m *mockRepo) SetRota(_ context.Context, _ uuid.UUID, req SetRotaRequest) error {
	m.rotaSetCalls = append(m.rotaSetCalls, req)
	return nil
}
func (m *mockRepo) GetScheduleExceptions(context.Context, uuid.UUID, time.Time) (
	[]*ArtistScheduleException, error) {
	return m.rotaExceptions, nil
}
func (m *mockRepo) UpsertScheduleException(_ context.Context, _ uuid.UUID,
	req CreateScheduleExceptionRequest) error {
	m.rotaUpserted = append(m.rotaUpserted, req)
	return nil
}
func (m *mockRepo) DeleteScheduleException(_ context.Context, _, id uuid.UUID) error {
	m.rotaDeleted = append(m.rotaDeleted, id)
	return nil
}

func newRotaService(linked bool) (*Service, *mockRepo) {
	m := &mockRepo{rotaArtistID: uuid.New(), rotaLinked: linked}
	return NewService(m), m
}

func day(dow int, from, to string, working bool) RotaDayItem {
	return RotaDayItem{DayOfWeek: dow, StartTime: from, EndTime: to, IsWorking: working}
}

// ── SetMyRota ─────────────────────────────────────────────────────────────

func TestSetMyRota_LinkedStore_Writes(t *testing.T) {
	svc, m := newRotaService(true)
	req := SetRotaRequest{StoreID: uuid.New(), Days: []RotaDayItem{
		day(1, "09:00", "18:00", true),
		day(2, "12:00", "20:00", true),
	}}

	require.NoError(t, svc.SetMyRota(context.Background(), uuid.New(), req))
	require.Len(t, m.rotaSetCalls, 1)
	assert.Len(t, m.rotaSetCalls[0].Days, 2)
}

// artist_stores is the record of where an artist is bookable. Writing a rota
// for a store you are not attached to would let one salon's member create
// rows keyed to another salon's store.
func TestSetMyRota_StoreNotLinked_Refused(t *testing.T) {
	svc, m := newRotaService(false)
	err := svc.SetMyRota(context.Background(), uuid.New(),
		SetRotaRequest{StoreID: uuid.New(), Days: []RotaDayItem{day(1, "09:00", "18:00", true)}})

	assert.ErrorIs(t, err, ErrStoreNotLinked)
	assert.Empty(t, m.rotaSetCalls, "nothing may be written for an unlinked store")
}

// The whole week is validated before any of it is written. The database
// would reject an inverted range anyway, but only after earlier days had
// already been inserted inside the transaction.
func TestSetMyRota_InvertedDay_RejectedBeforeAnyWrite(t *testing.T) {
	svc, m := newRotaService(true)
	err := svc.SetMyRota(context.Background(), uuid.New(), SetRotaRequest{
		StoreID: uuid.New(),
		Days: []RotaDayItem{
			day(1, "09:00", "18:00", true),
			day(2, "20:00", "09:00", true), // inverted
		},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "before end time")
	assert.Empty(t, m.rotaSetCalls, "no partial week may reach the database")
}

func TestSetMyRota_DuplicateDay_Rejected(t *testing.T) {
	svc, m := newRotaService(true)
	err := svc.SetMyRota(context.Background(), uuid.New(), SetRotaRequest{
		StoreID: uuid.New(),
		Days:    []RotaDayItem{day(1, "09:00", "12:00", true), day(1, "13:00", "18:00", true)},
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "twice")
	assert.Empty(t, m.rotaSetCalls)
}

// An empty submission clears the rota, which restores the default: available
// for the whole store window. It is a legitimate way to opt back out.
func TestSetMyRota_EmptyWeek_ClearsTheRota(t *testing.T) {
	svc, m := newRotaService(true)
	require.NoError(t, svc.SetMyRota(context.Background(), uuid.New(),
		SetRotaRequest{StoreID: uuid.New(), Days: []RotaDayItem{}}))

	require.Len(t, m.rotaSetCalls, 1)
	assert.Empty(t, m.rotaSetCalls[0].Days)
}

func TestSetMyRota_NotAnArtist_Refused(t *testing.T) {
	svc, m := newRotaService(true)
	m.rotaArtistErr = ErrNotAnArtist

	err := svc.SetMyRota(context.Background(), uuid.New(),
		SetRotaRequest{StoreID: uuid.New()})
	assert.ErrorIs(t, err, ErrNotAnArtist)
}

// ── Exceptions ────────────────────────────────────────────────────────────

// The table's CHECK forbids a row that says "unavailable, 09:00-17:00",
// which reads two opposite ways. The service normalises rather than letting
// the caller meet a constraint name.
func TestSetMyScheduleException_DayOff_TimesAreCleared(t *testing.T) {
	svc, m := newRotaService(true)
	from, to := "09:00", "17:00"

	require.NoError(t, svc.SetMyScheduleException(context.Background(), uuid.New(),
		CreateScheduleExceptionRequest{
			ExceptionDate: "2026-12-25", IsUnavailable: true,
			StartTime: &from, EndTime: &to,
		}))

	require.Len(t, m.rotaUpserted, 1)
	assert.Nil(t, m.rotaUpserted[0].StartTime, "a day off carries no times")
	assert.Nil(t, m.rotaUpserted[0].EndTime)
}

func TestSetMyScheduleException_DifferentHours_RequiresBothTimes(t *testing.T) {
	svc, _ := newRotaService(true)
	from := "09:00"

	err := svc.SetMyScheduleException(context.Background(), uuid.New(),
		CreateScheduleExceptionRequest{
			ExceptionDate: "2026-12-25", IsUnavailable: false, StartTime: &from,
		})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "needs a start and end time")
}

func TestSetMyScheduleException_InvertedHours_Rejected(t *testing.T) {
	svc, _ := newRotaService(true)
	from, to := "17:00", "09:00"

	err := svc.SetMyScheduleException(context.Background(), uuid.New(),
		CreateScheduleExceptionRequest{
			ExceptionDate: "2026-12-25", StartTime: &from, EndTime: &to,
		})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "before end time")
}

// A NULL store_id means "away from every store", which is the common case
// and must not be checked against artist_stores.
func TestSetMyScheduleException_NoStore_SkipsTheLinkCheck(t *testing.T) {
	svc, m := newRotaService(false) // not linked to anything
	require.NoError(t, svc.SetMyScheduleException(context.Background(), uuid.New(),
		CreateScheduleExceptionRequest{ExceptionDate: "2026-12-25", IsUnavailable: true}))
	assert.Len(t, m.rotaUpserted, 1)
}

func TestSetMyScheduleException_UnlinkedStore_Refused(t *testing.T) {
	svc, m := newRotaService(false)
	store := uuid.New()

	err := svc.SetMyScheduleException(context.Background(), uuid.New(),
		CreateScheduleExceptionRequest{
			ExceptionDate: "2026-12-25", StoreID: &store, IsUnavailable: true,
		})
	assert.ErrorIs(t, err, ErrStoreNotLinked)
	assert.Empty(t, m.rotaUpserted)
}
