//go:build dbtest

package earnings

// The salon overview's SQL on a real clone. A mock would agree with any
// aggregate it was handed; what has to be proved here is which bookings each
// figure is made of - and above all that another salon's bookings are never
// among them.
//
// Run with: make test-db

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

type salonFixture struct {
	pool                  *pgxpool.Pool
	salon, otherSalon     uuid.UUID
	store, otherStore     uuid.UUID
	service, otherService uuid.UUID
	customer              uuid.UUID
	owner, member, left   uuid.UUID // artists.id
	ownerUser             uuid.UUID
	outsider              uuid.UUID // an artist of the other salon
	next                  time.Time // each booking gets its own hour
}

func newSalonFixture(t *testing.T) *salonFixture {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	f := &salonFixture{pool: pool, next: time.Date(2026, 10, 5, 6, 0, 0, 0, time.UTC)}

	user := func(name, role string) uuid.UUID {
		var id uuid.UUID
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO users (name, email, password_hash, role) VALUES ($1,$2,'x',$3) RETURNING id`,
			name, uuid.NewString()+"@dbtest.local", role).Scan(&id))
		return id
	}
	salon := func(owner uuid.UUID, name string) (salon, store, service uuid.UUID) {
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO salons (owner_id, name) VALUES ($1,$2) RETURNING id`, owner, name).Scan(&salon))
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO stores (salon_id, name, city) VALUES ($1,'Main','Beirut') RETURNING id`, salon).Scan(&store))
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO services (salon_id, name, duration_min, price) VALUES ($1,'Makeup',60,100) RETURNING id`,
			salon).Scan(&service))
		return
	}
	artist := func(userID uuid.UUID, salonID *uuid.UUID) uuid.UUID {
		var id uuid.UUID
		require.NoError(t, pool.QueryRow(ctx,
			`INSERT INTO artists (user_id, salon_id) VALUES ($1,$2) RETURNING id`, userID, salonID).Scan(&id))
		return id
	}

	f.ownerUser = user("Rania", "artist")
	f.salon, f.store, f.service = salon(f.ownerUser, "Overview Salon")
	f.owner = artist(f.ownerUser, &f.salon)
	f.member = artist(user("Maya", "artist"), &f.salon)
	f.left = artist(user("Lina", "artist"), nil) // worked here, has since left

	otherOwner := user("Other", "artist")
	f.otherSalon, f.otherStore, f.otherService = salon(otherOwner, "Other Salon")
	f.outsider = artist(otherOwner, &f.otherSalon)

	f.customer = user("Customer", "customer")
	return f
}

// booking inserts a booking at this salon for artist, in its own hour.
func (f *salonFixture) booking(t *testing.T, artistID uuid.UUID, status, price, deposit string) {
	t.Helper()
	f.bookingAt(t, f.salon, f.store, f.service, artistID, status, price, deposit)
}

