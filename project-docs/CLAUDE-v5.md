# CLAUDE-v5.md — B-Edge Engineering Context

> Single source of truth for continuing the B-Edge build. Read this first in any new chat. Supersedes CLAUDE-v4.md and all earlier versions.
> Last updated: August 7, 2026 · Schema v13 · 9 domains live · Both frontend apps substantially built · Gap analysis complete against locked specs (see `B-Edge-Targets-vs-Actual-Analysis-v1.md`).

---

## Who & What

**B-Edge** — beauty booking SaaS for Lebanon / MENA ("Fresha for Lebanon"). Solo founder build.

- **Founder:** Edge (Abdallah Kadour). GitHub `abdallahkadour`. Java background, strong DevOps/K8s, deepening Go through this build.
- **AI partner:** Spark (Claude).
- **Launch artist:** Rania — verified, ~300K IG followers, two studios (Beirut Downtown + Tripoli). Makeup category. Public handle now set: `rania` (`/book/rania` works identically to the UUID link).

**Backend repo:** `abdallahkadour/b-edge-api` (Go).
**Frontend repo:** `b-edge-web` — Angular 21 workspace (`customer-pwa`, `artist-dashboard`, shared lib `@bedge/shared`).

---

## How Spark works with Edge (non-negotiable)

1. **Never rush.** Edge sets the pace.
2. **Always work from the real current files** — ask for uploads before writing code or making architectural/schema decisions.
3. **Validate decisions online** (competitors + best practices) before presenting a DB schema or business rule.
4. **Ask for what's needed, every time** — Edge would rather paste a file than have Spark guess.
5. **Defer undecided product questions** rather than block implementation. Stub with a clear TODO.
6. Edge pushes back, asks deep "why" questions, communicates casually and directly.
7. **Delivery format:** complete drop-in replacement files, not diffs/snippets.
8. **Read/write skepticism toward mockups:** the Stitch/AI Studio design zip is a *reference*, not gospel — several screens assumed backend capabilities that don't exist (price-per-artist-card, a "post publicly" review checkbox, WhatsApp-confirmed badges). Match designs where the backend genuinely supports them; deviate deliberately and document why when it doesn't, rather than silently faking functionality.

---

## Stack & Environment

- **Backend:** Go 1.22+, Fiber v2, pgx v5/pgxpool, golang-migrate, golang-jwt/jwt v5, go-playground/validator v10, shopspring/decimal, zap, OpenTelemetry+Jaeger, swaggo/swag, google/uuid, testify.
- **DB:** PostgreSQL 15, Docker container `bedge-postgres`, db=`bedge`.
- **Frontend:** Angular 21 (standalone, Signals, `inject()`), Tailwind 3, CDK 21, Lucide Angular (2px stroke, kebab-case names), Node 22.
- **Media:** Cloudinary — genuinely wired end-to-end (real cloud name `mlop5tfg`, unsigned preset `bedge-media`), not a stub.
- **Notifications:** Twilio WhatsApp — worker fully built, enqueue wired (see below), **no Twilio account exists yet**. Everything queues as `pending` and will start sending the moment real credentials are configured — no code changes needed at that point.
- **Infra:** K8s single-node, AWS EC2 t3.medium planned for launch. Not yet deployed to production.
- **Brand:** Inter font, ink `#0a0a0a`, success green `#16a34a`, no blue/gold, 390px mobile-first, enterprise/restrained.

**Makefile targets:** `run`, `dev` (air), `test`, `migrate`, `swagger`, `build`, `docker-up/down`, `lint`.

---

## Critical environment facts (save hours)

