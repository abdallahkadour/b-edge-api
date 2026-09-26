//go:build dbtest

package discovery

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func TestGetArtistServices_OnlyHerOfferings_AtHerPrice(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var owner, salon, artist, bridal, nails uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('R','r@disc.local','x','artist') RETURNING id`).Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id,name) VALUES ($1,'S') RETURNING id`,
		owner).Scan(&salon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO artists (user_id,salon_id) VALUES ($1,$2) RETURNING id`,
		owner, salon).Scan(&artist))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Bridal',90,100) RETURNING id`, salon).Scan(&bridal))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Nails',45,40) RETURNING id`, salon).Scan(&nails))
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id,service_id,price) VALUES ($1,$2,200)`,
		artist, bridal)
	require.NoError(t, err) // she offers Bridal at 200; she does NOT offer Nails

	rows, err := NewRepository(pool).GetArtistServices(ctx, artist)

	require.NoError(t, err)
	require.Len(t, rows, 1, "a service she has not switched on must not be listed")
	assert.Equal(t, "Bridal", rows[0].Name)
	assert.True(t, rows[0].Price.Equal(decimal.RequireFromString("200")), "got %s", rows[0].Price)
}

// TestGetArtistServices_StrayForeignSalonRow_Excluded - artist_services must
// only hold rows for her CURRENT salon's services, and every write path keeps
// it that way. If a stray row exists anyway (inserted by hand, a future bug),
// the customer's list must not show another salon's service as hers.
func TestGetArtistServices_StrayForeignSalonRow_Excluded(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var owner, salon, artist, bridal, otherOwner, otherSalon, foreign uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('R','r@disc.local','x','artist') RETURNING id`).Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id,name) VALUES ($1,'S') RETURNING id`,
		owner).Scan(&salon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO artists (user_id,salon_id) VALUES ($1,$2) RETURNING id`,
		owner, salon).Scan(&artist))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Bridal',90,100) RETURNING id`, salon).Scan(&bridal))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('O','o@disc.local','x','artist') RETURNING id`).Scan(&otherOwner))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id,name) VALUES ($1,'Other') RETURNING id`,
		otherOwner).Scan(&otherSalon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Foreign',30,5) RETURNING id`, otherSalon).Scan(&foreign))
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id,service_id) VALUES ($1,$2),($1,$3)`,
		artist, bridal, foreign)
	require.NoError(t, err, "precondition: her own row and a stray foreign one both exist")

	rows, err := NewRepository(pool).GetArtistServices(ctx, artist)

	require.NoError(t, err)
	require.Len(t, rows, 1, "only her own salon's service may be listed")
	assert.Equal(t, "Bridal", rows[0].Name)
}
