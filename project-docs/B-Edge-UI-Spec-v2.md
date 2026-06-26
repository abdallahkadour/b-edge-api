# B-Edge — UI Specification v2
**Complete Screen Inventory · API Dependencies · Navigation Flows · Backend Validation**
*Prepared by Spark · June 2026 · Confidential*

---

## STATUS: PRE-ANGULAR BUILD CHECKLIST

Before writing a single Angular component, every item in this checklist must be green.

### Backend — Critical Migrations (run first)
- [ ] Migration 006: Add columns to `bookings` — `deposit_deadline`, `deposit_paid_at`, `held_until`, `channel`, `cancelled_by`, `cancel_reason`, `refund_amount`, `refunded_at`
- [ ] Migration 007: Create `audit_events` table (append-only status change log)
- [ ] Migration 008: Create `client_notes` table (artist private notes per client)
- [ ] Migration 009: Create `block_dates` table (artist blocks a full day)
- [ ] Migration 010: Add `earliest_start_time` column to `services`

### Backend — Critical Endpoints
- [ ] `EnrichedBookingResponse` type with all JOINed fields (customer name, phone, service name, store name, deposit fields)
- [ ] `GET /api/v1/artists/:slug` — public artist profile with `stores[]` array
- [ ] `GET /api/v1/artists/:id/services` — public services list (no auth)
- [ ] `GET /api/v1/artists/:id/slots?date=&store_id=&service_id=` — full slot algorithm
- [ ] `POST /api/v1/bookings/hold` — create held booking (two-step flow, Step 1)
- [ ] `PATCH /api/v1/bookings/:id/submit` — held → pending (two-step flow, Step 2)
- [ ] `GET /api/v1/bookings/lookup?phone=` — guest booking lookup
- [ ] `GET /api/v1/bookings/my?status=` — customer's own bookings
- [ ] `GET /api/v1/bookings?status=&week_start=` — artist bookings with filters
- [ ] `PATCH /api/v1/bookings/:id/approve|decline|mark-deposit-received|confirm|complete|no-show|cancel|mark-refunded`
- [ ] `GET /api/v1/clients` + `GET /api/v1/clients/:id` + `PATCH /api/v1/clients/:id/notes`
- [ ] `GET /api/v1/artists/me/earnings?period=month&month=2026-06`
- [ ] `GET /api/v1/artists/me/hours` + `PATCH` + exceptions endpoints
- [ ] `GET/POST/DELETE /api/v1/artists/me/block-dates`
- [ ] `GET/POST/DELETE/PATCH /api/v1/media` (portfolio)
- [ ] `POST /api/v1/reviews`

### Design — Missing Screens (8 total)
- [ ] C-16: Customer Forgot Password
- [ ] C-17: Customer Reset Password
- [ ] C-18: Customer Booking Detail
- [ ] C-19: Customer Cancel Booking (bottom sheet)
- [ ] A-18: Artist Forgot Password
- [ ] A-19: Artist Reset Password
- [ ] A-20: Artist Change Password
- [ ] A-21: Artist Block Dates

---

## SECTION 1 — COMPLETE SCREEN INVENTORY (40 screens)

### Customer PWA

| ID | Screen | URL | Auth | Designed |
|---|---|---|---|---|
| C-01 | Home / Discover | `bedge.app` | None | ✅ |
| C-02 | Artist Profile | `bedge.app/:slug` | None | ✅ |
| C-03 | Select Service | `bedge.app/:slug/book` | None | ✅ |
| C-04 | Pick Date & Time | `bedge.app/:slug/book/slots` | None | ✅ |
| C-05 | Guest Details Form | `bedge.app/:slug/book/details` | None | ✅ |
| C-06 | Booking Confirmed | `bedge.app/:slug/book/done` | None | ✅ |
| C-07 | Slot Unavailable | `bedge.app/:slug/book/error` | None | ✅ |
| C-08 | Artist Not Found | `bedge.app/invalid` | None | ✅ |
| C-09 | Booking Lookup | `bedge.app/booking` | None | ✅ |
| C-10 | Booking Status (Guest) | `bedge.app/booking/:ref` | None | ✅ |
| C-11 | Register | `bedge.app/register` | None | ✅ |
| C-12 | Login | `bedge.app/login` | None | ✅ |
| C-13 | My Bookings | `bedge.app/bookings` | Customer JWT | ✅ |
| C-14 | Leave a Review | `bedge.app/review/:bookingId` | JWT or magic link | ✅ |
| C-15 | PWA Install Prompt | Overlay on C-01 | None | ✅ |
| C-16 | Forgot Password | `bedge.app/forgot-password` | None | ⚠️ Missing |
| C-17 | Reset Password | `bedge.app/reset-password?token=` | Token in URL | ⚠️ Missing |
| C-18 | Booking Detail | `bedge.app/bookings/:id` | Customer JWT | ⚠️ Missing |
| C-19 | Cancel Booking | Modal on C-18 | Customer JWT | ⚠️ Missing |

