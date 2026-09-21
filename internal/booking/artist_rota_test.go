package booking

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/schedule"
)

// Mock implementations of the per-artist rota reads.
//
// Both default to nil, which is what every artist on the platform returns:
// no declared rota, therefore no personal restriction, therefore the store's
// window applies unchanged. Defaulting them to nil rather than to an empty
// struct is what keeps every pre-existing slot test meaningful - an
// accidental zero-valued DayRota would read as "works 00:00 to 00:00" and
// silently empty every calendar in the suite.
func (m *mockRepo) GetArtistRotaDay(_ context.Context, _, _ uuid.UUID, dow int) (*schedule.DayRota, error) {
	if m.rotaDayErr != nil {
		return nil, m.rotaDayErr
	}
	if m.rotaDays == nil {
		return nil, nil
	}
	return m.rotaDays[dow], nil
}

func (m *mockRepo) GetArtistRotaException(_ context.Context, _, _ uuid.UUID,
	_ time.Time) (*schedule.DayException, error) {
	return m.rotaException, m.rotaExceptionErr
}
