# B-Edge — CLAUDE.md v3

> This file is the single source of truth for the B-Edge project.
> Read this before writing a single line of code.
> Claude's nickname on this project: **Spark**
> Last updated: June 2026

---

## What Is B-Edge

Beauty booking SaaS for Lebanon and MENA.
Tagline: **"Beauty at the Edge / الجمال عند الحافة"**

Fresha for Lebanon. Built by Lebanese, for Lebanese beauty artists.
Connects clients with beauty artists for booking makeup, hair, nails, and related services.

**Launch partner:** Rania — 400K Instagram followers, 2 studios (Beirut Downtown + Tripoli), staff artists.

---

## Current State (June 2026)

```
Go backend       In progress — auth done, booking domain in progress
Design system    32 screens designed in Stitch (8 more missing, prompts ready)
Angular PWA      NOT STARTED — do not start until backend is solid
Migrations       001–005 applied. 006–010 pending (see migration order below)
```

**Do not start Angular until:**
1. All 10 backend issues from UI Spec v2 are resolved
2. All 5 pending migrations are applied
3. `make dev` compiles clean (see compile fix below)

**Immediate compile fix needed:**
```go
// Add to service_test.go mockRepo
func (m *mockRepo) CreateGuestUser(_ context.Context, _ string, _ string) (uuid.UUID, error) {
    return uuid.New(), nil
}
```

---

## Tech Stack

```
Backend       Go 1.22+ with Fiber v2
Database      PostgreSQL 15 (Docker locally, AWS RDS production)
Migrations    golang-migrate v4
Auth          golang-jwt/jwt v5 + golang.org/x/crypto/bcrypt
Validation    go-playground/validator v10
Logging       go.uber.org/zap + Fiber HTTP logger
Tracing       OpenTelemetry + Jaeger (otlptracehttp exporter)
Swagger       swaggo/swag + swaggo/fiber-swagger
UUIDs         google/uuid
Money         shopspring/decimal (NEVER float64)
Environment   joho/godotenv
Testing       testing + testify + net/http/httptest

Frontend      Angular 21 workspace (b-edge-web)
State         Angular Signals (no NgRx)
Styling       Tailwind 3 + CDK 21
Node          22 via nvm
```

---

## Repository Structure

```
b-edge-api/                       Go backend
├── cmd/main.go                   Entry point
├── internal/
│   ├── config/                   DB pool, telemetry
│   ├── domain/
│   │   ├── auth/                 Register, login, refresh, logout, forgot-password, reset, change-password
│   │   ├── artist/               Profile, hours, services, portfolio, block-dates, earnings
│   │   ├── booking/              Full lifecycle: hold → pending → approved → confirmed → completed
│   │   ├── customer/             Customer bookings (/my endpoint)
│   │   ├── review/               Post-appointment reviews
│   │   └── notification/         Async WhatsApp/SMS worker
│   ├── middleware/               Auth, CORS, rate limiter, recover, error handler
│   └── pkg/                      apperror, response, jwt, hash, phone, whatsapp
├── db/migrations/
│   ├── 001_initial_schema.sql
│   ├── 002_add_user_status.sql
│   ├── 003_add_salon_id_to_artists.sql
│   ├── 004_password_resets.sql
│   ├── 005_stores.sql
│   ├── 006_booking_columns.sql       ← PENDING
│   ├── 007_audit_events.sql          ← PENDING
│   ├── 008_client_notes.sql          ← PENDING
│   ├── 009_block_dates.sql           ← PENDING
│   └── 010_services_earliest_start.sql ← PENDING
└── CLAUDE.md

b-edge-web/                       Angular workspace
├── projects/
│   ├── customer/                 Customer PWA (bedge.app)
│   ├── artist/                   Artist Dashboard (bedge.app/artist)
│   └── shared/                   Shared lib: models, components, i18n, services
└── angular.json
```

---

## Pending Migrations (run in order)

### 006_booking_columns.up.sql
```sql
ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS deposit_deadline   TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS deposit_paid_at    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS held_until         TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS channel            VARCHAR(30) NOT NULL DEFAULT 'customer_pwa',
    ADD COLUMN IF NOT EXISTS cancelled_by       VARCHAR(20),
    ADD COLUMN IF NOT EXISTS cancel_reason      TEXT,
    ADD COLUMN IF NOT EXISTS refund_amount      NUMERIC(10,2),
    ADD COLUMN IF NOT EXISTS refunded_at        TIMESTAMPTZ;
```