- **`bedge_test` DB does not exist.** All tests use in-memory `mockRepo` — `make test` needs no database.
- **`make dev` (air) is the real compile authority.** Spark's sandbox has no Go toolchain.
- **`deleted_at` columns exist ONLY on `users`, `bookings`, `salons`.** Never filter on it for `artists`, `stores`, `services`, `reviews`, `client_notes` — causes a live SQLSTATE 42703.
- **Timezone convention (hard-won, see "Fixed bugs" below):** store wall-clock LOCAL times as-is + an IANA zone string; never pre-convert to UTC and store that. `stores.timezone` (default `Asia/Beirut`) now exists. `cmd/main.go` blank-imports `time/tzdata` so this works regardless of the container's OS.
- **`bookings.artist_id` references `artists.id`, NOT `users.id`.** Every artist-action ownership check must resolve `user_id → artists.id` first via `GetArtistIDByUserID`. This bug existed in 6 booking endpoints until this session; every other domain already had the correct pattern.
- **A route param can be a UUID OR a handle** on `GET /artists/:id` and its two sibling public endpoints (`/services`, `/stores`) — `Service.ResolveArtistID` tries UUID-parse first, falls back to a handle lookup. Downstream calls needing a genuine UUID (guest hold, media portfolio) must use the *resolved* value from the profile response, not the raw route param — a real regression happened here once (a child component's independent API call missed this).
- **Angular:** `withComponentInputBinding()` required for router-bound inputs. Load data in `ngOnInit`, not a constructor `effect()` — router inputs aren't guaranteed set yet at construction time (throws NG0950). `<lucide-icon>` requires `LucideAngularModule` in the component's own `imports` array, even though it's registered globally at the app level — forgetting this is a real, repeatable mistake made more than once this session.
- **Copy-paste discipline note:** several sessions have lost significant time to browser download-duplicate-filename issues (`file (2).go`, `files (14)/`). When re-downloading a corrected file with the same name as an earlier attempt, always verify with `grep -c "<unique string>" <path>` before trusting a `cp`.

---

---

## ⚠️ Spec-vs-actual gap analysis (new — read before planning next work)

A full pass comparing the locked spec docs (PRD v7 Final, Booking Domain Spec, Technical Decisions) against actual verified code found:

**1. Two of the project's own target docs disagree with each other.** `B-Edge-Product-Roadmap.docx` tags Product Store as Phase 3 and Waitlist as Phase 2. `B-Edge-PRD-v7-Final.docx` — later, longer, explicitly "reviewed against 3 AI models, 34 gaps resolved" — tags **both as Phase 1 launch requirements**. The actual build has followed the older Roadmap doc (both deferred). **Unresolved — needs an explicit decision on which doc is ground truth**, and the loser updated to match.

**2. A deliberate, undocumented deviation in the deposit-confirm flow.** PRD v7 §8.1 and the dedicated Booking Domain Spec both describe two separate artist taps: "Mark deposit received" (→ `deposit_paid` status) then a *separate* "Confirm booking" tap (→ `confirmed`). What's actually live is `ConfirmDepositReceived` — **one tap**, collapsing both steps atomically. This was a reasoned UX improvement made earlier in the build (fewer clicks for the artist's most common action), not an oversight — the old two-step endpoints still exist for edge cases (partial payment, disputed transfer). The docs were never updated to reflect it.