### Artist Dashboard

| ID | Screen | URL | Auth | Designed |
|---|---|---|---|---|
| A-01 | Login | `dashboard/login` | None | ✅ |
| A-02 | Bookings List | `dashboard/bookings` | Artist JWT | ✅ |
| A-03 | Booking Detail | `dashboard/bookings/:id` | Artist JWT | ✅ |
| A-04 | Business Hours | `dashboard/hours` | Artist JWT | ✅ |
| A-05 | Services | `dashboard/services` | Artist JWT | ✅ |
| A-06 | Profile | `dashboard/profile` | Artist JWT | ✅ |
| A-07 | Settings | `dashboard/settings` | Artist JWT | ✅ |
| A-08 | Calendar | `dashboard/calendar` | Artist JWT | ✅ |
| A-09 | Deposit Queue | `dashboard/deposits` | Artist JWT | ✅ |
| A-10 | Refund Queue | `dashboard/refunds` | Artist JWT | ✅ |
| A-11 | Client List | `dashboard/clients` | Artist JWT | ✅ |
| A-12 | Client Detail | `dashboard/clients/:id` | Artist JWT | ✅ |
| A-13 | Earnings Summary | `dashboard/earnings` | Artist JWT | ✅ |
| A-14 | Portfolio | `dashboard/portfolio` | Artist JWT | ✅ |
| A-15 | Onboarding Step 1 | `dashboard/onboarding/salon` | None | ✅ |
| A-16 | Onboarding Step 2 | `dashboard/onboarding/store` | None | ✅ |
| A-17 | Onboarding Step 3 | `dashboard/onboarding/service` | None | ✅ |
| A-18 | Forgot Password | `dashboard/forgot-password` | None | ⚠️ Missing |
| A-19 | Reset Password | `dashboard/reset-password?token=` | Token in URL | ⚠️ Missing |
| A-20 | Change Password | `dashboard/settings/password` | Artist JWT | ⚠️ Missing |
| A-21 | Block Dates | `dashboard/block-dates` | Artist JWT | ⚠️ Missing |

**Total: 32 designed · 8 missing · 40 screens complete**

---

## SECTION 2 — SCREEN-BY-SCREEN API MAP

### C-01 · Home / Discover
```
GET /api/v1/artists?service=makeup&city=beirut&limit=20
```
Response per artist: `id, name, slug, bio, city, rating, review_count, is_verified, primary_service, starting_price, cover_photo_url`

**Backend gap:** Discovery endpoint does not exist. MVP: return all artists, no filter.

---

### C-02 · Artist Profile
```
GET /api/v1/artists/:slug           (no auth)
GET /api/v1/artists/:id/services    (no auth)
GET /api/v1/artists/:id/reviews     (no auth, limit=5)
```
ArtistResponse MUST include `stores: [{ id, name, city }]` — needed for C-04 store selector.

**Backend gap:** `stores[]` missing from ArtistResponse. PrimaryCity must be derived from first store.

---

### C-03 · Select Service
No new API call. Data comes from C-02 services load. Sets `selectedService` Signal.

---

### C-04 · Pick Date & Time

**Step 1 — Load slots:**
```
GET /api/v1/artists/:id/slots?date=2026-06-24&store_id=uuid&service_id=uuid
```
Response: `{ date, store_id, slots: [{ start_time, end_time, is_available, is_early_bird, early_bird_fee }] }`

