//go:build dbtest

package artist

// Repository tests against a real PostgreSQL database.
//
// The subject is UpdateArtistProfile's presence/value SQL:
//
//	bio = CASE WHEN $2 THEN $3 ELSE bio END
//
// This replaced COALESCE($2, bio), and the difference is not stylistic. A
// pointer cannot tell an ABSENT key from an explicit null, and COALESCE reads
// NULL as "keep what is there" - so {"bio": null} returned 200 and wrote
// nothing, and no nullable column could ever be emptied through the API. The
// user saw a successful save and their old bio still on the page.
//
// A MOCK CANNOT TEST THIS. The whole bug lives in how Postgres evaluates the
// expression; a mock repository would simply record whatever the service
// passed it and agree with either version. The service-layer suite was green
// throughout the entire period the bug existed.
//
// Three properties, and the second and third are the ones with teeth:
//
//   - a value is written
//   - an ABSENT field leaves the column alone
//   - a PRESENT-BUT-EMPTY field clears the column to NULL
//
// Run with: make test-db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/optional"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func newArtistFixture(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	ctx := context.Background()

	var userID, salonID, artistID uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ('DB Test','artist@dbtest.local','x','artist') RETURNING id`).Scan(&userID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO salons (owner_id, name) VALUES ($1,'DB Test Salon') RETURNING id`,
		userID).Scan(&salonID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO artists (user_id, salon_id) VALUES ($1,$2) RETURNING id`,
		userID, salonID).Scan(&artistID))
	return artistID
}

func readBio(t *testing.T, pool *pgxpool.Pool, artistID uuid.UUID) *string {
	t.Helper()
	var bio *string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT bio FROM artists WHERE id = $1`, artistID).Scan(&bio))
	return bio
}

func TestUpdateArtistProfile_SetsAValue(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	artistID := newArtistFixture(t, pool)

	require.NoError(t, repo.UpdateArtistProfile(context.Background(), artistID,
		UpdateProfileRequest{Bio: optional.From("Bridal specialist")}))

	bio := readBio(t, pool, artistID)
	require.NotNil(t, bio, "bio must have been written")
	assert.Equal(t, "Bridal specialist", *bio)
}

func TestUpdateArtistProfile_AbsentField_LeavesColumnUnchanged(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	artistID := newArtistFixture(t, pool)

	// Establish the starting condition explicitly. Asserting "unchanged"
	// against a column that was NULL to begin with would pass no matter what
	// the SQL did.
	require.NoError(t, repo.UpdateArtistProfile(ctx, artistID,
		UpdateProfileRequest{Bio: optional.From("Bridal specialist")}))
	require.NotNil(t, readBio(t, pool, artistID), "precondition: bio is set")

	// An update that does not mention bio at all.
	require.NoError(t, repo.UpdateArtistProfile(ctx, artistID,
		UpdateProfileRequest{Instagram: optional.From("@bedge")}))

	bio := readBio(t, pool, artistID)
	require.NotNil(t, bio, "an absent field must not clear the column")
	assert.Equal(t, "Bridal specialist", *bio)
}

func TestUpdateArtistProfile_PresentButEmpty_ClearsColumnToNull(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	artistID := newArtistFixture(t, pool)

	require.NoError(t, repo.UpdateArtistProfile(ctx, artistID,
		UpdateProfileRequest{Bio: optional.From("Bridal specialist")}))
	require.NotNil(t, readBio(t, pool, artistID), "precondition: bio is set")

	// This is the case COALESCE got wrong: the user explicitly emptied the
	// field. Under the old SQL this returned 200 and changed nothing.
	require.NoError(t, repo.UpdateArtistProfile(ctx, artistID,
		UpdateProfileRequest{Bio: optional.From("")}))

	assert.Nil(t, readBio(t, pool, artistID),
		`an explicitly emptied field must clear the column to NULL`)
}

func TestUpdateArtistProfile_ExplicitNull_ClearsColumnToNull(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	artistID := newArtistFixture(t, pool)

	require.NoError(t, repo.UpdateArtistProfile(ctx, artistID,
		UpdateProfileRequest{Bio: optional.From("Bridal specialist")}))
	require.NotNil(t, readBio(t, pool, artistID), "precondition: bio is set")

	// The other spelling of empty: {"bio": null} rather than {"bio": ""}.
	// optional.Text folds both to NULL precisely so two spellings of the same
	// intent cannot reach the database as different values - this asserts the
	// fold actually happens rather than trusting the comment that says it does.
	require.NoError(t, repo.UpdateArtistProfile(ctx, artistID,
		UpdateProfileRequest{Bio: optional.Null[string]()}))

	assert.Nil(t, readBio(t, pool, artistID),
		"an explicit JSON null must clear the column to NULL")
}

func TestGetOfferedServicesByArtist_OnlyHerOfferings_AtHerPrice(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	artistID := newArtistFixture(t, pool)
	var salon, bridal, nails uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT salon_id FROM artists WHERE id=$1`, artistID).Scan(&salon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Bridal',90,100) RETURNING id`, salon).Scan(&bridal))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Nails',45,40) RETURNING id`, salon).Scan(&nails))
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id,service_id,price) VALUES ($1,$2,200)`,
		artistID, bridal)
	require.NoError(t, err)

	recs, err := NewRepository(pool).GetOfferedServicesByArtist(ctx, artistID)

	require.NoError(t, err)
	require.Len(t, recs, 1, "the owner's menu has two services; the customer must see only hers")
	assert.Equal(t, "Bridal", recs[0].Name)
	assert.True(t, recs[0].Price.Equal(decimal.RequireFromString("200")), "got %s", recs[0].Price)
}

// TestGetOfferedServicesByArtist_CheapestFirst - spec §6: "Her profile lists
// only services she has switched on, at her price and deposit, cheapest
// first." Name order and price order deliberately DISAGREE here ("Acrylic"
// sorts before "Bridal" alphabetically, but Bridal is her cheaper service),
// so an ORDER BY name would pass this test for the wrong reason if it
// happened to also be cheapest; picking names where the two orders conflict
// is what makes the assertion mean anything.
func TestGetOfferedServicesByArtist_CheapestFirst(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	artistID := newArtistFixture(t, pool)
	var salon, acrylic, bridal uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT salon_id FROM artists WHERE id=$1`, artistID).Scan(&salon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Acrylic',60,100) RETURNING id`, salon).Scan(&acrylic))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Bridal',90,100) RETURNING id`, salon).Scan(&bridal))
	// She prices Acrylic at 90 and Bridal at 40 - alphabetical order and
	// price order disagree.
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id,service_id,price) VALUES ($1,$2,90)`,
		artistID, acrylic)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO artist_services (artist_id,service_id,price) VALUES ($1,$2,40)`,
		artistID, bridal)
	require.NoError(t, err)

	recs, err := NewRepository(pool).GetOfferedServicesByArtist(ctx, artistID)

	require.NoError(t, err)
	require.Len(t, recs, 2)
	assert.Equal(t, "Bridal", recs[0].Name, "her cheaper service must lead, even though it sorts second by name")
	assert.True(t, recs[0].Price.Equal(decimal.RequireFromString("40")), "got %s", recs[0].Price)
	assert.Equal(t, "Acrylic", recs[1].Name)
}
