# B-Edge — Security, UI & Enterprise-Readiness Assessment

> **Snapshot, not current state.** Figures below (domain counts, schema
> version, table counts) were true when written. Verified against code on
> 2026-09-02 the project has **32 migrations, 29 tables, 17 domains, 115
> route registrations, 570 tests**. Read this for its analysis, not its
> numbers; `CLAUDE.md` has the current figures.


> Purpose: a single reference for "where are we, and what would it take to go further" across three axes — security, UI/design quality, and enterprise-grade readiness. Written so a future session can pick up any one item without re-deriving the reasoning. This is a snapshot as of August 15, 2026; verify anything load-bearing against current code before acting on it, the same way this document itself was built by checking code, not memory.
>
> For day-to-day engineering context, read `CLAUDE-v6.md` first — this document is a focused synthesis of one slice of it (security + UI + "enterprise" gap analysis), not a replacement.

---

## Executive summary

B-Edge is production-functional for its actual scope: one launch artist, guest-first booking and shopping, no payment gateway (manual OMT/Whish transfer + artist confirmation). Two real, silent, production-impacting bugs were found and fixed this session by actually driving the app rather than reading code — both are documented in detail below because the *pattern* behind them (Fiber route-group middleware leaking across domain boundaries) is a class of bug, not a one-off, and needs to stay on the radar for every new domain added. The UI is genuinely polished for a consumer-facing beauty-booking product and was never "enterprise-grade" in the strict SaaS-data-tooling sense — that's discussed on its own terms below, including what it would actually take and why none of it is needed yet.

---

## 1. Security

### 1.1 What's actually been hardened (verified, not assumed)

- **Cloudinary uploads are signed**, not unsigned-preset. Backend issues a short-lived signature (`GET /media/signature`, authenticated, artist-only); the API secret never reaches the client. Closed a real exposure (unlimited anonymous uploads to the account) during an earlier security pass.
- **Cross-tenant IDOR**: every artist-scoped endpoint resolves `user_id → artists.id` via `GetArtistIDByUserID` before trusting a URL-supplied ID — `RequireRole("artist")` alone only proves *an* artist, never proves *that* artist owns the resource being accessed. This was a real, fixed bug class (6 booking endpoints originally) and is now the consistent pattern across domains.
- **JWT**: 15-minute access token, 7-day refresh token in an httpOnly cookie, rotation on every refresh, timing-safe comparisons, no plaintext secrets in the repo (`.env`-sourced, 64+ char minimum).
- **Admin accounts are structurally hard to create**: capped at 2, seeded only via an interactive `cmd/seedadmin` prompt, and the public `/auth/register` endpoint explicitly rejects `role=admin`. No API path to self-promote.
- **Audit logging exists and is wired**, narrowly and deliberately: admin logins, admin approve/reject decisions. Writes to `audit_events` (append-only, 7-year retention comment in the schema). Not wired for every domain — a considered scope choice, not an oversight, and the pattern (`internal/audit`) is ready to extend to other domains without new infrastructure.
- **Rate limiting**: 100 requests / 15 minutes per IP, globally applied via Fiber middleware.
- **CORS**: origin allowlist via `CLIENT_URL` env var, not wildcard.
- **Password handling**: bcrypt, never plaintext; email-enumeration-safe login responses (same message for wrong email vs wrong password).

### 1.2 Two real bugs found this session — both silent, both production-impacting

Both were found by actually clicking through the app as a guest would, not by reading code. Neither showed up in `go build`, `go test`, or a code review pass.

**Bug class: `app.Group(prefix, authMiddleware...)` leaks across domain registration order.** Fiber registers a Group's middleware as an app-wide `Use()` bound to that prefix; each route's middleware chain is frozen against whatever's in the global stack *at the moment that route is registered* — not scoped to routes created via that specific group object, and this bites even within a single file if a public route is registered after an authed group sharing its prefix.

- **Instance 1 — `product/handler.go`**: `authed := app.Group("/api/v1", auth)` was scoped to the bare API root instead of `/api/v1/orders`. Because `product.RegisterRoutes` runs before `media.RegisterRoutes` in `cmd/main.go`, this silently required auth on media's public portfolio endpoint — meaning any guest who reached an artist's profile with portfolio photos got 401'd on that call, which the frontend's session-expiry interceptor read as "your session expired" and redirected straight to `/login`, killing the guest booking funnel entirely for that artist. Fixed by scoping the group to `/api/v1/orders`.
- **Instance 2 — `review/handler.go`**: same shape, pure within-file self-collision. The guest review-by-token routes (`GET/POST /reviews/by-token/:token` — the link sent after a completed booking) were registered on raw `app` *after* an authed group sharing the `/api/v1/reviews` prefix, despite a comment directly above them saying "deliberately OUTSIDE the RequireAuth() group." The comment described the intent correctly; the mechanism didn't achieve it. This broke the entire guest review-submission flow.
- **Audit of every other instance of this pattern**, done after finding #2: `client`, `admin`, `earnings`, `onboarding` each own a prefix no other domain shares, confirmed safe by grep. `booking/handler.go` has two Group objects sharing a prefix and is *correct but fragile* — safe today only because its public routes happen to be registered before its authed group; a public route appended later would break silently, the same way review's did.
- **The safe pattern, established and now documented**: never call `app.Group(prefix, authMiddleware...)` when any route under that prefix is meant to be public, ever, even a route registered later, even in the same file. Create the group with no middleware and apply `middleware.RequireAuth()` per-route inline instead — `artist/handler.go` and `domain/auth/handler.go` already do this correctly and are the reference examples.

