# B-Edge — Documentation Index

> 56 documents on disk (54 in `b-edge-api/project-docs/` including this index + 2 in `b-edge-web/project-docs/`). Read `CLAUDE-v6.md` first in any new chat — it supersedes all earlier CLAUDE.md versions.
>
> **Last updated: August 31, 2026 — four sprints of the feasibility plan shipped.** Schema is now **v28** (migrations 026 USD-only, 027 store location, 028 media↔service tags). Two new leaf packages under `internal/pkg/`, one new domain (`internal/share`), and `internal/billing` is no longer untested — it was the only substantial domain with zero coverage and now has 59 service-layer tests. See "What shipped Aug 30–31" below before assuming anything in an older doc is current.
>
> Previously updated: August 30, 2026 · Billing UI and enforcement both shipped (`401db07`, `06e5e9b`, `0cd82f8`, `73c3cd8`); `B-Edge-Feature-Feasibility-Assessment-v1.md` added.
> Previously updated: August 29, 2026 · **Schema v22 + migrations 023–025 (`plans`, `subscriptions`, `invoices`)** · 13 backend domains live (12 route-bearing + audit), including the new `internal/billing` domain — full subscription billing backend (plans, subscriptions, invoices, admin confirm/void), not just the plan catalogue. All three billing UI screens are now also built and committed: `/pricing` (public), `/dashboard/billing` (artist), and the admin Billing/Plans/Artists tabs — see `B-Edge-Monetization-Implementation-Spec-v1.md`.
>
> **Schema version corrected Aug 29, 2026:** this header said v19 through the Aug 21/22 passes, but `db/migrations/` actually ends at `022_order_delivery_location`. Migrations 020 (`product_stock_quantity`), 021 (`product_photo_gallery`), and 022 (`order_delivery_location`) all landed without a doc pass. Only the version number here is corrected — the three migrations' contents have **not** been cross-checked against `B-Edge-PRD-v7-Final.docx`, `B-Edge-ERD.html`, or the UI spec, so treat product/order docs as potentially behind by those three changes.

---

## What shipped Aug 30 – Sep 1, 2026

Four sprints of `B-Edge-Feature-Feasibility-Assessment-v1.md`'s plan. Listed here
because several older docs describe the *previous* state and have not all been
rewritten — this is the diff to hold in mind while reading them.

| Area | What changed | Where to read |
|---|---|---|
| **Billing tests** | `internal/billing` went from 0 to 59 service-layer tests. `DeriveStatus` and `ensureInvoicesUpTo` at 100%. No DB test infrastructure exists, so repository tests remain deliberately out of scope. | `B-Edge-Monetization-Implementation-Spec-v1.md` |
| **Enforcement windows** | **Changed from 7/21 days to 21/45** (decision D2). Grace 0–21 (full access), past_due 21–45 (hidden + unbookable, still editable), suspended 45+ (also no writes). The policy now lives in one function, `subscription.Enforce`. | `internal/pkg/subscription/status.go` — the reasoning is in the `GraceDays` doc comment |
| **`internal/pkg/subscription`** | New leaf package holding the subscription state machine. Exists because `middleware` cannot import `billing` (import cycle) and had been hand-copying the threshold *and* re-implementing the derivation. | package doc comment |
| **`internal/pkg/openinghours`** | New leaf package resolving whether a store is open. Extracted from `GetAvailableSlots` step 1, which was the only place the algorithm existed. | package doc comment |
| **Open/closed status** | `discovery.StoreCard` gains `open_status`, `phone`, `latitude`, `longitude`. Derived per request, never stored. An unresolvable status reports `unknown` and renders **no badge**, deliberately not "Closed". | `E2E-TEST-PLAN.md` Suite 9 |
| **Store map pins** | Migration 027. `PATCH /artists/stores/:id` accepts coordinates; half a pin is rejected, and `clear_location` exists because COALESCE cannot express "remove". | `E2E-TEST-PLAN.md` Suite 9 |
| **Photo→service tags** | Migration 028 + `PUT /media/:id/services`. Portfolio photos tag to services; the customer gallery filters by them. | `E2E-TEST-PLAN.md` Suite 10 |
| **`internal/share`** | New domain. `GET /a/:handle` returns crawlable Open Graph tags for shared links — previously every shared link previewed as a bare URL. | `B-Edge-Share-Previews-Decision-v1.md` |
| **USD only** | Migration 026 pins currency at the database. Closes the question `B-Edge-Monetization-Implementation-Spec-v1.md` §11 left open. | migration header |
| **`internal/inbox`** | New domain: the in-app notification centre. Named `inbox`, not `notification`, because `internal/notification` already exists and is the *outbound* WhatsApp queue — two things called "notification" would be a standing invitation to wire the wrong one. Bundling is enforced by a partial unique index (migration 030), not application logic. | package doc comment |
| **Notification bell (frontend)** | **Sep 1, 2026.** `bedge-notification-bell` in the dashboard shell: badge, dropdown panel, mark-read / mark-all-read / dismiss, 60s polling that pauses on a hidden tab. Until this shipped the `inbox` backend had no reader — the feature existed only over curl. | `E2E-TEST-PLAN.md` Suite 13 |
| **`internal/pkg/bidi`** | **Sep 1, 2026.** New leaf package. `StripControls` was inlined in `internal/share`; a second caller appeared (`notification/worker.go` interpolates a customer-supplied name into a body another person reads), and a security rule in two copies is a rule that drifts. | package doc comment |

