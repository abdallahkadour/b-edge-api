# Per-Artist Service Pricing — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let each artist in a salon offer a chosen subset of the salon's services at her own optional price and deposit, with the owner able to override, and guarantee the price a customer is shown is the price she is charged.

**Architecture:** A new `artist_services` table (one row = "this artist offers this service"; NULL price/deposit = the salon's). The effective price is computed by ONE SQL fragment in a new leaf package `internal/pkg/pricing`, and a build-breaking guard test forbids any other SQL from reading `services.price`/`deposit_amount`. A new `internal/offering` domain owns the settings endpoints; booking, discovery and artist read through the fragment; the hold returns what it charged.

**Tech Stack:** Go 1.26 · Fiber v2 · pgx/v5 (no ORM) · PostgreSQL 15 · Angular 21 (zoneless, signals, standalone) · Tailwind 3.4 · Playwright WebKit for UI verification.

**Spec:** `b-edge-api/project-docs/B-Edge-Per-Artist-Pricing-Spec-v1.md` (approved 2026-09-25, `1a7c89b`). Decisions are cited as PP-1 … PP-9.

## Global Constraints

- Every money string from a request goes through `internal/pkg/money` (`money.Parse(raw, field)`); never `decimal.NewFromString` on input.
- A clearable field is `optional.Field[T]` (`IsSet()`, `IsNull()`, `Get()`); SQL pairs presence with value: `CASE WHEN $n THEN $m ELSE col END`.
- Ownership failures return **404**, built by ONE `errXNotFound()` constructor used by both the missing and the not-yours branch.
- Every list is built with `make([]*T, 0)`, never `var xs []*T`.
- Handlers pass `c.UserContext()`; validators come from `validation.New()`; body errors via `validation.MapBodyError`, struct errors via `validation.MapError`.
- Migrations are paired `.up`/`.down` with a prose header saying **why**; test both directions on a scratch database before touching dev.
- Go tests: in-package, hand-written mocks, `Test<Method>_<Condition>_<Expected>`, decimals compared with `.Equal()`. DB tests carry `//go:build dbtest` and use `testdb.New(t)`.
- **Every guard and every new test is watched failing before it is trusted.** Mutation checks use `${=TAGS}` in zsh (an unquoted `$TAGS` is passed as ONE argument) and must distinguish a real kill from a mutant that did not compile.
- `psql` always with `-v ON_ERROR_STOP=1`. A zero is only evidence once its denominator is shown to be non-zero.
- `@bedge/shared` resolves to `./dist/shared` — a BUILT library. After editing `projects/shared/**`, run `npx ng build shared` before building an app.
- Commit trailer: `Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`.

**Deviation from the spec, decided while planning:** the artist's own endpoints live at **`/api/v1/artists/salon/my-services`**, not `/artists/me/services`. `internal/middleware/routecoverage_test.go` only demands a capability guard on paths containing `/artists/salon/`; moving the routes there makes that existing build-breaking test enforce `own_services:write` automatically. Task 14 updates the spec to match.

**Two live defects this plan also fixes, found while reading the code:**
1. **Approval quotes the menu's deposit, not the booking's.** `ApproveBooking` builds *"Please send a $X deposit"* from `service.DepositAmount` — the salon menu's *current* value — instead of `b.DepositAmount`, stored when the booking was made. With per-artist deposits it would quote the salon's $30 to a customer who booked Rania's $60. (Task 3)
2. **The confirmation screen omits the early-bird fee.** The picker badges early-bird slots and the hold charges `price + early_bird_fee`, but the last funnel screen shows `service().price`. A customer confirms at $150 and is charged $165. (Tasks 4, 12)

---

## File Map

**b-edge-api — create**
| File | Responsibility |
|---|---|
| `db/migrations/052_artist_services.up.sql` / `.down.sql` | the table, constraints, backfill |
| `internal/pkg/pricing/pricing.go` | the ONE effective-price SQL fragment |
| `internal/pkg/pricing/pricing_test.go` | fragment unit tests |
| `internal/pkg/pricing/pricing_db_test.go` | the fragment executed against real rows |
| `internal/pkg/pricing/noleak_test.go` | guard: no other SQL reads service money |
| `internal/offering/{model,repository,service,handler}.go` | settings endpoints for switches, prices, deposits |
| `internal/offering/{service_test,repository_db_test,migration_db_test}.go` | tests |

**b-edge-api — modify**
| File | Change |
|---|---|
| `internal/booking/{model,repository,parties,holds,service}.go` | read the offering; approval uses the booking's deposit; hold returns its quote |
| `internal/discovery/{repository,service}.go` | profile lists the artist's offered services at her price |
| `internal/artist/{repository,service}.go` | public service list via the fragment; owner's new service gets the owner's row |
| `internal/onboarding/repository.go` | first service gets the owner's row |
| `internal/membership/repository.go` | leaving deletes her rows |
| `internal/pkg/salonrole/{role,role_test}.go` | two capabilities |
| `cmd/main.go` | register `offering` routes |
| `scripts/chaos-booking.py` | members switch services on; pricing cases |

**b-edge-web — create:** `projects/shared/src/lib/models/offering.model.ts`, `projects/shared/src/lib/core/offering-data.service.ts`, `projects/artist-dashboard/src/app/features/dashboard/service-offerings.component.{ts,html}`, `.../my-services.page.ts`, `.../member-services.page.ts`, `scripts/verify-offerings-ui.mjs`.
**b-edge-web — modify:** shared `models/index.ts`, `core` public API, `models/booking.model.ts`; dashboard `app.routes.ts`, `dashboard-layout.component.ts`, `team.component.html`, `join/join-salon.page.{ts,html}`; customer-pwa `booking-funnel.page.{ts,html}`, `screens/guest-details-screen.component.{ts,html}`.

---

### Task 1: Migration 052 — the `artist_services` table

**Files:**
- Create: `db/migrations/052_artist_services.up.sql`, `db/migrations/052_artist_services.down.sql`
- Create: `internal/offering/doc.go`, `internal/offering/migration_db_test.go`

**Interfaces:**
- Produces: table `artist_services(artist_id, service_id, price NULL, deposit_amount NULL, updated_by NULL, created_at, updated_at)`, PK `(artist_id, service_id)`.

- [ ] **Step 1: Write the failing DB test**

`internal/offering/doc.go`:
```go
// Package offering owns which services an artist offers and at what price.
//
// A row in artist_services means "this artist offers this service". A NULL
// price or deposit means "the salon's". See
// project-docs/B-Edge-Per-Artist-Pricing-Spec-v1.md.
package offering
```

`internal/offering/migration_db_test.go`:
```go
//go:build dbtest

package offering

// Constraints of migration 052, executed. Every one of these is a property
// of the SQL - a mock would accept any value.

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

type fixture struct {
	SalonID, OwnerArtist, ServiceID uuid.UUID
}

func newFixture(t *testing.T, pool *pgxpool.Pool) fixture {
	t.Helper()
	ctx := context.Background()
	var f fixture
	var owner uuid.UUID
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO users (name, email, password_hash, role)
		 VALUES ('Owner','owner@offering.local','x','artist') RETURNING id`).Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO salons (owner_id, name) VALUES ($1,'Offering Salon') RETURNING id`,
		owner).Scan(&f.SalonID))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO artists (user_id, salon_id) VALUES ($1,$2) RETURNING id`,
		owner, f.SalonID).Scan(&f.OwnerArtist))
	require.NoError(t, pool.QueryRow(ctx,
		`INSERT INTO services (salon_id, name, duration_min, price, deposit_amount)
		 VALUES ($1,'Bridal',90,150.00,30.00) RETURNING id`, f.SalonID).Scan(&f.ServiceID))
	return f
}

func TestArtistServices_NullPriceMeansSalon_Accepted(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO artist_services (artist_id, service_id) VALUES ($1,$2)`,
		f.OwnerArtist, f.ServiceID)
	assert.NoError(t, err, "a row with no override is how 'use the salon price' is stored")
}

func TestArtistServices_NegativePrice_Refused(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO artist_services (artist_id, service_id, price) VALUES ($1,$2,-1)`,
		f.OwnerArtist, f.ServiceID)
	assert.Error(t, err)
}

func TestArtistServices_NaNDeposit_Refused(t *testing.T) {
	// Postgres accepts 'NaN'::numeric; a stored NaN makes every later read
	// of the row fail. INJ-04 found this on services; it must not reappear.
	pool := testdb.New(t)
	f := newFixture(t, pool)
	_, err := pool.Exec(context.Background(),
		`INSERT INTO artist_services (artist_id, service_id, deposit_amount) VALUES ($1,$2,'NaN')`,
		f.OwnerArtist, f.ServiceID)
	assert.Error(t, err)
}

func TestArtistServices_OneRowPerArtistPerService(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id, service_id) VALUES ($1,$2)`,
		f.OwnerArtist, f.ServiceID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `INSERT INTO artist_services (artist_id, service_id) VALUES ($1,$2)`,
		f.OwnerArtist, f.ServiceID)
	assert.Error(t, err, "a second row would make 'her price' ambiguous")
}

func TestArtistServices_DeletingTheServiceRemovesTheRow(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id, service_id) VALUES ($1,$2)`,
		f.OwnerArtist, f.ServiceID)
	require.NoError(t, err)
	_, err = pool.Exec(ctx, `DELETE FROM services WHERE id=$1`, f.ServiceID)
	require.NoError(t, err)
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT count(*) FROM artist_services WHERE service_id=$1`, f.ServiceID).Scan(&n))
	assert.Equal(t, 0, n)
}
```

- [ ] **Step 2: Run it — expect FAIL**

Run: `go test -tags dbtest ./internal/offering/ -v`
Expected: every test FAILS with `relation "artist_services" does not exist`.

- [ ] **Step 3: Write the migration**

`db/migrations/052_artist_services.up.sql`:
```sql
-- 052_artist_services.up.sql
--
-- Which services an artist offers, and at what price.
--
-- ── The problem this fixes ───────────────────────────────────────────────
--
-- Services belong to the salon: one price, one deposit, and every artist in
-- the salon offers every service at that price. So in a salon with a makeup
-- artist and a nail artist, a customer can book the NAIL artist for Bridal
-- makeup, and the salon learns about it when the bride arrives. And a senior
-- artist cannot charge more than a junior one for the same service - the
-- founder's example is Rania's Bridal at $200 beside a colleague's at $100.
--
-- ── What this adds ───────────────────────────────────────────────────────
--
-- One row per artist per service she offers. NO ROW means she does not
-- offer it - there is deliberately no is_offered column, so "offered" can
-- never disagree with "has a row". A NULL price or deposit means "the
-- salon's"; the effective value is computed in ONE place,
-- internal/pkg/pricing, and nothing else may read services.price.
--
-- The deposit is per artist too, not only the price: bookings carry
-- CHECK (deposit_amount <= original_price), so a $25 junior price under a
-- $30 salon deposit would otherwise be unbookable. Where a blank deposit
-- follows the salon's above her price, the read CAPS it at the price.
--
-- ── Backfill ─────────────────────────────────────────────────────────────
--
-- Every existing artist gets a row for every service of her salon, ACTIVE
-- AND INACTIVE, prices NULL. Release day therefore changes nothing a
-- customer can see. Inactive services are included because the resolver
-- filters is_active already; leaving them out would mean a service
-- reactivated after release comes back offered by nobody.
--
-- Decisions: B-Edge-Per-Artist-Pricing-Spec-v1.md, PP-1 ... PP-9.

BEGIN;

CREATE TABLE artist_services (
    artist_id      UUID          NOT NULL REFERENCES artists(id)  ON DELETE CASCADE,
    service_id     UUID          NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    price          NUMERIC(10,2),
    deposit_amount NUMERIC(10,2),
    updated_by     UUID          REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (artist_id, service_id),
    CONSTRAINT artist_services_price_valid
        CHECK (price IS NULL OR (price >= 0 AND price <> 'NaN')),
    CONSTRAINT artist_services_deposit_valid
        CHECK (deposit_amount IS NULL OR (deposit_amount >= 0 AND deposit_amount <> 'NaN'))
);

-- The PK serves "this artist's services". This serves the reverse, used by
-- the cascade and by "who offers this service".
CREATE INDEX idx_artist_services_service ON artist_services (service_id);

INSERT INTO artist_services (artist_id, service_id)
SELECT a.id, s.id
  FROM artists a
  JOIN services s ON s.salon_id = a.salon_id
 WHERE a.salon_id IS NOT NULL
ON CONFLICT DO NOTHING;

COMMIT;
```

`db/migrations/052_artist_services.down.sql`:
```sql
-- 052_artist_services.down.sql
--
-- Drops the table. Nothing else references it, so the only loss is the
-- overrides themselves - every artist returns to offering every service of
-- her salon at the salon's price, which is exactly the behaviour before 052.
DROP TABLE IF EXISTS artist_services;
```

- [ ] **Step 4: Run the DB tests — expect PASS**

Run: `go test -tags dbtest ./internal/offering/ -v -count=1`
Expected: 5 PASS. (The template database is re-migrated automatically because `latestMigrationVersion()` moved to 52.)

- [ ] **Step 5: Up → down → up on a scratch copy of the dev data, and verify the backfill**

```bash
docker exec -i bedge-postgres psql -U postgres -tA -c "DROP DATABASE IF EXISTS bedge_052 WITH (FORCE);"
docker exec -i bedge-postgres psql -U postgres -tA -c "CREATE DATABASE bedge_052;"
docker exec -i bedge-postgres pg_dump -U postgres -d bedge | docker exec -i bedge-postgres psql -U postgres -d bedge_052 -q
P="docker exec -i bedge-postgres psql -U postgres -d bedge_052 -v ON_ERROR_STOP=1 -q"
$P < db/migrations/052_artist_services.up.sql && echo up
docker exec -i bedge-postgres psql -U postgres -d bedge_052 -tA -v ON_ERROR_STOP=1 -c "
SELECT 'pairs expected: '||count(*) FROM artists a JOIN services s ON s.salon_id=a.salon_id;
SELECT 'rows created:   '||count(*) FROM artist_services;
SELECT 'MISSING:        '||count(*) FROM artists a JOIN services s ON s.salon_id=a.salon_id
 WHERE NOT EXISTS (SELECT 1 FROM artist_services x WHERE x.artist_id=a.id AND x.service_id=s.id);"
$P < db/migrations/052_artist_services.down.sql && echo down
$P < db/migrations/052_artist_services.up.sql && echo "up again"
docker exec -i bedge-postgres psql -U postgres -tA -c "DROP DATABASE bedge_052 WITH (FORCE);"
```
Expected: `pairs expected` equals `rows created`, it is **not 0**, and `MISSING: 0`.

- [ ] **Step 6: Apply to dev and commit**

```bash
docker exec -i bedge-postgres psql -U postgres -d bedge -v ON_ERROR_STOP=1 -q < db/migrations/052_artist_services.up.sql
docker exec -i bedge-postgres psql -U postgres -d bedge -tA -c "UPDATE schema_migrations SET version=52, dirty=false;"
git add db/migrations/052_* internal/offering/
git commit -m "feat(db): artist_services - which services an artist offers, at what price (052)"
```

---

### Task 2: `internal/pkg/pricing` — the one price calculation, and the guard

