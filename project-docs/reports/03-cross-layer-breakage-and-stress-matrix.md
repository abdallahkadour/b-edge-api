# 03 — Cross-Layer Breakage & Stress Matrix

**Authors:** Daria (Go), Tarek (PostgreSQL), Leo (Angular)
**Date:** 2026-09-18
**Method:** static contract diffing, live probing of the running stack, and
direct inspection of the production-shaped database. No findings are theoretical
unless explicitly marked.

---

## 1. Headline: the Angular ⇄ Go contract is clean

We expected this to be the richest seam for bugs. It was not.

A script extracted every Go struct carrying `json` tags (**124**) and every
TypeScript interface (**98**), paired them, and diffed field by field:

```
interfaces paired   79 / 98
fields compared     500
genuine mismatches  0
```

Specifically checked, and clean in every case:

- **Go `omitempty` where TS requires the key.** Would mean an absent key read as
  `undefined` at runtime. None found.
- **Go pointer without `omitempty` where TS forbids null.** Would mean JSON
  `null` hitting a non-nullable type. None found — the nullable fields
  (`trial_ends_at`, `payment_reference`, `handle`, `cancelled_at`, …) are all
  correctly typed `string | null` on the TS side.
- **Non-pointer string with `omitempty`** — the subtle one, where `""`
  disappears from the payload entirely. None found.

**Leo:** this is not luck. The `ApiService.getArray` helper coalescing `null` to
`[]`, and the repositories consistently returning `make([]T, 0)` rather than a
nil slice, remove the single largest source of this class of bug. Both are
commented in-place explaining why. Keep both.

---

## 2. Boundary failure matrix

| # | Boundary | Failure | Severity | Status |
|---|---|---|---|---|
| X1 | Go ⇄ PG | Public review list had **no `LIMIT`** — every visible review returned on an unauthenticated endpoint | High | **FIXED** — capped at 50 |
| X2 | Go ⇄ PG | Integers with `min=1` and no `max` against `INTEGER` columns → value past 2³¹ became a **500** | Medium | **FIXED** — ceilings added; now 422 |
| X3 | PG only | **Nothing reaped expired credentials.** `refresh_tokens` 1240 rows / 1168 expired (94%); `customer_otps` 48/48 expired | High | **FIXED** — hourly reaper; live count 1240 → 72 |
| X4 | Go ⇄ PG | `report.Resolve` reused `$3` inside `CASE`/`IN`; PG could not infer its type → **500 on every call** | High | **FIXED** — explicit casts |
| X5 | Go ⇄ Angular | Contract drift | — | **None found** (§1) |
| X6 | Go ⇄ PG | Pool 20 vs in-flight 300 — **15:1** | Medium | Open, by design at this stage (§4) |
| X7 | PG only | `idx_reviews_artist_id` does not carry `created_at`, so the public list index-scans then sorts | Low | Open — see patch §5.1 |
| X8 | Angular | Both PWAs fire a `401` on `auth/refresh` on every cold signed-out load | Cosmetic | Open, deliberate — the app must try in order to restore a session |

---

## 3. Null, zero-value and payload-poisoning risks

### 3.1 Already defended, verified live

| Vector | Defence | Verified |
|---|---|---|
| SQL injection | `$n` placeholders throughout | 6 payload families across `q`, `city`, path UUIDs — no execution, **no DB error text leaked** |
| Mass assignment | Explicit request structs, never decoding into DB models | `is_verified`, `rating`, `status`, `role` all silently ignored on PATCH |
| Money coercion | `pkg/money` whitelist regex | `10.999`, `1e3`, `+5`, `Infinity`, `NaN`, `0x10`, overflow — all 400/422 |
| `NaN` in NUMERIC | `CHECK (col <> 'NaN'::numeric)` on 16 columns | Migration 034. Note: **PostgreSQL defines `NaN = NaN` as TRUE**, so the naive `CHECK (price = round(price,2))` does *not* catch it. |
| SSTI / HTML injection | `html.EscapeString` at the share boundary | `{{7*7}}`, `${7*7}`, `</title><script>`, `onerror=` — all escaped |
| Booking hold race | GIST exclusion constraint | 8 concurrent holds on one slot, **≤1 won** |
| Order total tampering | **No total field exists on the request** | The attack surface is absent by construction, which is stronger than validating it |
| Nil slice → JSON `null` | `make([]T, 0)` in repositories + `getArray` coalescing | Contract diff clean |

### 3.2 Residual, accepted

