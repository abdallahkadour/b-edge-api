package payout

// The salon's payment accounts are where customers send deposits. A change
// to one is the most consequential write in the app - redirect it and money
// goes to someone else - so it is recorded with what it was and what it
// became (2026-10-10).

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/audit/audittest"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/caller"
)

func ownerCtx(salon uuid.UUID) (context.Context, uuid.UUID) {
	user := uuid.New()
	return caller.With(context.Background(), caller.Caller{UserID: user, Role: "artist", SalonID: &salon}), user
}

func TestUpsert_ChangingAnAccount_RecordsTheOldAndTheNew(t *testing.T) {
	salon := uuid.New()
	ctx, user := ownerCtx(salon)
	repo := &mockRepo{listed: []*PaymentMethod{{ID: uuid.New(), SalonID: salon, Method: MethodWhish, AccountName: "Rania", AccountRef: "71900001", IsActive: true}}}
	rec := &audittest.Recorder{}

	_, err := newTestService(repo).WithAudit(rec).Upsert(ctx, salon,
		UpsertPaymentMethodRequest{Method: "whish", AccountName: "Someone Else", AccountRef: "70111222"})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	e := rec.Events[0]
	assert.Equal(t, audit.ActionPaymentMethodSave, e.Action)
	assert.Equal(t, audit.EntityPaymentMethod, e.EntityType)
	assert.Equal(t, &salon, e.SalonID)
	assert.Equal(t, &user, e.ActorID)
	assert.Equal(t, map[string]any{"method": "whish", "account_name": "Rania", "account_ref": "71900001", "is_active": true}, e.OldValues)
	assert.Equal(t, map[string]any{"method": "whish", "account_name": "Someone Else", "account_ref": "70111222", "is_active": true}, e.NewValues)
}

func TestUpsert_AFirstAccount_HasNoOldValues(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerCtx(salon)
	rec := &audittest.Recorder{}

	_, err := newTestService(&mockRepo{}).WithAudit(rec).Upsert(ctx, salon,
		UpsertPaymentMethodRequest{Method: "omt", AccountName: "Rania", AccountRef: "71900001"})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	assert.Nil(t, rec.Events[0].OldValues)
}

func TestUpsert_Refused_RecordsNothing(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerCtx(salon)
	rec := &audittest.Recorder{}

	_, err := newTestService(&mockRepo{upsertErr: errors.New("db down")}).WithAudit(rec).Upsert(ctx, salon,
		UpsertPaymentMethodRequest{Method: "omt", AccountName: "Rania", AccountRef: "71900001"})

	require.Error(t, err)
	assert.Empty(t, rec.Events)
}

func TestSetActive_RecordsRetiringAndRestoring(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerCtx(salon)
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{}).WithAudit(rec)

	_, err := svc.SetActive(ctx, uuid.New(), salon, false)
	require.NoError(t, err)
	_, err = svc.SetActive(ctx, uuid.New(), salon, true)
	require.NoError(t, err)

	require.Len(t, rec.Events, 2)
	assert.Equal(t, audit.ActionPaymentMethodRetire, rec.Events[0].Action)
	assert.Equal(t, audit.ActionPaymentMethodRestore, rec.Events[1].Action)
	assert.Equal(t, "71900001", rec.Events[0].NewValues.(map[string]any)["account_ref"])
}

func TestSetNoShowPolicy_RecordsTheOldAndNewSetting(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerCtx(salon)
	rec := &audittest.Recorder{}
	repo := &mockRepo{noShowAfter: 2}
	three := 3

	_, err := newTestService(repo).WithAudit(rec).SetNoShowPolicy(ctx, salon, SetNoShowPolicyRequest{After: &three})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	e := rec.Events[0]
	assert.Equal(t, audit.ActionSalonNoShowPolicy, e.Action)
	assert.Equal(t, audit.EntitySalon, e.EntityType)
	assert.Equal(t, salon, e.EntityID)
	assert.Equal(t, map[string]any{"after": 2}, e.OldValues)
	assert.Equal(t, map[string]any{"after": 3}, e.NewValues)
}

func TestUpsert_SavedUnchanged_RecordsNothing(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerCtx(salon)
	repo := &mockRepo{listed: []*PaymentMethod{{ID: uuid.New(), SalonID: salon, Method: MethodWhish, AccountName: "Rania", AccountRef: "71900001", IsActive: true}}}
	rec := &audittest.Recorder{}

	_, err := newTestService(repo).WithAudit(rec).Upsert(ctx, salon,
		UpsertPaymentMethodRequest{Method: "whish", AccountName: "Rania", AccountRef: "71900001"})

	require.NoError(t, err)
	assert.Empty(t, rec.Events)
}

func TestSetNoShowPolicy_Unchanged_RecordsNothing(t *testing.T) {
	salon := uuid.New()
	ctx, _ := ownerCtx(salon)
	rec := &audittest.Recorder{}
	two := 2

	_, err := newTestService(&mockRepo{noShowAfter: 2}).WithAudit(rec).SetNoShowPolicy(ctx, salon, SetNoShowPolicyRequest{After: &two})

	require.NoError(t, err)
	assert.Empty(t, rec.Events)
}
