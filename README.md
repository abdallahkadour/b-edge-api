# B-Edge API

> **Beauty at the Edge · الجمال عند الحافة**

B-Edge is a beauty booking platform built specifically for Lebanon and the MENA region. It replaces the chaotic WhatsApp-based booking process that every Lebanese beauty artist endures today — double bookings, 100+ daily messages, no-shows with zero enforcement, and zero client history — with a clean, professional platform built by a Lebanese engineer for a Lebanese market.

The platform connects customers with vetted beauty artists through two Progressive Web Apps (customer + artist) powered by this Go API.

---

## Tech Stack

| Layer | Technology |
|---|---|
| Language | Go 1.26 |
| HTTP Framework | Fiber v2 |
| Database | PostgreSQL 15 |
| Database client | pgx/v5 |
| Migrations | golang-migrate v4 |
| Auth | golang-jwt/jwt v5 + bcrypt |
| Validation | go-playground/validator v10 |
| Logging | Zap (structured JSON) |
| Tracing | OpenTelemetry + Jaeger |
| API docs | swaggo/swag |
| Hot reload | air |

---

## Architecture

```
HTTP request arrives
  → Middleware (recover · requestid · logger · cors · rate limiter)
  → Handler    — reads request, validates input, writes response
  → Service    — business logic, no HTTP knowledge
  → Repository — SQL queries only, no business logic
  → PostgreSQL — 17 tables, GIST exclusion constraint
```

Every domain follows the same four-file pattern:

```
internal/domain/{domain}/
├── model.go       → Go types mapping to database tables
├── repository.go  → SQL queries — the only layer that touches PostgreSQL
├── service.go     → Business logic — calls repository, never HTTP
└── handler.go     → HTTP layer — calls service, never SQL
```

### Repository structure

```
b-edge-api/
├── cmd/
│   ├── main.go              # Entry point — starts server, wires dependencies
│   └── migrate/
│       └── main.go          # Migration runner — apply SQL files to DB
├── internal/
│   ├── config/
│   │   ├── database.go      # pgx connection pool
│   │   ├── env.go           # Validates required environment variables
│   │   ├── logger.go        # Zap logger (JSON in prod, readable in dev)
│   │   └── telemetry.go     # OpenTelemetry → Jaeger
│   ├── domain/
│   │   └── auth/
│   │       ├── model.go     # User, RefreshToken, PasswordReset structs
│   │       ├── repository.go# All auth SQL queries
│   │       ├── service.go   # Auth business logic
│   │       └── handler.go   # Auth HTTP handlers
│   ├── middleware/
│   │   ├── auth.go          # JWT guard, role check, context helpers
│   │   ├── logger.go        # Structured Zap request logger
│   │   └── register.go      # Global middleware chain
│   └── pkg/
│       ├── apperror/        # AppError type + global Fiber error handler
│       ├── response/        # Standard JSON response helpers
│       ├── jwt/             # Generate and verify JWT tokens
│       └── hash/            # bcrypt password hashing
├── db/
│   └── migrations/
│       ├── 001_initial_schema.up.sql  # 17 tables + GIST constraint
│       ├── 002_indexes.up.sql         # All indexes
│       ├── 003_*.up.sql               # no-op (superseded by 001)
│       └── 004_*.up.sql               # no-op (superseded by 001)
├── docs/                    # All project documentation (PRD, HLD, LLD, etc.)
├── .env.example             # Environment variable template
├── .air.toml                # Hot reload configuration
├── docker-compose.yml       # PostgreSQL + Jaeger for local development
└── Makefile                 # All build commands
```

---

## Prerequisites

- Go 1.26+
- Docker Desktop
- `air` for hot reload

Install air:
```bash
go install github.com/air-verse/air@latest
export PATH=$PATH:$(go env GOPATH)/bin
```

---

## Local Setup

### 1. Clone the repo

```bash
git clone git@github.com:abdallahkadour/b-edge-api.git
cd b-edge-api
```

### 2. Install dependencies

```bash
go mod tidy
```

### 3. Configure environment

```bash
cp .env.example .env
```

Open `.env` and set your values. The required variables are:

| Variable | Description | Example |
|---|---|---|
| `DB_HOST` | PostgreSQL host | `localhost` |
| `DB_PORT` | PostgreSQL port | `5432` |
| `DB_NAME` | Database name | `bedge` |
| `DB_USER` | Database user | `postgres` |
| `DB_PASSWORD` | Database password | `postgres` |
| `JWT_SECRET` | Access token secret (min 32 chars) | `your-64-char-hex-string` |
| `JWT_REFRESH_SECRET` | Refresh token secret (min 32 chars, different from JWT_SECRET) | `your-other-64-char-hex-string` |
| `CLIENT_URL` | Allowed CORS origin | `http://localhost:4200` |
| `PORT` | Server port | `3000` |
| `APP_ENV` | Environment (`development` or `production`) | `development` |

### 4. Start the database

```bash
make docker-up
```

Starts PostgreSQL 15 and Jaeger in Docker containers.

### 5. Run migrations

```bash
make migrate
```

Creates all 17 tables in the database. Safe to run multiple times — only applies new migrations.

### 6. Start the server

```bash
make dev
```

Server starts with hot reload on port 3000. Every `.go` file save triggers an automatic rebuild.

