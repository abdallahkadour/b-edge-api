package artist

// The salon's menu prices, its stores' settings and its opening hours are
// shared by every artist in it, so a change to any of them is written to the
// activity log with what changed (2026-10-10).

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/audit/audittest"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/caller"
)

func ownerSignedIn(salon uuid.UUID) (context.Context, uuid.UUID) {
	user := uuid.New()
	return caller.With(context.Background(), caller.Caller{UserID: user, Role: "artist", SalonID: &salon}), user
}

// serviceSeqRepo answers GetServiceByID with the service before the update,
// then after it - the mock's single canned value cannot show a change.
type serviceSeqRepo struct {
	*mockRepo
	seq []*SalonServiceRecord
}

func (r *serviceSeqRepo) GetServiceByID(_ context.Context, _ uuid.UUID) (*SalonServiceRecord, error) {
	s := r.seq[0]
	if len(r.seq) > 1 {
		r.seq = r.seq[1:]
	}
	return s, nil
}

func menuItem(salon uuid.UUID, price string) *SalonServiceRecord {
	return &SalonServiceRecord{ID: uuid.New(), SalonID: salon, Name: "Bridal makeup", DurationMin: 90,
		Price: decimal.RequireFromString(price), DepositAmount: decimal.RequireFromString("50"), IsActive: true}
}

func TestUpdateService_RecordsOnlyWhatChanged(t *testing.T) {
	salon := uuid.New()
	ctx, user := ownerSignedIn(salon)
	before, after := menuItem(salon, "200"), menuItem(salon, "250")
	after.ID = before.ID
	rec := &audittest.Recorder{}
	price := "250"

	_, err := newTestService(&serviceSeqRepo{mockRepo: &mockRepo{}, seq: []*SalonServiceRecord{before, after}}).
		WithAudit(rec).UpdateService(ctx, before.ID, salon, UpdateServiceRequest{Price: &price})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	e := rec.Events[0]
	assert.Equal(t, audit.ActionServiceUpdate, e.Action)
	assert.Equal(t, audit.EntityService, e.EntityType)
	assert.Equal(t, before.ID, e.EntityID)
	assert.Equal(t, &salon, e.SalonID)
	assert.Equal(t, &user, e.ActorID)
	assert.Equal(t, map[string]any{"name": "Bridal makeup", "price": "200.00"}, e.OldValues, "the name says which service; the rest is the change")
	assert.Equal(t, map[string]any{"name": "Bridal makeup", "price": "250.00"}, e.NewValues)
}

func TestUpdateService_AnotherSalonsService_RecordsNothing(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerSignedIn(salon)
	rec := &audittest.Recorder{}
	price := "1"

	_, err := newTestService(&mockRepo{getServiceByIDSvc: menuItem(uuid.New(), "200")}).
		WithAudit(rec).UpdateService(ctx, uuid.New(), salon, UpdateServiceRequest{Price: &price})

	require.Error(t, err)
	assert.Empty(t, rec.Events)
}

func TestCreateService_RecordsTheNewItem(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerSignedIn(salon)
	rec := &audittest.Recorder{}

	out, err := newTestService(&mockRepo{}).WithAudit(rec).CreateService(ctx, salon,
		CreateServiceRequest{Name: "Lashes", DurationMin: 60, Price: "80", DepositAmount: "20", DepositDeadlineHours: 24})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	e := rec.Events[0]
	assert.Equal(t, audit.ActionServiceCreate, e.Action)
	assert.Equal(t, out.ID, e.EntityID)
	assert.Nil(t, e.OldValues)
	assert.Equal(t, "80.00", e.NewValues.(map[string]any)["price"])
}

func TestDeleteService_RecordsWhatWasRemoved(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerSignedIn(salon)
	item := menuItem(salon, "200")
	rec := &audittest.Recorder{}

	err := newTestService(&mockRepo{getServiceByIDSvc: item}).WithAudit(rec).DeleteService(ctx, item.ID, salon)

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	assert.Equal(t, audit.ActionServiceDelete, rec.Events[0].Action)
	assert.Equal(t, "Bridal makeup", rec.Events[0].OldValues.(map[string]any)["name"])
}

func TestSetBusinessHours_RecordsTheDay(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerSignedIn(salon)
	store := &Store{ID: uuid.New(), SalonID: salon, Name: "Hamra"}
	rec := &audittest.Recorder{}

	err := newTestService(&mockRepo{getStoreByIDStore: store}).WithAudit(rec).SetBusinessHours(ctx, store.ID, salon,
		SetBusinessHoursRequest{DayOfWeek: 5, OpenTime: "10:00:00", CloseTime: "16:00:00", IsOpen: true})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	e := rec.Events[0]
	assert.Equal(t, audit.ActionStoreHours, e.Action)
	assert.Equal(t, audit.EntityStore, e.EntityType)
	assert.Equal(t, store.ID, e.EntityID)
	assert.Equal(t, map[string]any{"day_of_week": 5, "is_open": true, "open_time": "10:00:00", "close_time": "16:00:00"}, e.NewValues)
}

func TestClosures_AddedAndRemoved_AreRecorded(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerSignedIn(salon)
	store := &Store{ID: uuid.New(), SalonID: salon}
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{getStoreByIDStore: store}).WithAudit(rec)

	require.NoError(t, svc.CreateException(ctx, store.ID, salon, CreateExceptionRequest{ExceptionDate: "2026-12-25", IsClosed: true}))
	require.NoError(t, svc.DeleteException(ctx, store.ID, salon, "2026-12-25"))

	require.Len(t, rec.Events, 2)
	assert.Equal(t, audit.ActionStoreClosureAdd, rec.Events[0].Action)
	assert.Equal(t, "2026-12-25", rec.Events[0].NewValues.(map[string]any)["date"])
	assert.Equal(t, audit.ActionStoreClosureClear, rec.Events[1].Action)
}

func TestUpdateStore_RecordsTheFeeChange(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerSignedIn(salon)
	before := &Store{ID: uuid.New(), SalonID: salon, Name: "Hamra", EarlyBirdFee: decimal.RequireFromString("10")}
	after := *before
	after.EarlyBirdFee = decimal.RequireFromString("15")
	rec := &audittest.Recorder{}
	fee := "15"

	_, err := newTestService(&storeSeqRepo{mockRepo: &mockRepo{}, seq: []*Store{before, &after}}).
		WithAudit(rec).UpdateStore(ctx, before.ID, salon, UpdateStoreRequest{EarlyBirdFee: &fee})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	assert.Equal(t, map[string]any{"name": "Hamra", "early_bird_fee": "10.00"}, rec.Events[0].OldValues)
	assert.Equal(t, map[string]any{"name": "Hamra", "early_bird_fee": "15.00"}, rec.Events[0].NewValues)
}

type storeSeqRepo struct {
	*mockRepo
	seq []*Store
}

func (r *storeSeqRepo) GetStoreByID(_ context.Context, _ uuid.UUID) (*Store, error) {
	s := r.seq[0]
	if len(r.seq) > 1 {
		r.seq = r.seq[1:]
	}
	return s, nil
}
