package audit

import (
	"context"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/caller"
)

// The salon activity vocabulary (2026-10-10).
//
// A salon owner's Activity screen reads audit_events by salon_id and says,
// for each row, who did what. Its wording is keyed on these strings, so they
// are constants: a typo in one write would leave that action showing as an
// unknown code rather than failing anything.
//
// Rows written before this list existed keep their own spellings (invite,
// accept, remove_member, offering.update, ...); the screen knows those too.
const (
	EntityBooking       = "booking"
	EntityArtist        = "artist"
	EntityPaymentMethod = "payment_method"
	EntitySalon         = "salon"
	EntityService       = "service"
	EntityStore         = "store"
	EntityDiscount      = "discount"
	EntityProduct       = "product"
	EntityOrder         = "order"
)

const (
	ActionBookingApprove     = "booking.approve"
	ActionBookingDepositPaid = "booking.deposit_paid"
	ActionBookingConfirm     = "booking.confirm"
	ActionBookingRefunded    = "booking.refunded"
	ActionBookingCancel      = "booking.cancel"
	ActionBookingComplete    = "booking.complete"
	ActionBookingNoShow      = "booking.no_show"
	ActionBookingReschedule  = "booking.reschedule"
	ActionBookingShiftDay    = "booking.shift_day"

	ActionPaymentMethodSave    = "payment_method.save"
	ActionPaymentMethodRetire  = "payment_method.retire"
	ActionPaymentMethodRestore = "payment_method.restore"
	ActionSalonNoShowPolicy    = "salon.no_show_policy"

	ActionServiceCreate = "service.create"
	ActionServiceUpdate = "service.update"
	ActionServiceDelete = "service.delete"

	ActionStoreUpdate       = "store.update"
	ActionStoreHours        = "store.hours"
	ActionStoreClosureAdd   = "store.closure_add"
	ActionStoreClosureClear = "store.closure_remove"

	ActionDiscountCreate = "discount.create"
	ActionDiscountUpdate = "discount.update"

	ActionProductCreate = "product.create"
	ActionProductUpdate = "product.update"

	ActionOrderPaymentConfirmed = "order.payment_confirmed"
	ActionOrderShipped          = "order.shipped"
	ActionOrderDelivered        = "order.delivered"
	ActionOrderCancelled        = "order.cancelled"
)

// WithCaller fills who acted from the request (internal/pkg/caller):
// ActorID, ActorRole, IPAddress, and SalonID when the event has none of its
// own. A booking's salon is the booking's, whoever touched it, so a SalonID
// already set is kept.
//
// Outside a request - a background worker - there is nobody to name, and the
// row says "system" rather than borrowing an identity.
func (e Event) WithCaller(ctx context.Context) Event {
	c, ok := caller.From(ctx)
	if !ok {
		e.ActorRole = "system"
		return e
	}
	id := c.UserID
	e.ActorID = &id
	e.ActorRole = c.Role
	e.IPAddress = c.IP
	if e.SalonID == nil && c.SalonID != nil {
		s := *c.SalonID
		e.SalonID = &s
	}
	return e
}

// Record writes e on behalf of the request's caller and never fails the
// action it describes: a change that happened must not be undone, or
// reported as failed, because its log row could not be written. The same
// posture as every audit write before it (`_ = s.audit.Log(...)`).
//
// l may be nil - a service built without an audit log (most unit tests)
// records nothing.
func Record(ctx context.Context, l Logger, e Event) {
	if l == nil {
		return
	}
	_ = l.Log(ctx, e.WithCaller(ctx))
}

// Logger is the one method a service needs to write the log. Repository
// satisfies it; tests pass a recorder.
type Logger interface {
	Log(ctx context.Context, e Event) error
}

// Changed narrows a before/after pair to the keys that differ, plus the
// naming keys (always) so a row still says WHICH thing changed. Both are nil
// when nothing but the naming keys would remain - a save that changed
// nothing is not activity.
//
// Values are compared with ==, so callers pass comparable values: strings
// for money (decimal.StringFixed), not decimal.Decimal.
func Changed(before, after map[string]any, always ...string) (map[string]any, map[string]any) {
	o, n := map[string]any{}, map[string]any{}
	for k, v := range after {
		if before[k] != v {
			o[k], n[k] = before[k], v
		}
	}
	if len(n) == 0 {
		return nil, nil
	}
	for _, k := range always {
		o[k], n[k] = before[k], after[k]
	}
	return o, n
}
