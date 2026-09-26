//go:build dbtest

package product

import (
	"context"
	"os"
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

type shopFixture struct{ Salon, Customer, Product uuid.UUID }

// newShop builds a salon, one product with 5 in stock, and a customer.
func newShop(t *testing.T, pool *pgxpool.Pool) shopFixture {
	t.Helper()
	ctx := context.Background()
	var f shopFixture
	var owner uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('Owner',$1,'x','artist') RETURNING id`, uuid.NewString()+"@shop.local").Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id,name) VALUES ($1,'Shop') RETURNING id`,
		owner).Scan(&f.Salon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO products (salon_id,name,price,stock_quantity)
		VALUES ($1,'Serum',20.00,5) RETURNING id`, f.Salon).Scan(&f.Product))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('Maya',$1,'x','customer') RETURNING id`, uuid.NewString()+"@shop.local").Scan(&f.Customer))
	return f
}

func (f shopFixture) order(requestID *uuid.UUID) (*Order, []*OrderItem) {
	o := &Order{ID: uuid.New(), SalonID: f.Salon, CustomerID: f.Customer,
		TotalAmount: decimal.RequireFromString("40"), DiscountAmount: decimal.Zero, RequestID: requestID}
	return o, []*OrderItem{{ID: uuid.New(), OrderID: o.ID, ProductID: f.Product, ProductName: "Serum",
		UnitPrice: decimal.RequireFromString("20"), Quantity: 2, Subtotal: decimal.RequireFromString("40")}}
}

func stockOf(t *testing.T, pool *pgxpool.Pool, product uuid.UUID) int {
	t.Helper()
	var n int
	require.NoError(t, pool.QueryRow(context.Background(),
		`SELECT stock_quantity FROM products WHERE id = $1`, product).Scan(&n))
	return n
}

func TestCreateOrder_SameRequestIDTwice_OneOrderAndStockTakenOnce(t *testing.T) {
	// A retry after a lost reply sends the same request_id. The second
	// insert must write nothing - no order, no items, no second stock
	// decrement - and say it was a repeat.
	pool := testdb.New(t)
	ctx := context.Background()
	f := newShop(t, pool)
	repo := NewRepository(pool)
	rid := uuid.New()

	first, firstItems := f.order(&rid)
	require.NoError(t, repo.CreateOrder(ctx, first, firstItems, nil))
	require.Equal(t, 3, stockOf(t, pool, f.Product), "positive control: the first order took 2 of 5")

	again, againItems := f.order(&rid)
	assert.ErrorIs(t, repo.CreateOrder(ctx, again, againItems, nil), ErrDuplicateOrderRequest)

	var orders int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE request_id = $1`, rid).Scan(&orders))
	assert.Equal(t, 1, orders)
	assert.Equal(t, 3, stockOf(t, pool, f.Product), "the repeat must not take stock again")

	found, items, err := repo.GetOrderByRequestID(ctx, rid)
	require.NoError(t, err)
	assert.Equal(t, first.ID, found.ID)
	assert.Len(t, items, 1)
}

func TestCreateOrder_NoRequestID_BehavesAsBefore(t *testing.T) {
	// Older clients send no request_id: two orders are two orders.
	pool := testdb.New(t)
	ctx := context.Background()
	f := newShop(t, pool)
	repo := NewRepository(pool)

	a, ai := f.order(nil)
	b, bi := f.order(nil)
	require.NoError(t, repo.CreateOrder(ctx, a, ai, nil))
	require.NoError(t, repo.CreateOrder(ctx, b, bi, nil))
	assert.Equal(t, 1, stockOf(t, pool, f.Product))

	_, _, err := repo.GetOrderByRequestID(ctx, uuid.New())
	assert.ErrorIs(t, err, ErrOrderNotFound)
}

func TestPlaceOrder_BurstOfRetries_OneOrderAndEveryoneGetsIt(t *testing.T) {
	// Five copies of one checkout land together - a flaky connection and an
	// impatient thumb. Only the unique key can settle this: every copy
	// passes the lookup before any has committed.
	pool := testdb.New(t)
	ctx := context.Background()
	f := newShop(t, pool)
	svc := NewService(NewRepository(pool))
	rid := uuid.NewString()
	req := CreateOrderRequest{SalonID: f.Salon.String(), Name: "Maya", Phone: "70123456",
		DeliveryLat: 33.8938, DeliveryLng: 35.5018, RequestID: &rid,
		Items: []OrderItemRequest{{ProductID: f.Product.String(), Quantity: 2}}}

	const copies = 5
	ids := make([]uuid.UUID, copies)
	errs := make([]error, copies)
	var wg sync.WaitGroup
	for i := range copies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := svc.PlaceOrder(ctx, req)
			errs[i] = err
			if err == nil {
				ids[i] = res.ID
			}
		}()
	}
	wg.Wait()

	for i := range copies {
		require.NoError(t, errs[i], "copy %d", i)
		assert.Equal(t, ids[0], ids[i], "every copy answers with the same order")
	}
	var orders int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM orders WHERE salon_id = $1`, f.Salon).Scan(&orders))
	assert.Equal(t, 1, orders)
	assert.Equal(t, 3, stockOf(t, pool, f.Product), "2 of 5 taken, once")
}