**Step 2 — Customer taps slot (HOLD IMMEDIATELY):**
```
POST /api/v1/bookings/hold
Body: { artist_id, store_id, service_id, start_time (UTC ISO8601) }
Response: { booking_id, held_until }
```
Angular starts 10-minute countdown. On expiry → C-07.

**Backend gap:** Both endpoints missing. Slot algorithm is the most complex piece — implement last after all other endpoints work.

---

### C-05 · Guest Details Form
```
PATCH /api/v1/bookings/:id/submit
Body: { guest_name, guest_phone, special_requests }
```
Transitions held → pending. Error: `SLOT_UNAVAILABLE` (hold expired during form fill) → C-07.

**Backend gap:** Submit endpoint missing. Currently guest booking creates in one step — must be refactored to two steps.

---

### C-06 · Booking Confirmed
No API call. Uses data from C-05 response. Shows booking_id, service, date, time, location, status.

---

### C-07 · Slot Unavailable
No API call. Triggered by `SLOT_UNAVAILABLE` or `TOKEN_EXPIRED` error code from C-04 or C-05. Guest details preserved in Signals.

---

### C-08 · Artist Not Found
No API call. Triggered by 404 from `GET /api/v1/artists/:slug`.

---

### C-09 · Booking Lookup
```
GET /api/v1/bookings/lookup?phone=%2B96170123456
```
Returns most recent booking for that phone. Error: `NOT_FOUND` → show inline error.

**Backend gap:** Lookup endpoint JOINs bookings → users on phone number.

---

### C-10 · Booking Status (Guest)
```
GET /api/v1/bookings/:ref
```
Returns EnrichedBookingResponse. UI adapts to booking status:
- `approved` → show deposit instructions (amount, deadline, Wish Money number)
- `confirmed` → show confirmed state
- `expired` → show expired + reason
- `cancelled` → show cancelled + refund info if applicable

---

### C-11 · Register
```
POST /api/v1/auth/register
Body: { name, email, phone, password, role: "customer" }
```
On success: store JWT → navigate to C-13. Errors: `INVALID_EMAIL`, `WEAK_PASSWORD`, `INVALID_PHONE`.

---

### C-12 · Login
```
POST /api/v1/auth/login
Body: { email, password, role: "customer" }
```
`role` field prevents artist JWT from being issued here.

---

### C-13 · My Bookings
```
GET /api/v1/bookings/my?status=upcoming&cursor=&limit=20
GET /api/v1/bookings/my?status=past&cursor=&limit=20
GET /api/v1/bookings/my?status=cancelled&cursor=&limit=20
```
**Backend gap:** `/bookings/my` customer endpoint missing. Different from artist's `/bookings` — filters by `customer_id = jwt.user_id`.

---

### C-14 · Leave a Review
```
POST /api/v1/reviews
Body: { booking_id, rating (1-5), comment, would_recommend }
```
Guard: only accessible if booking status is `completed`. Error: `REVIEW_ALREADY_EXISTS` (409).

---

### C-15 · PWA Install Prompt
Browser PWA API only. No backend. Show on second visit. localStorage key: `bedge_pwa_dismissed`.

---

### C-16 · Forgot Password ⚠️
```
POST /api/v1/auth/forgot-password
Body: { email, role: "customer" }
```
Always responds success (prevents email enumeration). Existing endpoint.

---

### C-17 · Reset Password ⚠️
```
POST /api/v1/auth/reset-password
Body: { token, new_password, confirm_password }
```
Extract `token` from URL query param. Existing endpoint.

---

### C-18 · Booking Detail (Customer) ⚠️
```
GET /api/v1/bookings/:id
```
Shows: service, date, time, store, status timeline, deposit info if approved, cancel button if status is cancellable AND appointment is >24h away.

---

### C-19 · Cancel Booking ⚠️
```
PATCH /api/v1/bookings/:id/cancel
Body: { cancelled_by: "customer", cancel_reason }
```
Bottom sheet modal. Shows refund policy based on timing. Requires cancel_reason.

---

### A-01 · Artist Login
```
POST /api/v1/auth/login
Body: { email, password, role: "artist" }
```

---

### A-02 · Bookings List
```
GET /api/v1/bookings?status=all|pending|confirmed|completed&cursor=&limit=20
```
Returns EnrichedBookingResponse[]. Inline approve: `PATCH /api/v1/bookings/:id/approve`.