**Files:**
- Create: `internal/pkg/pricing/pricing.go`, `pricing_test.go`, `pricing_db_test.go`, `noleak_test.go`

**Interfaces:**
- Produces (all return SQL expressions with NO alias, so callers place them positionally):
  - `func Price(s, os string) string` → effective price
  - `func Deposit(s, os string) string` → effective deposit, capped at the effective price
  - `func DepositCapped(s, os string) string` → boolean
  - `func Detail(s, os string) string` → 7 columns in this order: salon price, salon deposit, own price, own deposit, effective price, effective deposit, deposit capped

- [ ] **Step 1: Write the failing unit tests**

`internal/pkg/pricing/pricing_test.go`:
```go
package pricing

import (
	"strings"
	"testing"
)

func TestPrice_OverrideBeforeSalon(t *testing.T) {
	// Breaks if the COALESCE order is reversed - the salon price would win
	// over every artist's override and the feature would silently do nothing.
	if got := Price("sv", "o"); got != "COALESCE(o.price, sv.price)" {
		t.Fatalf("got %q", got)
	}
}

func TestDeposit_IsCappedAtTheEffectivePrice(t *testing.T) {
	// PP-6: a deposit above the price makes a booking unstorable
	// (bookings_deposit_not_above_price). The cap is what prevents that.
	want := "LEAST(COALESCE(o.deposit_amount, sv.deposit_amount), COALESCE(o.price, sv.price))"
	if got := Deposit("sv", "o"); got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}
}

func TestDetail_ColumnOrderIsTheContract(t *testing.T) {
	cols := strings.Split(Detail("sv", "o"), ", ")
	if len(cols) < 7 {
		t.Fatalf("Detail must yield 7 columns, got %d: %q", len(cols), Detail("sv", "o"))
	}
	if cols[0] != "sv.price" || cols[1] != "sv.deposit_amount" ||
		cols[2] != "o.price" || cols[3] != "o.deposit_amount" {
		t.Fatalf("first four columns must be salon price, salon deposit, own price, own deposit; got %q", cols[:4])
	}
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/pkg/pricing/ -v`
Expected: build failure, `undefined: Price`.

- [ ] **Step 3: Implement**

`internal/pkg/pricing/pricing.go`:
```go
// Package pricing is the one definition of what a customer pays for a
// service, as SQL.
//
// WHY THIS EXISTS
//
// Since migration 052 a service's price depends on the artist: her override
// in artist_services if she set one, the salon's otherwise. A customer sees
// that number in four places - the discovery profile, the artist's public
// service list, the hold, and the booking. Four independent calculations is
// how "shown $100, charged $200" happens, and this codebase has already
// shipped that class three times (subscriptionVisibleCond, calendar_sequence,
// the cross-salon booking). So the calculation lives here, once, and
// noleak_test.go fails the build if any other SQL reads services.price or
// services.deposit_amount.
//
// WHY SQL EXPRESSIONS WITHOUT ALIASES
//
// Callers scan into existing structs whose column order is fixed (for
// example artist.scanServices). An unaliased expression can be dropped into
// any position; an aliased fragment of three columns could not.
//
// s is the alias of the services table, os of artist_services. With a LEFT
// JOIN and no row, os.* is NULL and every expression falls back to the
// salon's values - which is what the settings screen needs for a service
// she has not switched on.
package pricing

import "fmt"

// Price is the effective price: her override, else the salon's.
func Price(s, os string) string {
	return fmt.Sprintf("COALESCE(%[2]s.price, %[1]s.price)", s, os)
}

// Deposit is the effective deposit, capped at the effective price (PP-6).
// The cap only ever lowers what a customer pays upfront, and a booking must
// never fail because two numbers set by two people drifted apart.
func Deposit(s, os string) string {
	return fmt.Sprintf("LEAST(COALESCE(%[2]s.deposit_amount, %[1]s.deposit_amount), %[3]s)",
		s, os, Price(s, os))
}

// DepositCapped reports whether the cap in Deposit changed the value, so the
// settings screen can say so instead of silently showing a lower number.
func DepositCapped(s, os string) string {
	return fmt.Sprintf("(COALESCE(%[2]s.deposit_amount, %[1]s.deposit_amount) > %[3]s)",
		s, os, Price(s, os))
}

// Detail is the settings-screen row: salon price, salon deposit, own price,
// own deposit, effective price, effective deposit, deposit capped - in that
// order, which is part of the contract (TestDetail_ColumnOrderIsTheContract).
func Detail(s, os string) string {
	return fmt.Sprintf("%[1]s.price, %[1]s.deposit_amount, %[2]s.price, %[2]s.deposit_amount, %[3]s, %[4]s, %[5]s",
		s, os, Price(s, os), Deposit(s, os), DepositCapped(s, os))
}
```

- [ ] **Step 4: Run — expect PASS**

Run: `go test ./internal/pkg/pricing/ -v` → 3 PASS.

- [ ] **Step 5: The fragment against real rows (DB test, write it, run it)**

`internal/pkg/pricing/pricing_db_test.go`:
```go
//go:build dbtest

package pricing

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

// resolve inserts one salon service (price 150, deposit 30) and optionally an
// override row, then evaluates the three expressions exactly as callers do.
func resolve(t *testing.T, own *[2]*string) (price, deposit decimal.Decimal, capped bool) {
	t.Helper()
	pool := testdb.New(t)
	ctx := context.Background()
	var owner, salon, artist, svc uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('P','p@pricing.local','x','artist') RETURNING id`).Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id,name) VALUES ($1,'S') RETURNING id`,
		owner).Scan(&salon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO artists (user_id,salon_id) VALUES ($1,$2) RETURNING id`,
		owner, salon).Scan(&artist))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price,deposit_amount)
		VALUES ($1,'Bridal',90,150.00,30.00) RETURNING id`, salon).Scan(&svc))
	if own != nil {
		_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id,service_id,price,deposit_amount)
			VALUES ($1,$2,$3::numeric,$4::numeric)`, artist, svc, own[0], own[1])
		require.NoError(t, err)
	}
	q := "SELECT " + Price("s", "os") + ", " + Deposit("s", "os") + ", " + DepositCapped("s", "os") +
		" FROM services s LEFT JOIN artist_services os ON os.service_id = s.id AND os.artist_id = $1 WHERE s.id = $2"
	require.NoError(t, pool.QueryRow(ctx, q, artist, svc).Scan(&price, &deposit, &capped))
	return
}

func str(s string) *string { return &s }

func TestResolve_NoRow_SalonValues(t *testing.T) {
	p, d, c := resolve(t, nil)
	assert.True(t, p.Equal(decimal.RequireFromString("150")), "got %s", p)
	assert.True(t, d.Equal(decimal.RequireFromString("30")), "got %s", d)
	assert.False(t, c)
}

func TestResolve_Override_Wins(t *testing.T) {
	p, d, _ := resolve(t, &[2]*string{str("200"), str("60")})
	assert.True(t, p.Equal(decimal.RequireFromString("200")), "got %s", p)
	assert.True(t, d.Equal(decimal.RequireFromString("60")), "got %s", d)
}

func TestResolve_BlankOverride_FallsBackToSalon(t *testing.T) {
	p, d, _ := resolve(t, &[2]*string{nil, nil})
	assert.True(t, p.Equal(decimal.RequireFromString("150")), "got %s", p)
	assert.True(t, d.Equal(decimal.RequireFromString("30")), "got %s", d)
}

func TestResolve_SalonDepositAboveHerPrice_Capped(t *testing.T) {
	// Her price 25 with a blank deposit, under the salon's 30.
	p, d, c := resolve(t, &[2]*string{str("25"), nil})
	assert.True(t, p.Equal(decimal.RequireFromString("25")))
	assert.True(t, d.Equal(decimal.RequireFromString("25")), "deposit must be capped at the price, got %s", d)
	assert.True(t, c, "the cap must be reported, not silent")
}
```
Run: `go test -tags dbtest ./internal/pkg/pricing/ -v -count=1` → 4 PASS.

- [ ] **Step 6: Write the guard, with its temporary allowlist**

`internal/pkg/pricing/noleak_test.go`:
```go
package pricing

// Anti-drift guard: no SQL outside this package may read the price or
// deposit of the services table.
//
// It scans every string literal in internal/ and flags one that names the
// services table (FROM/JOIN services) AND reads price or deposit_amount -
// "final_price" and "effective_price" do not match, because the token must
// not be preceded by a letter or underscore.
//
// Allowlisted by FUNCTION, not file: artist/repository.go holds the owner's
// menu (which must read the salon price) beside code that must not.
//
// Known limitation, stated: a query split across two literals with the table
// in one and the column in the other is not seen. Every current reader keeps
// both in one literal.
//
// PROVEN TO FIRE: see the commit that introduced it.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var (
	reServicesTable = regexp.MustCompile(`(?i)\b(from|join)\s+services\b`)
	reMoneyColumn   = regexp.MustCompile(`(?i)(^|[^a-z_])(price|deposit_amount)\b`)
)

// allowedReaders are functions that legitimately read the SALON's price.
// Entries marked TEMPORARY are removed by the task that migrates them;
// TestAllowlist_EveryEntryStillReadsMoney fails if one is left behind.
var allowedReaders = map[string]string{
	"artist/repository.go:GetServicesBySalon": "the owner's menu screen - it shows and edits the SALON price, which is what it must read",
	"artist/repository.go:GetServiceByID":     "the owner editing one service of the salon menu",
	"booking/repository.go:GetService":        "TEMPORARY - replaced by GetOfferedService in Task 3",
	"discovery/repository.go:GetSalonServices": "TEMPORARY - replaced by GetArtistServices in Task 5",
}

func moneyReaders(t *testing.T) map[string]bool {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolving internal/: %v", err)
	}
	found := map[string]bool{}
	fset := token.NewFileSet()
	err = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if strings.HasPrefix(rel, "pkg/pricing/") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return nil
		}
		for _, decl := range file.Decls {
			// Package-level SQL is scanned too. Several repositories keep
			// column lists as consts (bookingSelectCols, enrichedSelectCols);
			// a guard that only looked inside functions would miss one of
			// those reading services.price outright.
			var name string
			var node ast.Node
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Body == nil {
					continue
				}
				name, node = d.Name.Name, d.Body
			case *ast.GenDecl:
				if len(d.Specs) == 0 {
					continue
				}
				if vs, ok := d.Specs[0].(*ast.ValueSpec); ok && len(vs.Names) > 0 {
					name = vs.Names[0].Name
				} else {
					name = "(decl)"
				}
				node = d
			default:
				continue
			}
			ast.Inspect(node, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if ok && lit.Kind == token.STRING &&
					reServicesTable.MatchString(lit.Value) && reMoneyColumn.MatchString(lit.Value) {
					found[rel+":"+name] = true
				}
				return true
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/: %v", err)
	}
	return found
}

func TestNoStrayReadsOfServiceMoney(t *testing.T) {
	var offenders []string
	for where := range moneyReaders(t) {
		if _, ok := allowedReaders[where]; !ok {
			offenders = append(offenders, where)
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("these functions read services.price or services.deposit_amount directly:\n\n  %s\n\n"+
			"Use pricing.Price / pricing.Deposit (or pricing.Detail) over a JOIN to "+
			"artist_services. Since migration 052 a price depends on the artist; a "+
			"second calculation is how a customer is shown one price and charged another.",
			strings.Join(offenders, "\n  "))
	}
}

func TestAllowlist_EveryEntryStillReadsMoney(t *testing.T) {
	found := moneyReaders(t)
	for where := range allowedReaders {
		if !found[where] {
			t.Errorf("allowlist entry %q no longer reads service money - delete it", where)
		}
	}
}
```

- [ ] **Step 7: Prove the guard fires — inside a function AND at package level — then run it green**

```bash
cat > internal/booking/zz_leak.go <<'EOF'
package booking
func zzLeak() string { return `SELECT price FROM services WHERE id = $1` }
EOF
go test ./internal/pkg/pricing/ -run TestNoStrayReadsOfServiceMoney 2>&1 | grep -E "FAIL|zzLeak"
cat > internal/booking/zz_leak.go <<'EOF'
package booking
const zzLeakCols = `SELECT s.deposit_amount FROM services s`
EOF
go test ./internal/pkg/pricing/ -run TestNoStrayReadsOfServiceMoney 2>&1 | grep -E "FAIL|zzLeakCols"
rm internal/booking/zz_leak.go
go test ./internal/pkg/pricing/ -v
```
Expected: the first run FAILS naming `booking/zz_leak.go:zzLeak`, the second `booking/zz_leak.go:zzLeakCols`; after removal both guard tests PASS. If either leak is NOT reported, the guard is blind there — fix it before continuing.

- [ ] **Step 8: Commit**

```bash
git add internal/pkg/pricing/
git commit -m "feat(pricing): one effective-price fragment, and a guard forbidding any other reader"
```

---

### Task 3: Booking reads the offering; approval quotes the booking's deposit

**Files:**
- Modify: `internal/booking/model.go` (add `ErrServiceNotFound`, `SalonService.DepositCapped`)
- Modify: `internal/booking/repository.go` (remove `GetService`, add `GetOfferedService`, `GetServiceDepositDeadlineHours`)
- Modify: `internal/booking/parties.go`, `internal/booking/service.go` (ApproveBooking)
- Modify: `internal/booking/service_test.go` (mock), `internal/booking/parties_test.go`
- Modify: `internal/booking/repository_db_test.go`
- Modify: `internal/pkg/pricing/noleak_test.go` (remove the TEMPORARY booking entry)

**Interfaces:**
- Consumes: `pricing.Price`, `pricing.Deposit`, `pricing.DepositCapped` (Task 2)
- Produces: `Repository.GetOfferedService(ctx, artistID, serviceID uuid.UUID) (*SalonService, error)` returning `ErrServiceNotFound` when the service is missing, inactive or not offered by her; `Repository.GetServiceDepositDeadlineHours(ctx, serviceID uuid.UUID) (int, error)`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/booking/parties_test.go`:
```go
func TestHoldGuestSlot_ServiceNotOffered_NotFoundAndNothingWritten(t *testing.T) {
	// PP-4: a service she has switched off is refused exactly like a missing
	// one. The repository reports both as ErrServiceNotFound; this asserts the
	// guard turns that into the SAME 404 and writes nothing.
	repo := &mockRepo{getServiceErr: ErrServiceNotFound, getStoreStore: defaultStore()}

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdReq())

	assert.Equal(t, "SERVICE_NOT_FOUND", appErrOf(t, err).Code)
	assert.Nil(t, repo.createBookingCaptured)
}

func TestHoldGuestSlot_DatabaseErrorOnService_IsNotA404(t *testing.T) {
	// Breaks if every error is mapped to "not found": a database outage
	// would then tell customers the service does not exist.
	repo := &mockRepo{getServiceErr: errors.New("connection reset"), getStoreStore: defaultStore()}

	_, err := newTestService(repo).HoldGuestSlot(context.Background(), holdReq())

	require.Error(t, err)
	var appErr *apperror.AppError
	assert.False(t, errors.As(err, &appErr), "a database error must surface as an internal error, got %v", err)
}
```
Add `"errors"` to that file's imports. In the same file change the missing-service fixture of `TestHoldGuestSlot_ForeignService_IndistinguishableFromMissing` from `getServiceErr: ErrStoreNotFound` to `getServiceErr: ErrServiceNotFound`.

Append to `internal/booking/service_test.go`:
```go
func TestApproveBooking_MessageQuotesTheBookingsDeposit(t *testing.T) {
	// The booking was made at a $60 deposit (Rania's own). The salon menu
	// says $30. The customer must be asked for what she agreed to, not for
	// whatever the menu says today.
	artistID := uuid.New()
	b := &Booking{
		ID: uuid.New(), ArtistID: artistID, CustomerID: uuid.New(), ServiceID: uuid.New(),
		StartTime:     time.Now().UTC().Add(72 * time.Hour),
		Status:        StatusPending,
		DepositAmount: decimal.RequireFromString("60.00"),
	}
	menu := defaultService()
	menu.DepositAmount = decimal.RequireFromString("30.00")
	repo := &mockRepo{
		getBookingByIDBooking:       b,
		getArtistIDByUserIDArtistID: artistID, // the requester IS the booking's artist
		getServiceSvc:               menu,
		notificationContextCustomer: "Maya",
		notificationContextService:  "Bridal",
	}

	_, err := newTestService(repo).ApproveBooking(context.Background(), b.ID, uuid.New())
	require.NoError(t, err)

	require.NotEmpty(t, repo.enqueuedNotifications, "an approval must notify the customer")
	msg := repo.enqueuedNotifications[len(repo.enqueuedNotifications)-1].Message
	assert.Contains(t, msg, "$60.00", "must quote the booking's deposit")
	assert.NotContains(t, msg, "$30.00", "must not quote the menu's current deposit")
}
```
(Field names verified against the mock: `getBookingByIDBooking`, `getArtistIDByUserIDArtistID`, `notificationContextCustomer`, `notificationContextService`, and `enqueuedNotification.Message`. `ApproveBooking(ctx, bookingID, requesterUserID)` has no role parameter; authorisation is the requester's artist matching the booking's, as `TestApproveBooking_Success` does.)

Append to `internal/booking/repository_db_test.go`:
```go
func offer(t *testing.T, pool *pgxpool.Pool, artist, service uuid.UUID, price, deposit *string) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO artist_services (artist_id, service_id, price, deposit_amount)
		 VALUES ($1,$2,$3::numeric,$4::numeric)`, artist, service, price, deposit)
	require.NoError(t, err)
}

