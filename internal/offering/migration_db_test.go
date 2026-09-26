//go:build dbtest

package offering

// Constraints of migration 052, executed. Every one of these is a property
// of the SQL - a mock would accept any value.

import (
	"context"
	"os"
	"path/filepath"
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

// ── The backfill, executed ──────────────────────────────────────────────
//
// testdb's template runs 052 against an EMPTY database, where the backfill
// INSERT has nothing to copy - so until this test the backfill had only
// ever been measured by hand on a dump of dev (22 pairs, 22 rows). Here the
// real migration files run against data: down, then up, then down and up
// again, asserting after each up that every artist got a row for every
// service of HER salon, active and inactive, prices NULL - and nothing else.
//
// Verified to FAIL: with the backfill narrowed to active services
// (AND s.is_active), and with the backfill INSERT deleted.

func migrationSQL(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "db", "migrations", name))
	require.NoError(t, err)
	return string(b)
}

// backfillCounts returns (pairs an artist's salon offers, rows present,
// pairs missing, rows that are NOT a same-salon pair, rows with a price).
func backfillCounts(t *testing.T, pool *pgxpool.Pool) (pairs, rows, missing, foreign, priced int) {
	t.Helper()
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT (SELECT count(*) FROM artists a JOIN services s ON s.salon_id = a.salon_id),
		       (SELECT count(*) FROM artist_services),
		       (SELECT count(*) FROM artists a JOIN services s ON s.salon_id = a.salon_id
		         WHERE NOT EXISTS (SELECT 1 FROM artist_services os
		                            WHERE os.artist_id = a.id AND os.service_id = s.id)),
		       (SELECT count(*) FROM artist_services os
		          JOIN artists a ON a.id = os.artist_id
		          JOIN services s ON s.id = os.service_id
		         WHERE a.salon_id IS DISTINCT FROM s.salon_id),
		       (SELECT count(*) FROM artist_services WHERE price IS NOT NULL OR deposit_amount IS NOT NULL)`,
	).Scan(&pairs, &rows, &missing, &foreign, &priced))
	return
}

func TestMigration052_DownUpDownUp_BackfillsEveryArtistEveryServiceOfHerSalon(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	a := newFixture(t, pool) // salon A: owner + active Bridal
	b := newFixture(t, pool) // salon B: its own owner + service
	member := addMember(t, pool, a.SalonID)
	var inactive, loner uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id, name, duration_min, price, is_active)
		VALUES ($1,'Retired',30,20,false) RETURNING id`, a.SalonID).Scan(&inactive))
	var lonerUser uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('Loner',$1,'x','artist') RETURNING id`, uuid.NewString()+"@l.local").Scan(&lonerUser))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO artists (user_id, salon_id) VALUES ($1, NULL) RETURNING id`,
		lonerUser).Scan(&loner))

	has := func(artist, service uuid.UUID) bool {
		var ok bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM artist_services
			WHERE artist_id=$1 AND service_id=$2)`, artist, service).Scan(&ok))
		return ok
	}

	for round := 1; round <= 2; round++ {
		_, err := pool.Exec(ctx, migrationSQL(t, "052_artist_services.down.sql"))
		require.NoError(t, err, "round %d: down", round)
		var gone bool
		require.NoError(t, pool.QueryRow(ctx, `SELECT to_regclass('artist_services') IS NULL`).Scan(&gone))
		require.True(t, gone, "round %d: down must drop the table", round)

		_, err = pool.Exec(ctx, migrationSQL(t, "052_artist_services.up.sql"))
		require.NoError(t, err, "round %d: up", round)

		pairs, rows, missing, foreign, priced := backfillCounts(t, pool)
		require.GreaterOrEqual(t, pairs, 5, "round %d: precondition - the fixture's 5 pairs are there to backfill", round)
		assert.Equal(t, pairs, rows, "round %d: one row per artist per service of her salon", round)
		assert.Equal(t, 0, missing, "round %d: nobody disappears", round)
		assert.Equal(t, 0, foreign, "round %d: no row for another salon's service", round)
		assert.Equal(t, 0, priced, "round %d: every backfilled row follows the salon's price", round)

		assert.True(t, has(a.OwnerArtist, a.ServiceID), "round %d: owner A x Bridal", round)
		assert.True(t, has(a.OwnerArtist, inactive), "round %d: owner A x an INACTIVE service", round)
		assert.True(t, has(member, a.ServiceID), "round %d: member A x Bridal", round)
		assert.True(t, has(member, inactive), "round %d: member A x an INACTIVE service", round)
		assert.True(t, has(b.OwnerArtist, b.ServiceID), "round %d: owner B x her own service", round)
		assert.False(t, has(member, b.ServiceID), "round %d: member A x salon B's service", round)
		var lonerRows int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM artist_services WHERE artist_id=$1`,
			loner).Scan(&lonerRows))
		assert.Equal(t, 0, lonerRows, "round %d: an artist with no salon offers nothing", round)
	}
}
