# B-Edge — Command Reference

> Every command used across the project, organized by purpose so a specific one can be found fast.
> Sessions 1–3 below are the original repo-setup history (May 2026). Everything after "Session 4 onward" covers the full build session through August 2026 — auth, booking lifecycle, Deposit Queue, Discover, Artist Handles, Calendar, guest Review Link, WhatsApp enqueue.

---

## Session 1 — Repo Setup

```bash
git clone git@github.com:abdallahkadour/b-edge-api-node.git
```
Cloned the old Node.js repo. Reference only — not touched again.

```bash
ls
```
Listed files in the current directory to see what exists.

```bash
cd b-edge-api
```
Moved into the active Go repo where all development happens.

```bash
find . -type f | grep -v ".git" | sort
```
Listed every file in the repo excluding git internals. Verified the scaffold was in place.

```bash
cat docker-compose.yml
```
Read the Docker config — confirmed PostgreSQL 15 and Jaeger are defined.

```bash
cat go.mod
```
Read the Go module file — confirmed all dependencies are installed.

```bash
cat Makefile
```
Read the build commands — confirmed run, dev, test, migrate, swagger all exist.

```bash
cat .env.example
```
Read the environment variable template — confirmed all required vars are defined.

```bash
cat .air.toml
```
Read the hot reload configuration — air watches .go files, rebuilds on change.

---

## Session 2 — Foundation Layer

```bash
mkdir -p cmd/migrate internal/config internal/middleware internal/pkg/apperror internal/pkg/response internal/pkg/hash internal/pkg/jwt
```
Created all required directories. `-p` means create parent directories too and don't error if they already exist.

```bash
cat > cmd/main.go << 'EOF' ... EOF
```
Created the server entry point. This is the first Go file — starts the server, connects the database, registers middleware.

```bash
bash setup-foundation.sh
```
Ran the foundation setup script. Created all remaining 12 Go files across config, middleware, and pkg layers.

---

## Session 3 — First Run

```bash
find . -type f -name "*.go" | sort
```
Lists every `.go` file alphabetically. Verifies all 13 foundation files exist before compiling.

```bash
make docker-up
```
Starts PostgreSQL and Jaeger in Docker containers in the background. Reads `docker-compose.yml`.

```bash
make migrate
```
Runs the SQL migration files in `db/migrations/` against the PostgreSQL database. Creates all tables. Safe to run repeatedly — only applies pending migrations.

```bash
make dev
```
Starts the Go server with hot reload via `air`. Every time you save a `.go` file, the server rebuilds and restarts automatically.

```bash
curl http://localhost:3000/api/v1/health
```
Sends a GET request to the health endpoint. Should return `{"status":"ok","service":"b-edge-api","env":"development"}`. Confirms the server is running correctly.

---

# Session 4 onward — Full Build (through August 2026)

Frontend commands assume you're in `~/Desktop/gitrepos/b-edge-web`, backend commands assume `~/Desktop/gitrepos/b-edge-api`, unless noted.

## Auth — getting a token

Every authenticated backend test starts here. Rania is the standard test account.

```bash
TOKEN=$(curl -s -X POST http://localhost:3000/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"rania@bedge.com","password":"password123"}' \
  | jq -r '.data.access_token')
```
Logs in as Rania, extracts just the access token into `$TOKEN` for reuse in subsequent commands. Every Bearer-protected curl below assumes `$TOKEN` is already set this way in the same shell session.

---

## Migrations & schema

```bash
make migrate
```
Applies pending migrations. Safe to run repeatedly.

```bash
docker exec -i bedge-postgres psql -U postgres -d bedge -c "SELECT * FROM schema_migrations;"
```
Shows current migration version and whether the `dirty` flag is set.

```bash
docker exec -it bedge-postgres psql -U postgres -d bedge -c "DELETE FROM schema_migrations;"
```
**Clean a dirty migration record** — use only when a migration failed partway and `golang-migrate` refuses to proceed. Forces a clean re-run of all migrations from scratch on the next `make migrate`.

```bash
docker exec -i bedge-postgres psql -U postgres -d bedge -c "\d <table_name>"
```
Describes a table's columns, types, constraints, indexes. Use before writing any migration that touches an existing table — confirms the real current schema rather than assuming.

---

## Booking lifecycle — the canonical end-to-end test sequence

This exact sequence (hold → submit → approve → confirm-payment) is what's actually used to manufacture a real, testable booking in any given status. Swap `service_id`/`store_id`/`start_time` as needed.

