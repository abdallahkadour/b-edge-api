//go:build dbtest

package activity

// The feed query on a real clone. What a mock cannot show: that another
// salon's rows never appear, that the names are joined from the right
// tables, and that the filters select what their names say.
//
// Run with: make test-db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

type world struct {
	pool                    *pgxpool.Pool
	salon, other            uuid.UUID
	owner, member, customer uuid.UUID // users.id
	memberArtist            uuid.UUID
	store, service, booking uuid.UUID
}

func newWorld(t *testing.T) *world {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	w := &world{pool: pool}
	user := func(name, role string) uuid.UUID {
		var id uuid.UUID
		require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name, email, password_hash, role) VALUES ($1,$2,'x',$3) RETURNING id`,
			name, uuid.NewString()+"@dbtest.local", role).Scan(&id))
		return id
	}
	w.owner, w.member, w.customer = user("Rania", "artist"), user("Maya", "artist"), user("Sara", "customer")
	otherOwner := user("Other", "artist")
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id, name) VALUES ($1,'Mine') RETURNING id`, w.owner).Scan(&w.salon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id, name) VALUES ($1,'Theirs') RETURNING id`, otherOwner).Scan(&w.other))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO stores (salon_id, name, city) VALUES ($1,'Hamra','Beirut') RETURNING id`, w.salon).Scan(&w.store))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id, name, duration_min, price) VALUES ($1,'Bridal makeup',90,200) RETURNING id`, w.salon).Scan(&w.service))
	_, err := pool.Exec(ctx, `INSERT INTO artists (user_id, salon_id) VALUES ($1,$2)`, w.owner, w.salon)
	require.NoError(t, err)
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO artists (user_id, salon_id) VALUES ($1,$2) RETURNING id`, w.member, w.salon).Scan(&w.memberArtist))
	start := time.Date(2026, 10, 20, 7, 0, 0, 0, time.UTC)
	require.NoError(t, pool.QueryRow(ctx, `
		INSERT INTO bookings (salon_id, store_id, artist_id, customer_id, service_id, start_time, end_time, blocked_until,
		                      status, original_price, final_price)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7,'approved',200,200) RETURNING id`,
		w.salon, w.store, w.memberArtist, w.customer, w.service, start, start.Add(90*time.Minute)).Scan(&w.booking))
	return w
}

func (w *world) log(t *testing.T, salon uuid.UUID, actor uuid.UUID, role, entity string, id uuid.UUID, action string, at time.Time) {
	t.Helper()
	_, err := w.pool.Exec(context.Background(), `
		INSERT INTO audit_events (salon_id, actor_id, actor_role, entity_type, entity_id, action, new_values, created_at)
		VALUES ($1,$2,$3,$4,$5,$6,'{"x":1}',$7)`, salon, actor, role, entity, id, action, at)
	require.NoError(t, err)
}

func TestList_OnlyThisSalon_NewestFirst_WithNames(t *testing.T) {
	w := newWorld(t)
	repo := NewRepository(w.pool)
	t0 := time.Now().UTC().Add(-time.Hour)

	w.log(t, w.salon, w.member, "artist", audit.EntityBooking, w.booking, audit.ActionBookingApprove, t0)
	w.log(t, w.salon, w.owner, "artist", audit.EntityStore, w.store, audit.ActionStoreHours, t0.Add(time.Minute))
	w.log(t, w.other, w.owner, "artist", audit.EntityPaymentMethod, uuid.New(), audit.ActionPaymentMethodSave, t0.Add(2*time.Minute))

	got, err := repo.List(context.Background(), w.salon, Filter{}, time.Now().UTC(), 10)
	require.NoError(t, err)

	require.Len(t, got, 2, "the other salon's change is not this salon's business")
	assert.Equal(t, audit.ActionStoreHours, got[0].Action, "newest first")
	assert.Equal(t, "Rania", got[0].Actor.Name)
	assert.Equal(t, "Hamra", got[0].Subject.StoreName)

	b := got[1]
	assert.Equal(t, "Maya", b.Actor.Name)
	assert.Equal(t, "artist", b.Actor.Role)
	assert.Equal(t, "Bridal makeup", b.Subject.ServiceName)
	assert.Equal(t, "Sara", b.Subject.CustomerName)
	assert.Equal(t, "Maya", b.Subject.ArtistName)
	require.NotNil(t, b.Subject.StartTime)
	assert.JSONEq(t, `{"x":1}`, string(b.New))
}

