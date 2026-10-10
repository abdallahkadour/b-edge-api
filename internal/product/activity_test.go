package product

// Shop orders move money and stock, and product prices are the salon's, so
// both are written to the activity log (2026-10-10).

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

func salonCtx(salon uuid.UUID) (context.Context, uuid.UUID) {
	user := uuid.New()
	return caller.With(context.Background(), caller.Caller{UserID: user, Role: "artist", SalonID: &salon}), user
}

func shopOrder(salon uuid.UUID, status string) *Order {
	return &Order{ID: uuid.New(), SalonID: salon, CustomerID: uuid.New(), Status: status, TotalAmount: decimal.RequireFromString("45.50")}
}

func TestShipOrder_RecordsWhoShippedIt(t *testing.T) {
	salon := uuid.New()
	ctx, user := salonCtx(salon)
	o := shopOrder(salon, OrderStatusConfirmed)
	rec := &audittest.Recorder{}

	_, err := newTestService(&mockRepo{order: o}).WithAudit(rec).ShipOrder(ctx, o.ID, salon)

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	e := rec.Events[0]
	assert.Equal(t, audit.ActionOrderShipped, e.Action)
	assert.Equal(t, audit.EntityOrder, e.EntityType)
	assert.Equal(t, o.ID, e.EntityID)
	assert.Equal(t, &salon, e.SalonID)
	assert.Equal(t, &user, e.ActorID)
	assert.Equal(t, map[string]any{"status": OrderStatusConfirmed}, e.OldValues)
	assert.Equal(t, map[string]any{"status": OrderStatusShipped, "total_amount": "45.50"}, e.NewValues)
}

func TestConfirmAndDeliver_AreRecorded(t *testing.T) {
	salon := uuid.New()
	ctx, _ := salonCtx(salon)
	rec := &audittest.Recorder{}
	svc := newTestService(&mockRepo{order: shopOrder(salon, OrderStatusPlaced)}).WithAudit(rec)

	_, err := svc.ConfirmOrderPayment(ctx, uuid.New(), salon, ConfirmOrderPaymentRequest{})
	require.NoError(t, err)
	_, err = svc.DeliverOrder(ctx, uuid.New(), salon)
	require.NoError(t, err)

	require.Len(t, rec.Events, 2)
	assert.Equal(t, audit.ActionOrderPaymentConfirmed, rec.Events[0].Action)
	assert.Equal(t, audit.ActionOrderDelivered, rec.Events[1].Action)
}

func TestShipOrder_AnotherSalonsOrder_RecordsNothing(t *testing.T) {
	salon := uuid.New()
	ctx, _ := salonCtx(salon)
	rec := &audittest.Recorder{}

	_, err := newTestService(&mockRepo{order: shopOrder(uuid.New(), OrderStatusConfirmed)}).WithAudit(rec).ShipOrder(ctx, uuid.New(), salon)

	require.Error(t, err)
	assert.Empty(t, rec.Events)
}

func TestCancelOrder_ByTheCustomer_IsRecordedUnderTheOrdersSalon(t *testing.T) {
	salon := uuid.New()
	o := shopOrder(salon, OrderStatusPlaced)
	rec := &audittest.Recorder{}
	ctx := caller.With(context.Background(), caller.Caller{UserID: o.CustomerID, Role: "customer"})

	_, err := newTestService(&mockRepo{order: o}).WithAudit(rec).CancelOrder(ctx, o.ID, o.CustomerID, "customer", nil, CancelOrderRequest{})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	e := rec.Events[0]
	assert.Equal(t, audit.ActionOrderCancelled, e.Action)
	assert.Equal(t, &salon, e.SalonID, "a customer has no salon; the order's salon is the one that needs to know")
	assert.Equal(t, "customer", e.ActorRole)
}

func TestUpdateProduct_RecordsThePriceChange(t *testing.T) {
	salon := uuid.New()
	ctx, _ := salonCtx(salon)
	before := &Product{ID: uuid.New(), SalonID: salon, Name: "Lip oil", Price: decimal.RequireFromString("18"), IsActive: true}
	after := *before
	after.Price = decimal.RequireFromString("22")
	rec := &audittest.Recorder{}
	price := "22"

	_, err := newTestService(&productSeqRepo{mockRepo: &mockRepo{}, seq: []*Product{before, &after}}).
		WithAudit(rec).UpdateProduct(ctx, before.ID, salon, UpdateProductRequest{Price: &price})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	assert.Equal(t, audit.ActionProductUpdate, rec.Events[0].Action)
	assert.Equal(t, map[string]any{"name": "Lip oil", "price": "18.00"}, rec.Events[0].OldValues)
	assert.Equal(t, map[string]any{"name": "Lip oil", "price": "22.00"}, rec.Events[0].NewValues)
}

func TestCreateProduct_RecordsIt(t *testing.T) {
	salon := uuid.New()
	ctx, _ := salonCtx(salon)
	rec := &audittest.Recorder{}

	out, err := newTestService(&mockRepo{}).WithAudit(rec).CreateProduct(ctx, salon, CreateProductRequest{Name: "Lip oil", Price: "18"})

	require.NoError(t, err)
	require.Len(t, rec.Events, 1)
	assert.Equal(t, audit.ActionProductCreate, rec.Events[0].Action)
	assert.Equal(t, out.ID, rec.Events[0].EntityID)
}

type productSeqRepo struct {
	*mockRepo
	seq []*Product
}

func (r *productSeqRepo) GetProductByID(_ context.Context, _ uuid.UUID) (*Product, error) {
	p := r.seq[0]
	if len(r.seq) > 1 {
		r.seq = r.seq[1:]
	}
	return p, nil
}