func TestMigration054_DownThenUp(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	down, err := os.ReadFile("../../db/migrations/054_order_request_id.down.sql")
	require.NoError(t, err)
	up, err := os.ReadFile("../../db/migrations/054_order_request_id.up.sql")
	require.NoError(t, err)
	col := func() int {
		var n int
		require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns
			WHERE table_name = 'orders' AND column_name = 'request_id'`).Scan(&n))
		return n
	}
	require.Equal(t, 1, col(), "positive control: the migrated template has the column")
	_, err = pool.Exec(ctx, string(down))
	require.NoError(t, err)
	assert.Equal(t, 0, col())
	_, err = pool.Exec(ctx, string(up))
	require.NoError(t, err)
	assert.Equal(t, 1, col())
}

func TestOrderReads_CarryTheDiscount(t *testing.T) {
	// A $40 order paid with a $10 code. Every screen lists $40 of items and a
	// $30 total, so a read that drops the discount makes the two disagree -
	// on the customer's receipt and on the screen where the artist confirms
	// she was paid $30.
	pool := testdb.New(t)
	ctx := context.Background()
	f := newShop(t, pool)
	repo := NewRepository(pool)
	code := "SAVE10"
	o, items := f.order(nil)
	o.TotalAmount = decimal.RequireFromString("30")
	o.DiscountAmount = decimal.RequireFromString("10")
	o.DiscountCode = &code
	require.NoError(t, repo.CreateOrder(ctx, o, items, nil))

	check := func(read string, got *Order) {
		t.Helper()
		assert.True(t, decimal.RequireFromString("10").Equal(got.DiscountAmount), "%s: discount %s", read, got.DiscountAmount)
		if assert.NotNil(t, got.DiscountCode, read) {
			assert.Equal(t, code, *got.DiscountCode, read)
		}
	}

	byID, _, err := repo.GetOrderByID(ctx, o.ID)
	require.NoError(t, err)
	check("GetOrderByID", byID)

	byCustomer, err := repo.GetOrdersByCustomer(ctx, f.Customer)
	require.NoError(t, err)
	require.Len(t, byCustomer, 1)
	check("GetOrdersByCustomer", byCustomer[0])

	bySalon, err := repo.GetOrdersBySalon(ctx, f.Salon, "")
	require.NoError(t, err)
	require.Len(t, bySalon, 1)
	check("GetOrdersBySalon", bySalon[0])

	queue, err := repo.GetEnrichedOrdersBySalon(ctx, f.Salon, "")
	require.NoError(t, err)
	require.Len(t, queue, 1)
	if assert.NotNil(t, queue[0].DiscountAmount, "GetEnrichedOrdersBySalon") {
		assert.True(t, decimal.RequireFromString("10").Equal(*queue[0].DiscountAmount))
	}
	if assert.NotNil(t, queue[0].DiscountCode, "GetEnrichedOrdersBySalon") {
		assert.Equal(t, code, *queue[0].DiscountCode)
	}
}

func TestOrderReads_NoDiscount_NoDiscountLine(t *testing.T) {
	// The artist's queue omits the field when nothing was taken off, so a
	// receipt shows a discount line only when there is one.
	pool := testdb.New(t)
	ctx := context.Background()
	f := newShop(t, pool)
	repo := NewRepository(pool)
	o, items := f.order(nil)
	require.NoError(t, repo.CreateOrder(ctx, o, items, nil))

	queue, err := repo.GetEnrichedOrdersBySalon(ctx, f.Salon, "")
	require.NoError(t, err)
	require.Len(t, queue, 1)
	assert.Nil(t, queue[0].DiscountAmount)
	assert.Nil(t, queue[0].DiscountCode)
}