func TestList_Filters(t *testing.T) {
	w := newWorld(t)
	repo := NewRepository(w.pool)
	t0 := time.Now().UTC().Add(-time.Hour)

	w.log(t, w.salon, w.member, "artist", audit.EntityBooking, w.booking, audit.ActionBookingCancel, t0)
	w.log(t, w.salon, w.owner, "artist", audit.EntityPaymentMethod, uuid.New(), audit.ActionPaymentMethodSave, t0.Add(time.Minute))
	w.log(t, w.salon, w.owner, "artist", audit.EntitySalon, w.salon, audit.ActionSalonNoShowPolicy, t0.Add(2*time.Minute))
	w.log(t, w.salon, w.owner, "artist", audit.EntityArtist, w.memberArtist, "remove_member", t0.Add(3*time.Minute))
	w.log(t, w.salon, w.owner, "artist", audit.EntityService, w.service, audit.ActionServiceUpdate, t0.Add(4*time.Minute))

	actions := func(f Filter) []string {
		got, err := repo.List(context.Background(), w.salon, f, time.Now().UTC(), 10)
		require.NoError(t, err)
		out := []string{}
		for _, e := range got {
			out = append(out, e.Action)
		}
		return out
	}

	assert.Equal(t, []string{audit.ActionBookingCancel}, actions(Filter{Kind: "bookings"}))
	assert.Equal(t, []string{audit.ActionSalonNoShowPolicy, audit.ActionPaymentMethodSave}, actions(Filter{Kind: "payments"}))
	assert.Equal(t, []string{"remove_member"}, actions(Filter{Kind: "team"}))
	assert.Equal(t, []string{audit.ActionServiceUpdate}, actions(Filter{Kind: "menu"}))
	assert.Equal(t, []string{audit.ActionBookingCancel}, actions(Filter{ActorID: &w.member}))
	assert.Len(t, actions(Filter{}), 5)
}

func TestList_CursorPagesBackwards(t *testing.T) {
	w := newWorld(t)
	repo := NewRepository(w.pool)
	t0 := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 3; i++ {
		w.log(t, w.salon, w.owner, "artist", audit.EntityStore, w.store, audit.ActionStoreHours, t0.Add(time.Duration(i)*time.Minute))
	}

	first, err := repo.List(context.Background(), w.salon, Filter{}, time.Now().UTC(), 2)
	require.NoError(t, err)
	require.Len(t, first, 2)
	rest, err := repo.List(context.Background(), w.salon, Filter{}, first[1].At, 2)
	require.NoError(t, err)

	require.Len(t, rest, 1)
	assert.True(t, rest[0].At.Before(first[1].At))
}

func TestList_ASystemRow_HasNoActor(t *testing.T) {
	w := newWorld(t)
	repo := NewRepository(w.pool)
	_, err := w.pool.Exec(context.Background(), `
		INSERT INTO audit_events (salon_id, actor_role, entity_type, entity_id, action) VALUES ($1,'system','booking',$2,'booking.cancel')`,
		w.salon, w.booking)
	require.NoError(t, err)

	got, err := repo.List(context.Background(), w.salon, Filter{}, time.Now().UTC().Add(time.Minute), 10)
	require.NoError(t, err)

	require.Len(t, got, 1)
	assert.Nil(t, got[0].Actor.ID)
	assert.Equal(t, "system", got[0].Actor.Role)
	assert.Empty(t, got[0].Old, "no old values recorded, none returned")
}