**3. Verified real gaps, all tagged Phase 1 in PRD v7 §14.1** (confirmed absent by direct code search, not assumption): Waitlist, Product Store (no `products`/`orders` tables), Rescheduling (single + bulk), Home-visit/outside-Lebanon booking types + reimbursable expenses, a genuine discount/promo-code system (only a raw `discount_amount` column exists — no redemption logic), SMS notification fallback (WhatsApp-only currently), Admin dashboard, Arabic RTL interface (confirmed zero app-level RTL code, only Angular's own library internals).

**4. Confirmed exact matches, verified against real code** — worth knowing what's *already right*, not just what's missing: multi-store travel buffer (150min weekday / 90min weekend, per-artist overrides — exact match to spec), early-bird cutoffs (9am Beirut / 7:30am Tripoli — exact match), slot holding via GIST exclusion, business hours exceptions.

**Full detail:** `B-Edge-Targets-vs-Actual-Analysis-v1.md`. Not yet cross-checked in that pass: the full 7-step slot algorithm (only the travel-buffer step verified so far), the 16 WhatsApp notification templates' exact wording, and a screen-by-screen diff against the 40-screen UI Spec.

---

## Domain status — 9 domains live, schema v13

Migrations 001–013 applied. `make test` green.

### `auth` (10 endpoints) — artist auth only
Register, login, refresh, forgot/reset password, freeze/unfreeze/delete account. JWT + httpOnly refresh cookie. **No customer auth domain exists** — deliberately parked, see below.

### `booking` — heavily extended this session
Full lifecycle: guest hold→submit, approve, deposit confirm (single-action `ConfirmDepositReceived` + legacy two-step pair for edge cases), cancel, complete, no-show. `CompleteBooking` now generates a review-link token and returns it directly. Notification enqueue wired at 4 transitions (see Notification below).

### `artist` — public handles added this session
Profile, stores, service catalogue, business hours + exceptions. `artists.handle` (migration 012) — nullable, unique, DB-CHECK-enforced format (`^[a-z0-9]([a-z0-9-]*[a-z0-9])?$`). Set via `PATCH /artists/:id`.

### `review` — extended this session with a guest flow
Standard authenticated CRUD/moderation (unchanged), **plus** a new no-login token-based flow: `GET/POST /reviews/by-token/:token`, registered outside the `RequireAuth()` group. `CreateReviewByToken` resolves identity from the token then delegates entirely to the existing `CreateReview` — no validation logic duplicated.

### `discovery` — search fixed this session
Public browse. `q` now matches name **or city** (was name-only, despite the UI promising both). Cards now include `handle`. Deliberately still has no price field — an artist's services can span too wide a range for one "starting price" number to be honest (documented in the Go model).

### `client` (CRM)
Aggregated list, notes, spend history. VIP flag stubbed `false` — rule undecided, parked.

### `earnings` — the single most timezone-bug-prone domain this session
Summary, daily/service breakdown, date-range picker. Fixed: `early_bird_cutoff`/business-hours UTC-literal bug (DST-unsafe), "today"/"this month" boundaries using raw UTC, explicit-range parsing disagreeing with the default-range branch, daily-breakdown SQL bucketing by UTC day instead of Beirut day, and `ThisMonth` silently aliasing whatever range was requested instead of the real current month.

### `media`
Cloudinary portfolio — confirmed genuinely wired, not a stub.

### `notification` — enqueue wired this session
Worker (Twilio WhatsApp REST, retry logic) was already fully built. The gap — nothing wrote to the `notifications` table — is now closed at 4 transitions: Approve (deposit-required vs. no-deposit-yet-not-confirmed, factually accurate either way), Confirm Deposit ("you're confirmed"), Cancel (**artist-initiated only** — a customer cancelling their own booking isn't notified about their own action), Complete (**auto-sends the review link**). Best-effort: a queuing failure never fails the booking operation that already succeeded (`Service` has no logger yet — a known, documented gap, not hidden).

**No domain exists for:** customer accounts, refunds.

---

## Frontend — Artist Dashboard (`artist-dashboard`)

Bookings, **Deposit Queue** (new — pending/received tabs, verify modal with optional transaction-ref note), **Calendar** (new — week nav, Beirut-correct hourly grid, detail slideout with real WhatsApp/Call links + "Send review request" manual stopgap), Clients, Earnings (rebuilt — This month/Last month/Last 3 months/Custom picker), Services, Hours, Profile+Portfolio, Login.

**Not built:** Block Dates, Onboarding (new-artist self-serve setup).

## Frontend — Customer PWA (`customer-pwa`)

**Discover** (new root route `/` — rebuilt to match the Stitch design: city-grouped 2-column grid, avatar-initial cards; deliberately no price row, no bottom tab bar — both need backend/features that don't exist), full guest booking funnel (Artist Profile → Select Service → Pick Date/Time → Guest Details → Confirmed), **Leave a Review** (new — `/review/:token`, no account, drops the mockup's non-functional "post publicly" checkbox).

**Not built:** Customer Login/Register/Reset, "My Bookings" — see Parked below.

---

## Real bugs fixed this session (worth knowing the root causes)

1. **Booking-domain ownership bug** — 6 endpoints compared `users.id` against `artists.id` directly, silently 403ing every real artist. Every other domain already had the resolve-first pattern.
2. **Timezone family** (the dominant theme) — see `earnings` and `Critical environment facts` above.
3. **Discovery search** — name-only despite promising city too.
4. **Handle regression** — `pick-datetime-screen`'s own independent slots call used the raw route param (could be a handle string) instead of the resolved UUID after handles shipped. Caught via live testing.
5. **Generic error messages** — guest-details form always showed "something went wrong" regardless of the actual validation reason. Fixed with a reusable `extractApiErrorMessage()` helper (`@bedge/shared`) — worth applying to other forms (Profile edit, Service create) as a quick follow-up, not yet done.
6. **Decimal comparison in tests** — `reflect.DeepEqual` vs `decimal.Equal()` giving false failures on numerically-identical values with different internal representations.

---

## Parked / deferred — deliberately, with reasoning

| Item | Why | Unblocked by |
|---|---|---|
| **Full customer accounts** | Large separate feature: new auth domain, guest-booking-merge decision, 3+ screens, a "My Bookings" screen with nothing to show without it | A dedicated scoping session |
| **Refunds** | No schema, no domain | Schema/domain design session |
| **VIP client flag** | Rule genuinely undecided | Edge's call |
| **`early_bird_fee` model** (flat vs. per-service/%) | Flagged, never revisited | Edge's call |
| **Bulk per-day availability endpoint** | Perf optimization, not correctness | Real traffic to justify it |
| **Mobile-width desktop wrapper** | Cosmetic testing convenience | Whenever it's annoying enough |

---

## Key live IDs (real DB)

- Rania: `artist_id = 378cd76e-6c75-4c63-9d38-6f8fa211f1e5`, `salon_id = 327ad1df-28dd-481a-b713-cca3bd1aaa51`, **handle = `rania`**, category=makeup.
- Stores: Beirut Downtown `24869c23-b5be-48d1-a22a-08fed461010c` (`early_bird_fee=$15`), Tripoli `135c6b9e-04fe-4822-8446-726bbb6c9e4a`.
- Services: `nails` (`9aa8cfe8-...`, $10, $0 deposit), `Bridal makeup` (`7787a7ce-...`, $200, $100 deposit).
- Login: `rania@bedge.com` / `password123`.

---

## Immediate next steps, in order

1. **Resolve the PRD-vs-Roadmap conflict** (Product Store, Waitlist phase assignment) — changes what "next" means for two whole feature areas
2. Sweep other forms for the same generic-error pattern `extractApiErrorMessage` just fixed on guest-details
3. Start a Twilio account — has its own external approval wait, worth kicking off regardless of when WhatsApp actually gets flipped on
4. Block Dates / Onboarding screens
5. `early_bird_cutoff`/fee, VIP rule — parked product decisions whenever ready
6. Customer accounts — own dedicated session
7. Once the phase conflict is resolved: scope whichever of Waitlist/Product Store/Rescheduling/Home-visit bookings/Discounts/SMS-fallback/Admin dashboard/Arabic RTL are actually launch-blocking

---

*B-Edge · Beauty at the Edge · August 7, 2026*