### 7. Verify

```bash
curl http://localhost:3000/api/v1/health
```

Expected response:
```json
{"status":"ok","service":"b-edge-api","env":"development"}
```

---

## Makefile Commands

| Command | Description |
|---|---|
| `make dev` | Start server with hot reload (use during development) |
| `make run` | Start server once, no hot reload |
| `make build` | Compile to binary at `bin/b-edge` |
| `make test` | Run all tests |
| `make coverage` | Run tests with coverage report |
| `make migrate` | Apply pending migrations to development database |
| `make migrate-test` | Apply pending migrations to test database |
| `make swagger` | Generate Swagger docs from code annotations |
| `make docker-up` | Start PostgreSQL and Jaeger containers |
| `make docker-down` | Stop and remove containers |
| `make lint` | Run golangci-lint |

---

## API

Base URL: `http://localhost:3000/api/v1`

All responses use the standard envelope:

```json
{
  "data": { },
  "error": null,
  "meta": null
}
```

Error responses:

```json
{
  "data": null,
  "error": {
    "code": "SLOT_UNAVAILABLE",
    "message": "This slot was just taken. Please choose another time."
  },
  "meta": null
}
```

### Auth endpoints

| Method | Endpoint | Auth required | Description |
|---|---|---|---|
| POST | `/auth/register` | No | Register new customer or artist |
| POST | `/auth/login` | No | Login, receive access + refresh token |
| POST | `/auth/refresh` | No | Refresh access token using refresh token |
| POST | `/auth/logout` | Yes | Revoke refresh token |
| POST | `/auth/forgot-password` | No | Send password reset token |
| POST | `/auth/reset-password` | No | Reset password using token |
| PATCH | `/auth/change-password` | Yes | Change password while logged in |
| PATCH | `/auth/freeze-account` | Yes | Freeze account temporarily |
| PATCH | `/auth/unfreeze-account` | Yes | Unfreeze account |
| DELETE | `/auth/delete-account` | Yes | Soft delete account |

Swagger docs available at `http://localhost:3000/swagger` after running `make swagger`.

---

## Database

17 tables. Key design decisions:

- **GIST exclusion constraint** on `bookings` — prevents double booking at the database level. No application-level race condition possible.
- **NUMERIC(10,2)** for all money columns — no float arithmetic, no rounding errors.
- **TIMESTAMPTZ** everywhere — all timestamps stored in UTC, displayed in Asia/Beirut.
- **Soft deletes** — records are never physically deleted. `deleted_at` is stamped instead.
- **Append-only audit_events** — every status change recorded. 7-year retention for dispute resolution.
- **Keyset pagination** — all list endpoints use `WHERE (created_at, id) < (cursor)`. Always O(1), never slows with data growth.

Core tables: `users`, `salons`, `stores`, `artists`, `services`, `bookings`, `notifications`, `audit_events`, `refresh_tokens`, `password_resets`

---

## Coding Rules

All rules are in `docs/CLAUDE.md`. Key rules enforced on every PR:

- Every exported function, struct, and constant has a Go doc comment
- No hardcoded values — all config via `.env`
- Named constants — no magic numbers
- Parameterized SQL always — no string concatenation in queries
- Never use `_` to discard errors
- `context.Context` as first parameter on all DB and service functions
- Pointer receivers on all service, handler, and repository methods
- No feature is done without code + tests + Swagger docs

---

## Observability

| Tool | URL | Purpose |
|---|---|---|
| Jaeger UI | http://localhost:16686 | Distributed traces for every request |
| Health check | http://localhost:3000/api/v1/health | Used by Kubernetes probes and Uptime Kuma |
| Swagger UI | http://localhost:3000/swagger | API documentation (after `make swagger`) |

---

## Documentation

All project documentation lives in `docs/`. Key documents:

| Document | Description |
|---|---|
| `B-Edge-PRD-v7-Final.docx` | Product requirements — every business rule. Locked. |
| `B-Edge-Technical-Decisions-v1.docx` | 30 architectural decisions, 11 bugs pre-solved |
| `B-Edge-LLD-v2-Go.docx` | Low level design — Go stack, patterns, types |
| `B-Edge-Slot-Algorithm-Spec-v1.docx` | Full slot availability algorithm as Go pseudocode |
| `B-Edge-API-Contract-v1.docx` | Response envelope, error codes, pagination format |
| `DOCUMENTATION.md` | Index of all 27 documents |
| `CLAUDE.md` | Coding rules enforced on every PR |

---

## Contributing

This is a private repository. If you are working on B-Edge:

1. Read `docs/CLAUDE.md` before writing any code
2. Read `docs/B-Edge-Technical-Decisions-v1.docx` before making any architectural decision
3. Every feature needs: working code + tests + Swagger annotations
4. Run `make lint` and `make test` before opening a PR
5. Migration files are never modified after they have been run — create a new file instead

---

## Roadmap

- **Phase 1** — Auth + Booking API + Artist Dashboard PWA + Customer PWA → Launch with Rania
- **Phase 2** — Open platform, marketplace discovery, Salon+ plan, MENA expansion
- **Phase 3** — AI scheduling assistant, WhatsApp bot booking, multi-country

---

*B-Edge · Beauty at the Edge · الجمال عند الحافة · Lebanon → MENA*