```bash
# 1. Hold a slot (guest, no auth needed)
curl -s -X POST http://localhost:3000/api/v1/bookings/guest/hold \
  -H "Content-Type: application/json" \
  -d '{
    "artist_id": "378cd76e-6c75-4c63-9d38-6f8fa211f1e5",
    "store_id": "24869c23-b5be-48d1-a22a-08fed461010c",
    "service_id": "7787a7ce-ea59-4bed-b552-c80585b4a321",
    "start_time": "2026-08-06T10:00:00Z"
  }' | jq
```
Returns a `booking_id` — grab it for the next steps.

```bash
# 2. Submit guest details (attaches identity to the held booking)
curl -s -X PATCH "http://localhost:3000/api/v1/bookings/guest/<BOOKING_ID>/submit" \
  -H "Content-Type: application/json" \
  -d '{"name":"Test Customer","phone":"+96170000000"}' | jq
```
Transitions `held → pending`.

```bash
# 3. Approve (Bearer = artist token)
curl -s -X PATCH "http://localhost:3000/api/v1/bookings/<BOOKING_ID>/approve" \
  -H "Authorization: Bearer $TOKEN" | jq
```
Transitions `pending → approved`.

```bash
# 4. Confirm payment — the single-action primary deposit-confirm path
curl -s -X PATCH "http://localhost:3000/api/v1/bookings/<BOOKING_ID>/confirm-payment" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"reference": "Whish Code #12345"}' | jq
```
Transitions `approved → confirmed` in one call. The `reference` field is optional — omit the body entirely and it still works.

```bash
# Check what's currently sitting in "approved" with a real deposit
curl -s "http://localhost:3000/api/v1/bookings/artist/378cd76e-6c75-4c63-9d38-6f8fa211f1e5?status=approved" \
  -H "Authorization: Bearer $TOKEN" \
  | jq '.data[] | {id, customer_name, service_name, deposit_amount, deposit_deadline}'
```
The single most-used diagnostic command tonight — confirms exactly what's in the Deposit Queue's Pending tab without opening the browser.

---

## Services — checking and toggling active status

```bash
# List ALL of an artist's services, including inactive ones
curl -s "http://localhost:3000/api/v1/artists/salon/services" \
  -H "Authorization: Bearer $TOKEN" | jq
```
Note the route: `/artists/salon/services`, not `/artists/services` — easy to get wrong.

```bash
# Reactivate (or deactivate) a specific service
curl -s -X PATCH "http://localhost:3000/api/v1/artists/salon/services/<SERVICE_ID>" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"is_active": true}' | jq
```

---

## Artist handles

```bash
# Set an artist's public handle
curl -s -X PATCH "http://localhost:3000/api/v1/artists/378cd76e-6c75-4c63-9d38-6f8fa211f1e5" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"handle": "rania"}' | jq
```
Once set, `GET /artists/rania` works identically to `GET /artists/378cd76e-...` — the backend resolves either form transparently.

---

## Discovery / search

```bash
curl -s "http://localhost:3000/api/v1/discovery/artists?q=beirut" | jq
```
Public, no auth. Matches name **or** city (fixed this session — was name-only before).

```bash
curl -s "http://localhost:3000/api/v1/discovery/artists?category=makeup" | jq
```
Filter by category. Valid values: `makeup`, `hair`, `nails`, `lashes`, `skincare`.

```bash
curl -s "http://localhost:3000/api/v1/discovery/artists/378cd76e-6c75-4c63-9d38-6f8fa211f1e5" | jq
```
Full public profile aggregate — artist + stores[] + services[].

---

## Reviews — the guest token-link flow

```bash
# Get the review-link context (what a customer sees before submitting)
curl -s "http://localhost:3000/api/v1/reviews/by-token/<TOKEN>" | jq
```
Public, no auth. `<TOKEN>` comes from a completed booking's `review_token` field (only visible on `EnrichedBookingResponse` — e.g. via the artist bookings list, not the plain booking response).

```bash
# Submit a review via the token
curl -s -X POST "http://localhost:3000/api/v1/reviews/by-token/<TOKEN>" \
  -H "Content-Type: application/json" \
  -d '{"rating": 5, "comment": "Amazing experience"}' | jq
```

```bash
# Standard authenticated review creation (artist/customer with a real session)
curl -s -X POST "http://localhost:3000/api/v1/reviews/" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"booking_id":"<COMPLETED_BOOKING_ID>","rating":5,"comment":"Amazing"}' | jq
```

```bash
# Hide / show a review (artist moderation)
curl -s -i -X PATCH "http://localhost:3000/api/v1/reviews/<REVIEW_ID>/hide" \
  -H "Authorization: Bearer $TOKEN"
curl -s -i -X PATCH "http://localhost:3000/api/v1/reviews/<REVIEW_ID>/show" \
  -H "Authorization: Bearer $TOKEN"
```
Both return 204 on success. Rating cache on `artists.rating`/`review_count` recomputes automatically.

---

## Calendar

