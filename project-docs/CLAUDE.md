# B-Edge — CLAUDE.md

> This file is the complete context for the B-Edge project.
> Read this before writing a single line of code.
> Claude's nickname on this project: **Spark**

---

## What is B-Edge

Beauty booking platform for Lebanon and the MENA region.
Tagline: **"Beauty at the Edge / الجمال عند الحافة"**

Fresha for Lebanon. Built by Lebanese, for Lebanese beauty artists.
Connects clients with beauty artists for booking makeup, hair, nails, and related services.

**Launch partner:** Rania — popular beauty artist, 300K Instagram followers,
2 physical studios in Beirut and Tripoli, staff artists working under her.

---

## Decision Log

| Decision | Choice | Reason |
|---|---|---|
| Backend language | Go (Fiber) | Performance, K8s native, love the language |
| Frontend framework | Angular 21 PWA | Developer knows it well |
| Database | PostgreSQL 15 | Relational, reliable |
| Logging | Zap + Fiber logger | Fastest logger, structured JSON |
| Tracing | OpenTelemetry + Jaeger | Industry standard, K8s native |
| Email | Resend | 3K free/month, TypeScript-friendly API |
| SMS | Twilio | Industry standard |
| Media | Cloudinary | Free tier 25GB, CDN included |
| Infra | Docker + K8s + AWS EC2 t3.medium | Developer has K8s experience |
| CI/CD | GitHub Actions + ArgoCD | GitOps, zero manual deploys |

**Note:** Previous Node.js + TypeScript version exists at `abdallahkadour/b-edge-api-node`
Use it as a blueprint for business logic only. Do not translate — rebuild the Go way.

---

## Tech Stack

```
Backend         Go 1.22+ with Fiber v2
Database        PostgreSQL 15 (Docker locally, AWS RDS production)
Migrations      golang-migrate (same SQL files from Node version)
Auth            golang-jwt/jwt v5 + golang.org/x/crypto/bcrypt
Validation      go-playground/validator v10
Logging         go.uber.org/zap + Fiber built-in HTTP logger
Tracing         OpenTelemetry + Jaeger (otlptracehttp exporter)
Swagger         swaggo/swag + swaggo/fiber-swagger
Environment     joho/godotenv
UUID            google/uuid
Testing         testing (built-in) + testify + net/http/httptest
Migrations      golang-migrate/migrate v4
```

---

## Project Structure

```
b-edge-api/
├── cmd/
│   └── main.go                         ← entry point
├── internal/
│   ├── config/
│   │   ├── database.go                 ← pgx pool
│   │   ├── swagger.go                  ← swaggo setup
│   │   └── telemetry.go                ← OpenTelemetry + Jaeger
│   ├── domain/
│   │   ├── auth/
│   │   │   ├── handler.go              ← HTTP handlers (controllers)
│   │   │   ├── service.go              ← business logic
│   │   │   ├── repository.go           ← DB queries
│   │   │   ├── types.go                ← structs + interfaces
│   │   │   ├── validation.go           ← input validation rules
│   │   │   ├── routes.go               ← route registration
│   │   │   ├── handler_test.go         ← handler tests
│   │   │   ├── service_test.go         ← service tests
│   │   │   └── repository_test.go      ← DB tests
│   │   ├── artist/                     ← same structure
│   │   ├── booking/                    ← same structure
│   │   ├── customer/                   ← same structure
│   │   └── review/                     ← same structure
│   ├── middleware/
│   │   ├── auth.go                     ← JWT authentication
│   │   ├── error.go                    ← global error handler
│   │   └── logger.go                   ← request logging
│   └── pkg/
│       ├── apperror/                   ← AppError type
│       ├── response/                   ← JSON response helpers
│       ├── jwt/                        ← JWT helpers
│       └── hash/                       ← bcrypt helpers
├── db/
│   └── migrations/                     ← SQL files (copied from Node version)
│       ├── 001_initial_schema.sql
│       ├── 002_add_user_status.sql
│       ├── 003_add_salon_id_to_artists.sql
│       └── 004_password_resets.sql
├── docs/                               ← swaggo generates this
├── logs/                               ← never committed to git
├── .env
├── .env.example
├── .gitignore
├── go.mod
├── go.sum
├── Makefile
└── README.md
```

---

## Database Schema — 9 Tables

