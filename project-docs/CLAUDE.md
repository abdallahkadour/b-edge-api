# B-Edge — engineering context

> **Read this first in any new session.** Verified against the code on
> 2026-09-05 — migrations, routes, `go.mod`, `package.json`, the live
> database — not against memory or earlier docs.
>
> This file replaced a version dated May 2026 that described 9 tables, ~34
> endpoints and "Go version: starting fresh." All three were wrong by an
> order of magnitude, and it was being auto-loaded into every session.
> `CLAUDE-v3` … `CLAUDE-v6` and `CLAUDE (1)/(5)` are **history, not context** —
> do not read them for current state.

---

## What it is

Beauty booking + product marketplace for Lebanon and MENA. "Fresha for
Lebanon." Solo founder build.

- **Founder:** Edge (Abdallah Kadour), GitHub `abdallahkadour`
- **Launch artist:** Rania — ~300K Instagram, two studios (Beirut Downtown,
  Tripoli), public handle `rania` (`/book/rania` works like the UUID link)
- **Backend:** `b-edge-api` (Go) · **Frontend:** `b-edge-web` (Angular monorepo)

---

## Where things actually stand — 2026-09-05

| | |
|---|---|
| Go / Fiber | 1.26.3 / v2.52.13 |
| Angular | 21.2 — `customer-pwa`, `artist-dashboard`, `@bedge/shared` |
| Migrations | **33** (latest `033_service_buffer`) |
| Tables | **29** |
| Route registrations | **115** (44 GET, 33 POST, 30 PATCH, 6 DELETE, 2 PUT) · **102** paths in swagger |
| Go tests | **645**, all passing |
| Frontend routes | 13 customer · 25 artist-dashboard |
| Branches | api `feature/feasibility-sprints-1-3` · web `feature/feasibility-sprints-2-3` |

**Domains** (`internal/`): admin, artist, audit, billing, booking, **calendar**,
client, customerauth, discovery, earnings, **inbox**, media, notification,
onboarding, product, review, **share** — plus `domain/auth`, `config`,
`middleware`.

**Leaf packages** (`internal/pkg/`): apperror, **bidi**, hash, jwt, **money**,
**openinghours**, response, **subscription**.

Bold entries are recent and are not described in any older doc.

---

## The five things most likely to trip you up

**1. The product does not work for customers yet, and it is one variable.**
`TWILIO_WHATSAPP_FROM` is unset. Every notification ever queued is `dead` —
**61 of them, 100%**, including 38 customer login codes. That gates customer
login, booking approvals/confirmations/cancellations, review requests and the
calendar link. Tracked as **D8**. Do not design around it; it is procurement.

**2. Decisions live in `B-Edge-Decision-Register-v1.md`.** One list, 8
resolved with the file each landed in, 11 open with who can settle each. Read
it before planning any sprint. The monetization spec's §11 is **history** and
says so.

**3. Leaf packages exist to break import cycles.** `billing/handler.go` imports
`middleware`, so `middleware` cannot import `billing` — hence
`internal/pkg/subscription`. Same shape for `openinghours` and `bidi`. When two
domains need the same rule, it goes in a leaf package, not a copy.

**4. State is derived, never stored, and there is no scheduler.** Subscription
status comes from `DeriveStatus`; a store's open/closed state is computed per
request; expired holds self-heal lazily on read. Adding a cron to "fix" any of
this would be undoing a deliberate design.

**5. There is no database test infrastructure.** `TEST_DB_NAME` is vestigial —
only `cmd/migrate` reads it. Every test is service-layer with hand-written
mocks. Repository tests are **out of scope**, not missing; do not spend a
sprint on testcontainers.

---

## Testing

House style: in-package, hand-written mock structs, `newTestService()`,
`Test<Method>_<Condition>_<Expected>`, no table-driven, no `t.Run`, decimals
compared with `.Equal()`.

Two suites are worth knowing before writing tests:

- **`internal/booking/statematrix_test.go`** — the booking state machine as
  data: 7 actions × 11 statuses × 2 time positions, 154 cells. It asserts the
  exact **error code** and that a rejected action **did not write the row**.
  Adding a status or an action means adding a row or a column. Mutation-tested.
  The design doc is `B-Edge-Booking-State-Machine-Matrix-v1.md` (in
  `b-edge-web/project-docs/`).
- **`E2E-TEST-PLAN.md`** (also in `b-edge-web/project-docs/`) — 16 suites plus
  an adversarial pass. Suites 1–10 and 12–16 have been executed at least once;
  11 and 14.8 **cannot be automated** (they need a link pasted into WhatsApp
  and an `.ics` imported on a real phone). Suite 10 is currently unrunnable for
  a different reason: no artist in the roster has any portfolio photos, so
  there is nothing to tag.

---

## Conventions that are enforced

- Migrations are paired `.up`/`.down` with a prose header saying **why**.
  Migrations 010, 016, 029 and 031 are the standard to match.
