// Package client contains unit tests for the client CRM service layer.
// These tests use a mock repository — no database required.
package client

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Mock repository ───────────────────────────────────────────────────────────

type mockRepo struct {
	artistID        uuid.UUID
	artistErr       error
	clients         []*ClientRow
	clientsErr      error
	client          *ClientRow
	clientErr       error
	history         []*BookingHistoryRow
	historyErr      error
	salonID         uuid.UUID
	salonErr        error
	upsertContent   string
	upsertUpdatedAt time.Time
	upsertErr       error
	// captured
	lastUpsertContent string
	lastListQuery     string
}

func (m *mockRepo) GetArtistIDByUserID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return m.artistID, m.artistErr
}
func (m *mockRepo) ListClients(_ context.Context, _ uuid.UUID, q string) ([]*ClientRow, error) {
	m.lastListQuery = q
	return m.clients, m.clientsErr
}
func (m *mockRepo) GetClient(_ context.Context, _, _ uuid.UUID) (*ClientRow, error) {
	return m.client, m.clientErr
}
func (m *mockRepo) GetClientHistory(_ context.Context, _, _ uuid.UUID) ([]*BookingHistoryRow, error) {
	return m.history, m.historyErr
}
func (m *mockRepo) UpsertNote(_ context.Context, _, _, _ uuid.UUID, content string) (string, time.Time, error) {
	m.lastUpsertContent = content
	if m.upsertErr != nil {
		return "", time.Time{}, m.upsertErr
	}
	return content, m.upsertUpdatedAt, nil
}
func (m *mockRepo) GetArtistSalonID(_ context.Context, _ uuid.UUID) (uuid.UUID, error) {
	return m.salonID, m.salonErr
}

func newTestService(repo Repository) *Service { return NewService(repo) }

// ── ListClients tests ─────────────────────────────────────────────────────────

// TestListClients_NotAnArtist — a user with no artist profile is forbidden.
func TestListClients_NotAnArtist(t *testing.T) {
	repo := &mockRepo{artistErr: ErrArtistNotFound}
	svc := newTestService(repo)

	_, err := svc.ListClients(context.Background(), uuid.New(), "")

	require.Error(t, err)
}

// TestListClients_MapsRows — aggregated rows convert to cards (VIP stubbed false).
func TestListClients_MapsRows(t *testing.T) {
	rating := decimal.NewFromFloat(4.8)
	repo := &mockRepo{
		artistID: uuid.New(),
		clients: []*ClientRow{
			{CustomerID: uuid.New(), Name: "Lara", BookingsCount: 2,
				TotalSpent: decimal.NewFromInt(350), AverageRating: &rating, NoteContent: "matte finish"},
		},
	}
	svc := newTestService(repo)

	cards, err := svc.ListClients(context.Background(), uuid.New(), "lara")

	require.NoError(t, err)
	require.Len(t, cards, 1)
	assert.Equal(t, "Lara", cards[0].Name)
	assert.Equal(t, 2, cards[0].BookingsCount)
	assert.False(t, cards[0].IsVIP, "VIP is stubbed false until the rule is decided")
	assert.Equal(t, "lara", repo.lastListQuery, "search term passes through")
}

// ── GetClient tests ───────────────────────────────────────────────────────────

// TestGetClient_NotFound — a non-client customer surfaces CLIENT_NOT_FOUND.
func TestGetClient_NotFound(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), clientErr: ErrClientNotFound}
	svc := newTestService(repo)

	_, err := svc.GetClient(context.Background(), uuid.New(), uuid.New())

	require.Error(t, err)
}

// TestGetClient_Aggregates — profile combines metrics, note, and history.
func TestGetClient_Aggregates(t *testing.T) {
	repo := &mockRepo{
		artistID: uuid.New(),
		client: &ClientRow{
			CustomerID: uuid.New(), Name: "Sahar", BookingsCount: 3,
			TotalSpent: decimal.NewFromInt(240), NoteContent: "light-sensitive eyes",
		},
		history: []*BookingHistoryRow{
			{ID: uuid.New(), ServiceName: "Blowout", StoreName: "Beirut", StartTime: time.Now(),
				Status: "completed", FinalPrice: decimal.NewFromInt(80)},
		},
	}
	svc := newTestService(repo)

	profile, err := svc.GetClient(context.Background(), uuid.New(), uuid.New())

	require.NoError(t, err)
	assert.Equal(t, "Sahar", profile.Name)
	assert.Equal(t, "light-sensitive eyes", profile.Note)
	require.Len(t, profile.History, 1)
	assert.Equal(t, "Blowout", profile.History[0].ServiceName)
}

// ── UpsertNote tests ──────────────────────────────────────────────────────────

// TestUpsertNote_Success — verified client, note stored and returned.
func TestUpsertNote_Success(t *testing.T) {
	now := time.Now()
	repo := &mockRepo{
		artistID:        uuid.New(),
		client:          &ClientRow{CustomerID: uuid.New(), Name: "Lara"}, // is a client
		salonID:         uuid.New(),
		upsertUpdatedAt: now,
	}
	svc := newTestService(repo)

	res, err := svc.UpsertNote(context.Background(), uuid.New(), uuid.New(), UpsertNoteRequest{Content: "prefers 2pm"})

	require.NoError(t, err)
	assert.Equal(t, "prefers 2pm", res.Content)
	assert.Equal(t, "prefers 2pm", repo.lastUpsertContent)
}

// TestUpsertNote_NotClient — cannot note a customer who isn't a client.
func TestUpsertNote_NotClient(t *testing.T) {
	repo := &mockRepo{
		artistID:  uuid.New(),
		clientErr: ErrClientNotFound, // GetClient says not a client
	}
	svc := newTestService(repo)

	_, err := svc.UpsertNote(context.Background(), uuid.New(), uuid.New(), UpsertNoteRequest{Content: "x"})

	require.Error(t, err)
	assert.Empty(t, repo.lastUpsertContent, "note must not be stored for a non-client")
}

// TestUpsertNote_EmptyAllowed — empty content is valid (clears the note).
func TestUpsertNote_EmptyAllowed(t *testing.T) {
	repo := &mockRepo{
		artistID: uuid.New(),
		client:   &ClientRow{CustomerID: uuid.New()},
		salonID:  uuid.New(),
	}
	svc := newTestService(repo)

	_, err := svc.UpsertNote(context.Background(), uuid.New(), uuid.New(), UpsertNoteRequest{Content: ""})

	require.NoError(t, err)
}
