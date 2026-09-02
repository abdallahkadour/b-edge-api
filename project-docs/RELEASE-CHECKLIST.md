# First-release checklist

> Written 2026-08-22, after a long live-testing effort covering every core
> flow (see `b-edge-web/project-docs/E2E-TEST-PLAN.md` for the full detail —
> this document is the summary view, not a replacement). Verified against
> real code and real live behavior throughout, not asserted from memory.
>
> **Updated 2026-08-31.** Four sprints of
> `B-Edge-Feature-Feasibility-Assessment-v1.md`'s plan shipped since. What
> that changes for release readiness is in the new section immediately
> below; the original assessment follows unchanged beneath it.

---

## Added since first writing (2026-08-30/31)

**Closed one thing this document did not list as a risk, because it did not
exist yet when this was written:** `internal/billing` shipped on Aug 29 with
**zero tests** — the only substantial domain without any, and the one
deciding who gets locked out. It now has 44 service-layer tests (83 across the billing surface as of 2026-09-02), with
`DeriveStatus` and `ensureInvoicesUpTo` at 100%. Subscriptions are the entire
revenue model in a market without card rails, so this was disproportionately
serious for its size.

**Changed a launch-relevant policy.** Subscription enforcement windows moved
from 7/21 days to **21/45** (decision D2, 2026-08-31). An artist is now
visible and bookable for three weeks after a missed payment, hidden and
unbookable from 21–45 days, and blocked from editing at 45+. The reasoning is
in `internal/pkg/subscription/status.go`'s `GraceDays` comment; the short
version is that every published dunning benchmark assumes an automatic card
retry, which B-Edge does not have, and that **no dunning reminder is sent at
all yet** — so the old timings punished artists for a missing feature of ours.

> ⚠️ **These numbers assume reminders do not exist. Revisit them once Twilio
> is live**, at which point 21 days is arguably generous.

**Shipped and verified live:** customer-facing open/closed status per store,
store map pins with one-tap directions, portfolio photos tagged to bookable
services with a customer-side filter, portfolio reordering, and crawlable
share-link previews (`GET /a/:handle`).

**New near-term blocker, in addition to WhatsApp.** Share previews work on
localhost and will silently do nothing in production until the reverse proxy
routes `/a/*` to the Go API rather than the static bundle. Nothing errors when
this is wrong — the preview simply stays blank. See
`B-Edge-Share-Previews-Decision-v1.md`.

