//go:build dbtest

package inbox

// E2E 28.13 - notifications bundle instead of piling up.
//
// Bundling is one INSERT ... ON CONFLICT against migration 030's partial
// unique index, so it lives entirely in SQL: a mock repository agrees with
// any count it is handed. Until now it was checked only by E2E journey 13.1,
// which runs the same statement through psql. These run the repository's
// own Create on a real clone. Watched failing with `item_count + 1` changed
// to `item_count` (both counting tests went red; the read-then-fresh-row
// test stayed green, as it should - it is about the index, not the count).
//
// Run with: make test-db

import (
	"context"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func inboxUser(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	var id uuid.UUID
	require.NoError(t, pool.QueryRow(context.Background(), `INSERT INTO users (name, email, password_hash, role)
		VALUES ('Artist',$1,'x','artist') RETURNING id`, uuid.NewString()+"@inboxtest.local").Scan(&id))
	return id
}

func bundled(user uuid.UUID) CreateParams {
	key := "delivery_failed"
	return CreateParams{UserID: user, Kind: KindDeliveryFailed, Level: LevelActionRequired,
		Title: "A customer could not be notified", GroupKey: &key}
}

func unread(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) (rows, count int) {
	t.Helper()
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*), COALESCE(sum(item_count),0) FROM user_notifications
		 WHERE user_id = $1 AND read_at IS NULL`, user).Scan(&rows, &count))
	return
}

func TestCreate_SameGroupAHundredTimes_OneRowCountingEvery(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	user := inboxUser(t, pool)

	for i := 0; i < 100; i++ {
		require.NoError(t, repo.Create(context.Background(), bundled(user)))
	}

	rows, count := unread(t, pool, user)
	assert.Equal(t, 1, rows)
	assert.Equal(t, 100, count)
}

func TestCreate_SameGroupTwentyAtOnce_OneRowCountingEvery(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	user := inboxUser(t, pool)

	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- repo.Create(context.Background(), bundled(user))
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err, "a concurrent insert into the same bundle must not collide")
	}

	rows, count := unread(t, pool, user)
	assert.Equal(t, 1, rows)
	assert.Equal(t, 20, count)
}

func TestCreate_AfterTheBundleIsRead_StartsAFreshRow(t *testing.T) {
	pool := testdb.New(t)
	repo := NewRepository(pool)
	user := inboxUser(t, pool)

	require.NoError(t, repo.Create(context.Background(), bundled(user)))
	_, err := pool.Exec(context.Background(), `UPDATE user_notifications SET read_at = NOW() WHERE user_id = $1`, user)
	require.NoError(t, err)
	require.NoError(t, repo.Create(context.Background(), bundled(user)))

	rows, count := unread(t, pool, user)
	assert.Equal(t, 1, rows, "something already dealt with is not resurrected")
	assert.Equal(t, 1, count)
}