**Backend gap:** Status filter missing. EnrichedBookingResponse needed.

---

### A-03 · Booking Detail (Artist)
```
GET /api/v1/bookings/:id
PATCH /api/v1/bookings/:id/approve          (pending → approved)
PATCH /api/v1/bookings/:id/decline          (pending → cancelled)
PATCH /api/v1/bookings/:id/mark-deposit-received  (approved → deposit_paid)
PATCH /api/v1/bookings/:id/confirm          (deposit_paid → confirmed)
PATCH /api/v1/bookings/:id/complete         (confirmed → completed)
PATCH /api/v1/bookings/:id/no-show          (confirmed → no_show)
PATCH /api/v1/bookings/:id/cancel           (any → cancelled or refund_due)
```

Status-driven button visibility (only one action possible per status):
- `pending` → Approve + Decline
- `approved` → Mark deposit received
- `deposit_paid` → Confirm booking
- `confirmed` → Mark completed + Mark no-show + Cancel
- All others → no action buttons

Status timeline reads from `audit_events` table.

---

### A-04 · Business Hours
```
GET  /api/v1/artists/me/hours
PATCH /api/v1/artists/me/hours        (per-row save: store_id + day_of_week + times)
GET  /api/v1/artists/me/hours/exceptions
POST /api/v1/artists/me/hours/exceptions
DELETE /api/v1/artists/me/hours/exceptions/:id
```

---

### A-05 · Services
```
GET    /api/v1/services
POST   /api/v1/services
PATCH  /api/v1/services/:id
DELETE /api/v1/services/:id    (soft deactivate, sets is_active = false)
```

Service fields: `name, description, duration_min, price, deposit_amount, is_active, earliest_start_time`.

---

### A-06 · Profile
```
GET   /api/v1/artists/me
PATCH /api/v1/artists/me      Body: { bio, instagram }
```
Name, email, phone are read-only (from users table, changed via auth endpoints only).

---

### A-07 · Settings
Navigation hub only. No API calls. Links: Profile, Hours, Services, Portfolio, Change Password, Sign out.

---

### A-08 · Calendar
```
GET /api/v1/bookings?week_start=2026-06-16&store_id=uuid
```
Returns confirmed + approved + deposit_paid bookings for the week, ordered by start_time ASC.

**Backend gap:** `week_start` filter missing. Calendar only shows blocking statuses.

---

### A-09 · Deposit Queue
```
GET   /api/v1/bookings?status=approved
PATCH /api/v1/bookings/:id/mark-deposit-received
```

---

### A-10 · Refund Queue
```
GET   /api/v1/bookings?status=refund_due
PATCH /api/v1/bookings/:id/mark-refunded    Body: { refund_amount }
```

---

### A-11 · Client List
```
GET /api/v1/clients?search=maya&cursor=&limit=50
```
Response: `{ id, name, phone, last_service, last_visit_at, booking_count, total_spent, is_vip }`
`is_vip` = computed (booking_count >= 3).

**Backend gap:** Client endpoint needed — JOINs bookings → users filtered by artist.

---

### A-12 · Client Detail
```
GET   /api/v1/clients/:id         (booking history for this artist)
PATCH /api/v1/clients/:id/notes   Body: { notes }
```
Notes come from `client_notes` table. Upsert semantics (create if not exists, update if exists).

---

### A-13 · Earnings
```
GET /api/v1/artists/me/earnings?period=month&month=2026-06
```
Response: `{ period, total, today, this_week, daily_breakdown[], service_breakdown[] }`

---

### A-14 · Portfolio
```
GET    /api/v1/media
POST   /api/v1/media                     (returns Cloudinary signed upload URL)
POST   /api/v1/media/confirm             Body: { cloudinary_url, cloudinary_id }
DELETE /api/v1/media/:id
PATCH  /api/v1/media/:id/set-cover
```
Max 20 photos enforced in service layer.

---

### A-15/16/17 · Onboarding
```
PATCH /api/v1/artists/me              (Step 1: save salon name)
POST  /api/v1/stores                  (Step 2: create store)
POST  /api/v1/services                (Step 3: create service)
```
Not critical for Rania's launch — her data is seeded.