### 1.3 A deliberate backdoor exists — read this before any real deployment

`internal/customerauth/service.go` accepts a hardcoded bypass code (`326321`) for **any phone number** at `POST /customer-auth/verify-otp`, issuing a completely real session — added so customer-auth-gated screens could be tested without a live Twilio account. It is gated on `os.Getenv("APP_ENV") == "development"`, fail-closed (mirrors the stack-trace gate already in `internal/middleware/register.go`), and covered by tests proving `production`/`staging`/unset all reject it as an ordinary wrong code.

**This is only as safe as the deployment's environment configuration.** If `APP_ENV` is ever unset, empty, or misspelled in a real deployment, this becomes a live universal customer-login bypass. Before any production or staging deploy: confirm `APP_ENV=production` is actually set, not just assumed. This is the single highest-leverage security check on this list — everything else here is defense in depth; this one is a live door if missed.

### 1.4 Not audited — flagged, not fixed

- **CSRF protection**: no explicit CSRF token mechanism found. Cookie-based refresh tokens with `SameSite` behavior not verified in this pass. Worth a dedicated look before scaling past a single trusted frontend origin.
- **Security headers** (CSP, X-Frame-Options, HSTS, etc.): not verified whether Fiber's `helmet`-equivalent middleware is wired — CLAUDE.md's original plan mentioned "Helmet security headers" as a goal; not confirmed present in `internal/middleware/register.go` as of this pass.
- **Dependency vulnerability scanning**: no `go.sum`/`package-lock.json` audit performed this session — not confirmed clean, not confirmed vulnerable, just not checked.
- **Secrets management**: `.env`-based, standard for this stage; no secrets-manager integration, which is appropriate for current scale but worth revisiting before a real multi-environment deployment.
- **Twilio credentials**: don't exist yet (tracked separately as a launch blocker, not a security gap).

---

## 2. UI / Design quality

### 2.1 Where it's strong

