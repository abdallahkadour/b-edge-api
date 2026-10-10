//go:build dbtest

package discovery

// Favourites on a real clone: the join to Discover's card query (so the
// visibility rule is the same one), saving twice, and removing.
//
// Run with: make test-db

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func TestFavourites_OnlyHers_AndOnlyWhoDiscoverWouldShow(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	repo := NewRepository(pool)

	var me, someoneElse uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('Me','me@fav.local','x','customer') RETURNING id`).Scan(&me))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('Other','other@fav.local','x','customer') RETURNING id`).Scan(&someoneElse))
	liked := linkedArtist(t, pool, "Liked Artist")
	hidden := linkedArtist(t, pool, "Hidden Artist")
	notLiked := linkedArtist(t, pool, "Not Liked")
	theirs := linkedArtist(t, pool, "Their Favourite")

	require.NoError(t, repo.AddFavourite(ctx, me, liked))
	require.NoError(t, repo.AddFavourite(ctx, me, liked), "saving twice is not an error")
	require.NoError(t, repo.AddFavourite(ctx, me, hidden))
	require.NoError(t, repo.AddFavourite(ctx, someoneElse, theirs))
	// Discover shows only status 'active'; 'rejected' is one it hides.
	_, err := pool.Exec(ctx, `UPDATE artists SET status = 'rejected' WHERE id = $1`, hidden)
	require.NoError(t, err)

	cards, err := repo.ListArtistCards(ctx, ListArtistCardsParams{FavouritesOf: &me, Limit: 50})
	require.NoError(t, err)
	ids := make([]uuid.UUID, 0, len(cards))
	for _, c := range cards {
		ids = append(ids, c.ID)
	}
	assert.Equal(t, []uuid.UUID{liked}, ids,
		"hers only; the rejected one is hidden by Discover's own rule; not someone else's; not one she never saved")
	_ = notLiked

	var rows int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM customer_favourite_artists WHERE customer_id = $1`, me).Scan(&rows))
	assert.Equal(t, 2, rows, "the hidden one is kept, to reappear if she comes back")

	require.NoError(t, repo.RemoveFavourite(ctx, me, liked))
	require.NoError(t, repo.RemoveFavourite(ctx, me, liked), "removing twice is not an error")
	cards, err = repo.ListArtistCards(ctx, ListArtistCardsParams{FavouritesOf: &me, Limit: 50})
	require.NoError(t, err)
	assert.Empty(t, cards)
}

func TestAddFavourite_AnArtistThatDoesNotExist_IsErrArtistNotFound(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	repo := NewRepository(pool)
	var me uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('Me','me2@fav.local','x','customer') RETURNING id`).Scan(&me))

	err := repo.AddFavourite(ctx, me, uuid.New())

	assert.ErrorIs(t, err, ErrArtistNotFound)
}
