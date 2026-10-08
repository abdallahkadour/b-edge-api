//go:build dbtest

package notification

// E2E 28.12 - a message that fails for good tells the artist.
//
// E2E 13.5 was the only check of this path and it was manual: it needs a
// forced Twilio failure through the running worker, and was never staged.
// markFailed and alertArtistOfDeadLetter talk to the database directly (the
// UPDATE, the booking join that finds the artist, the inbox insert), so a
// mock could only agree with whatever it was told; this runs them on a real
// clone instead. Watched failing with the alert call removed from
// markFailed (the alert assertions went red, the "dead" one stayed green).
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
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

type deadFixture struct {
	ArtistUser uuid.UUID
	BookingID  uuid.UUID
}

func newDeadFixture(t *testing.T, pool *pgxpool.Pool, customerName string) deadFixture {
	t.Helper()
	ctx := context.Background()
	var f deadFixture
	var salon, store, artist, service, customer uuid.UUID

	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name, email, password_hash, role)
		VALUES ('Artist','artist@deadtest.local','x','artist') RETURNING id`).Scan(&f.ArtistUser))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id, name) VALUES ($1,'Dead Test Salon') RETURNING id`,
		f.ArtistUser).Scan(&salon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO stores (salon_id, name, city) VALUES ($1,'Main','Beirut') RETURNING id`,
		salon).Scan(&store))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO artists (user_id, salon_id) VALUES ($1,$2) RETURNING id`,
		f.ArtistUser, salon).Scan(&artist))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id, name, duration_min, price)
		VALUES ($1,'Makeup',60,100.00) RETURNING id`, salon).Scan(&service))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name, email, password_hash, role)
		VALUES ($1,$2,'x','customer') RETURNING id`, customerName, uuid.NewString()+"@deadtest.local").Scan(&customer))

	start := time.Now().Add(72 * time.Hour).Truncate(time.Hour)
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO bookings
		(salon_id, store_id, artist_id, customer_id, service_id, start_time, end_time, blocked_until,
		 status, original_price, final_price, deposit_amount)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7,'confirmed',100,100,0) RETURNING id`,
		salon, store, artist, customer, service, start, start.Add(time.Hour)).Scan(&f.BookingID))
	return f
}

// queued inserts an outbound message for the booking that has already been
// tried `attempts` times, and returns it as the worker would have read it.
func queued(t *testing.T, pool *pgxpool.Pool, bookingID uuid.UUID, attempts int) *PendingNotification {
	t.Helper()
	var id string
	require.NoError(t, pool.QueryRow(context.Background(), `INSERT INTO notifications
		(booking_id, template_name, channel, status, attempts, recipient_phone)
		VALUES ($1,'booking_confirmed','whatsapp','failed',$2,'+96170000001') RETURNING id`,
		bookingID, attempts).Scan(&id))
	b := bookingID.String()
	return &PendingNotification{ID: id, BookingID: &b, TemplateName: "booking_confirmed", Attempts: attempts}
}

func alertsFor(t *testing.T, pool *pgxpool.Pool, user uuid.UUID) (rows int, count int, body string) {
	t.Helper()
	require.NoError(t, pool.QueryRow(context.Background(), `
		SELECT count(*), COALESCE(max(item_count),0), COALESCE(max(body),'')
		  FROM user_notifications WHERE user_id = $1 AND kind = 'delivery_failed'`, user).Scan(&rows, &count, &body))
	return
}

func TestMarkFailed_FinalAttempt_MarksDeadAndTellsTheArtist(t *testing.T) {
	pool := testdb.New(t)
	w := NewWorker(pool, zap.NewNop())
	f := newDeadFixture(t, pool, "Lina")
	n := queued(t, pool, f.BookingID, maxAttempts-1)

	w.markFailed(context.Background(), n, "63016 outside the session window")

	var status string
	var attempts int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status, attempts FROM notifications WHERE id = $1`, n.ID).Scan(&status, &attempts))
	assert.Equal(t, "dead", status)
	assert.Equal(t, maxAttempts, attempts)

	rows, count, body := alertsFor(t, pool, f.ArtistUser)
	assert.Equal(t, 1, rows, "the artist gets exactly one alert")
	assert.Equal(t, 1, count)
	assert.Contains(t, body, "Lina", "the alert names the customer the artist must now call")
}

func TestMarkFailed_AnEarlierAttempt_IsRetriedSilently(t *testing.T) {
	pool := testdb.New(t)
	w := NewWorker(pool, zap.NewNop())
	f := newDeadFixture(t, pool, "Lina")
	n := queued(t, pool, f.BookingID, 0)

	w.markFailed(context.Background(), n, "timeout")

	var status string
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT status FROM notifications WHERE id = $1`, n.ID).Scan(&status))
	assert.Equal(t, "failed", status, "a first failure is retried, not given up on")
	rows, _, _ := alertsFor(t, pool, f.ArtistUser)
	assert.Equal(t, 0, rows, "no alert while the message may still arrive")
}

func TestMarkFailed_TwoMessagesDie_OneAlertCountingBoth(t *testing.T) {
	pool := testdb.New(t)
	w := NewWorker(pool, zap.NewNop())
	f := newDeadFixture(t, pool, "Lina")

	w.markFailed(context.Background(), queued(t, pool, f.BookingID, maxAttempts-1), "63016")
	w.markFailed(context.Background(), queued(t, pool, f.BookingID, maxAttempts-1), "63016")

	rows, count, _ := alertsFor(t, pool, f.ArtistUser)
	assert.Equal(t, 1, rows, "failures bundle into one unread alert rather than flooding the feed")
	assert.Equal(t, 2, count)
}
