//go:build dbtest

package offering

// Constraints of migration 052, executed. Every one of these is a property
// of the SQL - a mock would accept any value.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

type fixture struct {
	SalonID, OwnerArtist, ServiceID uuid.UUID
}

func newFixture(t *testing.T, pool *pgxpool.Pool) fixture {
	t.Helper()
	ctx := context.Background()
	var f fixture
	var owner uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ('Owner',$1,'x','artist') RETURNING id`,
		uuid.NewString()+"@o.local").Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO salons (owner_id, name) VALUES ($1,'Offering Salon') RETURNING id`,
		owner).Scan(&f.SalonID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO artists (user_id, salon_id) VALUES ($1,$2) RETURNING id`,
		owner, f.SalonID).Scan(&f.OwnerArtist))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO services (salon_id, name, duration_min, price, deposit_amount)
		 VALUES ($1,'Bridal',90,150.00,30.00) RETURNING id`, f.SalonID).Scan(&f.ServiceID))
	return f
}

func TestArtistServices_NullPriceMeansSalon_Accepted(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO artist_services (artist_id, service_id) VALUES ($1,$2)`,
		f.OwnerArtist, f.ServiceID)
	assert.NoError(t, err, "a row with no override is how 'use the salon price' is stored")
}

func TestArtistServices_NegativePrice_Refused(t *testing.T) {
	// Asserts the specific CHECK, not just "some error" - a missing table
	// also produces an error, which would make this pass for the wrong
	// reason. Verified to FAIL when artist_services_price_valid is dropped.
	pool := testdb.New(t)
	f := newFixture(t, pool)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO artist_services (artist_id, service_id, price) VALUES ($1,$2,-1)`,
		f.OwnerArtist, f.ServiceID)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23514", pgErr.Code)
	assert.Equal(t, "artist_services_price_valid", pgErr.ConstraintName)
}

func TestArtistServices_NaNDeposit_Refused(t *testing.T) {
	// Postgres accepts 'NaN'::numeric; a stored NaN makes every later read
	// of the row fail. INJ-04 found this on services; it must not reappear.
	// Asserts the specific CHECK, not just "some error". Verified to FAIL
	// when artist_services_deposit_valid is dropped.
	pool := testdb.New(t)
	f := newFixture(t, pool)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO artist_services (artist_id, service_id, deposit_amount) VALUES ($1,$2,'NaN')`,
		f.OwnerArtist, f.ServiceID)
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, "23514", pgErr.Code)
	assert.Equal(t, "artist_services_deposit_valid", pgErr.ConstraintName)
}

func TestArtistServices_OneRowPerArtistPerService(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id, service_id) VALUES ($1,$2)`,
		f.OwnerArtist, f.ServiceID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO artist_services (artist_id, service_id) VALUES ($1,$2)`,
		f.OwnerArtist, f.ServiceID)
	assert.Error(t, err, "a second row would make 'her price' ambiguous")
}

func TestArtistServices_DeletingTheServiceRemovesTheRow(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id, service_id) VALUES ($1,$2)`,
		f.OwnerArtist, f.ServiceID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM services WHERE id=$1`, f.ServiceID)
	require.NoError(t, err)
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM artist_services WHERE service_id=$1`, f.ServiceID).Scan(&n))
	assert.Equal(t, 0, n)
}
