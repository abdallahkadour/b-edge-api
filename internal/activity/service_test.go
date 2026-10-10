package activity

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
)

type mockRepo struct {
	entries []Entry
	salon   uuid.UUID
	filter  Filter
	cursor  time.Time
	limit   int
}

func (m *mockRepo) List(_ context.Context, salonID uuid.UUID, f Filter, cursor time.Time, limit int) ([]Entry, error) {
	m.salon, m.filter, m.cursor, m.limit = salonID, f, cursor, limit
	return m.entries, nil
}

func TestList_MarksTheCallersOwnChanges(t *testing.T) {
	me, colleague := uuid.New(), uuid.New()
	repo := &mockRepo{entries: []Entry{{Actor: Actor{ID: &colleague}}, {Actor: Actor{ID: &me}}, {Actor: Actor{Role: "system"}}}}

	got, _, err := NewService(repo).List(context.Background(), me, uuid.New(), "", "", time.Now(), 20)

	require.NoError(t, err)
	assert.False(t, got[0].Actor.IsYou)
	assert.True(t, got[1].Actor.IsYou)
	assert.False(t, got[2].Actor.IsYou, "the system is nobody")
}

func TestList_AsksForOneMoreThanThePage_ToKnowThereIsMore(t *testing.T) {
	repo := &mockRepo{entries: make([]Entry, 3)}

	got, hasMore, err := NewService(repo).List(context.Background(), uuid.New(), uuid.New(), "", "", time.Now(), 2)

	require.NoError(t, err)
	assert.Equal(t, 3, repo.limit)
	assert.Len(t, got, 2)
	assert.True(t, hasMore)
}

func TestList_ReadsOnlyTheCallersSalon_WithTheFilters(t *testing.T) {
	salon, actor := uuid.New(), uuid.New()
	repo := &mockRepo{}

	_, _, err := NewService(repo).List(context.Background(), uuid.New(), salon, "payments", actor.String(), time.Now(), 20)

	require.NoError(t, err)
	assert.Equal(t, salon, repo.salon)
	assert.Equal(t, "payments", repo.filter.Kind)
	assert.Equal(t, &actor, repo.filter.ActorID)
}

func TestList_UnknownKind_IsRefused(t *testing.T) {
	_, _, err := NewService(&mockRepo{}).List(context.Background(), uuid.New(), uuid.New(), "everything'; --", "", time.Now(), 20)

	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "INVALID_KIND", ae.Code)
}

func TestList_MalformedActor_IsRefused(t *testing.T) {
	_, _, err := NewService(&mockRepo{}).List(context.Background(), uuid.New(), uuid.New(), "", "not-a-uuid", time.Now(), 20)

	var ae *apperror.AppError
	require.ErrorAs(t, err, &ae)
	assert.Equal(t, "INVALID_ACTOR", ae.Code)
}

func TestList_NothingYet_IsAnEmptyListNotNull(t *testing.T) {
	got, _, err := NewService(&mockRepo{}).List(context.Background(), uuid.New(), uuid.New(), "", "", time.Now(), 20)

	require.NoError(t, err)
	assert.NotNil(t, got)
}