```bash
curl -s "http://localhost:3000/api/v1/bookings/artist/378cd76e-6c75-4c63-9d38-6f8fa211f1e5/calendar?week_start=2026-08-03" \
  -H "Authorization: Bearer $TOKEN" | jq
```
`week_start` must be a Monday. Only returns "committed" statuses (approved/deposit_paid/confirmed/completed/no_show) — pending requests never appear on the grid.

---

## Client CRM

```bash
curl -s "http://localhost:3000/api/v1/clients?q=" \
  -H "Authorization: Bearer $TOKEN" | jq
```
A "client" = a customer with ≥1 completed booking with this artist. Empty array is expected/correct if none exist yet.

```bash
curl -s -X PUT "http://localhost:3000/api/v1/clients/<CUSTOMER_ID>/notes" \
  -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
  -d '{"content":"prefers 2pm appointments"}' | jq
```

---

## Direct DB queries (docker exec psql) — the ones actually reused

```bash
# Every artist with their real name (find UUIDs fast)
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT a.id, u.name FROM artists a JOIN users u ON u.id = a.user_id;"
```

```bash
# All stores — city, salon, active status
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT id, name, city, salon_id, is_active FROM stores;"
```

```bash
# Confirm an artist actually has a store linked (discovery needs this)
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT a.id AS artist, s.id AS store, s.city, s.is_active
   FROM artists a
   LEFT JOIN artist_stores ast ON ast.artist_id = a.id
   LEFT JOIN stores s ON s.id = ast.store_id
   WHERE a.id = '378cd76e-6c75-4c63-9d38-6f8fa211f1e5';"
```

```bash
# Find a user's login email from an artist_id (when the password is known but email isn't)
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT u.id, u.email, u.role FROM users u
   JOIN artists a ON a.user_id = u.id
   WHERE a.id = '378cd76e-6c75-4c63-9d38-6f8fa211f1e5';"
```

```bash
# Before/after check when testing anything that touches the rating cache
docker exec -i bedge-postgres psql -U postgres -d bedge -c \
  "SELECT rating, review_count FROM artists WHERE id='378cd76e-6c75-4c63-9d38-6f8fa211f1e5';"
```

---

## Frontend — build & serve

```bash
lsof -ti:4200 | xargs kill -9 2>/dev/null
ng build shared && ng serve --project customer-pwa --port 4200
```
The standard restart sequence after any change touching `@bedge/shared`. `2>/dev/null` suppresses the harmless "nothing was running" error from `lsof`/`kill` when the port was already free.

```bash
lsof -ti:4200 | xargs kill -9 2>/dev/null
ng build shared && ng serve --project artist-dashboard --port 4200
```
Same, for the artist dashboard. Only one of the two frontend apps can run on :4200 at a time — always kill first.

```bash
ng build shared
```
Rebuild just the shared lib without restarting a dev server — rarely used alone, almost always paired with one of the two commands above.

---

## Deploying a delivered fix (the repeated pattern)

```bash
cd ~/Desktop/gitrepos/<b-edge-api OR b-edge-web>
unzip -o ~/Downloads/<delivered-file>.zip -d /tmp/<some-label>
cp /tmp/<some-label>/<path>/<file> <real destination path>
# then either:
make test                                    # backend
# or:
lsof -ti:4200 | xargs kill -9 2>/dev/null && ng build shared && ng serve --project <app> --port 4200   # frontend
```
The general shape every delivered fix follows. The most common mistake all session: **destination path errors** — always double-check a file's real destination folder depth before running `cp`, especially when a zip bundles files from multiple different directories together (e.g. `dashboard-layout.component.ts` belongs in `features/dashboard/`, not `src/app/` directly, even though `app.config.ts` sitting right next to it in the same delivery zip *does* belong in `src/app/`).

---

## Diagnostics — verifying a fix actually landed

```bash
grep -c "<some unique string from the fix>" <path/to/file>
```
Prints `1` if the string is present, `0` if not. The single most useful command this session for cutting through "did the download/copy actually work" uncertainty before spending time debugging a fix that never actually applied.

```bash
find ~/Downloads -iname "<base-filename>*"
```
Lists every duplicate-named download (`file.go`, `file (1).go`, `file (2).go`, …) when the browser has silently saved multiple versions under similar names. Pair with the `grep -c` command above on each candidate to find which one actually has the fix.

```bash
grep -l "<unique string only the correct version has>" ~/Downloads/<pattern>*
```
Faster version of the above when you already know what text distinguishes the correct file — searches all matching downloads at once and prints only the filename(s) that contain it.

---

## Go / Git reference

```bash
go get <package>
```
Download a package.

```bash
go mod tidy
```
Scans all `.go` files, finds every import, adds missing entries to `go.sum`, removes unused entries from `go.mod`/`go.sum`, upgrades indirect to direct where needed.