### 007_audit_events.up.sql
```sql
CREATE TABLE IF NOT EXISTS audit_events (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    booking_id  UUID NOT NULL REFERENCES bookings(id),
    event_type  VARCHAR(50) NOT NULL,
    old_status  VARCHAR(30),
    new_status  VARCHAR(30),
    actor_id    UUID,
    actor_type  VARCHAR(20),
    metadata    JSONB,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_events_booking_id ON audit_events(booking_id);
```

### 008_client_notes.up.sql
```sql
CREATE TABLE IF NOT EXISTS client_notes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id   UUID NOT NULL REFERENCES artists(id),
    customer_id UUID NOT NULL REFERENCES users(id),
    content     TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (artist_id, customer_id)
);
```

### 009_block_dates.up.sql
```sql
CREATE TABLE IF NOT EXISTS block_dates (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id   UUID NOT NULL REFERENCES artists(id),
    store_id    UUID REFERENCES stores(id),
    date        DATE NOT NULL,
    reason      TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_block_dates_unique
    ON block_dates(artist_id, COALESCE(store_id::text, 'ALL'), date);
```

### 010_services_earliest_start.up.sql
```sql
ALTER TABLE services
    ADD COLUMN IF NOT EXISTS earliest_start_time TIME;
-- Bridal makeup must start at 11:00 at earliest
UPDATE services SET earliest_start_time = '11:00'
WHERE LOWER(name) LIKE '%bridal%' OR LOWER(name) LIKE '%makeup%';
```

---

## Critical Go Types

### EnrichedBookingResponse
This type is the standard response for ALL booking endpoints. Never return raw Booking struct.

```go
type EnrichedBookingResponse struct {
    ID               uuid.UUID        `json:"id"`
    Status           string           `json:"status"`
    StartTime        time.Time        `json:"start_time"`
    EndTime          time.Time        `json:"end_time"`
    // Customer
    CustomerName     string           `json:"customer_name"`
    CustomerPhone    string           `json:"customer_phone"`
    // Service
    ServiceName      string           `json:"service_name"`
    DurationMin      int              `json:"duration_min"`
    Price            decimal.Decimal  `json:"price"`
    // Store
    StoreName        string           `json:"store_name"`
    StoreCity        string           `json:"store_city"`
    // Deposit
    DepositAmount    decimal.Decimal  `json:"deposit_amount"`
    DepositDeadline  *time.Time       `json:"deposit_deadline"`
    DepositPaidAt    *time.Time       `json:"deposit_paid_at"`
    // Refund
    RefundAmount     *decimal.Decimal `json:"refund_amount"`
    RefundedAt       *time.Time       `json:"refunded_at"`
    // Cancellation
    CancelledBy      *string          `json:"cancelled_by"`
    CancelReason     *string          `json:"cancel_reason"`
    // Meta
    Channel          string           `json:"channel"`
    SpecialRequests  *string          `json:"special_requests"`
    HeldUntil        *time.Time       `json:"held_until"`
    CreatedAt        time.Time        `json:"created_at"`
}
```

### ArtistResponse — must include stores
```go
type ArtistResponse struct {
    ID          uuid.UUID      `json:"id"`
    Name        string         `json:"name"`
    Slug        string         `json:"slug"`
    Bio         *string        `json:"bio"`
    Instagram   *string        `json:"instagram"`
    PrimaryCity string         `json:"primary_city"`  // from first store
    Rating      decimal.Decimal `json:"rating"`
    ReviewCount int            `json:"review_count"`
    IsVerified  bool           `json:"is_verified"`
    Stores      []StoreResponse `json:"stores"`
    Portfolio   []MediaResponse `json:"portfolio"`
}

type StoreResponse struct {
    ID   uuid.UUID `json:"id"`
    Name string    `json:"name"`
    City string    `json:"city"`
}
```

---

## Booking Status State Machine

```
held → pending → approved → deposit_paid → confirmed → completed
                ↘                                    ↘ no_show
                 cancelled                    cancelled
                                                     ↘ refund_due → refunded
expired (from: held or approved if deposit deadline passes)
```

**Status values (exact strings in DB):**
`held` | `pending` | `approved` | `deposit_paid` | `confirmed` | `completed` | `cancelled` | `expired` | `no_show` | `refund_due` | `refunded`

**Blocking statuses** (slot cannot be rebooked): `held`, `pending`, `approved`, `deposit_paid`, `confirmed`