---

### A-18/19 · Artist Forgot/Reset Password ⚠️
Same as C-16/C-17 but `role: "artist"`. Existing auth endpoints.

---

### A-20 · Change Password ⚠️
```
PATCH /api/v1/auth/change-password
Body: { current_password, new_password }
```
Existing endpoint. Screen is missing.

---

### A-21 · Block Dates ⚠️
```
GET    /api/v1/artists/me/block-dates
POST   /api/v1/artists/me/block-dates    Body: { date, store_id (null=all), reason }
DELETE /api/v1/artists/me/block-dates/:id
```
**Backend gap:** `block_dates` table and endpoints needed.

---

## SECTION 3 — NAVIGATION FLOWS

### Customer PWA — Booking Flow (Happy Path)
```
C-01 → C-02 → C-03 → C-04 [hold] → C-05 [submit] → C-06
```

### Customer PWA — Error Paths
```
C-04 slot taken → C-07 → C-04 (form pre-filled)
C-05 hold expired → C-07 → C-04
C-02 artist not found → C-08
```

### Customer PWA — Auth Flow
```
C-12 → success → C-13
C-12 "forgot?" → C-16 → email → C-17 → C-12
C-11 → success → C-13
```

### Customer PWA — Post-Appointment
```
C-13 booking card → C-18 (detail)
C-18 cancel → C-19 (modal) → C-13
WhatsApp link → C-14 (review)
```

### Customer PWA — Bottom Nav
```
Tab 1: Home → C-01
Tab 2: Bookings → C-13 (guard: auth required, else → C-12)
Tab 3: Profile → C-12/account screen
```

### Artist Dashboard — Approval Flow
```
A-02 → tap card → A-03
A-03 approve → A-02
A-03 decline → A-02
A-03 mark deposit → A-03 (refresh status)
A-03 confirm → A-03 (refresh status)
A-03 complete/no-show → A-02
```

### Artist Dashboard — Queues
```
A-09 (deposits) → mark received → inline refresh
A-10 (refunds) → copy phone → mark refunded → inline refresh
```

### Artist Dashboard — Bottom Nav
```
Tab 1: Bookings → A-02
Tab 2: Calendar → A-08
Tab 3: Clients → A-11
Tab 4: Settings → A-07
```

### Artist Dashboard — Settings Navigation
```
A-07 → Profile → A-06
A-07 → Hours → A-04
A-07 → Services → A-05
A-07 → Portfolio → A-14
A-07 → Change Password → A-20
A-07 → Block Dates → A-21
A-07 → Sign out → A-01 (clear JWT)
```

---

## SECTION 4 — BACKEND STRUCTURAL ISSUES

### CRITICAL — Must fix before ANY Angular build

**Issue 1: Guest booking is one-step, must be two-step**

Business rule: Customer taps slot → immediate HOLD. Customer fills form → submit transitions held→pending.

Current code creates booking in one step. This bypasses the GIST constraint's race-condition protection.

Required changes:
- `POST /api/v1/bookings/hold` — new endpoint
- `PATCH /api/v1/bookings/:id/submit` — new endpoint
- `held_until` column on bookings table (migration 006)
- Background job to expire holds after 10 minutes

**Issue 2: EnrichedBookingResponse does not exist**

Every booking display screen needs: customer name, phone, service name, store name, price, deposit fields. Current response returns only IDs.

All booking endpoints must return EnrichedBookingResponse. Single source of truth:

```go
type EnrichedBookingResponse struct {
    ID                uuid.UUID        `json:"id"`
    Status            string           `json:"status"`
    StartTime         time.Time        `json:"start_time"`
    EndTime           time.Time        `json:"end_time"`
    CustomerName      string           `json:"customer_name"`
    CustomerPhone     string           `json:"customer_phone"`
    ServiceName       string           `json:"service_name"`
    DurationMin       int              `json:"duration_min"`
    Price             decimal.Decimal  `json:"price"`
    StoreName         string           `json:"store_name"`
    StoreCity         string           `json:"store_city"`
    DepositAmount     decimal.Decimal  `json:"deposit_amount"`
    DepositDeadline   *time.Time       `json:"deposit_deadline"`
    DepositPaidAt     *time.Time       `json:"deposit_paid_at"`
    RefundAmount      *decimal.Decimal `json:"refund_amount"`
    RefundedAt        *time.Time       `json:"refunded_at"`
    CancelledBy       *string          `json:"cancelled_by"`
    CancelReason      *string          `json:"cancel_reason"`
    Channel           string           `json:"channel"`
    SpecialRequests   *string          `json:"special_requests"`
    HeldUntil         *time.Time       `json:"held_until"`
    CreatedAt         time.Time        `json:"created_at"`
}
```