```sql
users           id, name, email, password_hash, role, phone,
                status (active/frozen/suspended/deleted),
                created_at, updated_at, deleted_at

artists         id, user_id, bio, city, country, instagram,
                rating, is_verified, salon_id,
                created_at, updated_at

services        id, artist_id, name, description, duration_min,
                price, currency, is_active, created_at

availability    id, artist_id, day_of_week (0-6),
                start_time, end_time, is_blocked, created_at
                UNIQUE (artist_id, day_of_week)

bookings        id, customer_id, artist_id, service_id,
                date, time, status, notes,
                created_at, updated_at, deleted_at
                status: pending/confirmed/cancelled/completed/no_show

reviews         id, booking_id, customer_id, artist_id,
                rating (1-5), comment, created_at
                UNIQUE (booking_id)

media           id, artist_id, url, cloudinary_id,
                type (photo/video), created_at

notifications   id, user_id, type, channel (email/sms/push),
                status (pending/sent/failed), payload JSONB,
                sent_at, created_at

password_resets id, user_id, token UUID, expires_at,
                used_at, created_at
```

---

## API Endpoints

### Auth — 10 endpoints
```
POST   /api/v1/auth/register
POST   /api/v1/auth/login
POST   /api/v1/auth/refresh
POST   /api/v1/auth/logout
POST   /api/v1/auth/forgot-password
POST   /api/v1/auth/reset-password
PATCH  /api/v1/auth/change-password      ← protected
DELETE /api/v1/auth/delete-account       ← protected
PATCH  /api/v1/auth/freeze-account       ← admin only
PATCH  /api/v1/auth/unfreeze-account     ← admin only
```

### Artist — ~10 endpoints (coming)
### Booking — ~6 endpoints (coming)
### Customer — ~4 endpoints (coming)
### Review — ~3 endpoints (coming)
### Search — ~1 endpoint (coming)

---

## Auth Rules

```
JWT access token      15 minutes
JWT refresh token     7 days (httpOnly cookie)
bcrypt rounds         10 (1 for tests)
Password rules        min 8 chars, 1 uppercase, 1 number
Phone format          Lebanese only +961XXXXXXXX (optional)
Role values           customer | artist (register API)
                      admin (seeded directly, never via API)
User status values    active | frozen | suspended | deleted
```

---

## Response Format

```json
// Success
{
  "success": true,
  "data": { }
}

// Error
{
  "success": false,
  "error": {
    "code": 400,
    "message": "Human readable message"
  }
}

// Validation error
{
  "success": false,
  "error": {
    "code": 400,
    "message": "Validation failed. Please check your input.",
    "details": [
      { "field": "email", "message": "Please enter a valid email address" }
    ]
  }
}
```

---

## Security Rules

```
Passwords           bcrypt hashed, NEVER stored plain
JWT secrets         64 random hex chars minimum
httpOnly cookies    refresh token only
CORS                whitelist only (CLIENT_URL env var)
Rate limiting       100 req / 15 min per IP
Helmet              security headers on all responses
Input validation    every endpoint validated
SQL queries         parameterized always, no raw interpolation
Email enumeration   wrong email = same message as wrong password
Timing safe         token comparison uses crypto safe equal
Token rotation      new refresh token on every /refresh call
Token blacklist     logout invalidates token immediately
```

---

## Observability Stack

```
Application logs    Zap (go.uber.org/zap)
                    JSON format in production
                    Human readable in development
                    Written to logs/app.log in production

HTTP logs           Fiber built-in logger middleware
                    Written to logs/access.log in production

Tracing             OpenTelemetry SDK
                    Auto instruments: Fiber routes, pgx queries
                    Exports to: Jaeger via OTLP HTTP
                    Jaeger UI: http://localhost:16686 (dev)
                               kubectl port-forward (prod)

Metrics             Prometheus (Phase 6)
Dashboards          Grafana (Phase 6)
EFK stack           Phase 6 on K8s

Health endpoint     GET /health
                    Returns: status, uptime, memory, DB status,
                             environment, version
```

---

## Infrastructure

```
Development
  PostgreSQL        Docker container port 5432
  Jaeger            Docker container port 16686 (UI) 4318 (OTLP)
  API               go run cmd/main.go port 3000
  Hot reload        air

Production
  Cloud             AWS EC2 t3.medium eu-west-1
  OS                Amazon Linux 2
  K8s               Single node kubeadm
  Gateway           Nginx (reverse proxy + SSL)
  SSL               Let's Encrypt (free)
  Domain            bedge.app
  DB                PostgreSQL in Docker on same EC2
  Registry          AWS ECR
  CI/CD             GitHub Actions → ArgoCD → K8s
  Monitoring        CloudWatch (basic) → Prometheus/Grafana (Phase 6)
  Uptime            UptimeRobot (free)
  Logs              logs/ folder → CloudWatch → EFK (Phase 6)
```