**Action → transition map:**
```
POST   /hold                    → held
PATCH  /:id/submit              → held → pending
PATCH  /:id/approve             → pending → approved (sets deposit_deadline)
PATCH  /:id/decline             → pending → cancelled
PATCH  /:id/mark-deposit-received → approved → deposit_paid
PATCH  /:id/confirm             → deposit_paid → confirmed
PATCH  /:id/complete            → confirmed → completed
PATCH  /:id/no-show             → confirmed → no_show
PATCH  /:id/cancel (artist)     → any cancellable → refund_due (if deposit paid) or cancelled
PATCH  /:id/cancel (customer, >24h) → confirmed → refund_due
PATCH  /:id/cancel (customer, <24h) → confirmed → cancelled (deposit forfeited)
PATCH  /:id/mark-refunded       → refund_due → refunded
```

Every status transition MUST write to `audit_events`.

---

## API Endpoints — Complete List

### Auth (existing)
```
POST   /api/v1/auth/register
POST   /api/v1/auth/login
POST   /api/v1/auth/refresh
POST   /api/v1/auth/logout
POST   /api/v1/auth/forgot-password
POST   /api/v1/auth/reset-password
PATCH  /api/v1/auth/change-password     ← protected
DELETE /api/v1/auth/delete-account      ← protected
```

### Artist (public — no auth)
```
GET    /api/v1/artists?service=&city=   ← discovery
GET    /api/v1/artists/:slug            ← profile (by slug)
GET    /api/v1/artists/:id/services     ← public services
GET    /api/v1/artists/:id/slots        ← available time slots (?date=&store_id=&service_id=)
GET    /api/v1/artists/:id/reviews      ← public reviews
```

### Artist (protected — artist JWT)
```
GET    /api/v1/artists/me
PATCH  /api/v1/artists/me

GET    /api/v1/artists/me/hours
PATCH  /api/v1/artists/me/hours
GET    /api/v1/artists/me/hours/exceptions
POST   /api/v1/artists/me/hours/exceptions
DELETE /api/v1/artists/me/hours/exceptions/:id

GET    /api/v1/artists/me/block-dates
POST   /api/v1/artists/me/block-dates
DELETE /api/v1/artists/me/block-dates/:id

GET    /api/v1/artists/me/earnings?period=month&month=2026-06
```

### Services (protected — artist JWT)
```
GET    /api/v1/services
POST   /api/v1/services
PATCH  /api/v1/services/:id
DELETE /api/v1/services/:id
```

### Bookings — guest (no auth)
```
POST   /api/v1/bookings/hold            ← Step 1: hold slot (returns booking_id + held_until)
PATCH  /api/v1/bookings/:id/submit      ← Step 2: held → pending (attach guest info)
GET    /api/v1/bookings/lookup?phone=   ← guest lookup by phone
GET    /api/v1/bookings/:id             ← get booking (public for guest ref access)
```

### Bookings — customer JWT
```
GET    /api/v1/bookings/my?status=upcoming|past|cancelled
PATCH  /api/v1/bookings/:id/cancel      ← customer cancels
POST   /api/v1/reviews                  ← post-appointment review
```

### Bookings — artist JWT
```
GET    /api/v1/bookings?status=&week_start=
PATCH  /api/v1/bookings/:id/approve
PATCH  /api/v1/bookings/:id/decline
PATCH  /api/v1/bookings/:id/mark-deposit-received
PATCH  /api/v1/bookings/:id/confirm
PATCH  /api/v1/bookings/:id/complete
PATCH  /api/v1/bookings/:id/no-show
PATCH  /api/v1/bookings/:id/cancel
PATCH  /api/v1/bookings/:id/mark-refunded
```

### Clients — artist JWT
```
GET    /api/v1/clients?search=&cursor=&limit=
GET    /api/v1/clients/:id
PATCH  /api/v1/clients/:id/notes
```

### Media — artist JWT
```
GET    /api/v1/media
POST   /api/v1/media              ← returns Cloudinary signed URL
POST   /api/v1/media/confirm      ← save after Cloudinary upload
DELETE /api/v1/media/:id
PATCH  /api/v1/media/:id/set-cover
```

---

## Response Format

```json
// Success
{ "data": { ... }, "error": null, "meta": null }

// List (paginated)
{ "data": [ ... ], "error": null, "meta": { "next_cursor": "...", "has_more": true } }

// Error
{ "data": null, "error": { "code": "SLOT_UNAVAILABLE", "message": "This slot was just taken" }, "meta": null }
```