**Issue 3: ArtistResponse missing stores array**

C-02 and C-04 need the list of stores the artist works in. Current ArtistResponse has only a `city` string.

```go
type ArtistResponse struct {
    // existing fields...
    PrimaryCity string          `json:"primary_city"`  // first store city
    Stores      []StoreResponse `json:"stores"`
}
```

**Issue 4: No customer bookings endpoint**

Artist and customer use different routes. `/api/v1/bookings` is salon-scoped. Customer needs:
```
GET /api/v1/bookings/my?status=upcoming|past|cancelled
```
Filtered by `customer_id = jwt.user_id`. Not by salon_id.

---

### HIGH — Must fix before building affected screens

**Issue 5: bookings table missing business-critical columns**

Migration 006 must add:
```sql
ALTER TABLE bookings
    ADD COLUMN deposit_deadline   TIMESTAMPTZ,
    ADD COLUMN deposit_paid_at    TIMESTAMPTZ,
    ADD COLUMN held_until         TIMESTAMPTZ,
    ADD COLUMN channel            VARCHAR(30) NOT NULL DEFAULT 'customer_pwa',
    ADD COLUMN cancelled_by       VARCHAR(20),
    ADD COLUMN cancel_reason      TEXT,
    ADD COLUMN refund_amount      NUMERIC(10,2),
    ADD COLUMN refunded_at        TIMESTAMPTZ;
```

**Issue 6: services missing earliest_start_time**

Full makeup cannot be booked before 11:00 AM (Booking Domain Spec). This is a service-level constraint, not store-level.

```sql
ALTER TABLE services ADD COLUMN earliest_start_time TIME;
```

**Issue 7: No audit_events table**

A-03 status timeline reads from audit_events. Every status transition must write an audit event.

```sql
CREATE TABLE audit_events (
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
CREATE INDEX idx_audit_events_booking_id ON audit_events(booking_id);
```

**Issue 8: No block_dates table**

A-21 screen cannot be built without this:

```sql
CREATE TABLE block_dates (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id   UUID NOT NULL REFERENCES artists(id),
    store_id    UUID REFERENCES stores(id),
    date        DATE NOT NULL,
    reason      TEXT,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (artist_id, COALESCE(store_id, '00000000-0000-0000-0000-000000000000'::UUID), date)
);
```

**Issue 9: No client_notes table**

```sql
CREATE TABLE client_notes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    artist_id   UUID NOT NULL REFERENCES artists(id),
    customer_id UUID NOT NULL REFERENCES users(id),
    content     TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (artist_id, customer_id)
);
```

---

### MEDIUM — Fix before Phase 2

**Issue 10: Booking status `deposit_pending` inconsistency**

LLD v2 uses `deposit_pending`. Booking Domain Spec uses `approved`. API Contract uses `approved`.

Decision: use `approved` everywhere. Remove `deposit_pending` from status enum.

**Issue 11: Slot algorithm is not implemented**

The slot availability endpoint is the most complex piece of code in B-Edge. It must be implemented in full — all 7 steps from the Slot Algorithm Spec — before C-04 can be built. There are no shortcuts. A partial implementation will produce incorrect results for Rania's cross-store schedule.

**Issue 12: mockRepo missing CreateGuestUser stub**

```go
func (m *mockRepo) CreateGuestUser(_ context.Context, _ string, _ string) (uuid.UUID, error) {
    return uuid.New(), nil
}
```
`make dev` will not compile without this.

---

## SECTION 5 — MIGRATION ORDER

```
006_booking_columns.up.sql
007_audit_events.up.sql
008_client_notes.up.sql
009_block_dates.up.sql
010_services_earliest_start.up.sql
```

---

## SECTION 6 — SIGNALS SPEC

