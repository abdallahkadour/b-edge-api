//go:build dbtest

package pricing

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

// resolve inserts one salon service (price 150, deposit 30) and optionally an
// override row, then evaluates the three expressions exactly as callers do.
func resolve(t *testing.T, own *[2]*string) (price, deposit decimal.Decimal, capped bool) {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	var owner, salon, artist, svc uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('P','p@pricing.local','x','artist') RETURNING id`).Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id,name) VALUES ($1,'S') RETURNING id`,
		owner).Scan(&salon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO artists (user_id,salon_id) VALUES ($1,$2) RETURNING id`,
		owner, salon).Scan(&artist))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price,deposit_amount)
		VALUES ($1,'Bridal',90,150.00,30.00) RETURNING id`, salon).Scan(&svc))
	if own != nil {
		_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id,service_id,price,deposit_amount)
			VALUES ($1,$2,$3::numeric,$4::numeric)`, artist, svc, own[0], own[1])
		require.NoError(t, err)
	}
	q := "SELECT " + Price("s", "os") + ", " + Deposit("s", "os") + ", " + DepositCapped("s", "os") +
		" FROM services s LEFT JOIN artist_services os ON os.service_id = s.id AND os.artist_id = $1 WHERE s.id = $2"
	require.NoError(t, pool.QueryRow(ctx, q, artist, svc).Scan(&price, &deposit, &capped))
	return
}

func str(s string) *string { return &s }

func TestResolve_NoRow_SalonValues(t *testing.T) {
	p, d, c := resolve(t, nil)
	assert.True(t, p.Equal(decimal.RequireFromString("150")), "got %s", p)
	assert.True(t, d.Equal(decimal.RequireFromString("30")), "got %s", d)
	assert.False(t, c)
}

func TestResolve_Override_Wins(t *testing.T) {
	p, d, _ := resolve(t, &[2]*string{str("200"), str("60")})
	assert.True(t, p.Equal(decimal.RequireFromString("200")), "got %s", p)
	assert.True(t, d.Equal(decimal.RequireFromString("60")), "got %s", d)
}

func TestResolve_BlankOverride_FallsBackToSalon(t *testing.T) {
	p, d, _ := resolve(t, &[2]*string{nil, nil})
	assert.True(t, p.Equal(decimal.RequireFromString("150")), "got %s", p)
	assert.True(t, d.Equal(decimal.RequireFromString("30")), "got %s", d)
}

func TestResolve_SalonDepositAboveHerPrice_Capped(t *testing.T) {
	// Her price 25 with a blank deposit, under the salon's 30.
	p, d, c := resolve(t, &[2]*string{str("25"), nil})
	assert.True(t, p.Equal(decimal.RequireFromString("25")))
	assert.True(t, d.Equal(decimal.RequireFromString("25")), "deposit must be capped at the price, got %s", d)
	assert.True(t, c, "the cap must be reported, not silent")
}