**Angular reads `error.code` — NEVER reads HTTP status codes for business logic.**

---

## Error Code Dictionary

| Code | HTTP | When |
|---|---|---|
| INVALID_EMAIL | 400 | Malformed email |
| WEAK_PASSWORD | 400 | Password fails complexity rules |
| INVALID_PHONE | 400 | Phone not +961 format |
| INVALID_BODY | 400 | JSON parse error |
| VALIDATION_ERROR | 422 | validator.v10 failure |
| INVALID_CREDENTIALS | 401 | Wrong email or password |
| TOKEN_EXPIRED | 401 | JWT expired |
| TOKEN_INVALID | 401 | JWT tampered or invalid |
| FORBIDDEN | 403 | Wrong role |
| NOT_FOUND | 404 | Resource missing or soft-deleted |
| SLOT_UNAVAILABLE | 409 | GIST constraint fired |
| BOOKING_IN_PAST | 422 | Start time in the past |
| OUTSIDE_WORKING_HOURS | 422 | Outside store open/close times |
| STORE_CLOSED | 422 | Store closed on that date |
| TOO_EARLY | 422 | Within 4-hour same-day notice window |
| SERVICE_INACTIVE | 422 | Service is deactivated |
| BOOKING_NOT_PENDING | 409 | Cannot approve — not in pending status |
| BOOKING_NOT_APPROVED | 409 | Cannot confirm deposit — not in approved status |
| BOOKING_NOT_CANCELLABLE | 409 | Cannot cancel — booking in final state |
| NOT_BOOKING_OWNER | 403 | Customer cancelling someone else's booking |
| DEPOSIT_ALREADY_PAID | 409 | Duplicate deposit mark |
| REVIEW_ALREADY_EXISTS | 409 | Duplicate review |
| PORTFOLIO_LIMIT | 400 | 21st photo upload attempt |
| RATE_LIMIT_EXCEEDED | 429 | >100 req/15min |
| INTERNAL_ERROR | 500 | Unexpected server error |

---

## Go Coding Rules (STRICTLY ENFORCED)

1. Every file has a package comment
2. Every exported function has a Go doc comment
3. Every exported struct has a Go doc comment
4. Every exported constant has a Go doc comment
5. No hardcoded values — all via `.env`
6. Named constants for magic numbers/strings
7. Parameterized SQL always — never interpolate
8. Migration files for all schema changes — no manual SQL
9. Human-readable error messages
10. Pointer receivers on all service/handler/repository methods
11. Always check errors — never ignore with `_`
12. `context.Context` as first param on all DB/service functions
13. Tests written alongside code
14. No feature done without: code + tests + Swagger docs
15. Money: `shopspring/decimal` everywhere — never float64
16. Timestamps: `TIMESTAMPTZ` in DB, `time.Time` in Go, ISO 8601 UTC in JSON
17. Status transitions: write to `audit_events` on every change
18. Notifications: queue asynchronously — never inside a transaction

---

## Angular Coding Rules (ENFORCED from first component)

1. RTL-safe CSS only: `margin-inline-*`, `padding-inline-*`, `text-align: start`, `inset-inline-*`
2. No `NgRx` — Signals only
3. Money displayed with `toFixed(2)` — never arithmetic in Angular
4. All timestamps converted to `Asia/Beirut` before display
5. Error handling reads `error.code` — never HTTP status
6. Guest info persisted to localStorage: `bedge_guest_name`, `bedge_guest_phone`, `bedge_lang`
7. Default language: Arabic (`lang="ar"`, `dir="rtl"`)
8. Direction icons flipped in RTL with `transform: scaleX(-1)`
9. Services use `inject()` not constructor injection
10. All forms use Angular Reactive Forms
11. `@angular/pwa` configured for both customer and artist apps

---

## Design System (Locked)

```
Font:           Inter 400/500/600/700 only
Primary:        #0a0a0a (ink)
Success:        #16a34a — confirmed/success states ONLY
WhatsApp:       #25D366 — WhatsApp CTAs ONLY
Error:          #dc2626 — destructive actions, cancel buttons
Amber/warning:  #d97706 — deadlines, urgent states
Background:     white (#ffffff)
Surface:        #f4f4f5 (gray-100)
Borders:        #e4e4e7 (gray-200)
Text secondary: #71717a (gray-500)
Text muted:     #a1a1aa (gray-400)

No blue. No gold. No gradients.
CTA height:     52px, full width, flush to bottom
Mobile-only:    390px viewport
Border radius:  8–12px on cards, 20px on pill tabs
```