### Customer PWA
```typescript
// Booking flow
selectedArtist     = signal<ArtistResponse | null>(null)
selectedService    = signal<ServiceResponse | null>(null)
selectedStore      = signal<StoreResponse | null>(null)
selectedDate       = signal<string | null>(null)
selectedSlot       = signal<SlotResponse | null>(null)
heldBookingId      = signal<string | null>(null)
holdExpiresAt      = signal<Date | null>(null)

// Guest (persisted to localStorage)
guestName          = signal<string>(localStorage.getItem('bedge_guest_name') ?? '')
guestPhone         = signal<string>(localStorage.getItem('bedge_guest_phone') ?? '')

// Auth
currentUser        = signal<UserResponse | null>(null)
accessToken        = signal<string | null>(null)

// My bookings
upcomingBookings   = signal<EnrichedBookingResponse[]>([])
pastBookings       = signal<EnrichedBookingResponse[]>([])
cancelledBookings  = signal<EnrichedBookingResponse[]>([])
```

### Artist Dashboard
```typescript
// Auth
artistUser         = signal<UserResponse | null>(null)
artistProfile      = signal<ArtistResponse | null>(null)
accessToken        = signal<string | null>(null)

// Bookings
allBookings        = signal<EnrichedBookingResponse[]>([])
pendingCount       = computed(() => allBookings().filter(b => b.status === 'pending').length)
depositQueue       = computed(() => allBookings().filter(b => b.status === 'approved'))
refundQueue        = computed(() => allBookings().filter(b => b.status === 'refund_due'))

// Calendar
calendarWeekStart  = signal<Date>(startOfWeek(new Date()))
weekBookings       = signal<EnrichedBookingResponse[]>([])

// Clients
clients            = signal<ClientResponse[]>([])
clientSearch       = signal<string>('')
selectedClient     = signal<ClientDetailResponse | null>(null)

// Earnings
earningsPeriod     = signal<string>('2026-06')
earningsData       = signal<EarningsResponse | null>(null)
```

---

## SECTION 7 — MISSING SCREENS — STITCH PROMPTS

### C-16 · Customer Forgot Password

Design a single mobile screen for B-Edge called "Forgot Password" for customers.

Brand: Inter, ink #0a0a0a, 390px, black phone frame.

URL: bedge.app/forgot-password

- Header: "B-Edge" wordmark centered 16px weight 700
- Heading: "Forgot your password?" 20px weight 700
- Body: "Enter your email and we'll send you a reset link." 13px gray-500
- Email input: label "Email", placeholder "you@example.com"
- Success state (shown after submit): centered green checkmark icon + "Check your email" heading + "We sent a reset link to your email. It expires in 30 minutes." gray-500 body. Replace the form with this state.
- Primary CTA: "Send reset link" black 52px flush bottom
- "Back to login" gray-400 12px centered above button

---

### C-17 · Customer Reset Password

Design a single mobile screen for B-Edge called "Set New Password" for customers.

URL: bedge.app/reset-password

- Header: "B-Edge" wordmark centered
- Heading: "Set a new password" 20px weight 700
- Body: "Choose a new password for your account." 13px gray-500
- New password input with eye toggle, label "New password"
- Confirm password input with eye toggle, label "Confirm password"
- Password strength hint: "Min 8 characters, 1 uppercase, 1 number" — 10px gray-400
- Error state: inline red banner "This link has expired. Request a new one." with "Request new link" underlined link
- Primary CTA: "Set password" black 52px flush bottom

---

### C-18 · Booking Detail (Customer)

Design a single mobile screen for B-Edge called "Your Booking" for customers.

URL: bedge.app/bookings/:id

- Header: back arrow + "Your booking" centered
- Artist card at top: artist initials circle 48px + "Rania" 16px weight 700 + "@rania.beauty" gray-400 + WhatsApp icon button
- Booking details card:
  - Service: "Bridal Makeup"
  - Date & Time: "Monday, 23 June 2026 · 10:00 AM"
  - Location: "Beirut Downtown · Rania Studio"
  - Duration: "120 minutes"
  - Price: "$200"