**Bidi spoofing, closed in two places.** Executing `E2E-TEST-PLAN.md` §2.5.1 found
that a U+202E in an artist bio survived into Open Graph tags, so a WhatsApp preview
could be made to read `Book now https://evil.com`. HTML escaping does not help — a
bidi override is not markup. Building the notification bell surfaced the same vector
on a second surface: `alertArtistOfDeadLetter` interpolates a **customer-supplied
name** into a body the **artist** reads. Both now go through `internal/pkg/bidi`.
U+200E/U+200F (directional *marks*) are deliberately KEPT — they are weak hints, not
overrides, and are what makes a phone number render correctly inside an Arabic
sentence.

**Two known-wrong things pinned by characterization tests, not fixed** — both are
product decisions, both live in `internal/billing`:

1. `ensureInvoicesUpTo`'s doc comment (and the monetization spec §12) claim an
   unpaid subscription never accumulates more than one outstanding invoice. **It
   does** — four unpaid months produce five invoices.
2. Go's `AddDate` normalizes rather than clamps, so a period starting Jan 31 rolls
   to **Mar 3**, not Feb 28. The billing day then shifts forward permanently.
   Anyone on the 29th–31st is affected.

---

## ⚠️ Before anything else

1. **The PRD-vs-Roadmap phase conflict is resolved (Aug 15, 2026).** `B-Edge-Product-Roadmap.docx` tagged Product Store as Phase 3 and Waitlist as Phase 2; `B-Edge-PRD-v7-Final.docx` tagged both Phase 1. Both have since shipped in full (migrations 016 and 017, full domains and screens on both frontend apps), so PRD v7's assignment wins — `B-Edge-Product-Roadmap.docx` has been edited to reclassify both as MVP with a note explaining why. That doc otherwise still reflects the original 2025 plan (email/password auth, SendGrid, 7-week timeline) rather than the actual build — not touched in this pass. See `CLAUDE-v6.md`.
2. **Two new backend features are now cross-checked against the specs (Aug 15, 2026).** Customer OTP auth (`internal/customerauth`) and self-service artist onboarding + admin approval gate (`internal/onboarding`, `internal/admin`) both shipped without a doc pass. `B-Edge-PRD-v7-Final.docx` §3.3 actually said the *opposite* of what got built ("no approval required") — corrected in place with an update note, not silently rewritten. `B-Edge-UI-Spec-v2.md`'s C-11/C-12 (customer Register/Login) specced the artist email/password API for customers — corrected to the real OTP flow. Neither doc was rewritten wholesale; both got targeted update notes plus inline corrections at the specific wrong sections. See `CLAUDE-v6.md`.
3. ~~**Two files referenced in earlier sessions still aren't on disk.**~~ **Corrected Aug 30, 2026 — both are on disk and always were.** `B-Edge-Angular-PWA-Architecture-v1.docx` and `B-Edge-Competitor-Analysis-v1.docx` are both present, dated May 31, 2026. This note appears to have been carried forward across several passes without re-checking. `B-Edge-Competitor-Analysis-v1.docx` in particular is substantial and current enough to be worth reading — full Fresha/DINGG/Zenoti feature inventories with per-platform strengths and weaknesses — and is now indexed properly under Competitor Intelligence. Both are now listed in their categories below.
4. **Cart persistence: decided.** Earlier chat context assumed server-side cart persistence was done; it isn't, and won't be. `CartStore` persists to `localStorage` with reconcile-against-fresh-data (survives refresh, same device). No `cart` table, no cross-device sync, checkout stays guest-style (name + phone, no login) — decided sufficient for launch on Aug 15 rather than gating Shop behind login or keying a cart off a bare phone number. See `CLAUDE-v6.md`.
5. **The guest booking funnel was silently broken until Aug 15.** `product/handler.go` scoped an authenticated-orders route group to the bare `/api/v1` prefix instead of `/api/v1/orders`; Fiber applied that auth requirement to every route registered afterward in `cmd/main.go`, which 401'd the public artist-portfolio endpoint and caused customer-pwa's session-expiry interceptor to bounce every guest straight to `/login` mid-funnel. Found via an actual browser walkthrough, not code review — fixed, and a full guest booking was re-verified end-to-end against the live API. See `CLAUDE-v6.md`'s Critical environment facts.

---

## Core Product

| File | Description |
|---|---|
| `B-Edge-PRD-v7-Final.docx` | Product requirements — every business rule, service catalogue, booking flow, deposit policy, notification events, product store, features roadmap. Claims "Final," reviewed against 3 AI models, 34 gaps resolved. Its Phase 1 assignment for Product Store/Waitlist is what actually shipped — see note above. §3.3 Artist Onboarding corrected Aug 15, 2026 — it previously said the opposite of the real approval-gated flow. Customer OTP auth also now noted (was undocumented). |
| `B-Edge-BRD.docx` | Business requirements — market context, revenue model, platform overview, customer and artist flows. |
| `B-Edge-Product-Roadmap.docx` | Phase 1→4 feature roadmap with timeline. Product Store and Waitlist reclassified to MVP on Aug 15, 2026 to match PRD v7's phasing (see note above). Otherwise still the original 2025 plan — auth method, notification channel, and timeline sections don't reflect the actual build; not touched in this pass. |
| `B-Edge-Booking-Scenarios.docx` | Complex booking edge cases pre-solved: multi-person, cross-city, home visit, outside Lebanon, processing gaps. |