**Two known-wrong billing behaviours are pinned by tests, not fixed**, because
both are product decisions rather than defects: unpaid months stack one
invoice each (contradicting the spec's claim of only ever one outstanding),
and month-end billing dates drift forward permanently because Go's `AddDate`
normalizes rather than clamps (Jan 31 → Mar 3). Anyone billed on the 29th–31st
is affected.

---

## Done, verified live this session

Each of these was actually driven through the real UI/API against the real
database, not just read as code — see `E2E-TEST-PLAN.md`'s update notes for
the specific reproduction of every bug found and fixed along the way (16
total across this effort).

- **Booking lifecycle** — hold → pending → approve → deposit → confirm →
  complete / no-show / cancel, including the cross-store travel-buffer
  differentiator, verified across 3 real regions (Beirut, Bekaa, Akkar).
  See `E2E-TEST-PLAN.md` Suites 1-3.
- **Product store** — browse, cart (including stock-cap enforcement under
  both rapid-click UI abuse and a direct over-limit API call), checkout
  with a real delivery pin, artist fulfillment through to delivered, order
  cancellation. See Suite 4.
- **Customer account** — phone/OTP login, booking/order history, sign-out
  and session restore. See Suite 5.
- **Client CRM** — client list membership, per-client history scoping, note
  persistence. See Suite 6.
- **Earnings** — revenue totals cross-checked against a manual DB sum. See
  Suite 7.
- **Admin approval flow** — pending-artist approve/reject, the real guarded
  SQL transition, tested against real onboarded test artists.
- **Self-service onboarding** — artist + salon + store + service creation,
  including a critical fix (the new artist was previously never linked to
  their own store — invisible on Discover and unbookable; fixed in
  `internal/onboarding/repository.go`).
- **Store management** — add a branch, edit/rename, activate/deactivate
  (including a self-lockout fix in `GetStoresBySalon` so deactivating a
  store never strands the artist without a way back to it).
- **Reviews** — guest review-token submission, hide/show, and an
  authorization gap closed (`GetReviewsByArtist` previously let any
  authenticated artist read another artist's full review list, including
  hidden reviews — now ownership-checked).
- **Concurrency** — the atomic guarded-`UPDATE` pattern used for every
  status transition was proven safe under 6 real concurrent
  approve-vs-cancel races on the same booking, not just assumed from
  reading the SQL.
- **Availability integrity** — abandoned holds and deposit-deadline-lapsed
  approvals no longer permanently block real slots; both self-heal via
  lazy expiry on the read path (`GetAvailableSlots`), since no background
  scheduler exists (see below).

## Deliberate, not gaps

These came up during testing and were confirmed as intentional design, not
things left unfinished:

- **Payment model**: OMT/Whish manual bank transfer, artist manually
  confirms receipt (optionally with a reference number). No payment
  gateway integration — confirmed as the accepted v1 model, not a TODO.
- **One salon per artist**: created once, implicitly, as part of
  onboarding (`internal/onboarding/repository.go`). No standalone "create a
  salon" screen, and no way to create a second one for an already-onboarded
  artist — matches the product's own data model.
- **`DELETE /reviews/:id` has no UI** — it's customer-owned (only the
  review's author or an admin may call it), and there's no "my reviews"
  surface on the customer side to hang a delete button off yet.
- **Client "VIP" badge** (`internal/client/model.go`'s `isVIP`) is a
  stubbed no-op — cosmetic, the actual rule (spend threshold? booking
  count?) was never decided. Deferred, not blocking.
- **No general-purpose background scheduler** exists (`ExpireDeadlineBookings`/
  `ReleaseExpiredHolds`/`ExpireStalePendingBookings` are all real,
  correct, but called lazily from read paths rather than a cron/ticker).
  This was a deliberate choice over standing up a scheduler, made twice
  this session for the specific cases that mattered (both now closed) —
  not a general architectural gap that needs closing before launch, but
  worth knowing about if a new case turns up.

## Needs its own review — not assessed by this effort

Everything above was verified against a local dev stack
(`localhost:3000`/`4200`/`4300`). None of the following has been touched or
checked this session, and nothing above should be read as implying they're
fine:

- **WhatsApp delivery is code-complete but not live** — see
  `WHATSAPP-SETUP.md` in this same directory. This is the one item from
  this list that's a genuine near-term blocker: without it, no OTP,
  booking confirmation, or review request actually reaches anyone.
- Production hosting/deployment (where the app actually runs, how it gets
  there — CI/CD, containers, whatever the real target is). **Critical
  deploy-time note verified by live testing (Aug 29, 2026):** after any code
  change the running binary must be replaced — pushing to git without
  restarting the Go process leaves the old binary active. Found during E2E
  billing testing: new middleware was compiled and committed but the running
  process was a prior build; the middleware silently did nothing until the
  server was restarted. A deploy checklist or Docker image rebuild+restart
  (ArgoCD rolling update, `kubectl rollout restart deployment/b-edge-api`)
  closes this completely — not a code problem, a process one.
- Domain + SSL/TLS
- Production secrets management (this session's `.env` is a local dev
  file; production JWT secrets, DB credentials, Cloudinary keys, and the
  new Twilio credentials all need a real secrets story, not a committed
  file)
- Database backups and disaster recovery
- Monitoring, alerting, and error tracking in production (this session
  relied on `docker exec bedge-postgres psql` and reading local log files —
  neither exists in a real deployed environment)
- Rate-limiting and abuse protection at the infrastructure layer (the app
  has some rate-limiting logic in code, e.g. OTP request limits — but
  nothing at the network/infra level has been reviewed)
- Load/performance testing under realistic concurrent traffic (the
  concurrency work this session proved *correctness* under a handful of
  simultaneous requests, not throughput or behavior under real load)
- Legal: terms of service, privacy policy, data retention policy — none
  reviewed or drafted as part of this engineering effort
