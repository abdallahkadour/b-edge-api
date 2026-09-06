# 04 — Database & Backend Optimizer

**2026-09-06.** Data model, indexes, query patterns, transactions, caching.

## Scope

29 tables, 102 indexes, 35 migrations, PostgreSQL 15 via `pgx/v5`. **No ORM** —
every query is hand-written SQL with `$n` placeholders.

---

## The model is stronger than typical

Three things worth stating before the findings, because each is load-bearing
and a "simplification" would break it.

**The concurrency guard is a database constraint, not application code.**

```sql
EXCLUDE USING gist (
  artist_id WITH =,
  tstzrange(start_time, blocked_until, '[)') WITH &&
) WHERE (status NOT IN ('cancelled','expired','no_show','refunded'))
  DEFERRABLE INITIALLY IMMEDIATE
```

`DEFERRABLE INITIALLY IMMEDIATE` is deliberate — a single-statement bulk shift
moves a whole day without tripping row-by-row. Verified under real
concurrency: 8 simultaneous holds on one slot → one succeeded.

**`blocked_until` is a stored column and the migration explains why.**
`timestamptz + interval` is only STABLE (DST depends on session TimeZone), so
no index expression or generated column can carry it. Both were tried against
the real database first. The redundancy is what makes cleanup time
*releasable*.

**Half-open intervals `[start, end)` match the application's `Occupancy` type
by construction**, so `internal/booking` and the GIST constraint agree rather
than coinciding.

---

## F1 — `ListOrdersByCustomer` is an N+1 · **P1**

`internal/product/service.go:252`

```go
orders, err := s.repo.GetOrdersByCustomer(ctx, customerID)
for _, o := range orders {
	items, err := s.repo.GetOrderItems(ctx, o.ID)   // ← one query per order
	result = append(result, toOrderResponse(o, items))
}
```

`order_items` **is** indexed on `(order_id, created_at)`, so each query is
fast — this is a round-trip problem, not a scan problem. A customer with 30
orders costs 31 round trips on "My Orders".

**Fix — one query, grouped in Go:**

```go
// repository
func (r *pgRepo) GetOrderItemsForOrders(ctx context.Context, orderIDs []uuid.UUID) (map[uuid.UUID][]*OrderItem, error) {
	rows, err := r.db.Query(ctx, `
		SELECT order_id, id, product_id, quantity, unit_price, subtotal
		FROM order_items
		WHERE order_id = ANY($1)
		ORDER BY order_id, created_at`, orderIDs)
	if err != nil {
		return nil, fmt.Errorf("get order items for orders: %w", err)
	}
	defer rows.Close()

	out := make(map[uuid.UUID][]*OrderItem, len(orderIDs))
	for rows.Next() {
		var oid uuid.UUID
		it := &OrderItem{}
		if err := rows.Scan(&oid, &it.ID, &it.ProductID, &it.Quantity, &it.UnitPrice, &it.Subtotal); err != nil {
			return nil, fmt.Errorf("scan order item: %w", err)
		}
		out[oid] = append(out[oid], it)
	}
	return out, rows.Err()
}
```

```diff
 	orders, err := s.repo.GetOrdersByCustomer(ctx, customerID)
+	ids := make([]uuid.UUID, len(orders))
+	for i, o := range orders { ids[i] = o.ID }
+	itemsByOrder, err := s.repo.GetOrderItemsForOrders(ctx, ids)
+	if err != nil { return nil, fmt.Errorf("list orders by customer: %w", err) }
+
 	result := make([]*OrderResponse, 0, len(orders))
 	for _, o := range orders {
-		items, err := s.repo.GetOrderItems(ctx, o.ID)
-		if err != nil { … }
-		result = append(result, toOrderResponse(o, items))
+		result = append(result, toOrderResponse(o, itemsByOrder[o.ID]))
 	}
```

`ANY($1)` with a `uuid[]` is one round trip regardless of order count.
`GetOrderItems` stays for the single-order read.

**Correction, 2026-09-06:** this document originally claimed the same shape
appears in the artist-side list. It does not — `ListOrdersBySalon` uses
`GetEnrichedOrdersBySalon`, which already joins. There is exactly **one** N+1
here, and it is fixed.

---

## F2 — Travel-buffer lookup inside the slot loop · **P2**

`internal/booking/service.go:332` and `:1975`

```go
for _, csb := range crossStoreBookings {
	buf, err := s.repo.GetArtistStoreBuffer(ctx, artistID, csb.StoreID, storeID)
```

One query per cross-store booking, on the **hottest read path in the product**
(slot generation runs on every calendar view).