func strp(s string) *string { return &s }

func TestGetOfferedService_Override_Wins(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool) // service price 100.00, deposit 0
	offer(t, pool, f.ArtistID, f.ServiceID, strp("200.00"), strp("40.00"))

	s, err := NewRepository(pool).GetOfferedService(context.Background(), f.ArtistID, f.ServiceID)

	require.NoError(t, err)
	assert.True(t, s.Price.Equal(decimal.RequireFromString("200")), "got %s", s.Price)
	assert.True(t, s.DepositAmount.Equal(decimal.RequireFromString("40")), "got %s", s.DepositAmount)
}

func TestGetOfferedService_NoOverride_SalonPrice(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	offer(t, pool, f.ArtistID, f.ServiceID, nil, nil)

	s, err := NewRepository(pool).GetOfferedService(context.Background(), f.ArtistID, f.ServiceID)

	require.NoError(t, err)
	assert.True(t, s.Price.Equal(decimal.RequireFromString("100")), "got %s", s.Price)
}

func TestGetOfferedService_NotOffered_ErrServiceNotFound(t *testing.T) {
	// Breaks if the JOIN becomes a LEFT JOIN: every artist would then be
	// bookable for every service, which is the behaviour PP-4 removes.
	pool := testdb.New(t)
	f := newFixture(t, pool)
	offer(t, pool, f.Artist2ID, f.ServiceID, nil, nil) // a COLLEAGUE offers it; she does not

	_, err := NewRepository(pool).GetOfferedService(context.Background(), f.ArtistID, f.ServiceID)

	assert.ErrorIs(t, err, ErrServiceNotFound)
}

func TestGetOfferedService_InactiveService_ErrServiceNotFound(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	offer(t, pool, f.ArtistID, f.ServiceID, nil, nil)
	_, err := pool.Exec(context.Background(), `UPDATE services SET is_active=false WHERE id=$1`, f.ServiceID)
	require.NoError(t, err)

	_, err = NewRepository(pool).GetOfferedService(context.Background(), f.ArtistID, f.ServiceID)

	assert.ErrorIs(t, err, ErrServiceNotFound)
}

func TestGetServiceDepositDeadlineHours_InactiveServiceStillAnswers(t *testing.T) {
	// PP-9: approving an already-agreed booking must not depend on the menu
	// still listing the service.
	pool := testdb.New(t)
	f := newFixture(t, pool)
	_, err := pool.Exec(context.Background(),
		`UPDATE services SET is_active=false, deposit_deadline_hours=12 WHERE id=$1`, f.ServiceID)
	require.NoError(t, err)

	h, err := NewRepository(pool).GetServiceDepositDeadlineHours(context.Background(), f.ServiceID)

	require.NoError(t, err)
	assert.Equal(t, 12, h)
}
```

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/booking/ 2>&1 | head -5` then `go test -tags dbtest ./internal/booking/ -run 'GetOfferedService|DeadlineHours' 2>&1 | head -5`
Expected: build failures — `undefined: ErrServiceNotFound`, `GetOfferedService undefined`.

- [ ] **Step 3: Implement**

`internal/booking/model.go` — beside `ErrStoreNotFound`:
```go
	// ErrServiceNotFound: the service does not exist, is inactive, or THIS
	// artist does not offer it. The three are one error on purpose - a
	// switched-off service must be indistinguishable from a missing one.
	ErrServiceNotFound = errors.New("service not found")
```
and in `SalonService`, after `DepositAmount`:
```go
	// DepositCapped is true when her deposit (or the salon's, if she left
	// hers blank) exceeded her price and was capped to it. See pricing.Deposit.
	DepositCapped bool `db:"deposit_capped"`
```

`internal/booking/repository.go` — in the `Repository` interface replace the `GetService` line with:
```go
	// GetOfferedService returns the service as THIS ARTIST sells it: her
	// price and deposit where she set them, the salon's otherwise, the
	// deposit capped at the price. ErrServiceNotFound when the service does
	// not exist, is inactive, or she does not offer it.
	GetOfferedService(ctx context.Context, artistID, serviceID uuid.UUID) (*SalonService, error)

	// GetServiceDepositDeadlineHours reads ONLY the deadline window. Approval
	// uses it, and must quote the BOOKING's stored deposit - never the menu's.
	GetServiceDepositDeadlineHours(ctx context.Context, serviceID uuid.UUID) (int, error)
```
Delete `func (r *pgRepo) GetService(...)` entirely and add:
```go
func (r *pgRepo) GetOfferedService(ctx context.Context, artistID, serviceID uuid.UUID) (*SalonService, error) {
	s := &SalonService{}
	err := r.db.QueryRow(ctx, `
		SELECT s.id, s.salon_id, s.name, s.duration_min, s.buffer_min, s.active_duration_min,
		       `+pricing.Price("s", "os")+`, `+pricing.Deposit("s", "os")+`, `+pricing.DepositCapped("s", "os")+`,
		       s.deposit_deadline_hours, s.is_active
		  FROM services s
		  JOIN artist_services os ON os.service_id = s.id AND os.artist_id = $1
		 WHERE s.id = $2 AND s.is_active = TRUE`,
		artistID, serviceID,
	).Scan(
		&s.ID, &s.SalonID, &s.Name, &s.DurationMin, &s.BufferMin, &s.ActiveDurationMin,
		&s.Price, &s.DepositAmount, &s.DepositCapped,
		&s.DepositDeadlineHours, &s.IsActive,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrServiceNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get offered service: %w", err)
	}
	return s, nil
}

func (r *pgRepo) GetServiceDepositDeadlineHours(ctx context.Context, serviceID uuid.UUID) (int, error) {
	var hours int
	err := r.db.QueryRow(ctx,
		`SELECT deposit_deadline_hours FROM services WHERE id = $1`, serviceID).Scan(&hours)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrServiceNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("get service deposit deadline: %w", err)
	}
	return hours, nil
}
```
Add the import `"github.com/abdallahkadour/b-edge-api/internal/pkg/pricing"`.

`internal/booking/parties.go` — replace the `GetService` block at the top of `validateBookingParties` with:
```go
	// Resolved AS THIS ARTIST SELLS IT (migration 052): her price, and not
	// found at all if she does not offer it. A switched-off service is
	// therefore refused here with the same 404 as a missing one.
	service, err := s.repo.GetOfferedService(ctx, artistID, serviceID)
	if err != nil {
		if errors.Is(err, ErrServiceNotFound) {
			return nil, nil, errServiceNotFound()
		}
		return nil, nil, fmt.Errorf("booking parties: get offered service: %w", err)
	}
	if service == nil {
		return nil, nil, errServiceNotFound()
	}
```

`internal/booking/service.go` — in `ApproveBooking` replace the `GetService` block and the two `service.DepositAmount` uses:
```go
	// Only the deadline window comes from the menu. The deposit comes from
	// the BOOKING: it was agreed when the booking was made, and since
	// migration 052 it can be the artist's own rather than the salon's.
	// This used to quote service.DepositAmount - the menu's CURRENT value.
	deadlineRaw, err := s.repo.GetServiceDepositDeadlineHours(ctx, b.ServiceID)
	if err != nil {
		return nil, fmt.Errorf("approve booking: get deposit deadline: %w", err)
	}
	deadlineHours := time.Duration(deadlineRaw) * time.Hour
```
then `if service.DepositAmount.IsPositive() {` → `if b.DepositAmount.IsPositive() {` and `service.DepositAmount.String(), hoursRemaining,` → `b.DepositAmount.String(), hoursRemaining,`.

`internal/booking/service_test.go` — replace the mock's `GetService` method with:
```go
func (m *mockRepo) GetOfferedService(_ context.Context, _, _ uuid.UUID) (*SalonService, error) {
	return m.getServiceSvc, m.getServiceErr
}
func (m *mockRepo) GetServiceDepositDeadlineHours(_ context.Context, _ uuid.UUID) (int, error) {
	if m.getServiceSvc == nil {
		return 0, m.getServiceErr
	}
	return m.getServiceSvc.DepositDeadlineHours, m.getServiceErr
}
```

`internal/pkg/pricing/noleak_test.go` — delete the `"booking/repository.go:GetService"` entry.

- [ ] **Step 4: Run — expect PASS**

```bash
go build ./... && go vet ./...
go test ./internal/booking/ ./internal/pkg/pricing/
go test -tags dbtest ./internal/booking/ -count=1
```
Expected: all PASS. If an older test fails because it set a generic `getServiceErr` expecting `SERVICE_NOT_FOUND`, change that fixture to `ErrServiceNotFound` — a database outage is not a missing service.

- [ ] **Step 5: Mutation check**

```bash
M() { cp "$1" /tmp/m.bak; python3 - "$1" "$2" "$3" <<'PY'
import io,sys;p,o,n=sys.argv[1:];s=io.open(p).read();assert o in s,o;io.open(p,"w").write(s.replace(o,n,1))
PY
go test ${=4} ./internal/booking/ -run "$5" >/tmp/m.out 2>&1; cp /tmp/m.bak "$1"
if grep -q "build failed\|no Go files\|setup failed" /tmp/m.out; then echo "FALSE KILL: $6"
elif grep -q "^--- FAIL" /tmp/m.out; then echo "killed:     $6"; else echo "SURVIVED:   $6"; fi; }
M internal/booking/repository.go "JOIN artist_services os ON" "LEFT JOIN artist_services os ON" "-tags dbtest" "NotOffered" "not-offered becomes bookable"
M internal/booking/service.go "if b.DepositAmount.IsPositive() {" "if true {" "" "QuotesTheBookingsDeposit" "deposit check ignores the booking"
M internal/booking/service.go "b.DepositAmount.String(), hoursRemaining," "\"30.00\", hoursRemaining," "" "QuotesTheBookingsDeposit" "message quotes a fixed amount"
M internal/booking/parties.go "if errors.Is(err, ErrServiceNotFound) {" "if err != nil {" "" "DatabaseErrorOnService" "every error becomes a 404"
```
Expected: four `killed`. Anything else — fix the test or the mutant before continuing.

- [ ] **Step 6: Commit**

```bash
git add internal/booking/ internal/pkg/pricing/noleak_test.go
git commit -m "feat(booking): book her service at her price; approval quotes the booking's deposit"
```

---

### Task 4: The hold returns what it charged

**Files:**
- Modify: `internal/booking/model.go` (`HoldGuestSlotResponse`), `internal/booking/holds.go`
- Test: `internal/booking/parties_test.go`

**Interfaces:**
- Produces: JSON fields on the hold response — `original_price`, `early_bird_fee`, `final_price`, `deposit_amount` (decimal strings).

- [ ] **Step 1: Write the failing test**

Append to `internal/booking/parties_test.go`:
```go
func TestHoldGuestSlot_ReturnsWhatItCharged(t *testing.T) {
	// The last funnel screen used to display the price remembered from when
	// the profile opened. It must display THIS, which is what was stored.
	svc := defaultService()
	svc.Price = decimal.RequireFromString("200.00")
	svc.DepositAmount = decimal.RequireFromString("60.00")
	repo := &mockRepo{getServiceSvc: svc, getStoreStore: defaultStore()}

	res, err := newTestService(repo).HoldGuestSlot(context.Background(), holdReq())

	require.NoError(t, err)
	stored := repo.createBookingCaptured
	require.NotNil(t, stored)
	assert.True(t, res.FinalPrice.Equal(stored.FinalPrice), "shown %s, stored %s", res.FinalPrice, stored.FinalPrice)
	assert.True(t, res.OriginalPrice.Equal(stored.OriginalPrice))
	assert.True(t, res.DepositAmount.Equal(stored.DepositAmount))
	assert.True(t, res.FinalPrice.Equal(res.OriginalPrice.Add(res.EarlyBirdFee)),
		"final must equal original + early-bird fee")
}
```
Add `"github.com/shopspring/decimal"` to the imports. In `TestHoldGuestSlot_EarlyBird_SurchargeApplied` (service_test.go), add after its existing assertions:
```go
	assert.True(t, res.EarlyBirdFee.IsPositive(), "the early-bird fee must be returned, not only charged")
```
(If that test discards the result, capture it as `res, err :=` first.)

- [ ] **Step 2: Run — expect FAIL**

Run: `go test ./internal/booking/ -run 'ReturnsWhatItCharged|EarlyBird_SurchargeApplied'`
Expected: build failure `res.FinalPrice undefined`.

- [ ] **Step 3: Implement**

`internal/booking/model.go`:
```go
type HoldGuestSlotResponse struct {
	BookingID uuid.UUID `json:"booking_id"`
	HeldUntil time.Time `json:"held_until"`
	StartTime time.Time `json:"start_time"`
	EndTime   time.Time `json:"end_time"`

	// What this hold CHARGED. The funnel's last screen displays these rather
	// than numbers remembered from the start of the funnel - which missed
	// the early-bird fee, and since migration 052 could also be a price the
	// artist has since changed.
	OriginalPrice decimal.Decimal `json:"original_price"`
	EarlyBirdFee  decimal.Decimal `json:"early_bird_fee"`
	FinalPrice    decimal.Decimal `json:"final_price"`
	DepositAmount decimal.Decimal `json:"deposit_amount"`
}
```

