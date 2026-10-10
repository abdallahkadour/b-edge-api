package artist

import (
	"context"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
)

// WithAudit records changes to what the salon shares - the menu, its stores'
// settings, their opening hours and closures - in the activity log
// (2026-10-10). Every artist's prices follow the menu, so a menu price
// changed is everyone's price changed; the owner has to be able to see who
// did it. Nil (the default, and every existing test) records nothing.
func (s *Service) WithAudit(l audit.Logger) *Service {
	s.activity = l
	return s
}

func (s *Service) record(ctx context.Context, salonID uuid.UUID, entity string, id uuid.UUID, action string, before, after map[string]any) {
	e := audit.Event{SalonID: &salonID, EntityType: entity, EntityID: id, Action: action}
	if before != nil {
		e.OldValues = before
	}
	if after != nil {
		e.NewValues = after
	}
	audit.Record(ctx, s.activity, e)
}

// menuFacts is what the activity row keeps about a menu item. Money is a
// fixed two-place string so before and after compare as values.
func menuFacts(m *SalonServiceRecord) map[string]any {
	return map[string]any{
		"name":           m.Name,
		"price":          m.Price.StringFixed(2),
		"deposit_amount": m.DepositAmount.StringFixed(2),
		"duration_min":   m.DurationMin,
		"is_active":      m.IsActive,
	}
}

// storeFacts is the store settings that change what a customer pays or
// when she can book.
func storeFacts(st *Store) map[string]any {
	f := map[string]any{
		"name":                  st.Name,
		"city":                  st.City,
		"same_day_notice_hours": st.SameDayNoticeHours,
		"early_bird_fee":        st.EarlyBirdFee.StringFixed(2),
		"weekday_buffer_min":    st.WeekdayBufferMin,
		"weekend_buffer_min":    st.WeekendBufferMin,
		"early_bird_cutoff":     "",
	}
	if st.EarlyBirdCutoff != nil {
		f["early_bird_cutoff"] = *st.EarlyBirdCutoff
	}
	return f
}
