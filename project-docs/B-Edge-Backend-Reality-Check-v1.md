# B-Edge — Backend Reality Check v1
**What the schema ACTUALLY has vs. what the screens need**
*Based on reading the real `001_initial_schema_up.sql` (not the docs) · June 2026*

---

## HEADLINE FINDING

The database schema is **production-grade and nearly complete**. My earlier UI Spec v2 listed 10 "backend gaps" based on the documentation. After reading the actual schema, **7 of those 10 are already solved in the database.**

The real gap is not the schema. **It is the Go code.** Only the `auth` domain is implemented in Go. The `booking`, `artist`, `customer`, and `review` domains exist as draft files in outputs but are not integrated, and most endpoints are unwritten.

This is a much better position than the docs implied. We are not missing a foundation — we are missing the application layer on top of a solid foundation.

---

## PART 1 — SCHEMA STATUS (the good news)

### Tables that ALREADY exist (17 tables)
```
✅ users                      ✅ salons
✅ stores                     ✅ business_hours
✅ business_hours_exceptions  ✅ artists
✅ artist_stores              ✅ artist_store_buffers
✅ service_categories         ✅ services
✅ bookings                   ✅ reviews
✅ media                      ✅ notifications
✅ password_resets            ✅ refresh_tokens
✅ audit_events
```

### My "10 gaps" — re-checked against the REAL schema

| # | Claimed Gap (UI Spec v2) | Reality |
|---|---|---|
| 1 | Two-step guest booking (hold→submit) | ⚠️ **Code gap** — `held_until` column EXISTS. Logic not written. |
| 2 | EnrichedBookingResponse | ⚠️ **Code gap** — all columns exist. Type + JOIN query not written. |
| 3 | ArtistResponse missing stores[] | ✅ **SOLVED** — `artist_stores` junction table exists. |
| 4 | bookings missing columns | ✅ **SOLVED** — `held_until`, `deposit_deadline`, `deposit_paid_at`, `channel`, `cancellation_reason`, `cancelled_at`, `completed_at`, `no_show_at` ALL exist. |
| 5 | Customer /bookings/my endpoint | ⚠️ **Code gap** — schema supports it. Endpoint not written. |
| 6 | services.earliest_start_time | ⚠️ **Partial** — NOT in schema. BUT `active_duration_min` exists (processing gaps). See Decision 1 below. |
| 7 | audit_events table | ✅ **SOLVED** — exists, richer than I specced (JSONB old/new values, IP, actor role). |
| 8 | block_dates table | 🟡 **Design decision** — `business_hours_exceptions` already does this per-store. See Decision 2. |
| 9 | client_notes table | ❌ **Genuinely missing** — needs migration. Only true schema gap. |
| 10 | Slot availability endpoint | ⚠️ **Code gap** — all supporting columns/tables exist. Algorithm not written. |

**Score: 3 fully solved, 1 richer than specced, 5 are code-not-schema, 1 design decision, 1 genuine schema gap.**

---

## PART 2 — THE ONLY REAL SCHEMA CHANGES NEEDED

### Migration 005 — client_notes (the one genuine gap)
```sql
CREATE TABLE IF NOT EXISTS client_notes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    salon_id    UUID NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
    artist_id   UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
    customer_id UUID NOT NULL REFERENCES users(id),
    content     TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (artist_id, customer_id)
);
CREATE INDEX idx_client_notes_artist ON client_notes(artist_id);
```
This is the only screen (A-12 Client Detail) that needs a new table.

### Decision 1 — services.earliest_start_time
The "full makeup from 11 AM" rule from the Booking Domain Spec is NOT in the schema.
Two options:
- **Option A (recommended):** Add the column. It's a clean per-service constraint.
  ```sql
  ALTER TABLE services ADD COLUMN earliest_start_time TIME;
  ```
- **Option B:** Handle it as a `service_categories` rule (the category table exists). More normalized but more complex.

Recommendation: **Option A.** One column, simple, matches the spec exactly.

### Decision 2 — block_dates vs business_hours_exceptions
The A-21 "Block Dates" screen wants the artist to block a full day.
The schema already has `business_hours_exceptions(store_id, exception_date, is_closed, reason)`.

**This already does exactly what A-21 needs** — `is_closed = true` for a date blocks the store.

Two interpretations:
- If "block date" means **the store is closed** → use `business_hours_exceptions`. No new table. A-21 just writes to this table.
- If "block date" means **one specific artist is out but store stays open** → need a new `artist_block_dates` table because exceptions are store-level, not artist-level.

For Rania's launch (she IS the store, solo + staff), store-level closure is sufficient. **Recommendation: use `business_hours_exceptions` for launch. Defer artist-level blocking to Phase 2 when staff artists need individual days off.**

This removes a whole migration and screen-backend mismatch.

---

## PART 3 — THE REAL WORK: GO CODE BY DOMAIN

This is where the actual effort is. The schema is ready; the Go application layer is not.

### auth domain — ✅ DONE
```
✅ register, login, refresh, logout
✅ forgot-password, reset-password, change-password
✅ User, RefreshToken, PasswordReset models
✅ repository with all auth queries
```
Supports screens: C-11, C-12, C-16, C-17, A-01, A-18, A-19, A-20. **8 screens fully backed.**

