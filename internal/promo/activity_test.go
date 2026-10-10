package promo

// A discount code takes money off the salon's prices, so creating one or
// changing one is written to the activity log (2026-10-10).

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

func ownerIn(salon uuid.UUID) context.Context {
	return caller.With(context.Background(), caller.Caller{UserID: uuid.New(), Role: "artist", SalonID: &salon})
}

func TestCreate_RecordsTheCode(t *testing.T) {
	salon := uuid.New()
	rec := &audittest.Recorder{}

	_, err := newSvc(&mockRepo{}).WithAudit(rec).Create(ownerIn(salon), salon,
		CreateDiscountRequest{Code: "eid20", Kind: "percentage", Value: "20"})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	e := rec.Events[0]
	assert.Equal(t, audit.ActionDiscountCreate, e.Action)
	assert.Equal(t, audit.EntityDiscount, e.EntityType)
	assert.Equal(t, &salon, e.SalonID)
	facts := e.NewValues.(map[string]any)
	assert.Equal(t, "EID20", facts["code"])
	assert.Equal(t, "20.00", facts["value"])
}

func TestUpdate_TurningACodeOff_RecordsTheChange(t *testing.T) {
	salon := uuid.New()
	before := &Discount{ID: uuid.New(), SalonID: salon, Code: "EID20", Kind: "percentage", Value: decimal.NewFromInt(20), IsActive: true}
	after := *before
	after.IsActive = false
	rec := &audittest.Recorder{}
	off := false

	_, err := newSvc(&mockRepo{byCode: before, updated: &after}).WithAudit(rec).Update(ownerIn(salon), before.ID, salon,
		UpdateDiscountRequest{IsActive: &off})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	assert.Equal(t, audit.ActionDiscountUpdate, rec.Events[0].Action)
	assert.Equal(t, map[string]any{"code": "EID20", "is_active": true}, rec.Events[0].OldValues)
	assert.Equal(t, map[string]any{"code": "EID20", "is_active": false}, rec.Events[0].NewValues)
}

func TestUpdate_AnotherSalonsCode_RecordsNothing(t *testing.T) {
	salon := uuid.New()
	rec := &audittest.Recorder{}
	off := false

	_, err := newSvc(&mockRepo{updateErr: ErrNotFound}).WithAudit(rec).Update(ownerIn(salon), uuid.New(), salon,
		UpdateDiscountRequest{IsActive: &off})

	require.Error(t, err)
	assert.Empty(t, rec.Events)
}
