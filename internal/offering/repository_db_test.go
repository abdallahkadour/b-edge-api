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

	"github.com/abdallahkadour/b-edge-api/internal/artist"
	"github.com/abdallahkadour/b-edge-api/internal/membership"
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
	// Precondition: the row EXISTS before the delete. Without this, an
	// Upsert that silently wrote nothing would leave "not offered" true
	// before Delete ever ran, and this test would pass on a no-op Delete.
	before, err := repo.Get(ctx, f.SalonID, f.OwnerArtist, f.ServiceID)
	require.NoError(t, err)
	require.True(t, before.Offered, "precondition: she offers it before switching it off")

	require.NoError(t, repo.Delete(ctx, f.OwnerArtist, f.ServiceID))
	o, err := repo.Get(ctx, f.SalonID, f.OwnerArtist, f.ServiceID)
	require.NoError(t, err)
	assert.False(t, o.Offered)
}

func TestCreateService_OwnerOffersIt_MemberDoesNot(t *testing.T) {
	// PP-7: a service the owner adds is on for her, off for members.
	pool := testdb.New(t)
	f := newFixture(t, pool)
	member := addMember(t, pool, f.SalonID)
	ctx := context.Background()
	svc := &artist.SalonServiceRecord{ID: uuid.New(), SalonID: f.SalonID, Name: "Lashes",
		DurationMin: 60, Price: decimal.RequireFromString("80"), IsActive: true, DepositDeadlineHours: 48}
	require.NoError(t, artist.NewRepository(pool).CreateService(ctx, svc))

	var owner, mem int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM artist_services WHERE artist_id=$1 AND service_id=$2`,
		f.OwnerArtist, svc.ID).Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM artist_services WHERE artist_id=$1 AND service_id=$2`,
		member, svc.ID).Scan(&mem))
	assert.Equal(t, 1, owner, "the owner must offer the service she just created")
	assert.Equal(t, 0, mem, "a member must switch it on herself")
}

func TestDetachArtist_RemovesHerOfferings(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	member := addMember(t, pool, f.SalonID)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id,service_id) VALUES ($1,$2)`, member, f.ServiceID)
	require.NoError(t, err)

	require.NoError(t, membership.NewRepository(pool).DetachArtist(ctx, f.SalonID, member))

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM artist_services WHERE artist_id=$1`, member).Scan(&n))
	assert.Equal(t, 0, n, "a departed member must not keep offering the salon's services")
}

// TestUpsert_ServiceOfAnotherSalon_WritesNothing_ErrNotFound - artist_services
// may only hold rows for services of the artist's CURRENT salon. The service
// layer checks that through Get first; the statement itself now refuses too,
// so no future caller can write a foreign row by skipping the check.
func TestUpsert_ServiceOfAnotherSalon_WritesNothing_ErrNotFound(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	other := newFixture(t, pool) // a second salon with its own service
	member := addMember(t, pool, f.SalonID)
	repo := NewRepository(pool)
	ctx := context.Background()

	// Positive control: her own salon's service IS written, so the refusal
	// below is the predicate and not a statement that writes nothing ever.
	require.NoError(t, repo.Upsert(ctx, UpsertParams{ArtistID: member, ServiceID: f.ServiceID,
		PriceSet: true, Price: dec("120")}))
	var own int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM artist_services WHERE artist_id=$1 AND service_id=$2`,
		member, f.ServiceID).Scan(&own))
	require.Equal(t, 1, own)

	err := repo.Upsert(ctx, UpsertParams{ArtistID: member, ServiceID: other.ServiceID,
		PriceSet: true, Price: dec("120")})

	assert.ErrorIs(t, err, ErrNotFound)
	var foreign int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM artist_services WHERE artist_id=$1 AND service_id=$2`,
		member, other.ServiceID).Scan(&foreign))
	assert.Equal(t, 0, foreign, "no row for another salon's service")
}

// TestUpsert_StrayForeignRow_NotUpdated_ErrNotFound - the ON CONFLICT half of
// the same rule: a foreign row that somehow already exists is not updated
// either (no row is proposed, so there is no conflict to resolve).
func TestUpsert_StrayForeignRow_NotUpdated_ErrNotFound(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	other := newFixture(t, pool)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id, service_id) VALUES ($1,$2)`,
		f.OwnerArtist, other.ServiceID)
	require.NoError(t, err, "precondition: a stray foreign row exists")

	err = NewRepository(pool).Upsert(ctx, UpsertParams{ArtistID: f.OwnerArtist, ServiceID: other.ServiceID,
		PriceSet: true, Price: dec("999")})

	assert.ErrorIs(t, err, ErrNotFound)
	var price *decimal.Decimal
	require.NoError(t, pool.QueryRow(ctx, `SELECT price FROM artist_services WHERE artist_id=$1 AND service_id=$2`,
		f.OwnerArtist, other.ServiceID).Scan(&price))
	assert.Nil(t, price, "the stray row must not be written through")
}