**But it is not an index problem.** The FK-index audit flags
`artist_store_buffers.from_store_id`/`to_store_id` as unindexed, and that is a
false positive for this query: a composite unique index
`(artist_id, from_store_id, to_store_id)` covers the lookup exactly.

The cost is round trips, and N is small — an artist works at 2–3 stores, so a
day rarely has more than a handful of cross-store bookings.

**Recommendation: fetch the artist's whole buffer set once per slot request**
(it is at most `stores²`, single digits) and look up in a map. Cheap, and it
removes a per-iteration round trip from the hot path.

**Do not** add indexes on the individual FK columns for this — they would be
redundant with the composite and cost write throughput.

---

## F3 — Unindexed foreign keys: 11 found, 3 worth fixing · **P2**

```
artist_store_buffers.from_store_id   ← covered by composite for reads
artist_store_buffers.to_store_id     ← covered by composite for reads
audit_events.actor_id
client_notes.customer_id
invoices.confirmed_by
order_items.product_id               ← fix
services.template_id
subscriptions.plan_code
waitlist_entries.customer_id         ← fix
waitlist_entries.service_id          ← fix
waitlist_entries.store_id            ← fix
```

An unindexed FK matters in two cases: joins on that column, and
`DELETE`/`UPDATE` on the *parent*, which scans the child table for every row
removed.

**Worth adding:**

```sql
-- waitlist: the sweep and the cascade both filter on these three together
CREATE INDEX idx_waitlist_lookup
  ON waitlist_entries (artist_id, store_id, service_id, status);

-- order_items: deleting/deactivating a product scans this table today
CREATE INDEX idx_order_items_product ON order_items (product_id);
```

**Not worth adding:** `audit_events.actor_id`, `invoices.confirmed_by`,
`services.template_id`, `subscriptions.plan_code`, `client_notes.customer_id`
— all low-cardinality parents, rarely deleted, small tables. Adding indexes
costs write throughput for no measured read.

---

## F4 — Transaction boundaries are correct · no action

Checked every multi-statement write. Each opens a transaction, defers rollback,
and commits explicitly:

```go
tx, err := r.db.Begin(ctx)
defer tx.Rollback(ctx) //nolint:errcheck // no-op after a successful commit
… writes + recompute …
return tx.Commit(ctx)
```

Both rating aggregates recompute **inside the same transaction as the row they
summarise**, so a reader can never see a review its average does not account
for. Hiding a review drops it from both averages atomically.

---

## F5 — Caching: do not add Redis · **P2, recommendation is to decline**

The brief asks about a Redis layer. **This project should not add one yet**,
and the reasons are specific rather than dogmatic:

1. **There is no measured hot read.** Discovery and slot generation are the
   candidates; neither has been profiled, and slot generation is *already*
   cache-hostile — its answer changes with every booking, hold, cancellation
   and exception.
2. **The project's core design is derive-on-read with no scheduler.**
   Subscription status, open/closed and hold expiry are all computed per
   request precisely so nothing can go stale. A cache is a second source of
   truth with an invalidation problem, which is the thing that design avoids.
3. **The two things that *are* cached** (`artists.rating`, `stores.rating`)
   are already showing the cost: they do not self-heal, and agent 02 F4
   recommends a drift-check query for them.
4. **The real scaling gap is the absent edge layer**, not the database. A CDN
   in front of `/discovery/*` would remove more load than Redis, with no
   invalidation problem for a resource that is public and slow-changing.

**If a cache is later justified by a profile**, the correct first target is
`GET /discovery/artists` (public, paginated, tolerant of seconds-old data) with
a short TTL at the edge — not an in-process store.

---

## F6 — Query hygiene · no action

- Every query uses `$n` placeholders. No `fmt.Sprintf` into SQL anywhere —
  confirmed by grep and by SQLmap-equivalent probing (agent 03, INJ-01).
- `COALESCE` appears 8× in `booking/repository.go`, all defending aggregate
  reads over empty sets.
- No `SELECT *`.
- Cursor pagination on the list endpoints rather than `OFFSET`.

---

## Recommended action

| # | Action | Priority |
|---|---|---|
| F1 | Batch `GetOrderItems` with `ANY($1)` — both order lists | **P1** |
| F2 | Preload the artist's travel buffers once per slot request | P2 |
| F3 | Add the waitlist composite + `order_items.product_id` index only | P2 |
| F5 | **Decline Redis**; put a CDN in front of `/discovery/*` instead | P2 |
| F4/F6 | No action — transactions and query hygiene are correct | — |