---

## B-Edge Coding Rules — STRICTLY ENFORCED

```
1. Every file has a package comment explaining its purpose
2. Every exported function has a Go doc comment
3. Every exported struct has a Go doc comment
4. Every exported constant has a Go doc comment
5. Every exported error variable has a Go doc comment
6. No hardcoded values — all configuration via .env
7. Named constants for all magic numbers/strings
8. Parameterized SQL queries always — never interpolate
9. Migration files for all DB schema changes — no manual SQL
10. Human-readable error messages always
11. Pointer receivers on all service/handler/repository methods
12. Always check error returns — never ignore with _
13. context.Context as first parameter on all DB/service functions
14. Tests written alongside code — not after
15. No feature is done without: code + tests + swagger docs
```

---

## Environment Variables

```env
# Server
PORT=3000
NODE_ENV=development

# Database
DB_HOST=localhost
DB_PORT=5432
DB_NAME=bedge
DB_USER=postgres
DB_PASSWORD=your_password

# JWT
JWT_SECRET=64_random_hex_chars_minimum
JWT_REFRESH_SECRET=64_different_random_hex_chars
JWT_EXPIRES_IN=15m
JWT_REFRESH_EXPIRES_IN=7d

# CORS
CLIENT_URL=http://localhost:4200

# Tracing
OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318/v1/traces

# Email (Phase 2)
RESEND_API_KEY=re_xxxx

# SMS (Phase 2)
TWILIO_ACCOUNT_SID=xxxx
TWILIO_AUTH_TOKEN=xxxx
TWILIO_PHONE_NUMBER=+1xxxx

# Media (Phase 2)
CLOUDINARY_CLOUD_NAME=xxxx
CLOUDINARY_API_KEY=xxxx
CLOUDINARY_API_SECRET=xxxx

# Test
TEST_DB_NAME=bedge_test
```

---

## Test Rules

```
Test database       bedge_test (separate from bedge)
Test bcrypt rounds  1 (speed — never in production)
Before each test    truncate relevant tables
After all tests     close DB connection

Every domain tests:
  handler_test.go   HTTP integration tests (like supertest)
  service_test.go   business logic unit tests
  repository_test.go DB query tests

Coverage required:
  All success paths
  All validation errors
  All auth failures
  All edge cases
  All security scenarios

Run tests:          go test ./...
Run with coverage:  go test ./... -cover
```

---

## Makefile Commands

```makefile
make run          # go run cmd/main.go
make dev          # air (hot reload)
make test         # go test ./...
make coverage     # go test ./... -cover
make migrate      # run migrations
make migrate-test # run migrations on bedge_test
make swagger      # swag init
make build        # go build -o bin/b-edge cmd/main.go
make docker-up    # docker-compose up -d
make docker-down  # docker-compose down
make lint         # golangci-lint run
```

---

## Git Strategy

```
main        production only — never commit directly
develop     integration branch
feature/*   all new work

Flow:
  git checkout -b feature/auth-api develop
  (build + test)
  git checkout develop
  git merge feature/auth-api
  git push origin develop
```

---

## Current State

```
Node.js version (reference)
  ✅ Auth API — 10 endpoints complete
  ✅ 54 tests passing
  ✅ Swagger docs live
  ✅ All project documents

Go version (this repo)
  ⏳ Starting fresh
  → Foundation first (config, pkg, middleware)
  → Then auth domain
  → Then tests + swagger
  → Then artist, booking, customer, review APIs
  → Then Angular PWAs
  → Then deploy
```

---

## Product Roadmap

```
Week 1-2    Backend APIs (Go)
Week 3-4    Artist PWA (Angular)
Week 5-6    Customer PWA (Angular)
Week 7      Deploy + Rania onboarded + first real booking
Month 2-3   Feedback + polish
Month 3-6   Growth features (SMS, Instagram, rebook)
Month 6-12  MENA expansion (Jordan, UAE, KSA)
Year 2      Payments, AI recommendations, white label
```

---

## People

```
Edge (Abdallah Kadour)    Founder, full stack, DevOps
Rania                     Launch artist + brand ambassador
                          300K Instagram followers
                          2 studios: Beirut + Tripoli
Spark (Claude)            AI engineering partner
```

---

*Last updated: May 2026*
*Go rewrite started after Node.js MVP auth API complete*
