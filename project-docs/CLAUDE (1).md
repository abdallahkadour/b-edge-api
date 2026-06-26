# CLAUDE.md — B-Edge Engineering Context

> Single source of truth for continuing the B-Edge backend. Read this first in any new chat.
> Last updated: June 2026 · Schema v8 · 6 domains live.

---

## Who & What

**B-Edge** — beauty booking SaaS for Lebanon / MENA ("Fresha for Lebanon"). Solo founder build.

- **Founder:** Edge (Abdallah Kadour). GitHub `abdallahkadour`. Java background, strong DevOps/K8s, deepening Go through this build.
- **AI partner:** Spark (Claude).
- **Launch artist:** Rania — 300K IG followers, two studios (Beirut + Tripoli), does makeup/hair/nails with staff artists under her. Primary launch partner + brand ambassador.

**Active repo:** `abdallahkadour/b-edge-api` (Go backend).
**Frontend repo:** separate Angular 21 workspace `b-edge-web` (projects: `customer-pwa`, `artist-dashboard`, shared lib `@bedge/shared`).

---

## How Spark works with Edge (non-negotiable)

1. **Never rush.** Edge sets the pace.
2. **Always ask Edge to upload the REAL current files before writing code or making architectural/schema decisions.** Never build against assumed or remembered files. This is the most important rule — it caught real bugs this build.
3. **Validate decisions online** (vs competitors + best practices) before presenting any DB schema or business rule.
4. **Ask for what you need, every time** — Edge would rather paste a file than have Spark guess. Stay connected, no wrong assumptions.
5. **Defer undecided product questions** rather than block implementation. Stub with a clear `TODO` and move on.
6. Edge asks deep "why" questions, pushes back, accepts honest criticism. Communicate casually and concisely.
7. **Delivery format:** Edge strongly prefers COMPLETE drop-in replacement files over snippets/diffs.

---

## Stack & Environment

- **Backend:** Go 1.22+, Fiber v2, pgx v5 / pgxpool, golang-migrate, golang-jwt/jwt v5, go-playground/validator v10, shopspring/decimal, zap, OpenTelemetry+Jaeger, swaggo/swag, google/uuid, testify.
- **DB:** PostgreSQL 15 in Docker (container `bedge-postgres`, db=`bedge`, user=postgres, pass=postgres, port 5432).
- **Frontend:** Angular 21, Tailwind 3, CDK 21, Signals state, Node 22 via nvm. Brand: Inter font, ink `#0a0a0a`, success green `#16a34a`, no blue/gold, 390px mobile, enterprise/restrained (Uber/Airbnb style).
- **Infra:** K8s single-node, AWS EC2 t3.medium, Cloudinary (pending), Twilio WhatsApp (pending).
- **Dev machines:** Windows (COMP-0905, MINGW64) + MacBook Air. Edge runs migrations at home on the MacBook.

**Makefile targets:** `run`, `dev` (air hot reload), `test`, `coverage`, `migrate` (`go run cmd/migrate/main.go`), `migrate-test`, `swagger` (`swag init -g cmd/main.go -o docs`), `build`, `docker-up`, `docker-down`, `lint`.

**Repo layout:** code in `cmd/` (main.go, migrate/) and `internal/<domain>/`. Migrations in `db/migrations/` (`NNN_name.up.sql` / `.down.sql`). Migrator reads `file://db/migrations`. Project docs in `project-docs/`.

---

## Critical environment facts (save hours)