- Nothing ships without code + tests + `make swagger`. Note that **`docs/` is
  gitignored** (`.gitignore:12`), so there is no `docs/` diff to commit — an
  older version of this rule said there was. Run `make swagger` anyway: the
  generated spec is what the annotations are checked against, and a stale
  `@Success` type is invisible until someone reads the wrong contract.
- **Ownership failures return 404, not 403**, so IDs cannot be enumerated.
  This was only half true until 2026-09-05: billing invoices and notifications
  did it, while bookings, stores, services, products, orders, media and reviews
  returned 403 — which told a caller the object was real. Security test AUTH-02
  found it live.

  The mechanism matters. Each domain has ONE `errXNotFound()` constructor and
  **both** the not-found branch and the ownership branch return it, so status,
  code and message are identical by construction. Do not hand-write a matching
  `apperror.NotFound` beside an ownership check — two literals that happen to
  agree is exactly how the original leak survived. `booking.errBookingNotFound`
  carries the long note; `TestOwnershipAndAbsence_AreIndistinguishable` fails if
  the branches ever diverge.

  Still 403 on purpose, because they describe the CALLER rather than an object:
  `NO_SALON`, `ACCOUNT_SUSPENDED`, `ACCOUNT_FROZEN`, `SUBSCRIPTION_SUSPENDED`,
  `NOT_AN_ARTIST`, `ARTIST_NOT_ACCEPTING_BOOKINGS`, the middleware role gates,
  and checks keyed on a **public** artist ID — a public ID has nothing to
  enumerate.

- **Every money string from a request body goes through `internal/pkg/money`.**
  Never call `decimal.NewFromString` on user input directly. It accepts `"1e3"`
  as a thousand and unlimited scale, and `NUMERIC(10,2)` then rounds silently;
  worse, two paths had no validation at all and passed the raw string to SQL,
  where Postgres accepts `'NaN'::numeric` — a stored NaN made every later read
  of that row fail with a 500, poisoned `SUM(price)`, and could not be repaired
  through the API. Security test INJ-04, 2026-09-05.

  The package is a **whitelist** (`^\d{1,8}(\.\d{1,2})?$`), not a list of
  rejections; enumerating rejections is how `NaN` got in. Deliberate
  consequence: `"10.999"` is now a 400, not a silent `11.00`.

  `B-Edge-Cross-Layer-Data-Flow-Audit-v1.md` is the full picture — why this is
  structural rather than one bad endpoint, and why **scale can never be
  defended at the database level** (the column coerces before any CHECK runs),
  which makes this package the only defence rather than defence-in-depth.

- **Security headers are middleware, registered second.** `SecurityHeaders()`
  in `internal/middleware/secheaders.go` runs before anything can return early,
  so a 429 from the rate limiter and a 503 from the concurrency limiter carry
  them too. The global CSP is `default-src 'none'` — accurate for JSON, not
  merely strict. The two routes that serve HTML (`share`, `calendar`) narrow it
  themselves at the handler by exactly one directive each, so a new HTML
  endpoint fails closed instead of inheriting permission nobody granted.
- User-supplied text rendered to a *different* person must go through
  `internal/pkg/bidi`. A bidi override is not markup, so no escaping catches
  it. Current surfaces: Open Graph tags, notification bodies, `.ics`
  SUMMARY/LOCATION, the Google Calendar deep link.
- **Two artist-lookup endpoints, and only one accepts a handle.**
  `GET /api/v1/artists/:id` resolves a **handle or a UUID** — this is what makes
  `/book/rania` work, and the funnel calls it first specifically to get a real
  UUID. `GET /api/v1/discovery/artists/:id` is **UUID-only** and answers
  `INVALID_ID` for a handle. Passing a route param straight to discovery is the
  easy mistake.
- **Any code changing `bookings.start_time`/`end_time` must increment
  `calendar_sequence` in the same statement**, or a rescheduled booking creates
  a second event in the customer's calendar instead of moving the first.

---

## Two known-wrong behaviours, pinned by tests rather than fixed

Both are product decisions, both in `internal/billing`:

1. `ensureInvoicesUpTo`'s doc comment claims an unpaid subscription never
   accumulates more than one outstanding invoice. **It does** — four unpaid
   months produce five invoices.
2. Go's `AddDate` normalises rather than clamps, so a period starting Jan 31
   rolls to **Mar 3**, not Feb 28, and the billing day then shifts forward
   permanently. Anyone on the 29th–31st is affected.

---

## Environment

Local Postgres runs in Docker as `bedge-postgres` (not a host install — there
is no `psql` on the PATH; use `docker exec -i bedge-postgres psql -U postgres
-d bedge`). API on **:3000**. Customer PWA :4200, artist dashboard :4300;
CORS whitelists only those two, so a dev server on any other port needs
`CLIENT_URL` updated.

There is a **permanent 14-account test roster** (`user1`–`user10`,
`mkup1`–`mkup4`) plus `rania@bedge.com`. **It is deliberately never cleaned
up.** Leave it alone.

---

## Where the documentation is

`project-docs/DOCUMENTATION.md` is the index — read it before opening anything
else in that folder. Many files there predate the current build and are marked
as such; the index says which.

*Last verified against code: 2026-09-05.*