- **Disciplined palette**: ink/white/gray plus exactly three functional colors (success/danger/warning), no default SaaS blue, no gradients. This is a deliberate constraint (`tailwind.config.js` comments confirm it), not an accident, and it reads as premium rather than sparse.
- **Real shared primitives**, not just visual sugar: `bedge-button`/`bedge-badge`/`bedge-card`/`bedgeInput` enforce identical radius, height, and color logic wherever used. Artist-dashboard is now fully migrated (all 14 screens); customer-pwa is migrated exactly where the shared primitives are a genuine style match, and deliberately left alone where the screen is pixel-matched to a specific reference design with its own conventions (see `CLAUDE-v6.md`'s design-system section for the full per-screen breakdown and reasoning).
- **The customer booking funnel is genuinely competitive** with the real comparison set (Fresha, Vagaro, GlossGenius) — confident restraint, real payment-expectation copy, large touch targets, low cognitive load per step.

### 2.2 Fixed this session

- **WCAG contrast on disabled buttons**: `disabled:opacity-40` on a solid ink button composited to ~2.8:1 against a white/gray-50 page, under the 3:1 floor for UI components. Replaced with solid `disabled:bg-gray-100 disabled:text-gray-500` (~4.85:1), one change in `button.component.ts`, fixes every disabled button in both apps.
- **Static ARIA labels** on repeated cart-action buttons (a screen reader heard "Add to cart" identically for every product in a grid, with no way to tell which). Made dynamic, product-name-specific.
- **Row-action buttons competing with page-level primary actions**: `orders.component.html`'s per-row buttons demoted to `variant="secondary"`. Found `bookings.component.html`'s row actions had never been migrated to the shared button system at all — raw buttons, one with a hardcoded `bg-green-600` bypassing the token system entirely.
- **Container width inconsistency**: six dashboard screens (`orders`, `clients`, `bookings`, `earnings`, `waitlist`, `deposit-queue`) had no width constraint at all; now `max-w-4xl mx-auto`. Form screens' existing (correctly narrower) widths left alone.
- **Micro-typography floor**: every `text-[10px]`/`text-[11px]` in artist-dashboard raised to `text-xs` (12px), across 9 files, verified for overflow afterward.
- **Desktop density**: `clients` and `waitlist` (uniform, compact rows) converted to a 2-column grid on wide viewports. `orders`/`bookings` deliberately left single-column — their cards carry line-item lists and multi-button rows that would cramp.
- **Product detail view**: closed a real gap where `Product.description` was captured on creation and displayed nowhere on the customer side. New screen, no new backend endpoint (resolves the same way `shop.page.ts` already does).

### 2.3 Known inconsistencies not yet fixed (found during this pass, low priority)

- **Error/success banner styling has drifted into two competing patterns**: some screens use the semantic tokens (`bg-danger-light text-danger-dark`, `bg-success-light text-success-dark`), others use raw Tailwind colors (`bg-red-50 border-red-200 text-red-700`, `bg-green-50 border-green-200`). Both render similarly but aren't literally identical, and only the semantic-token version actually uses the design system. The style guide (see §4) documents the semantic-token version as canonical; the raw-color instances are drift to clean up opportunistically, not urgently.

---

## 3. Enterprise-grade roadmap (deliberately not built now — reference for later)

None of this is a launch blocker. It's real, scoped, and here specifically so it doesn't need to be re-argued from scratch if B-Edge's shape changes (more artists per salon, higher booking/order volume, an internal ops team). Roughly ordered by when it would actually start to matter:

| Item | What it would take | Trigger to actually build it |
|---|---|---|
| **Dense data tables for Orders/Clients/Bookings** | A new `<bedge-table>` primitive (sortable columns, `th`/`td` structure), responsive fallback to the existing cards below `lg:` breakpoint. Real component work, not a class-name change. | A salon with a real team processing dozens of orders/bookings a day, where scrolling a card list genuinely costs time. Not justified at ~7 clients total. |
| **Bulk row actions** | Multi-select + batch "confirm payment" / "mark shipped" etc. Needs the table above as a prerequisite. | Same trigger as above — volume. |
| **Global toast/snackbar notification system** | A shared service + a fixed-position notification stack; currently successful saves show inline text (e.g. "Profile saved.") which can be missed. | Worth doing whenever convenient — cheaper than the table work, moderate value now. |
| **Custom masked time input** | Replace `<input type="time">` (Hours screen) with a real component supporting typed entry ("0900" → "09:00") and consistent cross-browser rendering. Genuine new form primitive, not a quick fix. | Whenever Hours-editing friction is reported, or proactively if artist onboarding volume increases. |
| **Keyboard-driven power-user navigation** | Global shortcuts, focus-visible rings (`focus-visible:ring-2 focus-visible:ring-offset-2` instead of the current subtle `focus:border-ink`), command-palette-style navigation. | Only relevant once there's a power user doing this all day — an ops/support role, not a solo artist. |
| **Audit-log UI** | A screen to actually browse `audit_events` (the data already exists and is written, just never displayed anywhere). | Whenever a second admin or a compliance need makes "who did what" a real question, not a hypothetical one. |
| **Role/permission management UI** | The PRD's originally-planned 5-level staff permission model (Owner/Manager/Receptionist/Artist/Assistant) has zero UI or backend today — only a single artist role exists. | Only when Rania (or another salon) actually hires staff — tracked already in `CLAUDE-v6.md` as a known, deliberately unbuilt gap. |

**Explicitly not recommended right now**: building any of the above speculatively. The house rule this whole project already follows — don't design for hypothetical future requirements — applies here exactly as it does to code. A data table for 7 rows is premature optimization with a real time cost against things that *are* blocking (Twilio, the parked product decisions in `CLAUDE-v6.md`).

---

## 4. Design guide

A living, rendered reference of every design token and shared primitive actually in use — `project-docs/style-guide.html`. Open it directly in a browser. It exists specifically so that adding a new screen means reusing what's shown there, not inventing a new font size, color, radius, or button style that isn't already one of the documented options. If a new need genuinely isn't covered by anything in the guide, that's a real design decision (add it deliberately, then add it to the guide) — not a default to improvise silently.

---

## 5. Feature completeness snapshot

12 backend domains live (schema v19): auth, customerauth, booking, artist, onboarding, admin, product, review, discovery, client, earnings, media, notification, audit. Full detail, including what's genuinely tested vs. not, lives in `CLAUDE-v6.md`'s domain-by-domain section — not duplicated here to avoid the two documents drifting out of sync.

**Deliberately parked, with reasoning already recorded in `CLAUDE-v6.md`**: Block Dates screen, artist rejected-status resubmit path, VIP client flag rule, `early_bird_fee` model, bulk per-day availability endpoint. **Newly decided this session**: cart stays client-side/local-only (no server persistence), product reviews and product "likes" are not being built now — both reasoned through explicitly in `CLAUDE-v6.md` rather than just marked "later" with no context.

---

## 6. What's actually next

1. Start a Twilio account — pure external lead time, unrelated to any of the above.
2. Make the calls on the parked product decisions (Block Dates, VIP rule, `early_bird_fee`, resubmit path) whenever ready — all four need Edge's judgment, not more engineering research.
3. Anything from the enterprise-grade table in §3, only if/when its trigger condition is actually met.

---

*B-Edge · Beauty at the Edge · August 15, 2026*