`internal/booking/holds.go` — replace the early-bird block with:
```go
	earlyBirdFee := zeroDecimal()
	if isEarlyBirdSlot(store, startTime.UTC()) {
		earlyBirdFee = store.EarlyBirdFee
	}
	finalPrice := service.Price.Add(earlyBirdFee)
```
and the return with:
```go
	return &HoldGuestSlotResponse{
		BookingID:     b.ID,
		HeldUntil:     heldUntil,
		StartTime:     b.StartTime,
		EndTime:       b.EndTime,
		OriginalPrice: b.OriginalPrice,
		EarlyBirdFee:  earlyBirdFee,
		FinalPrice:    b.FinalPrice,
		DepositAmount: b.DepositAmount,
	}, nil
```

- [ ] **Step 4: Run — expect PASS**; then `make swagger`.

Run: `go test ./internal/booking/` → PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/booking/
git commit -m "feat(booking): the hold returns what it charged, early-bird fee included"
```

---

### Task 5: Customer-facing service lists use her price

**Files:**
- Modify: `internal/discovery/repository.go`, `internal/discovery/service.go`, `internal/discovery/service_test.go` (mock)
- Modify: `internal/artist/repository.go`, `internal/artist/service.go`, `internal/artist/service_test.go` (mock)
- Create: tests in `internal/discovery/repository_db_test.go`, `internal/artist/repository_db_test.go` (append)
- Modify: `internal/pkg/pricing/noleak_test.go` (remove the TEMPORARY discovery entry)

**Interfaces:**
- Produces: `discovery.Repository.GetArtistServices(ctx, artistID uuid.UUID) ([]*ServiceRow, error)` (replaces `GetSalonServices`); `artist.Repository.GetOfferedServicesByArtist(ctx, artistID uuid.UUID) ([]*SalonServiceRecord, error)`.

- [ ] **Step 1: Write the failing DB tests**

Create `internal/discovery/repository_db_test.go`:
```go
//go:build dbtest

package discovery

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func TestGetArtistServices_OnlyHerOfferings_AtHerPrice(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	var owner, salon, artist, bridal, nails uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('R','r@disc.local','x','artist') RETURNING id`).Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO salons (owner_id,name) VALUES ($1,'S') RETURNING id`,
		owner).Scan(&salon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO artists (user_id,salon_id) VALUES ($1,$2) RETURNING id`,
		owner, salon).Scan(&artist))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Bridal',90,100) RETURNING id`, salon).Scan(&bridal))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Nails',45,40) RETURNING id`, salon).Scan(&nails))
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id,service_id,price) VALUES ($1,$2,200)`,
		artist, bridal)
	require.NoError(t, err) // she offers Bridal at 200; she does NOT offer Nails

	rows, err := NewRepository(pool).GetArtistServices(ctx, artist)

	require.NoError(t, err)
	require.Len(t, rows, 1, "a service she has not switched on must not be listed")
	assert.Equal(t, "Bridal", rows[0].Name)
	assert.True(t, rows[0].Price.Equal(decimal.RequireFromString("200")), "got %s", rows[0].Price)
}
```
Append to `internal/artist/repository_db_test.go`:
```go
func TestGetOfferedServicesByArtist_OnlyHerOfferings_AtHerPrice(t *testing.T) {
	pool := testdb.New(t)
	ctx := context.Background()
	artistID := newArtistFixture(t, pool)
	var salon, bridal, nails uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `SELECT salon_id FROM artists WHERE id=$1`, artistID).Scan(&salon))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Bridal',90,100) RETURNING id`, salon).Scan(&bridal))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO services (salon_id,name,duration_min,price)
		VALUES ($1,'Nails',45,40) RETURNING id`, salon).Scan(&nails))
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id,service_id,price) VALUES ($1,$2,200)`,
		artistID, bridal)
	require.NoError(t, err)

	recs, err := NewRepository(pool).GetOfferedServicesByArtist(ctx, artistID)

	require.NoError(t, err)
	require.Len(t, recs, 1, "the owner's menu has two services; the customer must see only hers")
	assert.Equal(t, "Bridal", recs[0].Name)
	assert.True(t, recs[0].Price.Equal(decimal.RequireFromString("200")), "got %s", recs[0].Price)
}
```
(add `"github.com/shopspring/decimal"` to that file's imports if absent).

- [ ] **Step 2: Run — expect FAIL**

Run: `go test -tags dbtest ./internal/discovery/ ./internal/artist/ -run 'GetArtistServices|GetOfferedServicesByArtist' 2>&1 | head -5`
Expected: build failure — methods undefined.

- [ ] **Step 3: Implement**

`internal/discovery/repository.go` — replace the `GetSalonServices` interface line and function:
```go
	// GetArtistServices returns the services THIS ARTIST offers, at her
	// price and deposit, cheapest first. Services she has not switched on
	// are not listed.
	GetArtistServices(ctx context.Context, artistID uuid.UUID) ([]*ServiceRow, error)
```
```go
func (r *pgRepo) GetArtistServices(ctx context.Context, artistID uuid.UUID) ([]*ServiceRow, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.id, s.name, s.duration_min,
		       `+pricing.Price("s", "os")+` AS effective_price,
		       `+pricing.Deposit("s", "os")+` AS effective_deposit
		  FROM services s
		  JOIN artist_services os ON os.service_id = s.id AND os.artist_id = $1
		 WHERE s.is_active = TRUE
		 ORDER BY effective_price ASC, s.name ASC`,
		artistID,
	)
	if err != nil {
		return nil, fmt.Errorf("get artist services: %w", err)
	}
	defer rows.Close()
	result := make([]*ServiceRow, 0)
	for rows.Next() {
		sr := &ServiceRow{}
		if err := rows.Scan(&sr.ID, &sr.Name, &sr.DurationMin, &sr.Price, &sr.DepositAmount); err != nil {
			return nil, fmt.Errorf("scan artist service: %w", err)
		}
		result = append(result, sr)
	}
	return result, rows.Err()
}
```
`internal/discovery/service.go` in `GetArtistProfile` — replace `s.repo.GetSalonServices(ctx, *profile.SalonID)` with `s.repo.GetArtistServices(ctx, artistID)` (keep the `profile.SalonID != nil` guard). Rename the mock method in `internal/discovery/service_test.go` to `GetArtistServices(_ context.Context, _ uuid.UUID)`.

`internal/artist/repository.go` — add to the interface and implement:
```go
	// GetOfferedServicesByArtist is the CUSTOMER's view of an artist's menu:
	// only services she offers, active only, at her price and deposit.
	// Scanned by scanServices, so the column order below is fixed.
	GetOfferedServicesByArtist(ctx context.Context, artistID uuid.UUID) ([]*SalonServiceRecord, error)
```
```go
func (r *pgRepo) GetOfferedServicesByArtist(ctx context.Context, artistID uuid.UUID) ([]*SalonServiceRecord, error) {
	rows, err := r.db.Query(ctx, `
		SELECT s.id, s.salon_id, s.category_id, s.name, s.name_ar, s.description,
		       s.duration_min, s.buffer_min, s.active_duration_min,
		       `+pricing.Price("s", "os")+`, `+pricing.Deposit("s", "os")+`,
		       s.deposit_deadline_hours, s.is_active, s.is_custom, s.created_at, s.updated_at
		  FROM services s
		  JOIN artist_services os ON os.service_id = s.id AND os.artist_id = $1
		 WHERE s.is_active = TRUE
		 ORDER BY s.name ASC`,
		artistID,
	)
	if err != nil {
		return nil, fmt.Errorf("get offered services by artist: %w", err)
	}
	defer rows.Close()
	return scanServices(rows)
}
```
`internal/artist/service.go` — `GetPublicServicesByArtist` keeps the `profile.SalonID == nil` early return, then replaces the `GetServicesBySalon` reuse and the active filter with:
```go
	records, err := s.repo.GetOfferedServicesByArtist(ctx, artistID)
	if err != nil {
		return nil, fmt.Errorf("get public services by artist: %w", err)
	}
	out := make([]*ServiceResponse, 0, len(records))
	for _, rec := range records {
		out = append(out, toServiceResponse(rec))
	}
	return out, nil
```
Add a mock method to `internal/artist/service_test.go`:
```go
func (m *mockRepo) GetOfferedServicesByArtist(_ context.Context, _ uuid.UUID) ([]*SalonServiceRecord, error) {
	return m.offeredServices, m.offeredServicesErr
}
```
with fields `offeredServices []*SalonServiceRecord` and `offeredServicesErr error`. Any existing `GetPublicServicesByArtist` test that seeded the salon list must now seed `offeredServices` — the public list no longer comes from the owner's menu.

Delete the `"discovery/repository.go:GetSalonServices"` allowlist entry. Add the pricing import to both repositories.

- [ ] **Step 4: Run — expect PASS**

```bash
go build ./... && go vet ./...
go test ./internal/discovery/ ./internal/artist/ ./internal/pkg/pricing/
go test -tags dbtest ./internal/discovery/ ./internal/artist/ -count=1
```

- [ ] **Step 5: Commit**

```bash
git add internal/discovery/ internal/artist/ internal/pkg/pricing/noleak_test.go
git commit -m "feat(discovery,artist): a customer sees only her services, at her price"
```

---

### Task 6: Two capabilities

**Files:** Modify `internal/pkg/salonrole/role.go`, `internal/pkg/salonrole/role_test.go`

**Interfaces:**
- Produces: `salonrole.OwnServicesWrite` (`"own_services:write"`, owner ✅ member ✅), `salonrole.MemberServicesWrite` (`"member_services:write"`, owner ✅ member ❌).

- [ ] **Step 1: Write the failing test**

In `role_test.go`, add `OwnServicesWrite` to the `required` list of `TestCan_MemberCanRunTheirOwnDay`, and append:
```go
func TestCan_MemberCannotSetAnotherMembersPrices(t *testing.T) {
	// PP-3: the artist sets her own; only the owner may change a colleague's.
	if Can(Member, MemberServicesWrite) {
		t.Fatal("a member must not be able to change another member's services or prices")
	}
}
```

- [ ] **Step 2: Run — expect FAIL** — `go test ./internal/pkg/salonrole/` → `undefined: OwnServicesWrite`.

- [ ] **Step 3: Implement**

In `role.go`, in the shared-resource `const` block add `MemberServicesWrite Capability = "member_services:write"`, in the personal block `OwnServicesWrite Capability = "own_services:write"`; add both to `All()`; in `matrix`: Owner `MemberServicesWrite: true`, `OwnServicesWrite: true`; Member `MemberServicesWrite: false`, `OwnServicesWrite: true` — each with a one-line comment citing PP-3.

- [ ] **Step 4: Run — expect PASS** — `go test ./internal/pkg/salonrole/ ./internal/middleware/` (the explicit-matrix test and the drift test must stay green).

- [ ] **Step 5: Commit** — `git commit -am "feat(salonrole): own_services:write and member_services:write (PP-3)"`

---

### Task 7: `internal/offering` — repository

**Files:** Create `internal/offering/model.go`, `internal/offering/repository.go`, `internal/offering/repository_db_test.go`

**Interfaces:**
- Consumes: `pricing.Detail` (Task 2), table `artist_services` (Task 1)
- Produces:
```go
type Offering struct {
	ServiceID        uuid.UUID        `json:"service_id"`
	ServiceName      string           `json:"service_name"`
	DurationMin      int              `json:"duration_min"`
	Offered          bool             `json:"offered"`
	SalonPrice       decimal.Decimal  `json:"salon_price"`
	SalonDeposit     decimal.Decimal  `json:"salon_deposit"`
	OwnPrice         *decimal.Decimal `json:"own_price"`
	OwnDeposit       *decimal.Decimal `json:"own_deposit"`
	EffectivePrice   decimal.Decimal  `json:"effective_price"`
	EffectiveDeposit decimal.Decimal  `json:"effective_deposit"`
	DepositCapped    bool             `json:"deposit_capped"`
	UpdatedByName    *string          `json:"updated_by_name,omitempty"`
}
type Repository interface {
	ArtistIDForUser(ctx context.Context, userID uuid.UUID) (uuid.UUID, error)
	ArtistInSalon(ctx context.Context, artistID, salonID uuid.UUID) (bool, error)
	List(ctx context.Context, salonID, artistID uuid.UUID) ([]*Offering, error)
	Get(ctx context.Context, salonID, artistID, serviceID uuid.UUID) (*Offering, error) // ErrNotFound
	Upsert(ctx context.Context, p UpsertParams) error
	Delete(ctx context.Context, artistID, serviceID uuid.UUID) error
}
type UpsertParams struct {
	ArtistID, ServiceID, ActorUserID uuid.UUID
	PriceSet   bool
	Price      *decimal.Decimal // nil with PriceSet = clear to the salon's
	DepositSet bool
	Deposit    *decimal.Decimal
}
var ErrNotFound = errors.New("offering: not found")
```

- [ ] **Step 1: Write the failing DB tests** — `internal/offering/repository_db_test.go` (reuses `newFixture` from Task 1; add a helper `addMember(t, pool, salonID) uuid.UUID` inserting a user + artist in the salon):
```go
//go:build dbtest

package offering

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func addMember(t *testing.T, pool *pgxpool.Pool, salonID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var u, a uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('Maya',$1,'x','artist') RETURNING id`, uuid.NewString()+"@m.local").Scan(&u))
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO artists (user_id,salon_id) VALUES ($1,$2) RETURNING id`,
		u, salonID).Scan(&a))
	return a
}

func dec(s string) *decimal.Decimal { d := decimal.RequireFromString(s); return &d }

func TestList_ShowsTheWholeMenu_WithOfferedFlag(t *testing.T) {
	// The settings screen needs services she has NOT switched on too, or she
	// has nothing to switch on. LEFT JOIN, not JOIN.
	pool := testdb.New(t)
	f := newFixture(t, pool)
	member := addMember(t, pool, f.SalonID)

	list, err := NewRepository(pool).List(context.Background(), f.SalonID, member)

	require.NoError(t, err)
	require.Len(t, list, 1)
	assert.False(t, list[0].Offered)
	assert.True(t, list[0].EffectivePrice.Equal(decimal.RequireFromString("150")))
}

func TestUpsert_AbsentKeeps_NullClears(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	member := addMember(t, pool, f.SalonID)
	repo := NewRepository(pool)
	ctx := context.Background()

	require.NoError(t, repo.Upsert(ctx, UpsertParams{ArtistID: member, ServiceID: f.ServiceID,
		ActorUserID: uuid.Nil, PriceSet: true, Price: dec("200")}))
	// A second save that does not mention the price must keep it.
	require.NoError(t, repo.Upsert(ctx, UpsertParams{ArtistID: member, ServiceID: f.ServiceID,
		DepositSet: true, Deposit: dec("50")}))
	o, err := repo.Get(ctx, f.SalonID, member, f.ServiceID)
	require.NoError(t, err)
	require.NotNil(t, o.OwnPrice, "an absent price must leave the override alone")
	assert.True(t, o.OwnPrice.Equal(decimal.RequireFromString("200")))

	// Explicit null clears back to the salon's.
	require.NoError(t, repo.Upsert(ctx, UpsertParams{ArtistID: member, ServiceID: f.ServiceID,
		PriceSet: true, Price: nil}))
	o, err = repo.Get(ctx, f.SalonID, member, f.ServiceID)
	require.NoError(t, err)
	assert.Nil(t, o.OwnPrice)
	assert.True(t, o.EffectivePrice.Equal(decimal.RequireFromString("150")))
}

func TestGet_ServiceOfAnotherSalon_ErrNotFound(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	other := newFixture(t, pool) // a second salon with its own service
	_, err := NewRepository(pool).Get(context.Background(), f.SalonID, f.OwnerArtist, other.ServiceID)
	assert.ErrorIs(t, err, ErrNotFound)
}

func TestDelete_SwitchesItOff(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	repo := NewRepository(pool)
	ctx := context.Background()
	require.NoError(t, repo.Upsert(ctx, UpsertParams{ArtistID: f.OwnerArtist, ServiceID: f.ServiceID}))
	require.NoError(t, repo.Delete(ctx, f.OwnerArtist, f.ServiceID))
	o, err := repo.Get(ctx, f.SalonID, f.OwnerArtist, f.ServiceID)
	require.NoError(t, err)
	assert.False(t, o.Offered)
}
```
`newFixture` uses a fixed email; make it unique by changing `'owner@offering.local'` to a `$1` bound to `uuid.NewString()+"@o.local"` in Task 1's helper so two fixtures can coexist (`TestGet_ServiceOfAnotherSalon_ErrNotFound` needs two).