---

## Technical Design

| File | Description |
|---|---|
| `B-Edge-Technical-Decisions-v1.docx` | 30 validated decisions, 11 bugs pre-solved, 7 migration rules — the engineering bible. |
| `B-Edge-HLD.docx` | High level design — system architecture, component responsibilities, key flows. |
| `B-Edge-LLD-v2-Go.docx` | Low level design (Go stack) — folder structure, handler/service/repository pattern. |
| `B-Edge-Booking-Domain-Spec-v1.docx` | Booking state machine, transitions, two-step hold→submit, deposit deadlines, cancellation policy. Describes a two-tap deposit-confirm flow that the actual build deliberately collapsed to one tap — see `B-Edge-Targets-vs-Actual-Analysis-v1.md`. |
| `B-Edge-Booking-Domain-Visual.html` | Booking state diagram — visual flowchart of all transitions. |
| `B-Edge-Backend-Reality-Check-v1.md` | Schema audit (June 2026) — what the DB actually had vs. what screens needed at that point. Historical. |
| `B-Edge-API-Reference-v1.docx` | Endpoint reference as of schema v8 (June 26) — stale; schema is now v19 (12 domains: auth, customerauth, booking, artist, onboarding, admin, product, review, discovery, client, earnings, media, notification, audit). Useful as a reference pattern, not current truth. |
| `B-Edge-API-Contract-v1.docx` | Go ↔ Angular contract — response envelope, HTTP status rules, error codes, pagination format. |
| `B-Edge-Auth-API-Docs.docx` | Artist auth endpoints — register, login, refresh, logout, forgot-password, reset-password, freeze, delete. Does not cover the newer `customer-auth` OTP endpoints. |
| `B-Edge-Diagrams.html` | Architecture diagrams, booking flow, notification flow, database ERD. |
| `B-Edge-ERD.html` | Database entity-relationship diagram. Predates migrations 014–019 (customer OTP, waitlist, products/orders, artist status) — check against real schema before trusting it. |
| `B-Edge-INFRA.docx` | Infrastructure design — EC2, Docker, Kubernetes, PostgreSQL, deployment topology. |
| `B-Edge-Bulk-Schedule-Operations-Spec-v1.md` | **New, Aug 31, 2026. Design only — not implemented.** Rania's two requested bulk actions: shift every booking on a day by N minutes, and cancel a whole day, each notifying customers over WhatsApp. **Read §1 before writing any multi-booking write, in this feature or another.** The `bookings` GIST exclusion constraint is not deferrable, and exclusion constraints are enforced row-by-row mid-statement — so shifting booking A into booking B's *old* slot fails with `23P01` even though the committed state would be valid. Verified against the real database, not reasoned about: a two-booking day fails on the obvious single `UPDATE`, and every ordering fails the same way. The fix (`DEFERRABLE INITIALLY IMMEDIATE` + `SET CONSTRAINTS ... DEFERRED` for that transaction only) is also verified, including that a genuine overlap is still caught at commit — the guarantee survives, only its timing moves. Also argues **against** the message queue the brief assumed: `notifications` already is a durable queue drained by a supervised worker, and enqueuing in the same transaction as the booking write avoids a dual-write problem that Redis or SQS would introduce. §4 covers edge cases a generic design misses, notably cross-store travel buffers. §8 lists six decisions needing Rania or the founder — the sharpest being what bulk cancellation does about **already-paid deposits**, since refunds are out-of-band bank transfers and the platform tracks no refund liability. |
| `B-Edge-Slot-Algorithm-Spec-v1.docx` | Full slot availability algorithm as Go pseudocode. Only the travel-buffer step has been cross-checked against real code so far (confirmed exact match) — the rest of the 7-step algorithm not yet verified. Read `B-Edge-Feature-Feasibility-Assessment-v1.md` §2.1/§2.3 before adding range durations, resource scheduling or split bookings to this algorithm — all four features modify the same calculation and adding them as incremental special cases is the documented failure mode. |
| `B-Edge-Angular-PWA-Architecture-v1.docx` | **Was wrongly listed as missing until Aug 30, 2026 — it is on disk and always was** (dated May 31, 2026). Angular PWA architecture from the pre-build design phase. Not yet cross-checked against the two apps as actually built, so treat as design intent rather than current truth. |
| `B-Edge-Share-Previews-Decision-v1.md` | **New, Aug 31, 2026.** Why shared artist links are rendered by a Go endpoint (`GET /a/:handle`, `internal/share`) rather than by Angular SSR. Records the verified problem — both PWAs are client-rendered, crawlers do not run JavaScript, and `customer-pwa/src/index.html` contained exactly three meta tags with **no `og:` or `twitter:` tag at all**, so every link an artist shared previewed as a bare URL on the channel that is B-Edge's primary distribution. Compares Angular SSR (a workspace-wide commitment with permanent build/deploy cost, adopted to serve two URL shapes to machines wanting four meta tags), a Go pre-render endpoint (chosen), and deploy-time static HTML (rejected: profiles change and a snapshot would serve stale previews with no way to invalidate). Also records the two conditions that should reopen the decision, and one thing it explicitly does not cover — **the reverse proxy must route `/a/*` to the API rather than the static bundle, which is where this silently fails in production while working perfectly on localhost.** |

