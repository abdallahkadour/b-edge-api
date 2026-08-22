# CLAUDE-v6.md — B-Edge Engineering Context

> Single source of truth for continuing the B-Edge build. Read this first in any new chat. Supersedes CLAUDE-v5.md and all earlier versions.
> Last updated: August 15, 2026, verified directly against code (migrations, routes, git log, package.json) in both repos, not against memory or prior docs.
> Schema v19 · 12 backend domains live (11 route-bearing + audit) · both frontend apps substantially built · design-system migration ongoing.

---

## Who & What

**B-Edge** — beauty booking + product marketplace SaaS for Lebanon / MENA ("Fresha for Lebanon"). Solo founder build.

- **Founder:** Edge (Abdallah Kadour). GitHub `abdallahkadour`.
- **AI partner:** Spark (Claude).
- **Launch artist:** Rania — verified, ~300K IG followers, two studios (Beirut Downtown + Tripoli). Makeup category. Public handle `rania` (`/book/rania` works identically to the UUID link).

**Backend repo:** `abdallahkadour/b-edge-api` (Go).
**Frontend repo:** `b-edge-web` — Angular 21 monorepo (`customer-pwa`, `artist-dashboard`, shared lib `@bedge/shared`).

---

## How Spark works with Edge (non-negotiable)

1. **No em dashes anywhere** — code, comments, UI copy. Use a period, comma, or colon instead.
2. **Delivery is complete drop-in files, never diffs or `sed` commands against templates** — a bad `sed` invocation against Angular control-flow syntax caused real damage earlier in this project.
3. **Never assume a struct/interface shape — read it first.** Several real bugs happened from writing code against an assumed field name or method signature instead of the actual one.
4. **Every new Lucide icon must be registered in `app.config.ts`, in both the import list and the `pick()` call.** A missing registration throws at runtime, not at compile time.
5. **`docker exec -i bedge-postgres psql -U postgres -d bedge -c "..."`** is how you query the dev database directly.
6. Ask for what's needed rather than guess. Delivery format: complete drop-in replacement files, not diffs/snippets.

---

## Stack & Environment