- [ ] **Step 2: Run — expect FAIL** — `go test -tags dbtest ./internal/offering/ 2>&1 | head -3` → `undefined: NewRepository`.

- [ ] **Step 3: Implement** `model.go` with the types in **Interfaces** above, and `repository.go`:
```go
package offering

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/pricing"
)

type pgRepo struct{ db *pgxpool.Pool }

func NewRepository(db *pgxpool.Pool) Repository { return &pgRepo{db: db} }

// offeringCols is the settings-screen row. pricing.Detail supplies the seven
// money columns so this package never names services.price itself.
var offeringCols = `s.id, s.name, s.duration_min, (os.artist_id IS NOT NULL), ` +
	pricing.Detail("s", "os") + `, uu.name`

const offeringFrom = `
	  FROM services s
	  LEFT JOIN artist_services os ON os.service_id = s.id AND os.artist_id = $2
	  LEFT JOIN users uu ON uu.id = os.updated_by
	 WHERE s.salon_id = $1 AND s.is_active = TRUE`

func scanOffering(row pgx.Row) (*Offering, error) {
	o := &Offering{}
	err := row.Scan(&o.ServiceID, &o.ServiceName, &o.DurationMin, &o.Offered,
		&o.SalonPrice, &o.SalonDeposit, &o.OwnPrice, &o.OwnDeposit,
		&o.EffectivePrice, &o.EffectiveDeposit, &o.DepositCapped, &o.UpdatedByName)
	return o, err
}

func (r *pgRepo) List(ctx context.Context, salonID, artistID uuid.UUID) ([]*Offering, error) {
	rows, err := r.db.Query(ctx, `SELECT `+offeringCols+offeringFrom+` ORDER BY s.name`, salonID, artistID)
	if err != nil {
		return nil, fmt.Errorf("list offerings: %w", err)
	}
	defer rows.Close()
	out := make([]*Offering, 0)
	for rows.Next() {
		o, err := scanOffering(rows)
		if err != nil {
			return nil, fmt.Errorf("scan offering: %w", err)
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (r *pgRepo) Get(ctx context.Context, salonID, artistID, serviceID uuid.UUID) (*Offering, error) {
	o, err := scanOffering(r.db.QueryRow(ctx,
		`SELECT `+offeringCols+offeringFrom+` AND s.id = $3`, salonID, artistID, serviceID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get offering: %w", err)
	}
	return o, nil
}

// Upsert switches the service on and applies whichever of price and deposit
// were SENT. An absent field keeps the stored value; an explicit null clears
// it to the salon's - the presence/value pair house pattern.
func (r *pgRepo) Upsert(ctx context.Context, p UpsertParams) error {
	var actor any
	if p.ActorUserID != uuid.Nil {
		actor = p.ActorUserID
	}
	_, err := r.db.Exec(ctx, `
		INSERT INTO artist_services (artist_id, service_id, price, deposit_amount, updated_by)
		VALUES ($1, $2, CASE WHEN $3 THEN $4::numeric END, CASE WHEN $5 THEN $6::numeric END, $7)
		ON CONFLICT (artist_id, service_id) DO UPDATE SET
		    price          = CASE WHEN $3 THEN $4::numeric ELSE artist_services.price END,
		    deposit_amount = CASE WHEN $5 THEN $6::numeric ELSE artist_services.deposit_amount END,
		    updated_by     = $7,
		    updated_at     = now()`,
		p.ArtistID, p.ServiceID, p.PriceSet, p.Price, p.DepositSet, p.Deposit, actor)
	if err != nil {
		return fmt.Errorf("upsert offering: %w", err)
	}
	return nil
}

func (r *pgRepo) Delete(ctx context.Context, artistID, serviceID uuid.UUID) error {
	if _, err := r.db.Exec(ctx,
		`DELETE FROM artist_services WHERE artist_id = $1 AND service_id = $2`, artistID, serviceID); err != nil {
		return fmt.Errorf("delete offering: %w", err)
	}
	return nil
}

func (r *pgRepo) ArtistIDForUser(ctx context.Context, userID uuid.UUID) (uuid.UUID, error) {
	var id uuid.UUID
	err := r.db.QueryRow(ctx, `SELECT id FROM artists WHERE user_id = $1`, userID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, ErrNotFound
	}
	return id, err
}

func (r *pgRepo) ArtistInSalon(ctx context.Context, artistID, salonID uuid.UUID) (bool, error) {
	var ok bool
	err := r.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM artists WHERE id = $1 AND salon_id = $2)`, artistID, salonID).Scan(&ok)
	return ok, err
}
```

- [ ] **Step 4: Run — expect PASS** — `go test -tags dbtest ./internal/offering/ -v -count=1`, then `go test ./internal/pkg/pricing/` (the guard must not flag `internal/offering`).

- [ ] **Step 5: Commit** — `git add internal/offering/ && git commit -m "feat(offering): repository for switches, prices and deposits"`

---

### Task 8: `internal/offering` — service, handler, routes

**Files:** Create `internal/offering/service.go`, `internal/offering/handler.go`, `internal/offering/service_test.go`; Modify `cmd/main.go`

**Interfaces:**
- Consumes: `Repository`, `Offering`, `UpsertParams`, `ErrNotFound` (Task 7); `salonrole.OwnServicesWrite`, `salonrole.MemberServicesWrite` (Task 6)
- Produces: routes `GET/PUT /api/v1/artists/salon/my-services[/:serviceId]`, `GET/PUT /api/v1/artists/salon/members/:artistId/services[/:serviceId]`; request `UpdateRequest{Offered bool; Price, DepositAmount optional.Field[string]}`.

- [ ] **Step 1: Write the failing service tests** — `internal/offering/service_test.go`:
```go
package offering

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/optional"
)

type mockRepo struct {
	artistID   uuid.UUID
	inSalon    bool
	current    *Offering
	getErr     error
	upserts    []UpsertParams
	deletes    int
}

func (m *mockRepo) ArtistIDForUser(context.Context, uuid.UUID) (uuid.UUID, error) { return m.artistID, nil }
func (m *mockRepo) ArtistInSalon(context.Context, uuid.UUID, uuid.UUID) (bool, error) { return m.inSalon, nil }
func (m *mockRepo) List(context.Context, uuid.UUID, uuid.UUID) ([]*Offering, error) {
	return []*Offering{m.current}, nil
}
func (m *mockRepo) Get(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (*Offering, error) {
	return m.current, m.getErr
}
func (m *mockRepo) Upsert(_ context.Context, p UpsertParams) error { m.upserts = append(m.upserts, p); return nil }
func (m *mockRepo) Delete(context.Context, uuid.UUID, uuid.UUID) error { m.deletes++; return nil }

type captureAudit struct{ events []audit.Event }

func (c *captureAudit) Log(_ context.Context, e audit.Event) error { c.events = append(c.events, e); return nil }

func menuRow(offered bool) *Offering {
	return &Offering{ServiceID: uuid.New(), Offered: offered,
		SalonPrice: decimal.RequireFromString("150"), SalonDeposit: decimal.RequireFromString("30"),
		EffectivePrice: decimal.RequireFromString("150"), EffectiveDeposit: decimal.RequireFromString("30")}
}

func code(t *testing.T, err error) string {
	t.Helper()
	var e *apperror.AppError
	require.ErrorAs(t, err, &e)
	return e.Code
}

func TestUpdateMine_SetsHerPrice_AndAuditsTheActor(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), current: menuRow(true)}
	aud := &captureAudit{}
	svc := NewService(repo, aud)
	user := uuid.New()

	_, err := svc.UpdateMine(context.Background(), uuid.New(), user, repo.current.ServiceID,
		UpdateRequest{Offered: true, Price: optional.From("200.00")})

	require.NoError(t, err)
	require.Len(t, repo.upserts, 1)
	assert.True(t, repo.upserts[0].PriceSet)
	assert.True(t, repo.upserts[0].Price.Equal(decimal.RequireFromString("200")))
	require.Len(t, aud.events, 1)
	require.NotNil(t, aud.events[0].ActorID)
	assert.Equal(t, user, *aud.events[0].ActorID, "the audit must name who changed it")
}

func TestUpdateMine_NullPrice_ClearsToSalon(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		repo.current.ServiceID, UpdateRequest{Offered: true, Price: optional.Null[string]()})
	require.NoError(t, err)
	assert.True(t, repo.upserts[0].PriceSet)
	assert.Nil(t, repo.upserts[0].Price, "an explicit null must clear, not be ignored")
}

func TestUpdateMine_InvalidMoney_400(t *testing.T) {
	// internal/pkg/money answers 400 INVALID_<FIELD> - verified against
	// money.invalid(), not assumed. "10.999" would otherwise round silently
	// to 11.00 in the NUMERIC(10,2) column.
	repo := &mockRepo{artistID: uuid.New(), current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		repo.current.ServiceID, UpdateRequest{Offered: true, Price: optional.From("10.999")})
	assert.Equal(t, "INVALID_PRICE", code(t, err))
	assert.Empty(t, repo.upserts)
}

func TestUpdateMine_OwnDepositAboveEffectivePrice_422(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		repo.current.ServiceID, UpdateRequest{Offered: true,
			Price: optional.From("40.00"), DepositAmount: optional.From("50.00")})
	assert.Equal(t, "VALIDATION_ERROR", code(t, err))
	assert.Empty(t, repo.upserts)
}

func TestUpdateMine_SwitchOff_Deletes(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		repo.current.ServiceID, UpdateRequest{Offered: false})
	require.NoError(t, err)
	assert.Equal(t, 1, repo.deletes)
	assert.Empty(t, repo.upserts)
}

func TestUpdateMine_ServiceNotInHerSalon_404(t *testing.T) {
	repo := &mockRepo{artistID: uuid.New(), getErr: ErrNotFound}
	_, err := NewService(repo, &captureAudit{}).UpdateMine(context.Background(), uuid.New(), uuid.New(),
		uuid.New(), UpdateRequest{Offered: true})
	assert.Equal(t, "SERVICE_NOT_FOUND", code(t, err))
}

func TestUpdateForMember_ArtistOfAnotherSalon_404(t *testing.T) {
	repo := &mockRepo{inSalon: false, current: menuRow(true)}
	_, err := NewService(repo, &captureAudit{}).UpdateForMember(context.Background(), uuid.New(), uuid.New(),
		uuid.New(), repo.current.ServiceID, UpdateRequest{Offered: true})
	assert.Equal(t, "MEMBER_NOT_FOUND", code(t, err))
	assert.Empty(t, repo.upserts)
}

```

- [ ] **Step 2: Run — expect FAIL** — `go test ./internal/offering/` → `undefined: NewService`.

- [ ] **Step 3: Implement** `internal/offering/service.go`:
```go
package offering

