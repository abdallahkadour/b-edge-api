package audit

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/caller"
)

func TestWithCaller_NamesWhoActed(t *testing.T) {
	salon, user := uuid.New(), uuid.New()
	ctx := caller.With(context.Background(), caller.Caller{UserID: user, Role: "artist", SalonID: &salon, IP: "10.1.2.3"})

	e := Event{EntityType: EntityBooking, Action: ActionBookingApprove}.WithCaller(ctx)

	assert.Equal(t, &user, e.ActorID)
	assert.Equal(t, "artist", e.ActorRole)
	assert.Equal(t, "10.1.2.3", e.IPAddress)
	assert.Equal(t, &salon, e.SalonID, "an event with no salon of its own is the caller's salon's")
}

func TestWithCaller_KeepsTheEventsOwnSalon(t *testing.T) {
	// A booking's salon is the booking's, whoever touched it.
	mine, theBookings := uuid.New(), uuid.New()
	ctx := caller.With(context.Background(), caller.Caller{UserID: uuid.New(), SalonID: &mine})

	e := Event{SalonID: &theBookings}.WithCaller(ctx)

	assert.Equal(t, &theBookings, e.SalonID)
}

func TestWithCaller_NoCaller_IsTheSystem(t *testing.T) {
	e := Event{Action: ActionBookingCancel}.WithCaller(context.Background())

	assert.Nil(t, e.ActorID)
	assert.Equal(t, "system", e.ActorRole)
}

func TestChanged_KeepsOnlyWhatDiffersAndTheNamingKeys(t *testing.T) {
	old := map[string]any{"name": "Bridal", "price": "200.00", "duration_min": 90}
	updated := map[string]any{"name": "Bridal", "price": "250.00", "duration_min": 90}

	o, n := Changed(old, updated, "name")

	assert.Equal(t, map[string]any{"name": "Bridal", "price": "200.00"}, o)
	assert.Equal(t, map[string]any{"name": "Bridal", "price": "250.00"}, n)
}

func TestChanged_NothingChanged_IsEmpty(t *testing.T) {
	same := map[string]any{"name": "Bridal", "price": "200.00"}

	o, n := Changed(same, same, "name")

	assert.Nil(t, o)
	assert.Nil(t, n)
}
