//go:build dbtest

package promo

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func TestSalonTimezone_ActiveStoreFirst_ElseBeirut(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	var owner, salon uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('Owner',$1,'x','artist') RETURNING id`, uuid.NewString()+"@promo.local").Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id,name) VALUES ($1,'Promo') RETURNING id`,
		owner).Scan(&salon))

	tz, err := repo.SalonTimezone(ctx, salon)
	require.NoError(t, err)
	assert.Equal(t, "Asia/Beirut", tz, "no store yet: the zone its first store will get")

	// The older store is closed; the active one decides.
	_, err = pool.Exec(ctx, `INSERT INTO stores (salon_id,name,city,country,timezone,is_active,created_at)
		VALUES ($1,'Old','Paris','FR','Europe/Paris',FALSE,NOW()-INTERVAL '1 year'),
		       ($1,'Main','Dubai','AE','Asia/Dubai',TRUE,NOW())`, salon)
	require.NoError(t, err)

	tz, err = repo.SalonTimezone(ctx, salon)
	require.NoError(t, err)
	assert.Equal(t, "Asia/Dubai", tz)
}