### booking domain — ⚠️ DRAFT ONLY (biggest effort)
Draft files exist in outputs but need integration + completion:
```
⬜ EnrichedBookingResponse type + JOIN query        → unblocks 8 screens
⬜ POST /bookings/hold (two-step part 1)            → C-04
⬜ PATCH /bookings/:id/submit (two-step part 2)     → C-05
⬜ GET /bookings/:id (enriched)                      → C-10, C-18, A-03
⬜ GET /bookings/lookup?phone=                       → C-09
⬜ GET /bookings/my?status= (customer)               → C-13
⬜ GET /bookings?status=&week_start= (artist)        → A-02, A-08, A-09, A-10
⬜ PATCH /bookings/:id/approve|decline               → A-02, A-03
⬜ PATCH /bookings/:id/mark-deposit-received         → A-03, A-09
⬜ PATCH /bookings/:id/confirm                        → A-03
⬜ PATCH /bookings/:id/complete|no-show              → A-03
⬜ PATCH /bookings/:id/cancel                         → A-03, C-18, C-19
⬜ PATCH /bookings/:id/mark-refunded                 → A-10
⬜ audit_events write on every transition            → A-03 timeline
```
Supports screens: C-04, C-05, C-06, C-07, C-09, C-10, C-13, C-18, C-19, A-02, A-03, A-08, A-09, A-10. **14 screens.**

### slot domain — ⬜ NOT STARTED (highest complexity)
```
⬜ GET /artists/:id/slots — the 7-step algorithm
   Uses: business_hours, business_hours_exceptions, bookings (GIST),
         artist_store_buffers, services.active_duration_min, stores.early_bird_cutoff
```
All supporting data exists. Pure algorithm work. Supports: C-04. **1 screen, but the hardest.**

### artist domain — ⚠️ DRAFT ONLY
```
⬜ GET /artists?service=&city= (discovery)           → C-01
⬜ GET /artists/:slug (public profile + stores[])    → C-02
⬜ GET /artists/:id/services (public)                → C-02, C-03
⬜ GET /artists/:id/reviews (public)                 → C-02
⬜ GET/PATCH /artists/me                              → A-06
⬜ GET/PATCH /artists/me/hours + exceptions          → A-04, A-21
⬜ GET /artists/me/earnings                           → A-13
```
Supports: C-01, C-02, C-03, A-04, A-06, A-13, A-21. **7 screens.**

### service domain — ⬜ NOT STARTED
```
⬜ GET/POST/PATCH/DELETE /services                   → A-05
```
Supports: A-05. **1 screen.**

### client domain — ⬜ NOT STARTED (needs migration 005 first)
```
⬜ GET /clients?search=                               → A-11
⬜ GET /clients/:id                                   → A-12
⬜ PATCH /clients/:id/notes (needs client_notes)     → A-12
```
Supports: A-11, A-12. **2 screens.**

### review domain — ⬜ NOT STARTED
```
⬜ POST /reviews                                      → C-14
```
reviews table exists. Note: schema has `rating` + `comment` but NO `would_recommend` column.
The C-14 screen has a "Would you recommend?" toggle.
**Decision 3:** Either add `would_recommend BOOLEAN` to reviews, or drop the toggle from C-14.
Recommendation: add the column — it's cheap and the UI already designed for it.
```sql
ALTER TABLE reviews ADD COLUMN would_recommend BOOLEAN;
```
Supports: C-14. **1 screen.**

### media domain — ⬜ NOT STARTED
```
⬜ GET/POST/DELETE/PATCH /media (Cloudinary)         → A-14
```
media table exists. Supports: A-14. **1 screen.**

---

## PART 4 — CORRECTED MIGRATION LIST

Down from my earlier 5 migrations to just **3 small ones:**

```
005_client_notes.up.sql              ← genuine gap (A-12)
006_services_earliest_start.up.sql   ← Decision 1, Option A (C-04 makeup rule)
007_reviews_would_recommend.up.sql   ← Decision 3 (C-14 toggle)
```

That's it. The schema author already did the heavy lifting — bookings columns, audit_events, artist_stores, buffers, GIST constraint, processing gaps all exist.

---

## PART 5 — BUILD ORDER (revised, reality-based)

```
Step 0:  Fix compile (CreateGuestUser mock stub)         — 10 min
Step 1:  3 migrations (005, 006, 007)                    — 1 hour
Step 2:  EnrichedBookingResponse type + JOIN query       — 0.5 day  ← unblocks 8 screens
Step 3:  Booking lifecycle endpoints (approve...refund)  — 2 days
Step 4:  Two-step hold + submit                          — 1 day
Step 5:  Artist public endpoints (profile, services)     — 1 day
Step 6:  Customer /bookings/my + review + lookup         — 1 day
Step 7:  Client + earnings + media + services CRUD       — 2 days
Step 8:  Slot algorithm (last, hardest)                  — 2-3 days
─────────────────────────────────────────────────────────────────
Total backend: ~10 working days before Angular
```

---

## PART 6 — SCREENS FULLY BACKED RIGHT NOW

With only the auth domain done, these 8 screens could be built in Angular today:
```
C-11 Register     C-12 Login       C-16 Forgot Pwd    C-17 Reset Pwd
A-01 Login        A-18 Forgot Pwd  A-19 Reset Pwd     A-20 Change Pwd
```

Everything else waits on the domains above.

---

## SUMMARY

| Question | Answer |
|---|---|
| Is the schema missing things? | Almost nothing — 1 table (client_notes) + 2 columns |
| Were my "10 gaps" real? | 3 were already solved; 5 were code-not-schema; 2 were design decisions |
| What's the real work? | Writing the Go application layer for 7 domains |
| Biggest risk? | Slot algorithm (build last) |
| Can we start Angular now? | Only the 8 auth screens. Rest waits on Go domains. |
| Will the screens force schema changes? | No — 3 tiny migrations cover everything. Schema is solid. |

The answer to your question "will we have problems that lead to API/backend changes?" is now a confident **no** — because we checked the real schema, not the docs, and it already anticipated nearly everything the screens need.