- **`bedge_test` DB does NOT exist.** All tests use in-memory `mockRepo`, so `make test` needs no database. Only create `bedge_test` when writing real integration tests.
- **`make dev` (air) is the real compile authority.** Spark's sandbox has no Go — Spark relies on static checks (brace balance, interface-impl-mock parity, import usage, signature matching) + Edge running `make dev`/`make test`.
- **deleted_at columns — ONLY on `users`, `bookings`, `salons`.** `artists`, `stores`, `services`, `reviews`, `client_notes` do NOT have `deleted_at`. Never filter on it for those tables. (A `services.deleted_at` filter caused a live SQLSTATE 42703 500 — fixed. Don't repeat.)
- **After `swag init`:** the "no Go files in root" warning is harmless (code is in cmd/ + internal/). Must restart air/`go run` AND hard-refresh browser (Cmd+Shift+R) or Swagger UI shows a stale spec.
- **CORS:** the `cors.New(...)` block in main.go is commented out; CORS is handled inside `middleware.Register`. Angular (:4200) -> API (:3000) is cross-origin — if frontend calls fail with CORS, check there.

---

## Code conventions (Go)

- Every file, exported func/struct/const/error gets a Go doc comment.
- No hardcoded values — use `.env`, named constants, no magic numbers.
- Parameterized SQL always. Migration file for every DB change.
- `context.Context` as first param on all DB/service funcs.
- Pointer receivers on all service/handler/repo methods.
- Always check errors (never `_`). Human-readable error messages.
- Tests written alongside code (in-memory mockRepo). No feature done without code + tests + Swagger docs.

**apperror signatures:** `BadRequest/NotFound/Conflict/Forbidden(code, message string)`; `UnprocessableEntity(code string, details []FieldError)`.
**response helpers:** `OK/Created(c, data)`, `List(c, data, *Meta)` (Meta has NextCursor, HasMore), `NoContent(c)`.
**pgx v5:** `pool.Begin(ctx)` -> `pgx.Tx`; `.Exec/.QueryRow/.Commit(ctx)/.Rollback(ctx)`. uniqueViolation = "23505". decimal via shopspring (`decimal.Zero` valid).

**Domain pattern:** each `internal/<domain>/` has `model.go` (types, sentinel errors, converters), `repository.go` (interface + pgRepo impl), `service.go` (business logic, validation), `handler.go` (Fiber handlers + RegisterRoutes), `service_test.go` (mockRepo + tests). Registered in `cmd/main.go` via `<domain>.RegisterRoutes(app, pool, logger)`.

---

## Current state — 6 domains live, schema v8

Migrations 001-008 applied. `make test` green. Swagger serves all at `http://localhost:3000/swagger/index.html`. 50 endpoints documented in `project-docs/B-Edge-API-Reference-v1.docx`.

### auth (10 endpoints) — `/api/v1/auth`
register, login, refresh, forgot-password, reset-password (public); logout, change-password, freeze-account, unfreeze-account, delete-account (Bearer). Access token = 15-min JWT in Authorization header; refresh token in secure httpOnly cookie, rotates on refresh.

### booking (15) — `/api/v1/bookings`
- Public: `GET /slots`, `POST /guest/hold` (C-04), `PATCH /guest/:id/submit` (C-05).
- Guest two-step: hold creates a held booking -> SystemGuestPlaceholderID `00000000-0000-0000-0000-0000000000ff` (seeded), 10-min lock; submit creates real guest user + `AttachGuestAndSubmit` repoints customer_id + held->pending atomically (zero orphan users). `ErrBookingNotHeld` added.
- Lifecycle (Bearer): create, get/:id, submit, approve, deposit-received, confirm-deposit, cancel, complete, no-show.
- Lists: `GET /artist/:artist_id?status=` (enriched, keyset paginated; INVALID_STATUS rejects unknown), `GET /artist/:artist_id/calendar?week_start=YYYY-MM-DD` (CalendarStatuses = approved/deposit_paid/confirmed/completed/no_show — pending NOT on grid; bounded 7-day, no pagination), `GET /customer/me`.
- **EnrichedBookingResponse:** repo INNER JOINs users.name/phone, services.name, stores.name/city. Additive — did NOT modify GetBookingByID. Price field on bookings = `final_price` NUMERIC(10,2).

### artist (15) — `/api/v1/artists`
Owner-facing: profile (me, update), salon stores, service catalogue (CRUD), business hours + exceptions. **NO booking endpoints — all booking lists live in internal/booking.**

### review (5) — `/api/v1/reviews`
create (POST), get by artist, delete, hide, show. All Bearer.
- **Rating cache recompute (FIXED this build):** every mutation (create/delete/hide/show) transactionally recomputes `artists.rating` + `artists.review_count` from VISIBLE reviews only, via `recomputeArtistRatingTx` inside a pgx.Tx. (Bug was: nothing ever updated the cache -> stale stars. Critical now that discovery reads the cache.)
- **HideReview ownership (FIXED this build):** was comparing `rev.ArtistID` (artists.id) against `middleware.UserIDFromContext` (users.id) -> always 403. Fixed via `GetArtistIDByUserID` lookup resolving user_id->artists.id before compare.
- **ShowReview added** (`PATCH /reviews/:id/show`) — unhide counterpart.
- `reviews.artist_id` REFERENCES artists(id); `reviews.booking_id` UNIQUE REFERENCES bookings(id). `would_recommend` column (migration 007) is UNUSED — LeaveReviewScreen collects only rating+comment.

### discovery (2) — `/api/v1/discovery` — NEW, FULLY VERIFIED LIVE
Public customer browse + profile. Separate from artist domain (owner view).
- `GET /discovery/artists?city=&category=&q=&limit=` — cards: name, category, rating, review_count, city, is_verified, is_new. One row per (artist, city) — two-studio artist appears in both city sections. NO price. is_new = created within 30 days. INNER JOINs artists->users->artist_stores->stores (so artist needs an active store to appear). INVALID_CATEGORY rejects unknown.
- `GET /discovery/artists/:id` — aggregate: artist + stores[] + services[] in one response. Services derive from salon (empty if no salon).
- Migration 008 added `artists.category VARCHAR(20) CHECK(category IN ('makeup','hair','nails','lashes','skincare'))` nullable + partial index. **Decisions:** one primary category per artist (not multi), fixed 5-enum, dynamic city grouping (backend returns any store city, frontend picks sections).

### client CRM (3) — `/api/v1/clients` — NEW, coded + unit-tested, NOT yet live-verified
Artist-facing. A "client" = customer with >=1 COMPLETED booking with the artist. Identity + metrics derived from bookings/reviews; only the private note is stored.
- `GET /clients?q=` — aggregated: bookings_count, total_spent (SUM final_price on completed), last_service, last_visit, average_rating (this client's rating of THIS artist), private note. ?q= searches name or service. Heaviest aggregate query in the codebase (correlated subqueries + GROUP BY).
- `GET /clients/:customer_id` — profile + full booking history (all statuses, newest first).
- `PUT /clients/:customer_id/notes` — upsert via `ON CONFLICT (artist_id, customer_id) DO UPDATE`. Verifies customer is the artist's client first. `client_notes` columns: id, salon_id (NOT NULL), artist_id, customer_id, content (TEXT NOT NULL DEFAULT ''), updated_at, created_at. UNIQUE(artist_id, customer_id). Table from migration 005.
- **VIP stubbed false** — `isVIP()` has a `TODO(VIP)` marker. No is_vip column; rule undecided.

---

## Key live IDs (real DB)

- Rania artist_id = `378cd76e-6c75-4c63-9d38-6f8fa211f1e5`, salon_id = `327ad1df-28dd-481a-b713-cca3bd1aaa51`, category=makeup, linked to 2 stores.
- Stores: Beirut Downtown `24869c23-b5be-48d1-a22a-08fed461010c` (Beirut), Tripoli `135c6b9e-04fe-4822-8446-726bbb6c9e4a` (Tripoli). Both active, same salon.
- Test Artist id = `a38ea468-3a73-44d4-9728-da7aaec0edcc`, category=hair.

---

## Pending / next steps

**Live verification still needed (needs running server + Rania artist JWT):**
1. Client CRM aggregate (`GET /clients`) — the heavy SQL, most likely to surface a live issue (like the discovery deleted_at bug did).
2. Review rating recompute — create a review on a completed booking -> confirm `artists.rating` moves off 0.
3. HideReview returns 204 (not the old 403).
   - To get a token: log in as Rania's user via `POST /auth/login`. Need her email+password (query: `SELECT u.email FROM users u JOIN artists a ON a.user_id=u.id WHERE a.id='378cd76e...'`). If password unknown, register a fresh artist via API and point at a salon.

**Parked product decision:**
- VIP rule for client CRM (spend threshold? booking count? manual? — currently false).

**Backend domains left to build (both have UI screens in the zip):**
- **Earnings** — `EarningsSummaryScreen`, revenue aggregation over completed bookings, date-range.
- **Media/portfolio** — `PortfolioUploadScreen`, Cloudinary integration.

**Frontend:** Angular workspace exists (customer-pwa + artist-dashboard + @bedge/shared). Login screen built, auth stack works end-to-end. Next: dashboard shell, customer PWA design exploration, Twilio/WhatsApp.

---

## Docs (in project-docs/, 32 total)

Key ones: `B-Edge-PRD-v7-Final.docx` (locked product rules), `B-Edge-API-Reference-v1.docx` (all 50 endpoints, built from real handlers), `B-Edge-Technical-Decisions-v1.docx`, `B-Edge-LLD-v2-Go.docx`, `B-Edge-UI-Spec-v2.md` (40 screens), `DOCUMENTATION.md` (the index). UI zip with all screens: `1781785478092_b-edge__2_.zip` (most complete).

---

*B-Edge · Beauty at the Edge · June 2026*