- **Backend:** Go **1.26.3** (bumped since v5's "1.22+"), Fiber v2.52.13, pgx v5.9.2/pgxpool, golang-migrate/migrate v4, golang-jwt/jwt v5.3.1, go-playground/validator v10, shopspring/decimal v1.4.0, zap, OpenTelemetry+Jaeger, swaggo/swag, google/uuid, testify.
- **DB:** PostgreSQL 15, Docker container `bedge-postgres`, db=`bedge`. Schema at **migration 019** (`artists.status`).
- **Frontend:** Angular **21.2.0** (standalone, Signals, `inject()`, `OnPush`), Angular CDK 21.2.14, Tailwind 3.4.19, Lucide Angular 1.0.0 (2px stroke, kebab-case names), TypeScript 5.9.2, Vitest 4.0.8, Node 22.
- **Media:** Cloudinary, signed uploads (`GET /media/signature`) — replaced the earlier unsigned-preset flow as part of the security audit.
- **Notifications:** Twilio WhatsApp — worker fully built (`internal/notification`), enqueue wired at 4 booking transitions. **No Twilio account exists yet** — queues as `pending`, will start sending the moment real credentials land, no code changes needed.
- **Infra:** K8s single-node, AWS EC2 t3.medium planned for launch. Not yet deployed to production.
- **Brand:** Inter font, ink `#0a0a0a`, success green `#16a34a`, no blue/gold, 390px mobile-first, enterprise/restrained.

**Makefile targets:** `run`, `dev` (air), `test`, `migrate`, `swagger`, `build`, `docker-up/down`, `lint`.

---

## Critical environment facts (save hours)

- **`bedge_test` DB does not exist.** All tests use in-memory `mockRepo` — `make test` needs no database.
- **`make dev` (air) is the real compile authority.** Spark's sandbox has no Go toolchain.
- **`deleted_at` columns exist ONLY on `users`, `bookings`, `salons`.** Never filter on it for `artists`, `stores`, `services`, `reviews`, `client_notes`, `products`, `orders` — causes a live SQLSTATE 42703.
- **Timezone convention:** store wall-clock LOCAL times as-is + an IANA zone string, never pre-convert to UTC and store that. `stores.timezone` defaults to `Asia/Beirut`. `cmd/main.go` blank-imports `time/tzdata` so this works regardless of container OS.
- **`bookings.artist_id` references `artists.id`, NOT `users.id`.** Every artist-action ownership check must resolve `user_id → artists.id` first via `GetArtistIDByUserID`.
- **A route param can be a UUID OR a handle** on `GET /artists/:id` and its siblings (`/services`, `/stores`). `Service.ResolveArtistID` tries UUID-parse first, falls back to handle lookup. Downstream calls needing a genuine UUID must use the *resolved* value, not the raw route param. **A third real instance found and fixed Aug 15** (caught by Edge manually clicking through the app, not by Claude): `reviews.page.ts` passed the raw `:artistId` route param straight to `GET /public/reviews/artist/:artist_id`, which — unlike `/artists/:id` and its siblings — has no handle-resolution fallback at all (`uuid.Parse` directly, 400s on anything else). Reaching Reviews via a handle URL (`/book/rania/reviews`, the normal path from an artist's profile) failed every time. Fixed by resolving the artist via `getArtistById` first and using its `.id` for the reviews call, same pattern as the other two known instances of this bug class (guest slot holds, media portfolio). Worth grep'ing for any other place that forwards `input.required<string>() /* route artistId */` straight into an endpoint without checking whether that endpoint actually resolves handles.
- **Angular:** `withComponentInputBinding()` required for router-bound inputs. Load data in `ngOnInit`, not a constructor `effect()` (router inputs aren't set yet at construction time, throws NG0950). `<lucide-icon>` requires `LucideAngularModule` in the component's own `imports` array even though it's registered globally.
- **`internal/admin`, `internal/audit`, `internal/notification` now have test coverage** (added Aug 15, after this doc's first pass). `admin`: full service-layer coverage including the `NOT_PENDING` conflict guard and audit-log content. `audit`: `NopRepository` plus `pgRepo.Log`'s JSON-marshal error paths (the only part of it testable without a real DB). `notification`: `buildMessageBody`, `resolveRecipientPhone`'s DB-free branches, and `sendWhatsApp` against a redirected `httptest` server. Note `notification` and `audit` still have real code paths (the actual Postgres reads/writes) that remain untested, same as every other domain's `pgRepo` — this codebase has no `bedge_test` DB.
- **`app.Group(prefix, middleware...)` middleware leaks across domain boundaries if `prefix` isn't scoped tightly — two real instances found + fixed Aug 15, full codebase audit done.** A Fiber Group's middleware is registered as an app-wide `Use()` bound to that prefix, and each route's middleware chain is frozen against whatever's in the global stack at the moment that route is registered — not scoped to routes created via that specific group object, and this bites even within a single file if a public route is registered on raw `app` *after* an authed group sharing its prefix is created.
  - **`product/handler.go`**: `authed := app.Group("/api/v1", auth)` (meant to cover just `/orders/*`, scoped to the bare API root instead). Since `product.RegisterRoutes` runs before `media.RegisterRoutes` in `cmd/main.go`, this silently 401'd media's public portfolio endpoint — confirmed even a nonexistent bogus path under `/api/v1` returned 401 instead of 404. Fixed by scoping the group to `/api/v1/orders`.
  - **`review/handler.go`**: `r := app.Group("/api/v1/reviews", auth)` created first, then the guest review-by-token routes (`/reviews/by-token/:token`, GET+POST) registered on raw `app` afterward *under the same prefix* — despite a comment right above them saying "deliberately OUTSIDE the RequireAuth() group." Same mechanism, pure within-file self-collision this time, no cross-domain interaction needed. This broke the entire guest review-submission flow (the link sent after a completed booking). Fixed by dropping the group-level middleware entirely and applying `auth` per-route instead, matching `artist/handler.go`'s and `domain/auth/handler.go`'s pattern.
  - **Audited every other `app.Group(prefix, authMiddleware...)` call in the codebase** (`client`, `admin`, `earnings`, `onboarding`) — each owns a prefix (`/api/v1/clients`, `/api/v1/admin`, `/api/v1/earnings`, `/api/v1/onboarding`) that no other domain shares anywhere in the codebase, confirmed by grep, so none are currently broken. `booking/handler.go` has two Group objects sharing `/api/v1/bookings` (one public, one authed) and is currently safe only because the public routes happen to be registered before the authed group is created — correct today, but fragile: a public route appended after the authed group would break silently, the same way review's did. `customerauth/handler.go` and `domain/auth/handler.go` already use the safe pattern (no middleware baked into the group, or none at all) and are good examples to copy for any new domain.
  - **The safe pattern going forward, per `artist/handler.go`'s own documented lesson**: never call `app.Group(prefix, authMiddleware...)` when *any* route under that prefix is meant to be public, ever, even a route registered later, even in the same file. Create the group with no middleware and apply `middleware.RequireAuth()` (and any role check) per-route inline instead.
  - Both bugs were caught only by actually calling the endpoints (curl and a real browser flow through the guest booking funnel), not by `go build`, `go test`, or code review — worth an actual request-level smoke test after touching any `RegisterRoutes` function, not just a compile check.
- **⚠️ A customer-auth OTP bypass exists, dev-only, added Aug 15.** `internal/customerauth/service.go`: submitting code `326321` for ANY phone number at `POST /customer-auth/verify-otp` issues a real session for that number, no real WhatsApp code needed — added so customer-auth-gated screens (My Bookings, My Orders) can be reached and tested without a live Twilio account. Gated on `os.Getenv("APP_ENV") == "development"`, fail-closed (same convention as the stack-trace gate in `internal/middleware/register.go`) — confirmed via test that `production`, `staging`, and an unset/empty `APP_ENV` all reject it as an ordinary wrong code. **Before this app ever deploys anywhere with `APP_ENV` unset or misspelled, this is a live universal customer-login bypass — verify `APP_ENV=production` is actually set in every real deployment, don't just trust the code gate exists.** Covered by 3 tests in `service_test.go` (`TestVerifyOTP_DevBypassCode_*`), including one that intentionally tries the wrong-`APP_ENV` cases.
- **Cart persistence: decided, shipping as-is (Aug 15).** `CartStore` (`cart.store.ts`, shipped Aug 11) persists `{productId, quantity}` pairs to `localStorage` and reconciles them against a freshly-fetched product list on load, dropping anything deactivated or repriced — survives a refresh or backgrounded tab on the same device. There is no `cart`/`carts` table and no cross-device sync: Shop/Cart checkout is guest-style (name + phone, no login), matching the booking funnel, and the OTP customer-auth flow isn't wired into it. **Decision: this is sufficient for launch** — gating Shop behind login for cross-device sync would be a real UX change, and a server-side cart keyed on a bare phone number with no auth would be a privacy smell (anyone could probe another customer's cart). Revisit only if real usage shows customers switching devices mid-cart.

---

## Domain status — 12 backend domains, schema v19

Migrations 001–019 applied, `make test` green. Route groups (all under `/api/v1`): `/auth`, `/customer-auth`, `/bookings`, `/artists/...` (+ `/artists/salon/...`), `/reviews` (+ `/reviews/by-token`, `/public/reviews`), `/clients`, `/discovery`, `/earnings`, `/salons/:salon_id/products` + `/orders`, `/media` (+ `/media/signature`), `/onboarding`, `/admin`, `/health`, `/swagger/*`.

### `auth` — artist auth
Register, login, refresh, forgot/reset password, freeze/unfreeze/delete account. JWT + httpOnly refresh cookie.

### `customerauth` — new since v5
Phone + WhatsApp OTP login for customers, fully separate from artist auth: `/api/v1/customer-auth/{request-otp,verify-otp,refresh,logout}`. Backed by `customer_otps` table (migration 015) and a dedupe-by-phone migration (014) that ran ahead of it.

### `booking`
Full lifecycle: guest hold→submit, approve, deposit confirm (single-action `ConfirmDepositReceived` + legacy two-step pair for edge cases), cancel, complete, no-show. `CompleteBooking` generates a review-link token directly. Notification enqueue wired at 4 transitions.

### `artist`
Profile, stores, service catalogue, business hours + exceptions, public handles (migration 012). New: `artists.status` (`pending`/`active`/`rejected`, migration 019) driving the onboarding gate.

### `onboarding` — new since v5
Self-service artist signup: `POST /onboarding/complete`, `GET /onboarding/status`. Lands an artist in `pending` status.

### `admin` — new since v5
`GET /admin/artists/pending`, `POST /admin/artists/:id/approve`, `POST /admin/artists/:id/reject`. Admin accounts are hard-capped at 2, created only via `cmd/seedadmin` (interactive, never via API — the register endpoint explicitly rejects `role=admin`). Full service-layer test coverage as of Aug 15.

### `product` — new since v5, resolves the PRD/Roadmap Phase conflict noted in v5
Full catalog + order lifecycle (migration 017: `products`, `orders`, `order_items`). Customer side: `POST /orders`, `GET /orders/me`, `GET /orders/:id`, `PATCH /orders/:id/cancel`. Artist side: `POST /products`, `PATCH /products/:id`, `GET /orders`, `PATCH /orders/:id/{confirm-payment,ship,deliver}`. State machine: `placed → confirmed → shipped → delivered`, plus `cancelled`/`returned`. No server-side cart — see Critical environment facts above; decided sufficient for launch. **Fixed Aug 15:** its authenticated-orders route group was scoped to the bare `/api/v1` prefix instead of `/api/v1/orders`, which silently required auth on media's public portfolio endpoint — see the `app.Group` entry in Critical environment facts above.

### `review`
Standard authenticated CRUD/moderation, plus a no-login token-based flow (`GET/POST /reviews/by-token/:token`, migration 013) and a public list (`GET /public/reviews/artist/:artist_id`, outside `RequireAuth()`). **Fixed Aug 15:** the by-token routes were silently 401ing (a `Group(prefix, auth)` self-collision, see the `app.Group` entry in Critical environment facts) — the guest review-submission link was completely broken until this was found and fixed.

### `discovery`
Public browse. `q` matches name or city. Cards include `handle`. Deliberately no price field.

### `client` (CRM)
Aggregated list, notes, spend history. VIP flag still stubbed `false`, rule still undecided.

### `earnings`
Summary, daily/service breakdown, date-range picker. Timezone bugs from earlier sessions fixed (see v5 for detail, not repeated here since nothing changed).

### `media`
Cloudinary portfolio, signed uploads. Its public `GET /media/portfolio/:artist_id` endpoint (used by the customer booking funnel to show an artist's photos) was silently 401ing every guest until Aug 15 — not its own bug, see `product`'s entry above and the `app.Group` note in Critical environment facts.

### `notification`
Worker (Twilio WhatsApp REST, retry logic) fully built, enqueue wired at Approve, Confirm Deposit, Cancel (artist-initiated only), Complete (auto-sends review link). No Twilio account yet. Unit tests cover the DB-free logic (`buildMessageBody`, `resolveRecipientPhone`'s early returns, `sendWhatsApp`); the DB-bound polling/claim/mark-sent/mark-failed paths remain untested, same as every domain's Postgres-touching code in this codebase.

### `audit`
Existed in schema since migration 001 but unused until recently. Writes to `audit_events`, called from exactly two places: `auth.Service` login (`service.go:208`) and `admin.Service` approve/reject (`service.go:46,72`). Reuse this path for new logging needs rather than building a parallel one. Unit tests cover `NopRepository` and `pgRepo.Log`'s JSON-marshal error paths; the actual INSERT is untested (no `bedge_test` DB).

**Still no domain for:** refunds.

---

## Frontend — Artist Dashboard (`artist-dashboard`)

Routes: `/login`, `/onboarding`, `/admin`, `/dashboard` (children: `bookings` [default], `clients`, `clients/:id`, `earnings`, `deposits`, `calendar`, `products`, `orders`, `waitlist`, `services`, `hours`, `profile`).

New since v5: **onboarding page**, **admin page** (pending-artist approval), **products/orders pages**, **waitlist page**. Dashboard layout gates a `pending` artist to `/dashboard/profile` only, deliberately, so they can upload portfolio photos while waiting for review (see `dashboard-layout.component.ts`).

Registers 27 Lucide icons in `app.config.ts`.

## Frontend — Customer PWA (`customer-pwa`)

Routes: `/` (discover), `/book/:artistId` (+ `/reviews`), `/review/:token`, `/login`, `/shop/:artistId` (+ `/cart`, `/confirmed/:orderId`), `/my-bookings` (+ `/:id`, guarded), `/my-orders` (guarded).

New since v5: **customer-login page** (phone + WhatsApp OTP, no password, two steps in one screen), **shop/cart/order-confirmed pages**, **my-orders page**. PWA install prompt with iOS fallback (`install-prompt.component.ts`, handles the iPad-reports-as-Mac case).

Registers ~22 Lucide icons in `app.config.ts`.

---

## Design system migration — status as of Aug 15

The shared UI kit (`projects/shared/src/lib/ui/`) is exactly 4 files: `bedge-button`, `bedge-badge`, `bedge-card`, `bedgeInput` directive, plus a barrel. Its own `index.ts` docstring: "deliberately four primitives, not a component library."

**Migrated to primitives:** all of artist-dashboard, including `profile` as of Aug 15 (see correction note below for why it was missed earlier the same day) — `admin`, `bookings`, `client-detail`, `deposit-queue`, `orders`, `products`, `waitlist`, `onboarding`, `login`, `hours`, `services`, `earnings`, `calendar`, `clients`, `profile`. customer-pwa: `my-orders` plus, as of Aug 15, targeted fixes across `customer-login`, `leave-review`, `my-bookings`, `booking-detail`, `shop/cart.page`, `shop/order-confirmed.page`, and `booking-funnel/screens/pick-datetime-screen` — see below, this is deliberately NOT the same as "fully migrated."

**`profile.component` was missed in the first migration pass (caught and fixed later the same day, Aug 15).** It still used `FormsModule`/`ngModel` and the legacy `bedge-input`/`bedge-btn-sm` CSS classes when this was first written — caught while fixing its `extractApiErrorMessage` handling. `client-detail` was already migrated before this session started (pre-existing), which is likely why `profile` got miscounted as done alongside it — they were both on the original 5-component `ngModel` list, but only one had actually been converted. Now converted to match: bio textarea uses `bedgeInput`, the Verified/Not verified pill uses `bedge-badge`, Save changes uses `bedge-button` (unconditionally full-width now, replacing a `w-full sm:w-auto` responsive split that no other primary submit button in the app has — every other one, Login/Onboarding/Leave-review/my-orders, is already always-full-width). The Instagram field's `@`-prefix wrapper stays hand-styled, matching the same prefixed-input pattern already established on Onboarding's handle field and the phone inputs elsewhere — `bedgeInput`'s own border would visually conflict with the wrapper's. Verified live: edited the bio, watched Save enable/disable correctly with `isDirty`, saved for real against the backend, got "Profile saved." back. Not every element on every screen uses a primitive — icon-only buttons (edit/delete pencils, the exception-delete X) deliberately stay native `<button>` since `bedge-button` has no icon-only variant, and `<select>` elements stay hand-styled Tailwind since neither `bedgeInput` nor any primitive covers `<select>`. Calendar's pixel-positioned appointment grid and its `<a>`-tag WhatsApp/Call/review-request links are also deliberately untouched — links need to stay real `<a>` tags for `href`/`target="_blank"` to work, and `bedge-button` only renders a `<button>`. The one primitive that did fit on Calendar (the status badge in the detail slideout) was migrated. Don't assume every visual element uses a primitive; do assume every *form* and every *labeled action button* on artist-dashboard does.

**customer-pwa is a fundamentally different case, and most of it should stay exactly as it is.** Discover, the booking funnel's per-screen bottom CTAs, artist-profile, and most of shop/cart use pixel-precise typography and radii matching a specific Stitch/"AI Studio reference build" mockup — `rounded` (4px) buttons in sentence case, or full-bleed no-radius bars, versus `bedge-button`'s fixed `rounded-lg` + forced uppercase on `md`/`lg` sizes. Forcing the primitive onto these would change the actual rendered design, not just the code, so on Aug 15 only elements that were **already** styled identically to a primitive (same radius, same casing, same disabled-opacity value) got converted — every one of these was verified pixel-for-pixel against the primitive's source before touching it, and confirmed visually afterward. What got migrated and why each one was a genuine match, not a forced one:
- `customer-login.page` — "Send code"/"Verify" buttons already used `rounded-lg` + uppercase + `h-[52px]`, an exact match to `bedge-button size="lg"`.
- `leave-review.page` — the comment textarea already used `rounded-lg`, matching `bedgeInput`. Its own submit button is full-bleed/no-radius (deliberately different from the funnel's inset CTAs) and was left alone.
- `my-bookings.page` — its status pill was pixel-identical to `bedge-badge`'s neutral tone. Switched to the *shared* `bookingStatusTone()`/`formatStatusLabel()` utilities (already used by artist-dashboard's `bookings`/`client-detail`/`waitlist`) instead of writing a new local mapping — this was a real DRY fix, not just a primitive swap, and had zero visual effect since the span already forced `uppercase` in CSS either way.
- `booking-detail.page` — same `formatStatusLabel()` swap (zero visual effect, same reasoning). The "Cancel booking" trigger button converted to `bedge-button variant="danger" size="sm"` to match the same pattern `my-orders.page` already established for order cancellation — this fixed a real drift (hardcoded `text-red-600`/`border-red-200` instead of the `danger`/`danger-light` tokens). The "Keep my booking" sheet button matched `variant="ghost"` exactly and converted; "Yes, cancel booking" (solid red fill) has no matching variant — all four are outline/flat, none do a solid destructive fill — so it was left custom rather than losing the danger signal.
- `shop/cart.page` — name input, delivery-notes textarea, and the "Place order" button (`disabled:opacity-40` — matches `bedge-button`'s exact disabled value, unlike the funnel's `opacity-60`) were exact matches. The empty-cart "Browse the shop" button converted to `variant="secondary" size="sm"`.
- `shop/order-confirmed.page` — "View my orders" matched `variant="secondary" size="lg"` exactly; "Back to shop" (no border/background at all) and not-found/booking-funnel's error-state CTAs (sentence case on a size that forces uppercase) did not match and were left alone.
- `booking-funnel/pick-datetime-screen` — the waitlist-signup modal's name input and "Join waitlist" submit button were exact matches (same `disabled:opacity-40` tell as cart's submit button).

**Discover, the artist-profile screen, the booking funnel's main step-to-step CTAs, and reviews.page were left entirely untouched** — checked individually, confirmed nothing on them matches a primitive without changing the rendered design. Don't read "customer-pwa: partial" as "someone didn't get to it" — most of what's left there is a deliberate mismatch, not an oversight.

**Reactive-vs-template forms inconsistency: resolved (Aug 15, 2026).** The codebase-wide count at the time of the decision: 5 components used `FormsModule`/`ngModel` (`hours`, `services`, `client-detail`, `profile`, `clients`), 1 used `ReactiveFormsModule` (`login` — the outlier), and customer-pwa used neither, binding signals directly via `[value]`/`(input)`. The already-migrated `onboarding.page.ts` (built after the UI kit existed) also uses plain signals with no Forms API at all, matching customer-pwa. That plain-signal pattern — no `FormsModule`, no `ReactiveFormsModule`, no `<form>`/`ngSubmit`, `(click)` + `(keydown.enter)` on the submit action — is the standard going forward. `login.component.ts` was converted to match (was the only Reactive Forms usage); `hours.component.ts` was converted from `ngModel` mutation-then-spread to explicit `updateRow()` calls, using the same pattern.

**A real bug surfaced during this migration, worth remembering:** converting `login.component.ts` off `ReactiveFormsModule` while leaving `<form (ngSubmit)="submit()">` in the template compiled cleanly but silently broke login — `ngSubmit` is an output of the `NgForm` directive (from `FormsModule`), not a native DOM event, so with neither Forms module imported it never fires and Angular does not error on the unmatched binding. The button's native `type="submit"` inside a real `<form>` then triggered an actual browser page reload on click instead of calling the component method. Caught only via an actual browser click, not by `ng build` or `tsc` — worth a headless-browser smoke test after any form-related refactor, not just a compile check. Fixed by dropping the `<form>` element entirely, matching the established `(click)` + `(keydown.enter)` pattern already used by `customer-login.page.ts`.

---

## Real bugs and gaps worth knowing about

1. **Booking-domain ownership bug** (from v5, historical): 6 endpoints once compared `users.id` against `artists.id` directly. Fixed; the resolve-first pattern (`GetArtistIDByUserID`) is now consistent across domains.
2. ~~`internal/admin`, `internal/audit`, `internal/notification` have zero test coverage.~~ **Closed Aug 15** — see domain entries above.
3. **`extractApiErrorMessage()` sweep: done on both flagged forms (Aug 15).** `services.component.ts`'s create/save-edit/deactivate handlers and `profile.component.ts`'s save-profile/remove-avatar/save-avatar-after-upload handlers all hardcoded a generic message regardless of what the backend said. All six now use `extractApiErrorMessage`, each verified live against the backend: a deliberately-invalid price (`"5abc"`, passes the client-side `canCreate()` check but fails the backend's decimal validator) now surfaces "Price must be a valid positive number"; a bio forced past its own `maxlength="500"` guard via direct DOM assignment now surfaces "Bio must be at most 500 characters". `profile.component`'s Cloudinary-upload failure path was deliberately left generic — `CloudinaryUploadService.upload()` throws a plain `Error`, not an `HttpErrorResponse`, so there's no backend envelope for the helper to read there.
4. **Guest booking funnel was silently broken for any artist with portfolio photos, found and fixed Aug 15.** A visit to `/book/:artistId` (customer-pwa) triggered a 401 from the public portfolio endpoint, which customer-pwa's global 401 interceptor treated as an expired session and redirected straight to `/login` — meaning a guest could not reach the booking funnel at all once the artist's portfolio call fired. Root cause was backend-side (see `app.Group` entry above), not the frontend interceptor, which behaved correctly given a 401 it had no way to know was wrong. Found via an actual headless-browser walkthrough of the funnel, not code review. Fixed and re-verified end-to-end: a full guest booking (service → date/time → guest details → submit) now completes and returns a real `pending` booking from the live API.
5. **Reviews screen was silently broken for every artist reached via a handle URL, found by Edge (manual click-through, not Claude) and fixed Aug 15.** `reviews.page.ts` passed the raw `:artistId` route param straight to `GET /public/reviews/artist/:artist_id`, which has no handle-resolution fallback (unlike `/artists/:id` and its two siblings) and 400s on anything that isn't a genuine UUID. The normal path into this screen — tapping the rating badge on an artist's profile reached via their handle (`/book/rania` → `/book/rania/reviews`) — hit this every time. Fixed by resolving the artist via `getArtistById` first and using its `.id`, matching the pattern `booking-funnel.page.ts` already uses correctly (resolve once, propagate `resolvedArtistId` to every child screen). Audited every other `artistId()` usage in customer-pwa afterward: everything else either calls a handle-safe endpoint (`getStoresByArtist`) or already receives a pre-resolved ID from a parent — this was the one remaining broken instance of the class.

---

## Spec-vs-actual: what's now resolved since v5

v5 flagged an unresolved conflict between `B-Edge-Product-Roadmap.docx` (Product Store = Phase 3, Waitlist = Phase 2) and `B-Edge-PRD-v7-Final.docx` (both Phase 1). **The build has since shipped both** (migrations 016, 017 and their full domains/screens), and PRD v7's phase assignment has been decided as the winner. **Resolved Aug 15, 2026** — `B-Edge-Product-Roadmap.docx` edited to reclassify both as MVP with an update note explaining the change.

Other v5 gap-analysis items (deposit-confirm one-tap deviation, slot-algorithm partial verification, WhatsApp template wording not cross-checked, screen-by-screen UI Spec diff not done) are unchanged, not re-verified this session — see `B-Edge-Targets-vs-Actual-Analysis-v1.md` for the original detail, which is otherwise still accurate.

**Newly built but not in any PRD gap list — now cross-checked (Aug 15, 2026):** customer OTP auth and self-service artist onboarding + admin approval. `B-Edge-PRD-v7-Final.docx` §3.3 didn't just omit the approval gate, it stated the opposite ("no approval required from B-Edge platform") — corrected in place with an update note. `B-Edge-UI-Spec-v2.md`'s C-11/C-12 specced the artist email/password API for customer Register/Login — corrected to the real phone+OTP flow, no separate Register screen. Neither doc was rewritten wholesale, both are otherwise still stale in ways not touched by this pass (PRD's staff-invite model, UI Spec's pre-Angular migration checklist).

---

## Parked / deferred — deliberately, with reasoning

| Item | Why | Unblocked by |
|---|---|---|
| **Refunds** | No schema, no domain | Schema/domain design session |
| **VIP client flag** | Rule genuinely undecided | Edge's call |
| **`early_bird_fee` model** (flat vs. per-service/%) | Flagged, never revisited | Edge's call |
| **Bulk per-day availability endpoint** | Perf optimization, not correctness | Real traffic to justify it |
| **Mobile-width desktop wrapper** | Cosmetic testing convenience | Whenever it's annoying enough |
| **Rejected-artist resubmit path** | `rejected` is currently terminal | Edge's call |

---

## Key live IDs (real DB)

- Rania: `artist_id = 378cd76e-6c75-4c63-9d38-6f8fa211f1e5`, `salon_id = 327ad1df-28dd-481a-b713-cca3bd1aaa51`, handle = `rania`, category=makeup.
- Stores: Beirut Downtown `24869c23-b5be-48d1-a22a-08fed461010c` (`early_bird_fee=$15`), Tripoli `135c6b9e-04fe-4822-8446-726bbb6c9e4a`.
- Services: `nails` (`9aa8cfe8-...`, $10, $0 deposit), `Bridal makeup` (`7787a7ce-...`, $200, $100 deposit).
- Login: `rania@bedge.com` / `password123`.

*(Not re-verified this session — carry over from v5 as likely-still-accurate, confirm before relying on them.)*

---

## Immediate next steps, in order

1. ~~Audit every `app.Group(prefix, authMiddleware...)` call for the same risk.~~ **Done Aug 15** — found and fixed a second live instance in `review/handler.go` (guest review-by-token routes), confirmed `client`/`admin`/`earnings`/`onboarding` are safe (unshared prefixes), and confirmed `booking/handler.go` is correct-but-fragile (safe only because its public routes are registered before its authed group). See Critical environment facts.
2. ~~`extractApiErrorMessage()` sweep on `services.component.ts` and `profile.component.ts`.~~ **Done Aug 15** — see Real bugs and gaps above.
3. ~~`profile.component` design-system migration.~~ **Done Aug 15** — artist-dashboard is now genuinely fully migrated, all 14 screens.
4. Start a Twilio account — external approval wait, worth kicking off regardless of timing.
5. Block Dates screen, rejected-artist resubmit path, VIP rule, `early_bird_fee` model — parked product decisions whenever ready.
6. If ever revisited: full screen-by-screen diff of `B-Edge-UI-Spec-v2.md` against actual routes, and PRD's staff-invite model (Manager/Receptionist roles) against the reality that only a single artist role exists today.
7. Product reviews and product "likes"/favorites — deliberately not built. See the design-audit remediation note below for the reasoning; revisit if the per-artist product catalogue grows past a handful of SKUs or repeat-browsing behavior shows up in real usage.

---

## Design-audit remediation (Aug 15, second pass)

A pasted UI/UX audit (structured, cited real files, but calibrated for an enterprise multi-tenant SaaS rather than a single-launch-artist MVP) prompted a verification pass — claims checked against actual code before acting, not accepted wholesale. Confirmed-real and fixed:

- **Disabled-button contrast, app-wide.** `disabled:opacity-40` in `button.component.ts` composited to ~2.8:1 against a white/gray-50 page — under WCAG's 3:1 floor for UI components, and this is used on every button in both apps. Replaced with solid `disabled:bg-gray-100 disabled:text-gray-500` (~4.85:1), applied once, affects every disabled button everywhere.
- **Row-action buttons competing with page-level actions.** `orders.component.html`'s per-order status buttons demoted from default `primary` to `variant="secondary"`. Found something the audit missed while verifying this: `bookings.component.html`'s row actions (`Approve`/`Mark complete`/`No show`) were never migrated to `bedge-button` at all — raw buttons, including a hardcoded `bg-green-600` bypassing the design-token system. Converted `Approve`→`secondary` and `No show`→`ghost`; deliberately left `Mark complete` hand-styled since `ButtonComponent` has no success-toned variant and forcing a genuinely positive action into an existing variant would either lose that meaning or require a real new variant — a design-system decision, not a row-button fix.
- **Container width inconsistency.** `orders`/`clients`/`bookings`/`earnings`/`waitlist`/`deposit-queue` had zero width constraint (the shared `dashboard-layout` `<main>` doesn't set one either); `hours`/`services`/`profile` self-constrain narrower for their form content. Added `max-w-4xl mx-auto` to the six unconstrained screens; left the form screens' existing (correctly narrower) widths alone rather than unifying everything to one width, which would make forms uncomfortably wide.
- **Micro-typography floor.** Every `text-[10px]`/`text-[11px]` in artist-dashboard (9 files: `dashboard-layout`, `calendar`, `products`, `deposit-queue`, `portfolio`, `waitlist`, `orders`, `admin`, `onboarding`) raised to `text-xs` (12px, Tailwind default, no custom override in this project's config) — structural labels and real content alike, verified visually afterward for overflow in the tighter contexts (deposit-queue rows, portfolio overlays) with none found.
- **Desktop visual density.** `clients` and `waitlist` (compact, uniform rows) converted to `grid grid-cols-1 lg:grid-cols-2`. `orders`/`bookings` deliberately left single-column — their cards carry line-item lists and multi-button action rows that would cramp in two columns; verified this holds by inspecting the actual card content, not assumed.
- **Product detail view.** Tapping a Shop product card body (not just its `+` button) now navigates to `/shop/:artistId/products/:productId`, a new screen (`product-detail.page.ts`). No new backend endpoint — resolves artist→salon→catalogue the same way `shop.page.ts` and `cart.page.ts` already do, then finds the product client-side, so a direct/bookmarked link works exactly like every other deep link in this app. This closes a real gap found earlier the same day: `Product.description` was captured on create and displayed nowhere on the customer side. Now shown on the detail screen. Verified live: card-body click navigates correctly, the `+` button's `stopPropagation()` correctly does NOT navigate, add-to-cart/stepper work identically to the grid.

**Declined, with reasoning, not silently skipped:** a full `<bedge-table>` data-grid rewrite (real future work once real data volume exists — not appropriate for a database with ~7 clients right now), a custom masked time-input to replace `<input type="time">` (genuinely new component work, not a quick fix), and the audit's deposits-empty-state icon claim (its code citation was wrong — it's a plain `✅` emoji, not a styled Lucide icon with a `text-[#16a34a]` class as described).

**Product reviews / "likes" for the Shop — considered, not built.** Reasoning: the app already has a full artist-review system (post-booking, guest-token, the actual trust signal that matters before a customer ever reaches the shop). A parallel product-review system needs real backend scope (a new domain/table, an order-completion trigger, its own guest-token flow) that doesn't pay for itself at the current catalogue size (5-7 SKUs per artist). A "like"/favorite feature has nowhere to surface (no "My Favorites" screen) and, without mandatory customer accounts, would either be local-only (low value, nothing to sync) or require making login mandatory for browsing (a real UX regression to the guest-first shop model). Revisit both once a real multi-artist catalogue or repeat-purchase behavior shows the need.

---

*B-Edge · Beauty at the Edge · August 15, 2026*