- Deposit card (only shown if status is `approved`): amber background — "Deposit required: $50 · Due by 25 Jun 6:00 PM" + Wish Money instructions
- Status timeline (read-only): same style as artist booking detail
- Status pill at top right of booking card (Confirmed / Pending / Approved etc.)
- "Cancel booking" — red text link at very bottom — only shown when status is pending/approved/confirmed AND appointment is >24h away
- No bottom CTA button for non-cancellable statuses

---

### C-19 · Cancel Booking (Customer)

Design a bottom sheet modal overlay for B-Edge called "Cancel Booking".

Appears over C-18.

- Drag handle bar at top
- Heading: "Cancel this booking?" 17px weight 700
- Refund policy card — two variants:
  - Variant A (>24h before): green tint — "You'll receive a full refund of $50 via Wish Money within 48 hours."
  - Variant B (<24h before): amber tint — "Cancellations less than 24 hours before the appointment are not refundable."
- Reason input: label "Reason for cancelling (required)", placeholder "Tell Rania why you're cancelling…", 3 rows
- Two stacked buttons:
  - "Yes, cancel booking" — red background, white text, 52px
  - "Keep my booking" — white background, ink border, 52px

---

### A-18 · Artist Forgot Password

Same visual structure as A-01 (Artist Login screen shell):

- Header: "B-Edge" wordmark + "Artist Dashboard" subtext
- Heading: "Forgot your password?" 18px weight 700
- Body: "Enter your email and we'll send a reset link." 12px gray-500
- Email input
- Success state: green checkmark + "Reset link sent to your email."
- "Send reset link" black 52px flush bottom
- "Back to login" gray link above button

---

### A-19 · Artist Reset Password

- Same shell as A-01
- "Set a new password" heading
- New password + confirm password inputs with eye toggles
- "Set password" black 52px flush bottom

---

### A-20 · Change Password (Artist)

Design a single mobile screen for B-Edge called "Change Password".

URL: dashboard/settings/password

- Header: back arrow + "Change Password" centered
- Three inputs stacked:
  - "Current password" with eye toggle
  - "New password" with eye toggle
  - "Confirm new password" with eye toggle
- Password hint: "Min 8 characters · 1 uppercase · 1 number" — 10px gray-400
- Success banner: green "Password changed successfully." auto-dismiss 3 seconds
- "Save new password" black 52px CTA flush bottom — disabled (gray) when fields incomplete or passwords don't match

---

### A-21 · Block Dates (Artist)

Design a single mobile screen for B-Edge called "Block Dates".

Rania marks days she's completely unavailable — the slot algorithm will return no slots for these dates.

URL: dashboard/block-dates

- Header: back arrow + "Block Dates" centered
- Store selector: "Beirut Downtown" / "Tripoli" / "Both" — 3-option pill row
- Date picker section: "Select date to block" 12px label. Show a compact month calendar (current month). Tap a date to select it (selected date gets ink background pill). Left/right arrows to navigate months.
- Selected date confirmation row: "23 June 2026 · Beirut Downtown" in a gray-100 chip
- Reason input (optional): placeholder "e.g. National holiday, personal day…"
- "Block this day" black 52px CTA
- Separator line + "Blocked dates" section heading
- Blocked dates list:
  - "25 Dec 2026 · Christmas Day · Beirut + Tripoli" — with ✕ delete button
  - "01 Jan 2026 · New Year · Both" — with ✕ delete button
- Empty state for blocked list: "No blocked dates. Tap a date above to block it." gray-400

---

## SECTION 8 — RTL / ARABIC RULES

All Angular components must use logical CSS properties from day one:

```css
/* NEVER */
margin-left, margin-right, padding-left, padding-right
text-align: left, text-align: right
left: 0 (absolute), right: 0 (absolute)

/* ALWAYS */
margin-inline-start, margin-inline-end
padding-inline-start, padding-inline-end
text-align: start
inset-inline-start, inset-inline-end
```

Direction-sensitive icons (back arrows, chevrons):
```css
[dir="rtl"] .directional-icon { transform: scaleX(-1); }
```

Language stored in `localStorage`: key `bedge_lang`, values `'ar' | 'en'`. Default `'ar'`.

---

*B-Edge · UI Specification v2 · June 2026 · Confidential*
*40 screens total: 32 designed, 8 need Stitch prompts*
*10 backend issues identified and documented with exact SQL and Go types*