**Tarek:** `bookings.deposit_amount` is a snapshot taken at creation. Migration
038 constrains it against `original_price`, not `final_price`, deliberately — a
discount reduces what is owed, and the discount resolver already refuses to cut
below the deposit, so `final_price >= deposit` holds without a second constraint
that could contradict the first. If that resolver rule is ever relaxed, this
constraint becomes wrong. It is the one place two invariants are coupled across
layers with nothing asserting the coupling.

---

## 4. Capacity and bottleneck analysis

```
in-flight ceiling        300 requests     middleware/register.go
database pool             20 connections  config/database.go
statement timeout          5 s            pool RuntimeParams
request timeout           15 s            middleware/requestcontext.go
```

**Daria:** the limiter sheds at 300 *arrivals*, not at pool exhaustion. Under
saturation 280 requests queue on 20 connections while holding request slots, so
the failure mode is **latency collapse before rejection** — the worst shape,
because clients retry into it. The 5s `statement_timeout` is what prevents an
outage and is carrying more weight than its size suggests.

**Tarek:** do not raise the pool without raising PostgreSQL `max_connections`
first, or the failure simply moves one layer down and gets harder to read. The
right order is: CDN in front (removes the cacheable majority), *then* measure,
*then* size the pool.

**Where load will actually land.** Cacheable surfaces (discovery, catalogue,
share previews) are absorbed by a CDN once one exists. The uncacheable path is
`GET /bookings/slots` — per-artist, per-date, per-service, computed. That is the
endpoint to load-test first and the one that justifies `EDGE-02` in the security
plan.

**Growth:** `refresh_tokens` was the fastest-growing table in the database and
grew **unboundedly** until today. Now bounded. Next-fastest by shape are
`notifications` and `audit_events`; neither has a retention policy. Not urgent,
but they are the next §3-style finding waiting to happen.

---

## 5. Defensive patches

### 5.1 Recommended, not yet applied

```sql
-- X7: the public review list filters on artist_id and orders by created_at,
-- but the index carries only artist_id, so every read index-scans then sorts.
-- Cheap to fix, and it matters exactly for the artists with the most reviews.
CREATE INDEX CONCURRENTLY idx_reviews_artist_recent
  ON reviews (artist_id, created_at DESC)
  WHERE is_visible = TRUE;
DROP INDEX CONCURRENTLY idx_reviews_artist_id;
```

Deliberately **not applied in this pass**: `CREATE INDEX CONCURRENTLY` cannot
run inside the migration runner's transaction, so it needs its own deployment
step. Applying it non-concurrently would take an `ACCESS EXCLUSIVE` lock on
`reviews` — fine at 0 rows today, wrong as a committed habit.

```sql
-- Retention for the next two unbounded tables, before they become X3.
DELETE FROM notifications
 WHERE status = 'sent' AND created_at < now() - interval '90 days';
DELETE FROM audit_events
 WHERE created_at < now() - interval '2 years';
```

`audit_events` at two years is a compliance decision, not an engineering one —
flagged for the founder rather than chosen here.

### 5.2 Applied this session

| Patch | Where |
|---|---|
| `LIMIT 50` on the public review query | `internal/review/repository.go` |
| `max=` ceilings on four integer fields | `artist`, `billing` ×3, `promo` ×2 |
| Explicit `::text` / `::uuid` casts in `report.Resolve` | `internal/report/repository.go` |
| Hourly expired-credential reaper with a 24h grace period | `internal/maintenance` |
| `TRUSTED_PROXIES`, `PROXY_HEADER` documented | `.env.example` |

---

## 6. Cross-examination, recorded

**Leo → Daria:** if reminders add `scheduled_at`, does the notification payload
shape change? — *Daria:* no. `scheduled_at` is worker-side; the client never
reads it. The TS `Notification` interface is unaffected, which §1's diff will
confirm on the next run.

**Daria → Tarek:** can the reaper's `DELETE` block a login? — *Tarek:* no. Row
locks only, on rows nothing else touches, and expired tokens are by definition
not being read. The absent index on `expires_at` makes it a sequential scan;
irrelevant at 1240 rows, worth adding at a million.

**Tarek → Leo:** the client caps the deposit at the price — does it duplicate a
server rule or replace it? — *Leo:* duplicates, deliberately, and the comment
says so. The server is the guarantee; the form exists so the artist sees the
problem while typing rather than as a rejected save.

**Consensus:** the seam we expected to be worst (Angular ⇄ Go) is the healthiest
in the system. Everything that actually broke was Go ⇄ PostgreSQL, and every one
of those was a **missing ceiling** — no `LIMIT`, no `max`, no retention, no type
cast. That is the pattern to look for next time.
