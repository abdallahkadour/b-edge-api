package product

import (
	"context"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
)

// WithAudit records product changes and every shop order's movement -
// payment confirmed, shipped, delivered, cancelled - in the salon's activity
// log (2026-10-10). Nil records nothing.
func (s *Service) WithAudit(l audit.Logger) *Service {
	s.activity = l
	return s
}

// orderActions names each status an order can be moved to.
var orderActions = map[string]string{
	OrderStatusConfirmed: audit.ActionOrderPaymentConfirmed,
	OrderStatusShipped:   audit.ActionOrderShipped,
	OrderStatusDelivered: audit.ActionOrderDelivered,
	OrderStatusCancelled: audit.ActionOrderCancelled,
}

// recordOrder writes one order movement under the ORDER's salon - a
// customer cancelling has no salon of her own.
func (s *Service) recordOrder(ctx context.Context, o *Order, from, to string) {
	action, ok := orderActions[to]
	if !ok {
		return
	}
	salon := o.SalonID
	audit.Record(ctx, s.activity, audit.Event{
		SalonID: &salon, EntityType: audit.EntityOrder, EntityID: o.ID, Action: action,
		OldValues: map[string]any{"status": from},
		NewValues: map[string]any{"status": to, "total_amount": o.TotalAmount.StringFixed(2)},
	})
}

func productFacts(p *Product) map[string]any {
	f := map[string]any{"name": p.Name, "price": p.Price.StringFixed(2), "is_active": p.IsActive, "stock_quantity": -1}
	if p.StockQuantity != nil {
		f["stock_quantity"] = *p.StockQuantity
	}
	return f
}