import (
	"context"
	"errors"
	"fmt"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/money"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/optional"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

// UpdateRequest is the body of both PUT endpoints.
//
// Price and DepositAmount are optional.Field: absent keeps her override,
// null clears it to the salon's value, a string sets it (PP-2).
type UpdateRequest struct {
	Offered       bool                   `json:"offered"`
	Price         optional.Field[string] `json:"price"`
	DepositAmount optional.Field[string] `json:"deposit_amount"`
}

type auditLogger interface {
	Log(ctx context.Context, e audit.Event) error
}

type Service struct {
	repo     Repository
	audit    auditLogger
	validate *validator.Validate
}

func NewService(repo Repository, a auditLogger) *Service {
	return &Service{repo: repo, audit: a, validate: validation.New()}
}

func errServiceNotFound() error {
	return apperror.NotFound("SERVICE_NOT_FOUND", "Service not found or no longer available")
}
func errMemberNotFound() error { return apperror.NotFound("MEMBER_NOT_FOUND", "Artist not found") }

func (s *Service) ListMine(ctx context.Context, salonID, userID uuid.UUID) ([]*Offering, error) {
	artistID, err := s.repo.ArtistIDForUser(ctx, userID)
	if err != nil {
		return nil, errMemberNotFound()
	}
	return s.repo.List(ctx, salonID, artistID)
}

func (s *Service) ListForMember(ctx context.Context, salonID, memberArtistID uuid.UUID) ([]*Offering, error) {
	ok, err := s.repo.ArtistInSalon(ctx, memberArtistID, salonID)
	if err != nil {
		return nil, fmt.Errorf("list for member: %w", err)
	}
	if !ok {
		return nil, errMemberNotFound()
	}
	return s.repo.List(ctx, salonID, memberArtistID)
}

func (s *Service) UpdateMine(ctx context.Context, salonID, userID, serviceID uuid.UUID,
	req UpdateRequest) (*Offering, error) {
	artistID, err := s.repo.ArtistIDForUser(ctx, userID)
	if err != nil {
		return nil, errMemberNotFound()
	}
	return s.update(ctx, salonID, artistID, serviceID, userID, req)
}

// UpdateForMember is the owner acting on a member's behalf (PP-3). The member
// must be in the caller's salon; otherwise 404, indistinguishable from absent.
func (s *Service) UpdateForMember(ctx context.Context, salonID, actorUserID, memberArtistID,
	serviceID uuid.UUID, req UpdateRequest) (*Offering, error) {
	ok, err := s.repo.ArtistInSalon(ctx, memberArtistID, salonID)
	if err != nil {
		return nil, fmt.Errorf("update for member: %w", err)
	}
	if !ok {
		return nil, errMemberNotFound()
	}
	return s.update(ctx, salonID, memberArtistID, serviceID, actorUserID, req)
}

func (s *Service) update(ctx context.Context, salonID, artistID, serviceID, actorUserID uuid.UUID,
	req UpdateRequest) (*Offering, error) {
	if err := s.validate.Struct(req); err != nil {
		return nil, validation.MapError(err)
	}
	current, err := s.repo.Get(ctx, salonID, artistID, serviceID)
	if errors.Is(err, ErrNotFound) {
		return nil, errServiceNotFound()
	}
	if err != nil {
		return nil, fmt.Errorf("update offering: %w", err)
	}

	// Switching off deletes the row, INCLUDING her custom price - "no row"
	// is what "not offered" means (spec §5). Existing bookings are untouched.
	if !req.Offered {
		if current.Offered {
			if err := s.repo.Delete(ctx, artistID, serviceID); err != nil {
				return nil, err
			}
			s.log(ctx, salonID, actorUserID, serviceID, "offering.off", current, nil)
		}
		return s.repo.Get(ctx, salonID, artistID, serviceID)
	}

	p := UpsertParams{ArtistID: artistID, ServiceID: serviceID, ActorUserID: actorUserID}
	newPrice, newDeposit := current.OwnPrice, current.OwnDeposit
	if req.Price.IsSet() {
		p.PriceSet = true
		if !req.Price.IsNull() {
			v, _ := req.Price.Get()
			d, err := money.Parse(v, "price")
			if err != nil {
				return nil, err
			}
			p.Price = &d
		}
		newPrice = p.Price
	}
	if req.DepositAmount.IsSet() {
		p.DepositSet = true
		if !req.DepositAmount.IsNull() {
			v, _ := req.DepositAmount.Get()
			d, err := money.Parse(v, "deposit_amount")
			if err != nil {
				return nil, err
			}
			p.Deposit = &d
		}
		newDeposit = p.Deposit
	}

	// Her OWN deposit above the price she will charge is refused. A BLANK
	// deposit follows the salon's and is capped when read (PP-6).
	effective := current.SalonPrice
	if newPrice != nil {
		effective = *newPrice
	}
	if newDeposit != nil && newDeposit.GreaterThan(effective) {
		return nil, apperror.UnprocessableEntity("VALIDATION_ERROR", []apperror.FieldError{{
			Field:   "deposit_amount",
			Message: "The deposit can't be more than the price ($" + effective.StringFixed(2) + ")",
		}})
	}

	if err := s.repo.Upsert(ctx, p); err != nil {
		return nil, err
	}
	s.log(ctx, salonID, actorUserID, serviceID, "offering.update", current, p)
	return s.repo.Get(ctx, salonID, artistID, serviceID)
}

func (s *Service) log(ctx context.Context, salonID, actor, serviceID uuid.UUID, action string, old, new any) {
	_ = s.audit.Log(ctx, audit.Event{
		SalonID: &salonID, ActorID: &actor, EntityType: "artist_service",
		EntityID: serviceID, Action: action, OldValues: old, NewValues: new,
	})
}

```
`internal/offering/handler.go`:
```go
package offering

import (
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"

	"github.com/abdallahkadour/b-edge-api/internal/audit"
	"github.com/abdallahkadour/b-edge-api/internal/middleware"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/apperror"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/response"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/salonrole"
	"github.com/abdallahkadour/b-edge-api/internal/pkg/validation"
)

type Handler struct{ svc *Service }

// RegisterRoutes mounts the offering endpoints under /artists/salon/, where
// routecoverage_test.go demands a capability guard on every write.
func RegisterRoutes(app *fiber.App, pool *pgxpool.Pool, _ *zap.Logger) {
	h := &Handler{svc: NewService(NewRepository(pool), audit.NewRepository(pool))}
	g := app.Group("/api/v1/artists/salon", middleware.RequireAuth(), middleware.RequireRole("artist", "admin"))
	own := middleware.RequireSalonCapability(salonrole.OwnServicesWrite)
	member := middleware.RequireSalonCapability(salonrole.MemberServicesWrite)

	g.Get("/my-services", own, h.ListMine)
	g.Put("/my-services/:serviceId", own, h.UpdateMine)
	g.Get("/members/:artistId/services", member, h.ListForMember)
	g.Put("/members/:artistId/services/:serviceId", member, h.UpdateForMember)
}

func param(c *fiber.Ctx, name string) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Params(name))
	if err != nil {
		return uuid.Nil, apperror.BadRequest("INVALID_ID", "Invalid "+name)
	}
	return id, nil
}

// ListMine godoc
// @Summary  The salon menu with my switches, prices and deposits
// @Tags     offerings
// @Security BearerAuth
// @Success  200 {object} response.Body
// @Router   /artists/salon/my-services [get]
func (h *Handler) ListMine(c *fiber.Ctx) error {
	out, err := h.svc.ListMine(c.UserContext(), *middleware.SalonIDFromContext(c), middleware.UserIDFromContext(c))
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// UpdateMine godoc
// @Summary  Switch one of my services on or off, and set my price and deposit
// @Tags     offerings
// @Security BearerAuth
// @Param    serviceId path string true "Service ID"
// @Param    body body UpdateRequest true "Switch, price, deposit"
// @Success  200 {object} response.Body
// @Failure  400 {object} response.Body "INVALID_PRICE, INVALID_DEPOSIT_AMOUNT"
// @Failure  404 {object} response.Body "SERVICE_NOT_FOUND"
// @Failure  422 {object} response.Body "VALIDATION_ERROR - deposit above the price"
// @Router   /artists/salon/my-services/{serviceId} [put]
func (h *Handler) UpdateMine(c *fiber.Ctx) error {
	serviceID, err := param(c, "serviceId")
	if err != nil {
		return err
	}
	var req UpdateRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	out, err := h.svc.UpdateMine(c.UserContext(), *middleware.SalonIDFromContext(c),
		middleware.UserIDFromContext(c), serviceID, req)
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// ListForMember godoc
// @Summary  A member's switches, prices and deposits (owner)
// @Tags     offerings
// @Security BearerAuth
// @Param    artistId path string true "Member artist ID"
// @Success  200 {object} response.Body
// @Failure  404 {object} response.Body "MEMBER_NOT_FOUND"
// @Router   /artists/salon/members/{artistId}/services [get]
func (h *Handler) ListForMember(c *fiber.Ctx) error {
	artistID, err := param(c, "artistId")
	if err != nil {
		return err
	}
	out, err := h.svc.ListForMember(c.UserContext(), *middleware.SalonIDFromContext(c), artistID)
	if err != nil {
		return err
	}
	return response.OK(c, out)
}

// UpdateForMember godoc
// @Summary  Change a member's switch, price or deposit (owner)
// @Tags     offerings
// @Security BearerAuth
// @Param    artistId path string true "Member artist ID"
// @Param    serviceId path string true "Service ID"
// @Param    body body UpdateRequest true "Switch, price, deposit"
// @Success  200 {object} response.Body
// @Failure  404 {object} response.Body "MEMBER_NOT_FOUND, SERVICE_NOT_FOUND"
// @Router   /artists/salon/members/{artistId}/services/{serviceId} [put]
func (h *Handler) UpdateForMember(c *fiber.Ctx) error {
	artistID, err := param(c, "artistId")
	if err != nil {
		return err
	}
	serviceID, err := param(c, "serviceId")
	if err != nil {
		return err
	}
	var req UpdateRequest
	if err := c.BodyParser(&req); err != nil {
		return validation.MapBodyError(err)
	}
	out, err := h.svc.UpdateForMember(c.UserContext(), *middleware.SalonIDFromContext(c),
		middleware.UserIDFromContext(c), artistID, serviceID, req)
	if err != nil {
		return err
	}
	return response.OK(c, out)
}
```
`cmd/main.go` — after `membership.RegisterRoutes(...)`: `offering.RegisterRoutes(app, pool, logger)` and the import. Confirm `audit.NewRepository(pool)` satisfies `auditLogger` (it has `Log(ctx, Event) error`).

- [ ] **Step 4: Run — expect PASS**

```bash
go build ./... && go vet ./...
go test ./internal/offering/ ./internal/middleware/ ./internal/pkg/pricing/
make swagger
```
`internal/middleware` must stay green: route coverage now sees two new `PUT` routes under `/artists/salon/`, each guarded.

- [ ] **Step 5: Live check against the API** (air rebuilds; confirm `pgrep -x air | wc -l` is 1)

```bash
TOK=$(curl -s -X POST localhost:3000/api/v1/auth/login -H 'Content-Type: application/json' \
  -d '{"email":"rania@bedge.com","password":"password123"}' | python3 -c "import sys,json;print(json.load(sys.stdin)['data']['access_token'])")
curl -s localhost:3000/api/v1/artists/salon/my-services -H "Authorization: Bearer $TOK" | python3 -m json.tool | head -30
```
Expected: `200`, every active service of her salon, each `"offered": true` (backfill), `own_price: null`.

- [ ] **Step 6: Commit**

```bash
git add internal/offering/ cmd/main.go
git commit -m "feat(offering): artists set their own services and prices; owners can override"
```

---

### Task 9: Lifecycle hooks — new services, onboarding, leaving

**Files:** Modify `internal/artist/repository.go` (`CreateService`), `internal/onboarding/repository.go` (`Complete`), `internal/membership/repository.go` (`DetachArtist`); Test: append to `internal/offering/repository_db_test.go`

- [ ] **Step 1: Write the failing DB tests** (append):
```go
func TestCreateService_OwnerOffersIt_MemberDoesNot(t *testing.T) {
	// PP-7: a service the owner adds is on for her, off for members.
	pool := testdb.New(t)
	f := newFixture(t, pool)
	member := addMember(t, pool, f.SalonID)
	ctx := context.Background()
	svc := &artist.SalonServiceRecord{ID: uuid.New(), SalonID: f.SalonID, Name: "Lashes",
		DurationMin: 60, Price: decimal.RequireFromString("80"), IsActive: true, DepositDeadlineHours: 48}
	require.NoError(t, artist.NewRepository(pool).CreateService(ctx, svc))

	var owner, mem int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM artist_services WHERE artist_id=$1 AND service_id=$2`,
		f.OwnerArtist, svc.ID).Scan(&owner))
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM artist_services WHERE artist_id=$1 AND service_id=$2`,
		member, svc.ID).Scan(&mem))
	assert.Equal(t, 1, owner, "the owner must offer the service she just created")
	assert.Equal(t, 0, mem, "a member must switch it on herself")
}

func TestDetachArtist_RemovesHerOfferings(t *testing.T) {
	pool := testdb.New(t)
	f := newFixture(t, pool)
	member := addMember(t, pool, f.SalonID)
	ctx := context.Background()
	_, err := pool.Exec(ctx, `INSERT INTO artist_services (artist_id,service_id) VALUES ($1,$2)`, member, f.ServiceID)
	require.NoError(t, err)

	require.NoError(t, membership.NewRepository(pool).DetachArtist(ctx, f.SalonID, member))

	var n int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM artist_services WHERE artist_id=$1`, member).Scan(&n))
	assert.Equal(t, 0, n, "a departed member must not keep offering the salon's services")
}
```
(imports: `github.com/abdallahkadour/b-edge-api/internal/artist` and `.../internal/membership`; both constructors are `NewRepository(pool)`.)

Create `internal/onboarding/repository_db_test.go`:
```go
//go:build dbtest

package onboarding

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/abdallahkadour/b-edge-api/internal/pkg/testdb"
)

func TestComplete_OwnerOffersHerFirstService(t *testing.T) {
	// Without this row a newly onboarded artist has an empty menu and cannot
	// be booked at all - the same failure migration 049 fixed for hours.
	pool := testdb.New(t)
	ctx := context.Background()
	var user uuid.UUID
	require.NoError(t, pool.QueryRow(ctx, `INSERT INTO users (name,email,password_hash,role)
		VALUES ('New','new@onb.local','x','artist') RETURNING id`).Scan(&user))

	artistID, err := NewRepository(pool).Complete(ctx, user, CompleteOnboardingRequest{
		ArtistProfile:      ArtistProfile{Handle: "newartist", Category: "makeup"},
		SalonName:          "New Salon",
		StoreName:          "Main",
		City:               "Beirut",
		ServiceName:        "Makeup",
		ServiceDurationMin: 60,
		ServicePrice:       "80.00",
	})
	require.NoError(t, err)

	var n int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT count(*) FROM artist_services os JOIN services s ON s.id = os.service_id
		 WHERE os.artist_id = $1 AND s.name = 'Makeup'`, artistID).Scan(&n))
	assert.Equal(t, 1, n)
}
```

- [ ] **Step 2: Run — expect FAIL** — counts are 0.

- [ ] **Step 3: Implement**

`artist.CreateService` — wrap the insert in a CTE so the owner's row is written in the same statement:
```go
	err := r.db.QueryRow(ctx, `
		WITH svc AS (
			INSERT INTO services (
				id, salon_id, category_id, name, name_ar, description,
				duration_min, buffer_min, active_duration_min, price,
				deposit_amount, deposit_deadline_hours, is_active, is_custom
			) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)
			RETURNING id, salon_id, created_at, updated_at
		), owner_offers AS (
			-- PP-7: the owner offers what she creates; members switch it on.
			INSERT INTO artist_services (artist_id, service_id)
			SELECT a.id, svc.id
			  FROM svc
			  JOIN salons sa ON sa.id = svc.salon_id
			  JOIN artists a ON a.user_id = sa.owner_id AND a.salon_id = sa.id
		)
		SELECT created_at, updated_at FROM svc`,
		/* same 14 arguments as before */
	).Scan(&s.CreatedAt, &s.UpdatedAt)
```
`onboarding.Complete` — change the service insert to capture its id and add the row:
```go
	var serviceID uuid.UUID
	if err = tx.QueryRow(ctx,
		`INSERT INTO services (salon_id, name, duration_min, price) VALUES ($1, $2, $3, $4) RETURNING id`,
		salonID, req.ServiceName, req.ServiceDurationMin, req.ServicePrice,
	).Scan(&serviceID); err != nil {
		return uuid.Nil, fmt.Errorf("complete onboarding: create service: %w", err)
	}
	// PP-7: the owner offers her first service. Without this row she would
	// be listed with an empty menu and could not be booked at all.
	if _, err = tx.Exec(ctx,
		`INSERT INTO artist_services (artist_id, service_id) VALUES ($1, $2)`, artistID, serviceID,
	); err != nil {
		return uuid.Nil, fmt.Errorf("complete onboarding: offer first service: %w", err)
	}
```
`membership.DetachArtist`:
```go
	ct, err := r.db.Exec(ctx, `
		WITH gone AS (
			-- She no longer belongs to the salon, so she no longer offers its
			-- services. Same statement as the detach, so neither can happen alone.
			DELETE FROM artist_services os
			 USING services s
			 WHERE os.service_id = s.id AND os.artist_id = $1 AND s.salon_id = $2
		)
		UPDATE artists SET salon_id = NULL, updated_at = now()
		 WHERE id = $1 AND salon_id = $2`, artistID, salonID)
```

- [ ] **Step 4: Run — expect PASS** — `go test -tags dbtest ./internal/offering/ ./internal/onboarding/ -count=1` and `go test ./...`.

- [ ] **Step 5: Commit** — `git commit -am "feat(offering): owners offer what they create; leaving a salon removes her offerings"`

---

### Task 10: Chaos suite — members switch on, pricing is charged correctly

**Files:** Modify `scripts/chaos-booking.py`

- [ ] **Step 1: Run the suite first — expect it RED**

Run: `make chaos-booking 2>&1 | tail -8`
Expected: FAIL at topology or on member holds with `SERVICE_NOT_FOUND` — joined members now offer nothing (PP-7). That failure is the reason for Step 2.

- [ ] **Step 2: Members switch every salon service on after joining**

In `join()`, after the accept succeeds and before `approve_artist`, add:
```python
    # PP-7: a member who joins offers NOTHING until she switches services on.
    # Before migration 052 every member offered everything, and this harness
    # relied on it. Uses the real endpoint rather than inserting rows.
    tok = login(email)
    st, r = call("GET", "/artists/salon/my-services", None, tok)
    if st >= 400:
        raise RuntimeError(f"my-services {email}: {st} {err(r)}")
    for o in (r.get("data") or []):
        st2, r2 = call("PUT", f"/artists/salon/my-services/{o['service_id']}", {"offered": True}, tok)
        if st2 >= 400:
            raise RuntimeError(f"switch on {o['service_id']} for {email}: {st2} {err(r2)}")
