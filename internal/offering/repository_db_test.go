//go:build dbtest

package offering

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func addMember(t *testing.T, pool *pgxpool.Pool, salonID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var u, a uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('Maya',$1,'x','artist') RETURNING id`, uuid.NewString()+"@m.local").Scan(&u))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO artists (user_id,salon_id) VALUES ($1,$2) RETURNING id`,
		u, salonID).Scan(&a))
	return a
}

func dec(s string) *decimal.Decimal { d := decimal.RequireFromString(s); return &d }

func TestList_ShowsTheWholeMenu_WithOfferedFlag(t *testing.T) {
	// The settings screen needs services she has NOT switched on too, or she
	// has nothing to switch on. LEFT JOIN, not JOIN.
	pool := testdb.New(t)
	f := newFixture(t, pool)
	member := addMember(t, pool, f.SalonID)

	list, err := NewRepository(pool).List(context.Background(), f.SalonID, member)

	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.False(t, list[0].Offered)
	assert.True(t, list[0].EffectivePrice.Equal(decimal.RequireFromString("150")))
}

func TestUpsert_AbsentKeeps_NullClears(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	member := addMember(t, pool, f.SalonID)
	repo := NewRepository(pool)
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, UpsertParams{ArtistID: member, ServiceID: f.ServiceID,
		ActorUserID: uuid.Nil, PriceSet: true, Price: dec("200")}))
	// A second save that does not mention the price must keep it.
	require.NoError(t, repo.Upsert(ctx, UpsertParams{ArtistID: member, ServiceID: f.ServiceID,
		DepositSet: true, Deposit: dec("50")}))
	o, err := repo.Get(ctx, f.SalonID, member, f.ServiceID)
	require.NoError(t, err)
	require.NotNil(t, o.OwnPrice, "an absent price must leave the override alone")
	assert.True(t, o.OwnPrice.Equal(decimal.RequireFromString("200")))

	// Explicit null clears back to the salon's.
	require.NoError(t, repo.Upsert(ctx, UpsertParams{ArtistID: member, ServiceID: f.ServiceID,
		PriceSet: true, Price: nil}))
	o, err = repo.Get(ctx, f.SalonID, member, f.ServiceID)
	require.NoError(t, err)
	assert.Nil(t, o.OwnPrice)
	assert.True(t, o.EffectivePrice.Equal(decimal.RequireFromString("150")))
}

func TestGet_ServiceOfAnotherSalon_ErrNotFound(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	other := newFixture(t, pool) // a second salon with its own service
	_, err := NewRepository(pool).Get(context.Background(), f.SalonID, f.OwnerArtist, other.ServiceID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestDelete_SwitchesItOff(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	repo := NewRepository(pool)
	ctx := context.Background()
	require.NoError(t, repo.Upsert(ctx, UpsertParams{ArtistID: f.OwnerArtist, ServiceID: f.ServiceID}))
	require.NoError(t, repo.Delete(ctx, f.OwnerArtist, f.ServiceID))
	o, err := repo.Get(ctx, f.SalonID, f.OwnerArtist, f.ServiceID)
	require.NoError(t, err)
	assert.False(t, o.Offered)
}
