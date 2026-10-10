package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

// A customer's saved artists (migration 056, 2026-10-09). The PRD's launch
// list asked for customer profiles with history, favourites and refund
// status; favourites never shipped. The list reads through the same card
// query as Discover, so an artist hidden from Discover is hidden here too.

func favCode(t *testing.T, err error) string {
	t.Helper()
	var appErr *apperror.AppError
	require.True(t, errors.As(err, &appErr), "want an AppError, got %v", err)
	return appErr.Code
}

func TestListFavourites_ReadsHerFavouritesThroughTheDiscoverQuery(t *testing.T) {
	me := uuid.New()
	repo := &mockRepo{}
	svc := newTestService(repo, time.Now())

	_, err := svc.ListFavourites(context.Background(), me)

	require.NoError(t, err)
	require.NotNil(t, repo.lastListParams.FavouritesOf)
	assert.Equal(t, me, *repo.lastListParams.FavouritesOf)
}

func TestListFavourites_AnArtistInTwoCities_IsListedOnce(t *testing.T) {
	a := uuid.New()
	repo := &mockRepo{listCards: []*ArtistCardRow{
		{ID: a, Name: "Rania", City: "Beirut"},
		{ID: a, Name: "Rania", City: "Tripoli"},
		{ID: uuid.New(), Name: "Lina", City: "Byblos"},
	}}
	svc := newTestService(repo, time.Now())

	cards, err := svc.ListFavourites(context.Background(), uuid.New())

	require.NoError(t, err)
	assert.Len(t, cards, 2, "Discover lists an artist once per city; a list of favourites lists her once")
	assert.Equal(t, "Beirut", cards[0].City)
}

func TestAddFavourite_AnArtistThatDoesNotExist_ReadsAsNotFound(t *testing.T) {
	repo := &mockRepo{addFavouriteErr: ErrArtistNotFound}
	svc := newTestService(repo, time.Now())

	err := svc.AddFavourite(context.Background(), uuid.New(), uuid.New())

	assert.Equal(t, "ARTIST_NOT_FOUND", favCode(t, err))
}

func TestAddAndRemoveFavourite_PassTheCustomerAndTheArtist(t *testing.T) {
	me, artist := uuid.New(), uuid.New()
	repo := &mockRepo{}
	svc := newTestService(repo, time.Now())

	require.NoError(t, svc.AddFavourite(context.Background(), me, artist))
	assert.Equal(t, [2]uuid.UUID{me, artist}, repo.addedFavourite)
	require.NoError(t, svc.RemoveFavourite(context.Background(), me, artist))
	assert.Equal(t, [2]uuid.UUID{me, artist}, repo.removedFavourite)
}