```

- [ ] **Step 3: Add the pricing cases** — after case `3.6b`:
```python
    # ── 3.7 per-artist pricing: shown == charged, per artist ──────────────
    # Two artists of ONE salon, one service. The member sets 200; the owner
    # keeps the salon price. Each hold must charge the right one, the hold's
    # quote must equal what was stored, and a switched-off service refuses.
    owner_key, member_key = "A1", member
    svc_id = SV["A"]
    tok_m = login(f"{TAG}.{member_key.lower()}@test.bedge.com")
    call("PUT", f"/artists/salon/my-services/{svc_id}", {"offered": True, "price": "200.00"}, tok_m)
    salon_price = sql(f"SELECT price FROM services WHERE id='{svc_id}'")

    def quote(artist, phone):
        st, r = call("POST", "/bookings/guest/hold", {
            "artist_id": artist, "store_id": ST["A"], "service_id": svc_id,
            "start_time": (datetime.now(timezone.utc) + timedelta(days=13))
                          .replace(hour=14, minute=0, second=0, microsecond=0).strftime("%Y-%m-%dT%H:%M:%SZ"),
            "customer_name": "Price Probe", "customer_phone": phone})
        d = r.get("data") or {}
        stored = sql(f"SELECT original_price FROM bookings WHERE id='{d.get('booking_id')}'") if d.get("booking_id") else None
        if d.get("booking_id"):
            sql(f"DELETE FROM bookings WHERE id='{d['booking_id']}'")
        return st, d.get("original_price"), stored, err(r)

    # The price a customer SEES on her profile, before any hold. Discovery
    # first; if a member is not listed there (visibility is keyed on
    # subscriptions, which is not what this case tests), the funnel's own
    # list. If NEITHER shows the service, `listed` stays None and 3.7 FAILS -
    # never skip the comparison.
    st_p, r_p = call("GET", f"/discovery/artists/{A[member_key]}")
    listed = next((x.get("price") for x in ((r_p.get("data") or {}).get("services") or [])
                   if x.get("id") == svc_id), None)
    if listed is None:
        st_p, r_p = call("GET", f"/artists/{A[member_key]}/services")
        listed = next((x.get("price") for x in (r_p.get("data") or []) if x.get("id") == svc_id), None)
    st_m, shown_m, stored_m, _ = quote(A[member_key], "+96176000471")
    st_o, shown_o, stored_o, _ = quote(A[owner_key], "+96176000472")
    ok = (st_m < 400 and st_o < 400 and listed == shown_m == stored_m == "200.00"
          and shown_o == stored_o == salon_price)
    rec("3.7", "PASS" if ok else "FAIL",
        f"member override: profile {listed} / hold {shown_m} / stored {stored_m}; "
        f"owner at salon price: hold {shown_o} / stored {stored_o} (salon {salon_price})")

    call("PUT", f"/artists/salon/my-services/{svc_id}", {"offered": False}, tok_m)
    st_off, _, _, e_off = quote(A[member_key], "+96176000473")
    rec("3.7b", "PASS" if e_off == "SERVICE_NOT_FOUND" else "FAIL",
        f"after she switched it off: {st_off} {e_off or 'ACCEPTED'}")
```
Also add `"Price Probe"` to whatever cleanup matches probe customers, and extend `4.2`'s money invariant to `artist_services` (no NaN, no negative).

- [ ] **Step 4: Run — expect GREEN** — `make chaos-booking 2>&1 | tail -4` → `0 FAIL`, and `3.7`, `3.7b` PASS.

- [ ] **Step 5: Commit** — `git commit -am "test(chaos): members switch services on; per-artist price shown == charged (3.7)"`

---

### Task 11: Web — shared model, data service, hold quote type

**Files:**
- Create: `projects/shared/src/lib/models/offering.model.ts`, `projects/shared/src/lib/core/offering-data.service.ts`
- Modify: `projects/shared/src/lib/models/index.ts`, `projects/shared/src/public-api.ts`, `projects/shared/src/lib/models/booking.model.ts`

**Interfaces:**
- Produces: `interface ServiceOffering`, `interface UpdateOfferingRequest`, `class OfferingDataService { listMine(); updateMine(id, req); listForMember(artistId); updateForMember(artistId, id, req) }`; `HoldGuestSlotResponse` gains `original_price`, `early_bird_fee`, `final_price`, `deposit_amount`.

- [ ] **Step 1: Write the code**

`offering.model.ts`:
```ts
/**
 * One row of an artist's services screen - the salon menu with her switch,
 * her price and deposit (null = the salon's), and what a customer will pay.
 * Mirrors internal/offering.Offering.
 */
export interface ServiceOffering {
  readonly service_id: string;
  readonly service_name: string;
  readonly duration_min: number;
  readonly offered: boolean;
  readonly salon_price: string;
  readonly salon_deposit: string;
  readonly own_price: string | null;
  readonly own_deposit: string | null;
  readonly effective_price: string;
  readonly effective_deposit: string;
  /** True when the deposit was lowered to the price. Shown, never silent. */
  readonly deposit_capped: boolean;
  readonly updated_by_name?: string;
}

/**
 * PUT body. Omit a field to leave it alone; send null to go back to the
 * salon's value; send a string to set it.
 */
export interface UpdateOfferingRequest {
  offered: boolean;
  price?: string | null;
  deposit_amount?: string | null;
}
```
`offering-data.service.ts`:
```ts
import { Injectable, inject } from '@angular/core';
import { Observable } from 'rxjs';

import { ApiService } from './api.service';
import type { ServiceOffering, UpdateOfferingRequest } from '../models';

@Injectable({ providedIn: 'root' })
export class OfferingDataService {
  private readonly api = inject(ApiService);

  listMine(): Observable<ServiceOffering[]> {
    return this.api.getArray<ServiceOffering>('/artists/salon/my-services');
  }

  updateMine(serviceId: string, req: UpdateOfferingRequest): Observable<ServiceOffering> {
    return this.api.put<ServiceOffering>(`/artists/salon/my-services/${serviceId}`, req);
  }

  listForMember(artistId: string): Observable<ServiceOffering[]> {
    return this.api.getArray<ServiceOffering>(`/artists/salon/members/${artistId}/services`);
  }

  updateForMember(artistId: string, serviceId: string, req: UpdateOfferingRequest): Observable<ServiceOffering> {
    return this.api.put<ServiceOffering>(`/artists/salon/members/${artistId}/services/${serviceId}`, req);
  }
}
```
Add `export * from './offering.model';` to `models/index.ts` and `export * from './lib/core/offering-data.service';` to `public-api.ts`. In `booking.model.ts` extend `HoldGuestSlotResponse`:
```ts
  /** What the hold CHARGED - display these, not the service list's numbers. */
  readonly original_price: string;
  readonly early_bird_fee: string;
  readonly final_price: string;
  readonly deposit_amount: string;
```

- [ ] **Step 2: Build — expect PASS**

```bash
npx ng build shared && npx ng build artist-dashboard && npx ng build customer-pwa
```

- [ ] **Step 3: Commit** — `git add projects/shared && git commit -m "feat(shared): offering model and data service; the hold quote"`

---

### Task 12: Web — the offerings component, and where it appears

**Files:**
- Create: `projects/artist-dashboard/src/app/features/dashboard/service-offerings.component.{ts,html}`, `my-services.page.ts`, `member-services.page.ts`
- Modify: `app.routes.ts`, `dashboard-layout.component.ts`, `team.component.html`, `join/join-salon.page.{ts,html}`

**Interfaces:**
- Produces: `<bedge-service-offerings [memberArtistId]="id | undefined" />` — `undefined` means "my own".

- [ ] **Step 1: Write the component**

`service-offerings.component.ts`:
```ts
import { ChangeDetectionStrategy, Component, OnInit, inject, input, signal } from '@angular/core';
import { HttpErrorResponse } from '@angular/common/http';

import {
  ButtonComponent,
  InputDirective,
  OfferingDataService,
  SkeletonComponent,
  extractApiErrorMessage,
} from '@bedge/shared';
import type { ServiceOffering, UpdateOfferingRequest } from '@bedge/shared';

/**
 * The salon menu with a switch per service, and her price and deposit.
 *
 * One component, three places (spec §7): the join step, "My services", and
 * the owner editing a member. memberArtistId undefined = the caller's own.
 *
 * Saves per row. A switch is a decision about one service; making her press
 * a page-level Save after flipping it would lose the change when she leaves.
 */
@Component({
  selector: 'bedge-service-offerings',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ButtonComponent, InputDirective, SkeletonComponent],
  templateUrl: './service-offerings.component.html',
})
export class ServiceOfferingsComponent implements OnInit {
  private readonly api = inject(OfferingDataService);

  readonly memberArtistId = input<string | undefined>(undefined);

  protected readonly rows = signal<ServiceOffering[]>([]);
  protected readonly loading = signal(true);
  protected readonly busy = signal<string | null>(null);
  protected readonly rowError = signal<Record<string, string>>({});
  protected readonly draftPrice = signal<Record<string, string>>({});
  protected readonly draftDeposit = signal<Record<string, string>>({});

  // Signal inputs are bound after construction - read them here, never in
  // the constructor (see phone-verification.component for the same rule).
  ngOnInit(): void {
    const id = this.memberArtistId();
    (id ? this.api.listForMember(id) : this.api.listMine()).subscribe({
      next: (rows) => { this.rows.set(rows); this.loading.set(false); },
      error: (err: HttpErrorResponse) => {
        this.loading.set(false);
        this.rowError.set({ _: extractApiErrorMessage(err, 'Could not load services.') });
      },
    });
  }

  protected toggle(row: ServiceOffering): void {
    if (row.offered && row.own_price &&
        !confirm(`Turning off ${row.service_name} removes your price of $${row.own_price}. Turn it off?`)) {
      return;
    }
    this.save(row, { offered: !row.offered });
  }

  protected savePrices(row: ServiceOffering): void {
    const req: UpdateOfferingRequest = { offered: true };
    const p = this.draftPrice()[row.service_id];
    const d = this.draftDeposit()[row.service_id];
    if (p !== undefined) req.price = p.trim() === '' ? null : p.trim();
    if (d !== undefined) req.deposit_amount = d.trim() === '' ? null : d.trim();
    this.save(row, req);
  }

  protected useSalonPrice(row: ServiceOffering): void {
    this.save(row, { offered: true, price: null, deposit_amount: null });
  }

  protected setDraft(kind: 'price' | 'deposit', id: string, value: string): void {
    const s = kind === 'price' ? this.draftPrice : this.draftDeposit;
    s.set({ ...s(), [id]: value });
  }

  private save(row: ServiceOffering, req: UpdateOfferingRequest): void {
    const id = this.memberArtistId();
    this.busy.set(row.service_id);
    this.rowError.set({ ...this.rowError(), [row.service_id]: '' });
    (id ? this.api.updateForMember(id, row.service_id, req) : this.api.updateMine(row.service_id, req))
      .subscribe({
        next: (updated) => {
          this.rows.set(this.rows().map((r) => (r.service_id === updated.service_id ? updated : r)));
          this.busy.set(null);
        },
        error: (err: HttpErrorResponse) => {
          this.busy.set(null);
          this.rowError.set({ ...this.rowError(), [row.service_id]: extractApiErrorMessage(err, 'Could not save.') });
        },
      });
  }
}
```
`service-offerings.component.html`:
```html
@if (loading()) {
  <div class="flex flex-col gap-2"><bedge-skeleton height="72px" /><bedge-skeleton height="72px" /></div>
} @else {
  @if (rowError()['_']; as e) { <p class="text-sm text-danger mb-3">{{ e }}</p> }
  <ul class="flex flex-col gap-2">
    @for (row of rows(); track row.service_id) {
      <li class="rounded-xl border border-gray-200 bg-white p-4">
        <label class="flex items-center justify-between gap-3">
          <span class="min-w-0">
            <span class="block font-medium text-ink truncate">{{ row.service_name }}</span>
            <span class="block text-xs text-gray-500">{{ row.duration_min }} min · salon price ${{ row.salon_price }}</span>
          </span>
          <input type="checkbox" role="switch" class="w-5 h-5 accent-ink shrink-0"
                 [checked]="row.offered" [disabled]="busy() === row.service_id"
                 [attr.aria-label]="'Offer ' + row.service_name"
                 (change)="toggle(row)" />
        </label>

        @if (row.offered) {
          <div class="mt-3 grid grid-cols-1 min-[360px]:grid-cols-2 gap-2">
            <div>
              <label class="block text-xs text-gray-500 mb-1" [attr.for]="'p-' + row.service_id">Your price</label>
              <input bedgeInput inputmode="decimal" class="h-10" [id]="'p-' + row.service_id"
                     [value]="row.own_price ?? ''" [placeholder]="row.salon_price"
                     (input)="setDraft('price', row.service_id, $any($event.target).value)" />
            </div>
            <div>
              <label class="block text-xs text-gray-500 mb-1" [attr.for]="'d-' + row.service_id">Your deposit</label>
              <input bedgeInput inputmode="decimal" class="h-10" [id]="'d-' + row.service_id"
                     [value]="row.own_deposit ?? ''" [placeholder]="row.salon_deposit"
                     (input)="setDraft('deposit', row.service_id, $any($event.target).value)" />
            </div>
          </div>
          <p class="text-xs text-gray-500 mt-2">
            Customers pay <span class="font-medium text-ink">${{ row.effective_price }}</span>,
            deposit ${{ row.effective_deposit }}.
            @if (row.deposit_capped) { The deposit was lowered to your price. }
            @if (row.updated_by_name) { Last changed by {{ row.updated_by_name }}. }
          </p>
          <div class="mt-2 flex flex-wrap gap-2">
            <bedge-button size="sm" [loading]="busy() === row.service_id" (click)="savePrices(row)">Save</bedge-button>
            @if (row.own_price || row.own_deposit) {
              <bedge-button size="sm" variant="ghost" (click)="useSalonPrice(row)">Use salon price</bedge-button>
            }
          </div>
        }
        @if (rowError()[row.service_id]; as re) { <p class="text-sm text-danger mt-2" role="alert">{{ re }}</p> }
      </li>
    }
  </ul>
}
```

- [ ] **Step 2: The three placements**

`my-services.page.ts`:
```ts
import { ChangeDetectionStrategy, Component } from '@angular/core';
import { ServiceOfferingsComponent } from './service-offerings.component';