---

## Seeded Data (Rania — Do Not Modify)

```
Salon:   "Rania Studio"     id: 327ad1df-28dd-481a-b713-cca3bd1aaa51
Store 1: Beirut Downtown    early_bird_cutoff: 09:00, same_day_notice: 4h
Store 2: Tripoli            early_bird_cutoff: 07:30, same_day_notice: 4h
Travel:  Weekday 150min buffer, Weekend 90min buffer
```

---

## Environment Variables

```env
PORT=3000
NODE_ENV=development

DB_HOST=localhost
DB_PORT=5432
DB_NAME=bedge
DB_USER=postgres
DB_PASSWORD=your_password

JWT_SECRET=64_random_hex_minimum
JWT_REFRESH_SECRET=64_different_hex
JWT_EXPIRES_IN=15m
JWT_REFRESH_EXPIRES_IN=7d

CLIENT_URL=http://localhost:4200

OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318/v1/traces

RESEND_API_KEY=re_xxxx
TWILIO_ACCOUNT_SID=xxxx
TWILIO_AUTH_TOKEN=xxxx
TWILIO_PHONE_NUMBER=+1xxxx

CLOUDINARY_CLOUD_NAME=xxxx
CLOUDINARY_API_KEY=xxxx
CLOUDINARY_API_SECRET=xxxx

TEST_DB_NAME=bedge_test
```

---

## Make Commands

```makefile
make run          # go run cmd/main.go
make dev          # air (hot reload)
make test         # go test ./...
make coverage     # go test ./... -cover
make migrate      # run migrations on bedge
make migrate-test # run migrations on bedge_test
make swagger      # swag init
make build        # go build -o bin/b-edge cmd/main.go
make docker-up    # docker-compose up -d
make docker-down  # docker-compose down
make lint         # golangci-lint run
```

---

## Estimated Timeline (from June 2026)

```
Week 1:  Apply migrations 006–010. Fix compile. Build EnrichedBookingResponse.
         Build booking lifecycle endpoints (approve/decline/deposit/confirm/complete).
Week 2:  Build slot algorithm endpoint. Build hold + submit two-step flow.
         Build client endpoints + earnings endpoint.
Week 3:  Build Angular customer PWA — screens C-01 through C-10 (booking flow).
Week 4:  Build Angular customer PWA — screens C-11 through C-19 (auth + my bookings).
Week 5:  Build Angular artist dashboard — A-01 through A-10 (login + bookings + queues).
Week 6:  Build Angular artist dashboard — A-11 through A-21 (clients + settings).
Week 7:  Testing, bug fixes, deploy to AWS, Rania onboarding.
```

---

## People

```
Edge (Abdallah Kadour)    Founder, full stack, DevOps
Rania                     Launch artist + brand ambassador (400K followers, 2 studios)
Spark (Claude)            AI engineering partner
```

---

## Documents

| File | Description |
|---|---|
| `B-Edge-PRD-v7-Final.docx` | Product requirements — every business rule locked |
| `B-Edge-BRD.docx` | Business requirements, revenue model |
| `B-Edge-HLD.docx` | High level architecture |
| `B-Edge-LLD-v2-Go.docx` | Go-specific low level design, patterns, validation |
| `B-Edge-API-Contract-v1.docx` | Response envelope, error codes, pagination, money/date conventions |
| `B-Edge-Booking-Domain-Spec-v1.docx` | Full booking lifecycle, state machine, deposit flow |
| `B-Edge-Slot-Algorithm-Spec-v1.docx` | Complete slot availability algorithm — 9 constraints |
| `B-Edge-Booking-Scenarios.docx` | 10 complex multi-store scheduling scenarios |
| `B-Edge-Angular-PWA-Architecture-v1.docx` | Angular workspace structure, RTL, Signals, PWA |
| `B-Edge-UI-Spec-v2.md` | Complete screen inventory, API map per screen, 10 backend issues |
| `B-Edge-WhatsApp-API-Templates-v1.docx` | All 16 WhatsApp notification templates |
| `B-Edge-Rania-Onboarding-Runbook-v1.docx` | Launch day checklist |
| `CLAUDE.md` (this file) | Engineering rules, migration scripts, API contract |

---

*B-Edge · Beauty at the Edge · الجمال عند الحافة · June 2026*