---

## Frontend Design & Screens

| File | Description |
|---|---|
| `B-Edge-UI-Spec-v2.md` | Screen inventory (40 screens: 19 customer PWA, 21 artist dashboard) with API dependency map. C-11/C-12 (customer Register/Login) corrected Aug 15, 2026 to the real OTP flow — the rest not yet cross-checked screen-by-screen. Both frontend apps still have more real routes than this spec accounts for (admin, products, orders, waitlist, shop/cart). |
| `b-edge-8-missing-screens.md` | Stitch prompts — customer/artist password reset, booking detail, cancel modal, block dates. Block Dates is still the one not built. |
| `b-edge-12-missing-screens.md` | Stitch prompts — Discover, register/login, my bookings, leave a review, PWA install, Deposit Queue, Refund Queue, client CRM, earnings, portfolio. Onboarding is now built (was listed as not-built as of the last pass); Refund Queue still isn't. |
| `b-edge-remaining-screens.md` | Stitch prompts — error states, booking lookup, artist booking detail, onboarding step 1. |
| `style-guide.html` *(in `b-edge-web/project-docs/`, not `b-edge-api`)* | **New, Aug 15/16, 2026.** Self-contained, open-in-browser design guide — every color token, type size, and the 4 shared primitives (`bedge-button`, `bedge-badge`, `bedge-card`, `bedgeInput`) rendered live in their real variants, sourced directly from `tailwind.config.js` and `projects/shared/src/lib/ui/*` (not reinvented). Also documents recurring hand-rolled patterns (error/success banners, empty states, modals, filter pills) and flags the one known drift — raw Tailwind color banners vs. the correct semantic-token banners — with both shown side by side. Check this before adding any new UI element so existing tokens get reused instead of new ones invented. |

---

## Testing, Quality & Gap Analysis

