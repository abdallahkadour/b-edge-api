//go:build dbtest

package onboarding

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func TestComplete_OwnerOffersHerFirstService(t *testing.T) {
	// Without this row a newly onboarded artist has an empty menu and cannot
	// be booked at all - the same failure migration 049 fixed for hours.
	pool := testdb.New(t)
	ctx := context.Background()
	var user uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('New','new@onb.local','x','artist') RETURNING id`).Scan(&user))

	artistID, err := NewRepository(pool).Complete(ctx, user, CompleteOnboardingRequest{
		ArtistProfile:      ArtistProfile{Handle: "newartist", Category: "makeup"},
		SalonName:          "New Salon",
		StoreName:          "Main",
		City:               "Beirut",
		ServiceName:        "Makeup",
		ServiceDurationMin: 60,
		ServicePrice:       "80.00",
	})
	require.NoError(t, err)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM artist_services os JOIN services s ON s.id = os.service_id
		 WHERE os.artist_id = $1 AND s.name = 'Makeup'`, artistID).Scan(&n))
	assert.Equal(t, 1, n)
}