@Component({
  selector: 'bedge-my-services-page',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ServiceOfferingsComponent],
  template: `
    <h1 class="text-lg font-bold text-ink mb-1">My services</h1>
    <p class="text-sm text-gray-500 mb-4">Switch on what you do. Leave a price blank to use the salon's.</p>
    <bedge-service-offerings />
  `,
})
export class MyServicesPage {}
```
`member-services.page.ts` — the dashboard does NOT enable `withComponentInputBinding()`, so the id comes from `ActivatedRoute`:
```ts
import { ChangeDetectionStrategy, Component, inject } from '@angular/core';
import { ActivatedRoute, RouterLink } from '@angular/router';
import { ServiceOfferingsComponent } from './service-offerings.component';

@Component({
  selector: 'bedge-member-services-page',
  standalone: true,
  changeDetection: ChangeDetectionStrategy.OnPush,
  imports: [ServiceOfferingsComponent, RouterLink],
  template: `
    <a routerLink="/dashboard/team" class="text-sm text-gray-500">← Team</a>
    <h1 class="text-lg font-bold text-ink mt-2 mb-1">Services &amp; prices</h1>
    <p class="text-sm text-gray-500 mb-4">Changes you make here are recorded as yours.</p>
    <bedge-service-offerings [memberArtistId]="artistId" />
  `,
})
export class MemberServicesPage {
  // Read once from the snapshot: the route is not reused across members.
  protected readonly artistId = inject(ActivatedRoute).snapshot.paramMap.get('artistId') ?? undefined;
}
```

`app.routes.ts`, inside the dashboard children:
```ts
      {
        // An artist's OWN services and prices. Not owner-guarded (PP-3).
        path: 'my-services',
        loadComponent: () => import('./features/dashboard/my-services.page').then((m) => m.MyServicesPage),
      },
      {
        path: 'team/:artistId/services',
        canActivate: [salonOwnerGuard()],
        loadComponent: () => import('./features/dashboard/member-services.page').then((m) => m.MemberServicesPage),
      },
```
`dashboard-layout.component.ts`:
- add `{ path: '/dashboard/my-services', label: 'My services', icon: 'scissors' }` after "My hours";
- replace `PENDING_ALLOWED_PATH` with `PENDING_ALLOWED_PATHS = ['/dashboard/profile', '/dashboard/my-services']` — the redirect uses `[0]`, the "is allowed" check uses `.some(p => url.startsWith(p))`, and the pending nav filter includes both plus Help. A pending member must be able to choose her services while she waits for approval;
- PP-8, solo salons: add `private readonly artistCount = signal<number | null>(null);`, load it in the constructor for artists with `inject(MembershipDataService).listMembers().subscribe(m => this.artistCount.set(m.length))`, and filter `'/dashboard/my-services'` out of `navItems` when `this.auth.isSalonOwner() && (this.artistCount() ?? 1) <= 1`.

`team.component.html` — inside each member row, after the Remove button block:
```html
            @if (!m.is_owner) {
              <a [routerLink]="['/dashboard/team', m.artist_id, 'services']"
                 class="text-sm text-ink underline underline-offset-2">Services &amp; prices</a>
            }
```
(add `RouterLink` to the component's `imports`).

`join/join-salon.page.ts` — the session does not know she joined: its access token carries `salon_id` from BEFORE the accept, so the offering endpoints would answer `NO_SALON`. On accept success, refresh first:
```ts
        next: () => {
          // The access token was minted before she joined and carries no
          // salon. Refresh so the services step can call salon endpoints.
          this.auth.refresh().subscribe({
            next: () => { this.submitting.set(false); this.joined.set(true); },
            error: () => { this.submitting.set(false); this.joined.set(true); this.needsLogin.set(true); },
          });
        },
```
(inject `AuthStore` as `auth`; add `protected readonly needsLogin = signal(false);`). In `join-salon.page.html`'s `joined()` block, between the review paragraph and the dashboard link:
```html
        @if (!needsLogin()) {
          <div class="text-left mt-6">
            <h2 class="text-sm font-semibold text-ink mb-1">Which services do you offer?</h2>
            <p class="text-xs text-gray-500 mb-3">
              Switch on what you do. Until you switch on at least one, customers can't book you.
            </p>
            <bedge-service-offerings />
          </div>
        } @else {
          <p class="text-sm text-gray-500 mt-4">Log in again, then choose your services under My services.</p>
        }
```
(add `ServiceOfferingsComponent` to the page's `imports`).

- [ ] **Step 3: Build — expect PASS** — `npx ng build shared && npx ng build artist-dashboard`

- [ ] **Step 4: Commit** — `git add projects/artist-dashboard && git commit -m "feat(dashboard): services and prices per artist - join step, My services, owner view"`

---

### Task 13: Web — the confirmation screen shows what was charged

**Files:** Modify `projects/customer-pwa/src/app/features/booking-funnel/booking-funnel.page.{ts,html}`, `screens/guest-details-screen.component.{ts,html}`

- [ ] **Step 1: Carry the quote** — in `booking-funnel.page.ts` add `protected readonly holdQuote = signal<HoldGuestSlotResponse | null>(null);` and in `onSlotChosen`'s `next:` add `this.holdQuote.set(hold);`. In `booking-funnel.page.html` pass `[quote]="holdQuote()!"` to `<app-guest-details-screen>`.

- [ ] **Step 2: Display it** — in `guest-details-screen.component.ts` add `readonly quote = input.required<HoldGuestSlotResponse>();` and change `hasDeposit` to `computed(() => Number(this.quote().deposit_amount) > 0)`. In the template replace `${{ service().price }}` with `${{ quote().final_price }}`, add directly beneath it:
```html
        @if (quote().early_bird_fee !== '0' && quote().early_bird_fee !== '0.00') {
          <span class="block text-xs text-gray-500">includes ${{ quote().early_bird_fee }} early-bird fee</span>
        }
```
and replace `${{ service().deposit_amount }}` with `${{ quote().deposit_amount }}`. Leave the discount preview (`p.deposit`) as it is — it already reads the server's preview.

- [ ] **Step 3: Build — expect PASS** — `npx ng build shared && npx ng build customer-pwa`

- [ ] **Step 4: Commit** — `git commit -am "fix(funnel): confirm at the price the hold charged, early-bird fee included"`

---

### Task 14: Verify in a real browser, record, and close out

**Files:** Create `b-edge-web/scripts/verify-offerings-ui.mjs`; Modify spec, `E2E-TEST-PLAN.md`, `B-Edge-Security-Test-Plan-v1.md`, `DOCUMENTATION.md`, `CLAUDE.md`

- [ ] **Step 1: WebKit at 390px — the My services flow, driven for real**

Rania's salon is solo, so PP-8 hides My services from her. The script creates a real **member** of her salon through the API (register with a phone, verify with the dev bypass, invite, accept), which also proves a *pending* member can reach the screen (Task 12). It tears the member down at the end.

Create `b-edge-web/scripts/verify-offerings-ui.mjs`:
```js
/**
 * Per-artist services & prices, in WebKit at 390px.
 *
 * Logs in within the session and navigates by CLICKING: the refresh cookie is
 * Secure, so WebKit drops it over plain HTTP and page.goto() would bounce to
 * /login. Asserts ELEMENTS and stored values, never loose text - a regex once
 * matched "Verify" inside "verified" and passed a broken button.
 * Requires the API built with -tags devbypass (for the 000000 phone code).
 */
import { webkit } from 'playwright';
import { execFileSync } from 'node:child_process';

const API = 'http://localhost:3000/api/v1';
const DASH = 'http://localhost:4300';
const results = [];
const rec = (id, ok, detail) => { results.push(ok); console.log(`  ${ok ? 'PASS' : 'FAIL'} ${id.padEnd(3)} ${detail}`); };
const sql = (q) => execFileSync('docker', ['exec', '-i', 'bedge-postgres', 'psql', '-U', 'postgres',
  '-d', 'bedge', '-tA', '-v', 'ON_ERROR_STOP=1', '-c', q]).toString().trim();
async function call(method, path, body, token) {
  const r = await fetch(API + path, { method,
    headers: { 'Content-Type': 'application/json', ...(token ? { Authorization: `Bearer ${token}` } : {}) },
    body: body ? JSON.stringify(body) : undefined });
  return { status: r.status, json: await r.json().catch(() => ({})) };
}
const login = async (email) => (await call('POST', '/auth/login', { email, password: 'password123' })).json.data.access_token;

const tag = String(Date.now()).slice(-6);
const email = `offer.${tag}@test.bedge.com`;
const phone = `+96176${tag}`;
const own = (col) => sql(`SELECT coalesce(os.${col}::text,'null') FROM artist_services os
  JOIN artists a ON a.id=os.artist_id JOIN users u ON u.id=a.user_id WHERE u.email='${email}' LIMIT 1`);

let browser;
try {
  // ── setup: a real member of Rania's salon ────────────────────────────
  await call('POST', '/auth/register', { name: 'Offer Probe', email, password: 'password123', role: 'artist', phone });
  const mtok = await login(email);
  const v = await call('POST', '/artists/me/phone/verify', { code: '000000' }, mtok);
  if (v.status >= 400) throw new Error(`phone verify ${v.status} - is the API built with -tags devbypass?`);
  const otok = await login('rania@bedge.com');
  const inv = await call('POST', '/artists/salon/members/invite', { phone }, otok);
  if (inv.status >= 400) throw new Error(`invite ${inv.status} ${JSON.stringify(inv.json.error)}`);
  const acc = await call('POST', `/invitations/${inv.json.data.link.split('/').pop()}/accept`,
    { handle: `offer-${tag}`, category: 'makeup' }, mtok);
  if (acc.status >= 400) throw new Error(`accept ${acc.status} ${JSON.stringify(acc.json.error)}`);

  // ── browser ──────────────────────────────────────────────────────────
  browser = await webkit.launch();
  const page = await (await browser.newContext({ viewport: { width: 390, height: 844 } })).newPage();
  await page.goto(DASH + '/login');
  await page.fill('#email', email);
  await page.fill('#password', 'password123');
  await page.click('button[type="submit"], bedge-button button');
  await page.waitForTimeout(2500);
  if (page.url().includes('/login')) throw new Error('login failed - nothing below would mean anything');

  const more = page.locator('button', { hasText: /More/i }).first();
  if (await more.count()) { await more.click(); await page.waitForTimeout(600); }
  const link = page.locator('a[href="/dashboard/my-services"]:visible').first();
  rec('1', (await link.count()) === 1, 'a PENDING member sees My services in the nav');
  await link.click();
  await page.waitForTimeout(2000);

  const rows = page.locator('bedge-service-offerings li');
  const n = await rows.count();
  const active = Number(sql(`SELECT count(*) FROM services s JOIN artists a ON a.salon_id=s.salon_id
    JOIN users u ON u.id=a.user_id WHERE u.email='${email}' AND s.is_active`));
  rec('2', n === active && n > 0, `${n} rows for ${active} active salon services`);

  const row = rows.first();
  const sw = row.locator('input[role="switch"]');
  rec('3', !(await sw.isChecked()), 'a joiner starts with every service OFF (PP-7)');

  await sw.click();
  await page.waitForTimeout(1500);
  rec('4', (await sw.isChecked()) && own('artist_id') !== 'null', 'switching on is saved (a row exists)');

  await row.locator('input[inputmode="decimal"]').first().fill('200.00');
  await row.locator('bedge-button', { hasText: 'Save' }).click();
  await page.waitForTimeout(1500);
  rec('5', own('price') === '200.00' && (await row.innerText()).includes('$200.00'),
    `her price is stored and shown (stored ${own('price')})`);

  await row.locator('input[inputmode="decimal"]').nth(1).fill('250.00');
  await row.locator('bedge-button', { hasText: 'Save' }).click();
  await page.waitForTimeout(1500);
  rec('6', (await row.locator('[role="alert"]').count()) === 1 && own('deposit_amount') === 'null',
    `a deposit above her price is refused on the row, nothing saved (stored ${own('deposit_amount')})`);

  await row.locator('bedge-button', { hasText: 'Use salon price' }).click();
  await page.waitForTimeout(1500);
  rec('7', own('price') === 'null', 'Use salon price clears her override');

  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  rec('8', overflow <= 0, `page overflow at 390px: ${overflow}px`);
} catch (e) {
  rec('err', false, String(e).slice(0, 160));
} finally {
  if (browser) await browser.close();
  // Teardown: remove the probe member entirely.
  sql(`DELETE FROM artist_services WHERE artist_id IN (SELECT a.id FROM artists a JOIN users u ON u.id=a.user_id WHERE u.email='${email}')`);
  sql(`DELETE FROM artists WHERE user_id IN (SELECT id FROM users WHERE email='${email}')`);
  sql(`DELETE FROM salon_invitations WHERE phone='${phone}'`);
  sql(`DELETE FROM users WHERE email='${email}'`);
  console.log(`\n  ${results.filter(Boolean).length} pass, ${results.filter((x) => !x).length} FAIL`);
}
```
Run: `node scripts/verify-offerings-ui.mjs` → 8 PASS. If the new route is missing, restart `ng serve` — a watcher started before a file existed can miss it.

- [ ] **Step 1b: Two checks that are manual, and why**

  1. **The join step's refresh.** Over plain HTTP, WebKit drops the Secure refresh cookie, so `auth.refresh()` can only ever fail there and a script would test only the fallback. In **Chrome** (which keeps Secure cookies on `localhost`): open an invitation link for a new artist, accept, and confirm "Which services do you offer?" appears with the switches. Then, with cookies blocked, confirm the "Log in again" fallback appears instead of a broken screen.
  2. **The early-bird line on the confirmation screen.** Set `early_bird_cutoff`/`early_bird_fee` on a test store, book an early slot in the customer app, and confirm the total equals the hold's `final_price` with the "includes $X early-bird fee" line. Restore the store.

  Record both in `E2E-TEST-PLAN.md` as manual, with their reasons, not as passes.

- [ ] **Step 2: Full regression**

```bash
cd b-edge-api
go build ./... && go vet ./...
go test ./... && go test -tags devbypass ./... && go test -tags dbtest ./... -count=1
make swagger && make chaos-booking
PKG=./internal/offering/ make mutation
cd ../b-edge-web && npx ng build shared && npx ng build artist-dashboard && npx ng build customer-pwa && npx ng test --watch=false
```
Every command green; mutation efficacy on `internal/offering` recorded.

- [ ] **Step 3: Documents**
  - Spec: endpoints → `/artists/salon/my-services…` with the route-coverage reason; add the two defects this plan fixed; status → built.
  - `E2E-TEST-PLAN.md`: a suite for per-artist pricing (join step, My services, owner override, shown == charged, early-bird on the confirmation) with results.
  - `B-Edge-Security-Test-Plan-v1.md`: **FRAUD-17** — a member setting a colleague's price (must 403 on the capability, 404 across salons), and a price/deposit outside the money whitelist.
  - `DOCUMENTATION.md`: spec entry → "Built", add the plan.
  - `CLAUDE.md`: under enforced conventions — *"A customer-facing price is read through `internal/pkg/pricing`; `noleak_test.go` fails the build on any other read of `services.price`."*

- [ ] **Step 4: Commit and push both repos**

```bash
cd b-edge-api && git add -A && git commit -m "docs: per-artist pricing built - spec, plans, FRAUD-17, conventions" && git push
cd ../b-edge-web && git add -A && git commit -m "test(e2e): per-artist pricing verified in WebKit at 390px" && git push
```
