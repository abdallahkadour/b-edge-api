package caller

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
)

func TestFrom_NoCaller_IsNotOK(t *testing.T) {
	_, ok := From(context.Background())
	assert.False(t, ok, "a background job or a test context has nobody calling")
}

func TestFrom_ReturnsWhatWithStored(t *testing.T) {
	salon := uuid.New()
	want := Caller{UserID: uuid.New(), Role: "artist", SalonID: &salon, SalonRole: salonrole.Owner, IP: "10.0.0.7"}

	got, ok := From(With(context.Background(), want))

	assert.True(t, ok)
	assert.Equal(t, want, got)
}

func TestInSalon_OnlyTheSameSalon(t *testing.T) {
	salon, other := uuid.New(), uuid.New()
	c := Caller{SalonID: &salon}

	assert.True(t, c.InSalon(salon))
	assert.False(t, c.InSalon(other))
	assert.False(t, Caller{}.InSalon(salon), "no salon is in no salon")
}
