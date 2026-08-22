// Package audit tests.
//
// pgRepo.Log's actual INSERT can't be unit tested without a real Postgres
// connection - this codebase deliberately has no bedge_test database (see
// CLAUDE-v6.md). What IS unit-testable without a database: the JSON-marshal
// error paths, which return before ever touching r.db, and NopRepository.
// A nil *pgxpool.Pool is used deliberately in the marshal-failure tests -
// if either test ever reached r.db.Exec, it would panic on the nil pool,
// which is exactly the signal that the early-return guard broke.
package audit

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unmarshalable is any value json.Marshal rejects - a channel, in this case.
type unmarshalable struct {
	C chan int
}

func TestPgRepoLog_OldValuesUnmarshalable_ReturnsErrorWithoutTouchingDB(t *testing.T) {
	repo := &pgRepo{db: nil}

	err := repo.Log(context.Background(), Event{
		ActorRole:  "admin",
		EntityType: "artist",
		EntityID:   uuid.New(),
		Action:     "approved",
		OldValues:  unmarshalable{C: make(chan int)},
	})

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "marshal old_values"), "got: %v", err)
}

func TestPgRepoLog_NewValuesUnmarshalable_ReturnsErrorWithoutTouchingDB(t *testing.T) {
	repo := &pgRepo{db: nil}

	err := repo.Log(context.Background(), Event{
		ActorRole:  "admin",
		EntityType: "artist",
		EntityID:   uuid.New(),
		Action:     "rejected",
		NewValues:  unmarshalable{C: make(chan int)},
	})

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "marshal new_values"), "got: %v", err)
}

func TestPgRepoLog_OldValuesFailureShortCircuits_NewValuesNeverMarshaled(t *testing.T) {
	// If OldValues fails first, the function must return immediately -
	// NewValues (also unmarshalable here) must never even be attempted,
	// and neither must the DB call. A nil pool panicking would fail this
	// test just as loudly as a wrong error message would.
	repo := &pgRepo{db: nil}

	err := repo.Log(context.Background(), Event{
		ActorRole:  "admin",
		EntityType: "artist",
		EntityID:   uuid.New(),
		Action:     "rejected",
		OldValues:  unmarshalable{C: make(chan int)},
		NewValues:  unmarshalable{C: make(chan int)},
	})

	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "marshal old_values"), "got: %v", err)
}

func TestNewRepository_ReturnsPgRepo(t *testing.T) {
	repo := NewRepository(nil)

	_, ok := repo.(*pgRepo)
	assert.True(t, ok, "NewRepository must return a *pgRepo")
}

// ── NopRepository ────────────────────────────────────────────────────────────

func TestNopRepository_Log_AlwaysReturnsNil(t *testing.T) {
	var repo Repository = NopRepository{}

	err := repo.Log(context.Background(), Event{
		ActorRole:  "admin",
		EntityType: "artist",
		EntityID:   uuid.New(),
		Action:     "approved",
	})

	assert.NoError(t, err)
}

func TestNopRepository_Log_DiscardsEventWithoutPanicking(t *testing.T) {
	// A zero-value Event (no EntityID, no action) must still be safe to
	// pass - NopRepository is the fallback used when a caller doesn't
	// care about audit logging at all, so it must never be pickier than
	// "discard whatever you're given."
	var repo Repository = NopRepository{}

	err := repo.Log(context.Background(), Event{})

	assert.NoError(t, err)
}