func (f *salonFixture) bookingAt(t *testing.T, salon, store, service, artistID uuid.UUID, status, price, deposit string) {
	t.Helper()
	start := f.next
	f.next = f.next.Add(time.Hour)
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO bookings (salon_id, store_id, artist_id, customer_id, service_id,
		                      start_time, end_time, blocked_until, status,
		                      original_price, final_price, deposit_amount)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$7,$8,$9,$9,$10)`,
		salon, store, artistID, f.customer, service,
		start, start.Add(time.Hour), status, price, deposit)
	require.NoError(t, err)
}

func (f *salonFixture) order(t *testing.T, salon uuid.UUID, status, total string, deliveredAt *time.Time) {
	t.Helper()
	_, err := f.pool.Exec(context.Background(), `
		INSERT INTO orders (salon_id, customer_id, status, total_amount, delivered_at)
		VALUES ($1,$2,$3,$4,$5)`, salon, f.customer, status, total, deliveredAt)
	require.NoError(t, err)
}

func october() (time.Time, time.Time) {
	return time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
}

func TestSalonTotals_CountsEveryArtistOfThisSalonAndNoOtherSalon(t *testing.T) {
	f := newSalonFixture(t)
	repo := NewRepository(f.pool)

	f.booking(t, f.owner, "completed", "100.00", "20.00")
	f.booking(t, f.member, "completed", "50.00", "0")
	f.booking(t, f.member, "no_show", "80.00", "40.00")
	f.booking(t, f.left, "completed", "30.00", "0") // she left; the booking is still the salon's
	f.booking(t, f.owner, "cancelled", "999.00", "0")
	f.booking(t, f.member, "refund_due", "999.00", "25.00")
	f.booking(t, f.owner, "refunded", "999.00", "25.00")
	f.booking(t, f.owner, "expired", "999.00", "0")   // never a booking
	f.booking(t, f.owner, "confirmed", "999.00", "0") // not happened yet
	f.bookingAt(t, f.otherSalon, f.otherStore, f.otherService, f.outsider, "completed", "5000.00", "100.00")

	delivered := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	september := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	f.order(t, f.salon, "delivered", "45.50", &delivered)
	f.order(t, f.salon, "delivered", "10.00", &september) // outside the window
	f.order(t, f.salon, "shipped", "70.00", nil)
	f.order(t, f.otherSalon, "delivered", "900.00", &delivered)

	from, to := october()
	got, err := repo.GetSalonTotals(context.Background(), f.salon, from, to)
	require.NoError(t, err)

	assert.True(t, got.Earned.Equal(decimal.RequireFromString("260.00")), "earned: got %s", got.Earned)
	assert.Equal(t, 4, got.EarnedBookings)
	assert.True(t, got.Deposits.Equal(decimal.RequireFromString("60.00")), "deposits: got %s", got.Deposits)
	assert.Equal(t, 3, got.Completed)
	assert.Equal(t, 1, got.NoShows)
	assert.Equal(t, 3, got.Cancelled, "cancelled, refund_due and refunded; not expired")
	assert.Equal(t, 1, got.OrdersDelivered)
	assert.True(t, got.OrdersValue.Equal(decimal.RequireFromString("45.50")), "orders: got %s", got.OrdersValue)
}

func TestSalonTotals_AgreeWithEachArtistsOwnEarnings(t *testing.T) {
	// The owner's total must be the sum of what each artist sees on her own
	// Earnings page, or the two screens contradict each other.
	f := newSalonFixture(t)
	repo := NewRepository(f.pool)
	ctx := context.Background()

	f.booking(t, f.owner, "completed", "120.00", "30.00")
	f.booking(t, f.member, "no_show", "75.00", "35.00")
	f.booking(t, f.member, "completed", "60.00", "0")

	from, to := october()
	salon, err := repo.GetSalonTotals(ctx, f.salon, from, to)
	require.NoError(t, err)

	sum, count, deposits := decimal.Zero, 0, decimal.Zero
	for _, a := range []uuid.UUID{f.owner, f.member} {
		r, n, d, err := repo.GetPeriodSummary(ctx, a, from, to)
		require.NoError(t, err)
		sum, count, deposits = sum.Add(r), count+n, deposits.Add(d)
	}
	assert.True(t, salon.Earned.Equal(sum), "salon %s vs artists %s", salon.Earned, sum)
	assert.Equal(t, count, salon.EarnedBookings)
	assert.True(t, salon.Deposits.Equal(deposits))
}

func TestSalonWaiting_IsThisSalonsQueueOnly(t *testing.T) {
	f := newSalonFixture(t)
	repo := NewRepository(f.pool)

	f.booking(t, f.member, "pending", "100.00", "0")
	f.booking(t, f.owner, "approved", "100.00", "30.00")
	f.booking(t, f.member, "deposit_paid", "100.00", "30.00")
	f.booking(t, f.owner, "refund_due", "100.00", "25.50")
	f.booking(t, f.member, "refund_due", "100.00", "20.00")
	f.booking(t, f.owner, "confirmed", "100.00", "30.00")
	f.bookingAt(t, f.otherSalon, f.otherStore, f.otherService, f.outsider, "refund_due", "900.00", "500.00")
	f.bookingAt(t, f.otherSalon, f.otherStore, f.otherService, f.outsider, "pending", "100.00", "0")

	got, err := repo.GetSalonWaiting(context.Background(), f.salon)
	require.NoError(t, err)

	assert.Equal(t, 1, got.AwaitingApproval)
	assert.Equal(t, 2, got.DepositsToCheck)
	assert.Equal(t, 2, got.RefundsOwed)
	assert.True(t, got.RefundsOwedAmount.Equal(decimal.RequireFromString("45.50")), "got %s", got.RefundsOwedAmount)
}

func TestSalonArtists_EveryMemberAndAnyoneWhoWorkedHere(t *testing.T) {
	f := newSalonFixture(t)
	repo := NewRepository(f.pool)

	f.booking(t, f.owner, "completed", "100.00", "0")
	f.booking(t, f.owner, "no_show", "40.00", "20.00")
	f.booking(t, f.owner, "cancelled", "100.00", "0")
	f.booking(t, f.left, "completed", "250.00", "0")
	// Maya has nothing this month and must still be listed: an artist with
	// no bookings is something an owner needs to see.
	f.bookingAt(t, f.otherSalon, f.otherStore, f.otherService, f.outsider, "completed", "5000.00", "0")

	from, to := october()
	rows, err := repo.GetSalonArtists(context.Background(), f.salon, from, to)
	require.NoError(t, err)

	byID := map[uuid.UUID]ArtistOverview{}
	for _, r := range rows {
		byID[r.ArtistID] = r
	}
	require.Len(t, rows, 3, "owner, member and the artist who left; never the other salon's artist")
	assert.NotContains(t, byID, f.outsider)

	lina := byID[f.left]
	assert.Equal(t, f.left, rows[0].ArtistID, "highest earned first")
	assert.False(t, lina.IsMember)
	assert.True(t, lina.Earned.Equal(decimal.RequireFromString("250.00")))

	rania := byID[f.owner]
	assert.Equal(t, "Rania", rania.Name)
	assert.True(t, rania.IsMember)
	assert.Equal(t, f.ownerUser, rania.userID)
	assert.True(t, rania.Earned.Equal(decimal.RequireFromString("140.00")), "got %s", rania.Earned)
	assert.Equal(t, 2, rania.EarnedBookings)
	assert.Equal(t, 1, rania.Completed)
	assert.Equal(t, 1, rania.NoShows)
	assert.Equal(t, 1, rania.Cancelled)

	maya := byID[f.member]
	assert.True(t, maya.IsMember)
	assert.True(t, maya.Earned.IsZero())
	assert.Equal(t, 0, maya.EarnedBookings)
}

func TestSalonArtists_ArtistWhoLeftWithNothingInThePeriod_IsNotListed(t *testing.T) {
	f := newSalonFixture(t)
	repo := NewRepository(f.pool)

	from, to := october()
	rows, err := repo.GetSalonArtists(context.Background(), f.salon, from, to)
	require.NoError(t, err)

	ids := []uuid.UUID{}
	for _, r := range rows {
		ids = append(ids, r.ArtistID)
	}
	assert.ElementsMatch(t, []uuid.UUID{f.owner, f.member}, ids)
}