```bash
go mod download
```
Downloads all dependencies already listed in `go.mod`.

```bash
sed -i '' 's/CREATE INDEX CONCURRENTLY/CREATE INDEX/g' db/migrations/002_indexes.up.sql
```
`sed` edits text in place (`-i ''` on macOS specifically — Linux uses `-i` with no trailing quotes). `s/old/new/g` replaces every occurrence. Used once to strip `CONCURRENTLY` from an index migration (that keyword can't run inside a transaction, which `golang-migrate` wraps every migration in by default).

```bash
git clone git@github.com:abdallahkadour/b-edge-api.git
cd b-edge-api
go mod tidy
make dev
```
The full clean-checkout-to-running-server sequence for a fresh machine.

---

## Makefile Commands Reference

| Command | What it does |
|---|---|
| `make run` | Compile and run the server once. No hot reload. |
| `make dev` | Run with air hot reload. Use this during development. |
| `make test` | Run all tests in the project. No database needed — every test uses an in-memory mock repository. |
| `make coverage` | Run tests and show coverage percentage per package. |
| `make migrate` | Apply pending migrations to the development database. |
| `make migrate-test` | Apply pending migrations to the test database. |
| `make swagger` | Generate Swagger docs from code annotations into `docs/`. |
| `make build` | Compile to a binary at `bin/b-edge`. Used for production builds. |
| `make docker-up` | Start PostgreSQL and Jaeger containers in the background. |
| `make docker-down` | Stop and remove the Docker containers. |
| `make lint` | Run golangci-lint against all Go files. |

---

## find Command Breakdown

```bash
find . -type f -name "*.go" | sort
```

| Part | Meaning |
|---|---|
| `find` | The search command |
| `.` | Start from the current directory |
| `-type f` | Only return files (`f`). Excludes directories (`d`) and symlinks. |
| `-name "*.go"` | Only return items whose name ends in `.go` |
| `\| sort` | Pipe results to `sort` — display alphabetically |

---

## air Hot Reload Flow

When you run `make dev`, this happens automatically on every file save:

```
You save a .go file
    → air detects the change
    → runs: go build -o ./tmp/main ./cmd/main.go
    → if build succeeds: kills old process, starts ./tmp/main
    → if build fails: logs error to build-errors.log, keeps old process running
```

---

## What Each Foundation File Does

| File | Purpose |
|---|---|
| `cmd/main.go` | Entry point. Starts everything in sequence: logger → env validation → database → telemetry → HTTP server. |
| `cmd/migrate/main.go` | Runs database migrations. Called by `make migrate` and `make migrate-test`. |
| `internal/config/logger.go` | Creates Zap logger. Readable output in development. JSON output in production. |
| `internal/config/env.go` | Validates all required env vars exist on startup. Fails immediately if anything is missing. |
| `internal/config/database.go` | Opens pgx connection pool to PostgreSQL. Pings DB to confirm connection works. |
| `internal/config/telemetry.go` | Starts OpenTelemetry tracing. Sends traces to Jaeger at `localhost:16686`. |
| `internal/pkg/apperror/apperror.go` | Defines AppError type. Every error in the API uses this — consistent JSON format every time. |
| `internal/pkg/response/response.go` | Success response helpers: OK, Created, List, NoContent. Handlers never call c.JSON directly. |
| `internal/pkg/hash/hash.go` | bcrypt password hashing and verification. Slower cost in production, faster in tests. |
| `internal/pkg/jwt/jwt.go` | JWT access token (15min) and refresh token (7 days) — generate and verify. |
| `internal/middleware/auth.go` | Reads Bearer token from request, verifies it, injects user_id + salon_id + role into context. |
| `internal/middleware/register.go` | Attaches global middleware in order: recover → requestid → logger → cors → rate limiter. |
| `internal/middleware/logger.go` | Logs every request: method, path, status, latency, IP, request ID. |

---

## Key live IDs (for constructing commands quickly)

```
Rania          artist_id = 378cd76e-6c75-4c63-9d38-6f8fa211f1e5
               salon_id  = 327ad1df-28dd-481a-b713-cca3bd1aaa51
               handle    = rania
               login     = rania@bedge.com / password123

Beirut Downtown store = 24869c23-b5be-48d1-a22a-08fed461010c
Tripoli store          = 135c6b9e-04fe-4822-8446-726bbb6c9e4a

nails service          = 9aa8cfe8-9a6b-4fa1-b07c-81588af3d9e8   ($10, $0 deposit)
Bridal makeup service  = 7787a7ce-ea59-4bed-b552-c80585b4a321   ($200, $100 deposit)
```

---

*B-Edge · Beauty at the Edge · الجمال عند الحافة · August 7, 2026*