| File | Description |
|---|---|
| `RELEASE-CHECKLIST.md` | **New, Aug 22, 2026.** The durable "what's remaining for first release" answer — three sections: what's done and verified live this session (with pointers into `E2E-TEST-PLAN.md`), what's deliberate design rather than a gap (payment model, one-salon-per-artist, VIP badge deferred, no scheduler), and what's genuinely unassessed by any engineering work so far (hosting, SSL, prod secrets, backups, monitoring, load testing, legal) — stated plainly as unassessed rather than implied fine. The one near-term blocker it flags: WhatsApp delivery, see `WHATSAPP-SETUP.md` under Infrastructure and Operations. |
| `E2E-TEST-PLAN.md` *(in `b-edge-web/project-docs/`, not `b-edge-api`)* | **Now 13 suites plus an adversarial hardening pass (§2.5), added Sep 1, 2026.** Suites 12-13 cover bulk schedule shift preview and the in-app notification centre. **§2.5 is the more important addition**: auditing this plan against its own standards found ZERO cases mentioning injection, fuzzing, overflow, idempotency, retry, timeout or Unicode — in a product positioned as Arabic-first with `name_ar` columns throughout. §2.5 adds Unicode/RTL/bidi, boundary-value tables, genuine concurrency races (N requests in flight, not sequences), fault injection, idempotency/replay, malformed-input fuzzing, and a full illegal-transition matrix for the booking state machine, and applies to EVERY suite rather than only the new ones. A status table at the top now separates live-executed suites (1-8) from written-but-unrun ones (9-13) and the never-run §2.5. **Suites 9-11 added Aug 31, 2026** (open/closed status + store map pins; portfolio-to-service tagging; share link previews) — written but **not yet human-executed**, unlike 1-8. Suite 8's setup SQL and boundary cases were corrected for the 21/45 enforcement windows; the old 7/21 fixtures would now silently test the wrong state. Suite 11 is the unusual one: it cannot be verified in a browser, because the whole feature exists for clients that do not run JavaScript — its only real acceptance test is pasting a link into WhatsApp. **Aug 22, 2026 — earlier closing session; all 7 suites live-executed, 12 real bugs found and fixed total.** UI-driven end-to-end test plan for a human tester — every real route in all 12 backend domains (~90 endpoints) mapped to the exact UI element that triggers it, 7 full Given/When/Then journeys, an exhaustive "click everything, try every boundary value" stress pass (the one deliberately-unexecuted piece, given its lower bug-yield against real journeys). Originally found 5 endpoints with no UI path at all; **all 5 are now closed**. Live execution across six passes found and fixed 12 real bugs: a registration flow bypassing admin review; a dead password-reset delivery path; a freeze/unfreeze endpoint 500ing on every call; an unreachable review "show" toggle; no cancel action anywhere in artist-dashboard; a product-photo-upload race; an over-restrictive Clients CRM filter; three related "nonsensical timing" bugs (approving/completing/no-showing a booking whose appointment time didn't make sense for that action); abandoned booking holds permanently blocking real availability (fixed with self-healing lazy expiry, no scheduler needed) — and, in the closing session, **the self-service onboarding flow never linking a new artist to their own store** (every self-onboarded artist was invisible and unbookable) and **`special_requests` never rendered anywhere in artist-dashboard** despite being fully present in the API (a customer's allergy/access note was invisible to the artist). The closing session also built a **permanent 14-account multi-persona test roster** (`user1`-`user10`, `mkup1`-`mkup4` — not cleaned up, meant to stay) spanning 5 regions including Bekaa and Akkar, verified the cross-region travel buffer and its interaction with cancellation, and proved the concurrency model (atomic guarded `UPDATE`, no `FOR UPDATE` needed) safe under 6 real concurrent approve-vs-cancel races. Two stale test-plan wording bugs also corrected. One deliberately-not-fixed gap noted: no ownership check on the artist review-list endpoint. Use this instead of writing test cases from scratch. |
| `B-Edge-Test-Strategy-v1.docx` | Unit vs integration tests, coverage targets, CI config. |
| `B-Edge-Targets-vs-Actual-Analysis-v1.md` | Verified gap analysis comparing PRD v7 / Booking Domain Spec / Technical Decisions against actual code as of Aug 7. Confirms exact-match wins (travel buffer, early-bird cutoffs), the deliberate deposit-confirm state-machine deviation, and the Phase-1 gaps that existed then. Two of those gaps (Waitlist, Product Store) have since closed — see `CLAUDE-v6.md` for what changed since this was written. Rescheduling, home-visit bookings, real discount system, SMS fallback, admin dashboard for ops (distinct from the new artist-approval admin), and Arabic RTL are still gaps as of Aug 15. |
| `CLAUDE-v6.md` | **CURRENT context document.** Supersedes v5. Verified directly against code on Aug 15: schema v19, 12 domains, both frontend apps' real routes, design-system migration status (artist-dashboard complete, customer-pwa partial by deliberate design). Test coverage for admin/audit/notification closed same day; cart-persistence decision made (ship as-is, no server cart); a real backend routing bug that silently broke guest booking was found and fixed. Read this first in any new chat. |
| `B-Edge-Security-Test-Plan-v1.md` | **New, Aug 31, 2026.** Threat matrix and executable penetration-test plan for the API border, auth boundaries and transaction workflows: 4 threat categories, 21 fraud/spam/abuse vectors, and **44 numbered test cases** (`AUTH-*`, `INJ-*`, `CLIENT-*`, `EDGE-*`, `FRAUD-*`, `SPAM-*`) with procedure, expected behaviour and remediation for each, plus severity SLAs and release-gate rules. **No test in it has been executed** — every row is a hypothesis for a human tester, and nothing in it asserts the system passes. Three things make it B-Edge-specific rather than a generic checklist: (1) **there is no edge layer** — no CDN, gateway or WAF, just one Go binary — so cache poisoning, request smuggling, TLS downgrade and subdomain takeover are marked ANTICIPATORY and would produce false passes if tested today; (2) **there is no payment gateway**, so card fraud and chargeback abuse are genuinely N/A and the real financial vector is fraud against the *human* who confirms an OMT transfer (`FRAUD-06`); (3) it names four things expected to fail before testing starts, so they are not double-counted as discoveries — chiefly that **no security-header middleware exists at all** (verified by grep: no CSP, HSTS, X-Frame-Options or nosniff anywhere). Flags `AUTH-08` as the single highest-value case: the dev OTP bypass (`326321`, accepts any phone) is separated from total customer-account compromise by one environment variable. Companion to the assessment below, which covers what is already hardened. |
| `B-Edge-Security-UI-Enterprise-Assessment-v1.md` | **New, Aug 15/16, 2026.** Cross-cutting assessment: security posture (what's hardened, the two Fiber route-group bugs found+fixed, the dev-only OTP bypass and its production risk, what's not yet audited — CSRF, security headers, dependency scanning), UI/design status and fixes, and a preserved **enterprise-grade remediation roadmap** (data tables, bulk actions, toast system, masked time input, keyboard nav, audit-log UI, role/permission UI) explicitly scoped as "not needed now, revisit at the listed trigger conditions" rather than re-argued from scratch later. Companion to `CLAUDE-v6.md`, not a replacement. |
| `CLAUDE-v5.md` | Superseded by v6. Accurate as of Aug 7 (9 domains, schema v13). Kept for the detailed root-cause writeups of bugs fixed in that session (booking ownership bug, timezone family, handle regression) which v6 doesn't repeat. |
| `CLAUDE-v4.md` | Superseded. Accurate as of June 26 (6 domains, schema v8). |
| `CLAUDE-v3.md`, `CLAUDE.md`, `CLAUDE__1_.md`, `CLAUDE__4_.md` | Older historical iterations, all superseded. `CLAUDE.md` in particular predates the Go rewrite even starting (describes a "coming soon" API with zero domains built) — don't read it for current state. Worth archiving out of the active docs folder at some point. |

---

## Infrastructure and Operations

| File | Description |
|---|---|
| `WHATSAPP-SETUP.md` | **New, Aug 22, 2026.** Runbook for the one concrete release blocker found this session: the WhatsApp delivery pipeline (`internal/notification/worker.go`) is fully built and already running as a background worker, but has no real Twilio account behind it — every OTP/booking/review notification queues and silently never sends. Covers getting Twilio + WhatsApp Business API access (has its own external approval timeline via Meta), where the 3 required env vars come from, and how to verify delivery is actually working via the real `notifications` table columns (`status`/`attempts`/`error_message`), not just a 200 response. Companion to `RELEASE-CHECKLIST.md`. |
| `B-Edge-DevOps-Infrastructure-v1.docx` | Infrastructure gaps + fixes, production checklist, disaster recovery scenarios, backup config, monitoring stack. |
| `B-Edge-Command-Reference.md` | Every command used across the whole project, organized by purpose (auth, migrations, booking lifecycle, services, handles, discovery, reviews, calendar, client CRM, direct DB queries, frontend build/serve, deployment pattern, diagnostics). Supersedes the original repo-setup-only `commands.txt` upload and overlaps with/extends `B-Edge-Session-Commands.md` below. |
| `B-Edge-WhatsApp-API-Templates-v1.docx` | Twilio vs Meta Cloud API comparison, Lebanon pricing, all 16 notification templates (EN + AR). Not yet checked whether the built messages match the specced wording. |
| `B-Edge-Rania-Onboarding-Runbook-v1.docx` | Pre-launch checklist, Rania account setup, go-live day protocol. |
| `B-Edge-Session-Commands.md` | Terminal command reference from the discovery+client-CRM session (June 2026) — narrower scope, folded into `B-Edge-Command-Reference.md` above. Kept for historical detail. |
| `README.md` | Repo-level README — setup instructions, Makefile commands, architecture overview. Stale on doc count and Go version; actual Go version is 1.26.3 per `go.mod` as of Aug 15. |

---

## Business and Market

| File | Description |
|---|---|
| `B-Edge-Monetization-Implementation-Spec-v1.md` | **New, Aug 29, 2026 — backend fully built same day.** The full "what do we need to actually charge money" spec, frontend and backend. As of Aug 29 the **entire `internal/billing` backend is real and live**: migrations `023_plans`/`024_subscriptions`/`025_invoices`, every artist endpoint (my subscription, my invoices, submit payment) and every admin endpoint (overview, confirmation queue, confirm, void, edit subscription), plus the public `/pricing` page in artist-dashboard. Verified two ways: a live browser screenshot of `/pricing` (Playwright, all 4 tiers + Growth's "Recommended" badge + `comped` correctly hidden), and a full backend walkthrough against the real dev DB — a backdated test subscription taken through trialing-equivalent→invoice generation→submit→admin confirm→period extension→status back to active, plus the edge cases (resubmitting, cross-artist ownership, re-confirming an already-paid invoice) all correctly rejected. ~~**What's NOT built: all three UI screens and enforcement.**~~ **Superseded Aug 30, 2026 — both now shipped.** The UI landed in `401db07` (public `/pricing`, artist `/dashboard/billing`, admin Billing/Plans/Artists tabs) and `06e5e9b` (subscription status banners in the dashboard shell); enforcement landed in `0cd82f8` (`RequireActiveSubscription` middleware wired onto the artist, product and media route groups), with two E2E-found bugs fixed in `73c3cd8` (artist-domain subscription filter, null invoice list). **One real gap remains: `internal/billing` is 1,889 lines with zero tests** — the only substantial domain in the codebase without any, and it violates this project's own "no feature is done without code + tests + swagger docs" rule in `CLAUDE-v6.md`. It was hand-verified against the dev DB, which is real, but doesn't survive a refactor. See the doc's top status banner and §12 for the exact built-vs-not split, phase by phase. Findings worth reading regardless of build status: (1) **`artists.status` must not be reused for billing** — migration 019 defines it as a terminal editorial/trust gate, so cycling it for payment state would make a late payer indistinguishable from a declined application and would let a payment silently bypass admin review; (2) **there is no marketing site** — `/pricing` was added to artist-dashboard rather than a new build target; (3) subscription status is **never stored, only derived from dates at read time** (`DeriveStatus`), matching this codebase's existing no-scheduler pattern — verified this actually works: a subscription's status flips from grace back to active immediately on payment confirmation with no job or delay involved; (4) `confirm` on an already-paid invoice returns a 409, not the silent no-op this doc originally described — a deliberate deviation, since a double-click is more likely than a real second payment and should be visible, not swallowed. §6.4 is the one to read before touching plan editing: grandfathering existing subscribers is the *default* and falls out of prices being snapshotted on `subscriptions`/`invoices` rather than joined from `plans`. |
| `B-Edge-Feature-Feasibility-Assessment-v1.md` | **New, Aug 30, 2026.** The "what should we build next, and what will it cost *us*" view — a proposed feature set (storefront/discovery, provider ergonomics, advanced calendar, payments) benchmarked against Fresha/Booksy/GlossGenius/Vagaro/Phorest/Zenoti, with each item scored against the **real codebase** rather than as a greenfield build. Contains a full feasibility matrix, three deep dives, a three-phase blueprint, and a strategic-risk section. Two findings worth knowing before reading anything else: (1) **there is no discount capability of any kind** — confirmed against real code, nothing in `services.price`/`deposit`/`products`/`invoices` supports a reduction; (2) **multi-merchant booth-renter payouts are blocked by geography, not complexity** — Stripe Connect (which Fresha/GlossGenius/Vagaro all build split payments on) does not operate in Lebanon, which is *why* the OMT/Whish manual flow exists. Card-on-file and automated cancellation-fee capture die on the same constraint, so all three are scoped as a boundary rather than a backlog. Also argues *against* variable duration ranges (no benchmark competitor ships them, and the reason is a real scheduling failure) in favour of a committed-duration-plus-releasable-buffer model. Flags that a discount engine makes the still-open USD-vs-LBP currency question in `B-Edge-Monetization-Implementation-Spec-v1.md` §11 materially worse. |
| `B-Edge-Pricing-Strategy-v1.docx` | Competitor pricing analysis, B-Edge pricing model. Predates the Aug 2026 subscription decision — the proposed tiers now live in `B-Edge-Monetization-Implementation-Spec-v1.md` §1 (still unfinalized in both). |
| `B-Edge-Lebanese-Market-GTM-v1.docx` | Market sizing, GTM phases. |

---

## Competitor Intelligence

| File | Description |
|---|---|
| `B-Edge-Competitor-Architecture-v1.docx` | Code structure, deployment architecture, engineering practices per competitor. |
| `B-Edge-Competitor-Problems-v1.docx` | Documented bugs across competitors from verified reviews/complaints. |
| `B-Edge-Competitor-Flows-v1.docx` | Booking flows and user journeys competitors have built. |
| `B-Edge-Competitor-Implementation-v1.docx` | Implementation regrets and limitations per competitor. |
| `B-Edge-Competitor-Technical-v1.docx` | Confirmed schema/stack details per competitor. |
| `B-Edge-Competitor-Failures-v1.docx` | 8 failed competitors and the failure patterns behind them. |
| `B-Edge-Competitor-Analysis-v1.docx` | **Was wrongly listed as missing until Aug 30, 2026 — it is on disk and always was.** The primary feature inventory: full Fresha, DINGG and Zenoti breakdowns (booking/scheduling, payments/POS, client management, marketing, operations/staff) with per-platform pricing, explicit "their strengths B-Edge must match or beat" and "weaknesses vs B-Edge" lists. The source for the competitor columns in `B-Edge-Feature-Feasibility-Assessment-v1.md`. Note its Arabic-support finding in particular: it scores DINGG as having *the best Arabic support of any competitor* and Fresha as partial — while B-Edge currently has **none** (verified Aug 30: zero Arabic strings, no i18n framework, one `dir="auto"` in the whole frontend), despite `B-Edge-Pricing-Strategy-v1.docx` positioning B-Edge as "the first Arabic-first platform for Lebanon." |

---

## Development Reference

| File | Description |
|---|---|
| `DOCUMENTATION.md` | This file. |

---

## Summary by use case

**Starting a new chat about B-Edge?**
1. Read `CLAUDE-v6.md` (current context, 12 domains, both frontend apps, verified against real code Aug 15)
2. Read `B-Edge-Targets-vs-Actual-Analysis-v1.md` for the deliberate spec deviations and the gaps that are still genuinely open (rescheduling, home-visit, discounts, SMS fallback, ops admin dashboard, Arabic RTL)

**Building a frontend screen?**
1. Check `B-Edge-UI-Spec-v2.md` and the three Stitch-prompt docs for the design
2. Cross-check against `CLAUDE-v6.md`'s route lists and design-system migration status — many screens are already built, and some already use the shared `bedge-*` primitives

**Deciding what to build next?**
1. `B-Edge-Feature-Feasibility-Assessment-v1.md` — start here. Every candidate feature scored against the real codebase, with a three-phase blueprint. Its Phase 1 is deliberately all things where the hard part already exists (Open/Closed status, maps routing, service templates, portfolio-tagged-to-services) — cheap wins that read as major features.
2. `B-Edge-Targets-vs-Actual-Analysis-v1.md` for the still-open Phase 1 gaps
3. `CLAUDE-v6.md`'s "Immediate next steps" — the Product Roadmap edit and the PRD/UI-Spec doc-sync pass are the most time-sensitive items right now

**Touching subscription enforcement — who gets hidden, blocked or locked out?**
1. `internal/pkg/subscription/status.go` is the source of truth, not any doc. `Derive` computes the state; `Enforce` says what that state permits. Both are in one leaf package precisely so the three enforcement points (middleware, booking, discovery/artist SQL) cannot drift.
2. The `GraceDays` doc comment carries the **D2 reasoning** for why the windows are 21/45 rather than the 7/21 they started at — chiefly that B-Edge cannot auto-charge, so card-dunning timings punish latency in our own manual collection loop, and that no dunning reminder is sent yet.
3. **Revisit the numbers once Twilio is live.** 21 days is generous precisely because nobody is currently being told they owe anything.

**Writing anything that updates MORE THAN ONE booking at once?**
1. `B-Edge-Bulk-Schedule-Operations-Spec-v1.md` §1 — non-negotiable reading. The GIST exclusion constraint is enforced per row, so any multi-booking time change fails with `23P01` mid-statement even when the final state is valid. This is not theoretical; it is reproduced against the real schema in that section.
2. The fix is a deferrable constraint scoped to the one transaction that opts in — existing single-booking writes keep failing fast exactly as today.

**Doing a security pass, or asked "is this safe"?**
1. `B-Edge-Security-Test-Plan-v1.md` — the 44 test cases, ordered by yield. Start with the AUTH-* pass: that is where both prior security passes found real bugs, and four domains have shipped since the last one.
2. `B-Edge-Security-UI-Enterprise-Assessment-v1.md` — what is already hardened, and the two bug *classes* (Fiber route-group leakage, cross-tenant IDOR) that recur with every new domain. Read §1.2 before testing; a regression is likelier than a novel finding.
3. Note §3.6 of the test plan: four cases are **expected to fail** before anyone starts, chiefly the total absence of security headers. Fixing those first makes the pass more informative.

**Benchmarking against competitors, or asked "why don't we have X"?**
1. `B-Edge-Competitor-Analysis-v1.docx` — the feature inventory (Fresha/DINGG/Zenoti, with their strengths and their weaknesses vs B-Edge)
2. `B-Edge-Feature-Feasibility-Assessment-v1.md` — what closing any given gap would actually cost, and which gaps are *not worth closing*. Read its §0 first: discounts genuinely don't exist, and the payments gaps are blocked by Lebanon's lack of card rails rather than by engineering capacity, so they belong outside the roadmap rather than at the bottom of it.
3. The six `B-Edge-Competitor-*.docx` files for deeper per-competitor detail (architecture, failures, flows, implementation regrets, documented bugs, technical stack)

**Need a specific command?**
1. `B-Edge-Command-Reference.md` — organized by purpose (auth, booking lifecycle, services, handles, discovery, reviews, calendar, DB queries, frontend build/serve, diagnostics), not raw chronological order

**Running a QA pass or writing test cases?**
1. `E2E-TEST-PLAN.md` (in `b-edge-web/project-docs/`) — the API→UI coverage map and the 7 end-to-end journeys are the starting point, not a fresh test plan
2. All 7 suites have been live-executed at least once as of Aug 22, 2026, and its original 5 "no UI trigger" gaps are all closed — check its top update notes for current status, not §4, before assuming something's untested

**Checking what's left before first release?**
1. `RELEASE-CHECKLIST.md` — done-and-verified vs. deliberate-not-a-gap vs. genuinely-unassessed, in one place
2. If it's the WhatsApp item: `WHATSAPP-SETUP.md` has the exact runbook

**Working on monetization / charging artists?**
1. `B-Edge-Monetization-Implementation-Spec-v1.md` — the whole thing in one place (data model, API, enforcement, screens, payment flow, legal, build order). Check its top status banner first: as of Aug 29, 2026 the **entire backend** (plans, subscriptions, invoices, every artist and admin endpoint) is live and verified against the real dev DB; only the UI screens and enforcement are not.
2. Read its §3 before touching anything — reusing `artists.status` for billing state is the trap it exists to prevent
3. §12 has the exact phase-by-phase built-vs-not split if you're picking up where this left off — the next real step is the UI screens (`/dashboard/billing`, admin Billing/Plans/Artists tabs), not more backend
4. `WHATSAPP-SETUP.md` is a hard dependency for billing reminders, and Meta's approval has an external timeline — start it before the code is ready

---

## File statistics

> **Recounted from disk Aug 30, 2026.** The previous totals had drifted — they
> assumed two files were missing that were actually present, and the per-category
> figures no longer summed to the real file count. The numbers below are a direct
> `ls` of both folders, not an increment of the last figure.

| Category | Count |
|---|---|
| Core Product | 4 |
| Technical Design | 15 (B-Edge-Share-Previews-Decision-v1.md, B-Edge-Bulk-Schedule-Operations-Spec-v1.md added) |
| Frontend Design | 5 (style-guide.html lives in b-edge-web) |
| Testing, Quality & Gap Analysis | 10 (B-Edge-Security-Test-Plan-v1.md added; E2E-TEST-PLAN.md lives in b-edge-web; the CLAUDE-v* history is one row covering 7 files) |
| Infrastructure & Ops | 7 |
| Business & Market | 4 |
| Competitor Intelligence | 7 |
| Development Reference | 1 |
| **TOTAL ON DISK** | **56 files — 54 in `b-edge-api/project-docs/` (incl. this index) + 2 in `b-edge-web/project-docs/`** |

*Category counts are by index row, not by file: the CLAUDE-v* row covers 7 historical
files (`CLAUDE.md`, `CLAUDE (1).md`, `CLAUDE (5).md`, `CLAUDE-v3` through `-v6`), which
is why the categories sum to fewer than 53. The index also refers to two of those by
names that don't match disk — `CLAUDE__1_.md` / `CLAUDE__4_.md` vs. the real
`CLAUDE (1).md` / `CLAUDE (5).md`. Left as-is rather than renamed, since they're
superseded historical files, but worth knowing if you go looking for them.*

---

*B-Edge · Beauty at the Edge · الجمال عند الحافة · August 16, 2026*
