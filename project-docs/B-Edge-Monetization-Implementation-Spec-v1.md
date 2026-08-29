# B-Edge — Monetization Implementation Spec v1

> **Status: the entire backend is live (Aug 29, 2026); only the artist and
> admin UI screens remain.** Migrations 023–025 (`plans`, `subscriptions`,
> `invoices`) are applied to the local dev DB, and the full `internal/billing`
> domain is built and running: plan catalogue (public + admin), an artist's
> own subscription and invoice history, submit-payment, and every admin
> action (overview, confirmation queue, confirm, void, edit subscription).
> Verified two ways: a live browser screenshot of the public `/pricing` page
> at `http://localhost:4300/pricing`, and a full backend walkthrough against
> the real dev DB exercising lazy invoice generation, derived status
> (trialing→active→grace→past_due→suspended), submit→confirm, ownership
> checks, and idempotency — all passed, including the subtle ones (no
> duplicate invoice for an already-current period, re-confirming an
> already-paid invoice correctly rejected). **What's NOT built: any of the
> three UI screens** (`/dashboard/billing` for artists, the admin Billing/
> Plans/Artists tabs), enforcement (nothing reads subscription status yet -
> Discover doesn't hide past_due artists, nothing blocks writes), and
> notifications. Section 12 tracks phase-by-phase status in detail.
>
> Scope: everything needed to charge artists money and know who has paid —
> backend data model, API, enforcement, the artist-facing screens, the admin
> console, the manual payment-confirmation flow, and the non-code work (legal,
> Twilio, OMT account) that gating money behind a login makes non-optional.
>
> Grounded against real code: schema at migration **022** when this doc was
> first written, now **025** after this session's own migrations. 12
> route-bearing backend domains at the time of the original research pass
> (now 13, with `internal/billing`), 3 Angular projects. Note the
> documentation index (`DOCUMENTATION.md`) has since been corrected to match.

---

## 1. The decision this implements

No payment gateway exists, and none is planned for v1. Booking deposits flow
artist-to-customer directly via OMT/Whish and never touch B-Edge — which means
**commission-per-booking is not enforceable**. There is no point in the platform
taking a cut of money it never holds and cannot verify.

So revenue comes from a **subscription**: a flat monthly SaaS fee per
artist/salon, billed by B-Edge, collected manually via OMT/Whish, and confirmed
by an admin. It does not depend on artists self-reporting cash.

The consequence that drives this entire spec: **B-Edge has to build its own
accounts-receivable system.** With Stripe you get invoices, dunning, retries,
receipts, and state transitions for free. With OMT you get a person checking a
bank app. Everything Stripe would have done has to exist as a table, a screen,
or a documented human step.

### Proposed pricing (NOT finalized — see §11)

| Tier | Price/month | Fits |
|---|---|---|
| Starter | $7–10 | New/independent artists, low volume |
| Growth | $15 | Solo bridal/event artists, higher avg. ticket |
| Studio (2–5 staff) | $25–35, or ~$6–7/seat | Small salons |
| Multi-location | $50–60, or per-seat at scale | Rania's tier |

Benchmarks: Fresha $19.95/mo solo (+20% marketplace commission), Booksy $29.99/mo
+ $20/seat, Vagaro ~$24–30/mo + ~$10/seat. B-Edge launches below all three —
Lebanon's purchasing power doesn't support US pricing when a basic blowout is
~$10 and threading is $2–4.

**Rania stays free for the first 6–12 months.** She is the launch partner and
the case study; monetizing the reference account is worth less than the
reference. The system still needs to *represent* her — a comped plan, not a
missing subscription row, so she doesn't look identical to a delinquent account
on the admin screen.

---

## 2. What already exists to build on

This is not greenfield in the ways that matter. Four existing pieces carry most
of the weight:

**The manual-payment-confirmation pattern is already proven in production code.**
Migration `011_bookings_deposit_reference.up.sql` added `bookings.deposit_reference`
— a free-text OMT/Whish transaction code the artist enters when confirming a
customer's deposit, backing the Deposit Queue screen at
`/dashboard/deposits`. **Subscription billing is the same flow with the roles
shifted up one level**: instead of customer→artist with the artist confirming,
it's artist→B-Edge with an admin confirming. Reuse the shape; don't invent a new
one.

**Admin plumbing exists but is minimal.** `internal/admin/handler.go:38-41` is
the entire admin surface:

```
g := app.Group("/api/v1/admin", middleware.RequireAuth(), middleware.RequireRole("admin"))
g.Get("/artists/pending",    handler.ListPending)
g.Post("/artists/:id/approve", handler.Approve)
g.Post("/artists/:id/reject",  handler.Reject)
```

`middleware.RequireAuth()` and `middleware.RequireRole("admin")` already work,
and admins are seeded via `cmd/seedadmin` rather than registered. Billing routes
extend this group. The frontend already has a matching top-level `/admin` route
in artist-dashboard guarded by `authGuard(['admin'])`.

**The notification pipeline is code-complete and reusable.** The `notifications`
table (`001_initial_schema.up.sql:305-320`) has `template_name`, `channel`
(whatsapp/sms/email), `status` (pending/sent/failed/dead), `payload` JSONB,
`attempts`, and retry/error columns — and critically **`booking_id` is
nullable**, so billing notifications insert cleanly with no booking attached and
no schema change. `internal/notification/worker.go` already drains it.

**Money already has a type convention.** `internal/earnings/model.go` uses
`shopspring/decimal` for all currency. Billing must match — never `float64` for
money.

### Two constraints that shape the design

**There is no background scheduler, deliberately.** Per `RELEASE-CHECKLIST.md`:
`ExpireDeadlineBookings`, `ReleaseExpiredHolds`, and `ExpireStalePendingBookings`
are all real and correct but are **called lazily from read paths**, not from a
cron or ticker. This was chosen twice, on purpose, over standing up a scheduler.
Billing must follow the same pattern or it becomes the first thing in the
codebase that needs infrastructure nothing else needs. §6.3 explains how.

**WhatsApp delivery is not live.** The worker runs, but there is no funded Twilio
account behind it, so every message queues and silently never sends
(`WHATSAPP-SETUP.md`). Billing reminders inherit this blocker completely — an
invoice notification that never arrives is worse than none, because the system
will show it as sent-to-artist while the artist heard nothing. **Twilio must be
live before the first real invoice goes out.**

---

## 3. The one thing not to do: overload `artists.status`

Earlier planning notes suggested tying plan/seat tracking to `artists.status`.
**Don't.** Migration `019_artist_onboarding.up.sql` defines it as:

```sql
status VARCHAR(20) NOT NULL DEFAULT 'active'
  CHECK (status IN ('pending', 'active', 'rejected'))
```

with the column comment: *"pending = self-service signup awaiting admin review…
active = reviewed and live. rejected = admin declined the application; kept for
audit, not deleted."*

That is an **editorial/trust gate** — did a human review this application. Billing
state is **orthogonal**: an artist can be fully approved and simply not have paid
this month. Collapsing them breaks in three concrete ways:

1. Suspending for non-payment would write `rejected`, which the schema and the
   admin queue both define as "we declined your application." Unrecoverable
   semantic collision — you can no longer tell a scammer from a late payer.
2. Marking an invoice paid would write `active`, silently approving an artist
   who may never have been reviewed. **A payment must never bypass the trust
   gate.**
3. `rejected` is documented as terminal and audit-preserving. Billing states are
   inherently cyclical (lapse → pay → active → lapse). Cycling a terminal column
   destroys the audit trail migration 019 exists to protect.

Keep `artists.status` exactly as-is. Subscription state lives in its own table,
and the two are ANDed at enforcement time: **an artist is bookable only if
`artists.status = 'active'` AND their subscription is in good standing.**

---

## 4. Data model

Two new migrations. Money records are append-mostly: invoices are never
retroactively edited, only transitioned.

### Migration 024 — `subscriptions`

One row per artist, holding current plan and seat count.

```sql
CREATE TABLE IF NOT EXISTS subscriptions (
  id                 UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  artist_id          UUID NOT NULL UNIQUE REFERENCES artists(id) ON DELETE CASCADE,

  plan_code          VARCHAR(30)  NOT NULL
                     CHECK (plan_code IN ('starter','growth','studio','multi','comped')),
  seats              INTEGER      NOT NULL DEFAULT 1 CHECK (seats >= 1),
  monthly_price      NUMERIC(10,2) NOT NULL CHECK (monthly_price >= 0),
  currency           VARCHAR(3)   NOT NULL DEFAULT 'USD',

  -- Lifecycle dates. These are the source of truth; status is DERIVED
  -- from them at read time (see §6.3) rather than stored and flipped by
  -- a scheduler that does not exist.
  trial_ends_at      TIMESTAMPTZ,
  current_period_end TIMESTAMPTZ,

  -- Set only by an explicit admin action, never by the passage of time.
  cancelled_at       TIMESTAMPTZ,

  created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_subscriptions_period_end
  ON subscriptions (current_period_end);
```

`UNIQUE` on `artist_id` mirrors the `artists_user_id_unique` constraint migration
019 added for the same reason: application-level idempotency checks need a
database constraint behind them, or there is always a race window between "does
this artist already have a subscription" and inserting one.

`plan_code = 'comped'` with `monthly_price = 0` is how Rania is represented —
present and visibly intentional on the admin screen, not a missing row that looks
like a data bug.

**No `status` column.** That is deliberate, and §6.3 explains why.

### Migration 025 — `invoices`

```sql
CREATE TABLE IF NOT EXISTS invoices (
  id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  subscription_id UUID NOT NULL REFERENCES subscriptions(id) ON DELETE CASCADE,
  artist_id       UUID NOT NULL REFERENCES artists(id),

  invoice_number  SERIAL UNIQUE,          -- human-quotable in WhatsApp
  period_start    DATE NOT NULL,
  period_end      DATE NOT NULL,
  due_date        DATE NOT NULL,

  -- Snapshot at issue time. Never recomputed from the subscription: if the
  -- artist changes plan or seat count later, an already-issued invoice must
  -- still say what was actually owed for that period.
  amount          NUMERIC(10,2) NOT NULL CHECK (amount >= 0),
  currency        VARCHAR(3) NOT NULL DEFAULT 'USD',
  seats_billed    INTEGER NOT NULL,
  plan_code       VARCHAR(30) NOT NULL,

  status          VARCHAR(25) NOT NULL DEFAULT 'issued'
                  CHECK (status IN ('issued','submitted','paid','void')),

  -- Artist-supplied, mirroring bookings.deposit_reference (migration 011):
  -- free text, not validated, an OMT/Whish transaction code or a note.
  payment_reference VARCHAR(255),
  submitted_at      TIMESTAMPTZ,

  -- Who confirmed the money actually arrived. Never NULL when status='paid'.
  confirmed_by      UUID REFERENCES users(id),
  paid_at           TIMESTAMPTZ,

  void_reason       TEXT,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_invoices_artist  ON invoices (artist_id);
CREATE INDEX IF NOT EXISTS idx_invoices_status  ON invoices (status);
CREATE UNIQUE INDEX IF NOT EXISTS idx_invoices_period
  ON invoices (subscription_id, period_start);
```

`idx_invoices_period` is the load-bearing constraint: it makes lazy invoice
generation (§6.3) safe to call from any read path, any number of times, from
concurrent requests, without ever double-billing. Two simultaneous requests both
deciding "this month's invoice is missing" is not a hypothetical — it is the
normal case when an artist and an admin load their dashboards at the same time.
The second insert fails on the unique index instead of charging twice.

**Invoice status transitions** (only these; no other edge is legal):

```
issued ──[artist submits reference]──> submitted ──[admin confirms]──> paid
   │                                        │
   └──────────[admin voids]────────────────┴────> void
```

`paid` is terminal. Correcting a mistaken `paid` means voiding and issuing a new
invoice, not editing the old one — the audit trail is the point.

### Migration 023 — `plans` ✅ Built Aug 29, 2026

Added because prices must be **editable from the admin UI without a deploy**
(decided Aug 29, 2026 — the driving scenario is "solo artist starts at $7, gets
raised to $10 later"). A Go constant would mean a code change, a build, and a
deploy every time a price moves.

Numbered 023 rather than after `subscriptions`/`invoices` (024/025 in this
doc's original draft) because it was built first and has no FK dependency on
either — `plans.code` is a label, never a foreign key (see below), so build
order was free to put the catalogue ahead of the tables that will reference it
by name. Live at `db/migrations/023_plans.up.sql`, seeded with the 5 tiers from
§1, backing `internal/billing`.

```sql
CREATE TABLE IF NOT EXISTS plans (
  code           VARCHAR(30) PRIMARY KEY,       -- 'starter','growth','studio','multi','comped'
  name           VARCHAR(80)  NOT NULL,
  monthly_price  NUMERIC(10,2) NOT NULL CHECK (monthly_price >= 0),
  currency       VARCHAR(3)   NOT NULL DEFAULT 'USD',
  seat_price     NUMERIC(10,2) NOT NULL DEFAULT 0 CHECK (seat_price >= 0),
  included_seats INTEGER      NOT NULL DEFAULT 1 CHECK (included_seats >= 1),
  description    TEXT,
  features       JSONB,          -- bullet list rendered on the pricing page
  is_public      BOOLEAN      NOT NULL DEFAULT TRUE,  -- 'comped' is FALSE
  sort_order     INTEGER      NOT NULL DEFAULT 0,
  updated_at     TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);
```

**`subscriptions` and `invoices` must NOT foreign-key their prices to this
table.** Both already carry their own `monthly_price` / `amount` as snapshots,
and that must stay. If invoice amounts were read through a join to `plans`,
editing Starter from $7 to $10 would retroactively rewrite the value of every
invoice ever issued at $7 — including ones already marked `paid`. That is an
accounting corruption bug, not a pricing feature. `plans.code` is referenced by
`subscriptions.plan_code` for *labelling*; the money is always local.

---

## 5. Backend: new `internal/billing` domain

Follows the established handler/service/repository/model layout used by all 12
existing domains. Money mutations get `audit` entries — the `internal/audit`
domain already exists and "who marked what as paid" is exactly what it is for.

**Built (Aug 29, 2026): the entire package** — `Plan`, `Subscription`,
`Invoice`, `Repository`, `Service`, `Handler` in `internal/billing/`, wired
into `cmd/main.go`, migrated and verified against the local dev DB (not just
plan seed data — a full trialing→invoice→submit→confirm walkthrough with a
real backdated subscription, see §12). `confirm`/`void`/subscription-update
call `internal/audit`; plan create/edit deliberately still does not — plan
create/edit isn't "money changing hands" the way confirming an invoice is.

**Every endpoint in both tables below is now built and verified against the
real dev DB** (see §12's Phase 1/2 for the exact scenarios tested). What's
missing is the UI that calls them, not the endpoints themselves.

### Artist-facing (`RequireAuth`, `RequireRole("artist","admin")`)

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/billing/subscription` | ✅ **Built.** Current plan, seats, derived status, trial/period end |
| `GET` | `/api/v1/billing/invoices` | ✅ **Built.** This artist's invoice history |
| `POST` | `/api/v1/billing/invoices/:id/submit` | ✅ **Built.** Submit OMT/Whish reference → `submitted` |
| `GET` | `/api/v1/billing/plans` | ✅ **Built.** Public. Plan catalogue for the pricing page |

`POST .../submit` verifies the invoice belongs to the calling artist -
implemented and tested: a non-owning caller gets the same 404 as a genuinely
missing invoice ID, not a 403, so probing invoice IDs that aren't theirs
learns nothing. Note the known precedent here: `E2E-TEST-PLAN.md` records a
deliberately-unfixed missing ownership check on the artist review-list
endpoint. That was tolerable for review data. **It is not tolerable on a
billing endpoint**, and this one has it.

### Admin-facing (extends the existing `/api/v1/admin` group)

| Method | Path | Purpose |
|---|---|---|
| `GET` | `/api/v1/admin/billing/overview` | ✅ **Built.** Every artist × plan × derived status × amount outstanding |
| `GET` | `/api/v1/admin/billing/invoices?status=submitted` | ✅ **Built.** The confirmation work queue |
| `POST` | `/api/v1/admin/billing/invoices/:id/confirm` | ✅ **Built.** Money arrived → `paid`, extend period |
| `POST` | `/api/v1/admin/billing/invoices/:id/void` | ✅ **Built.** Write off / correct |
| `PATCH` | `/api/v1/admin/billing/subscriptions/:id` | ✅ **Built.** Change plan, seats, comp an account, cancel |
| `GET` | `/api/v1/admin/plans` | ✅ **Built.** Full catalogue including non-public plans |
| `POST` | `/api/v1/admin/plans` | ✅ **Built.** Create a tier |
| `PATCH` | `/api/v1/admin/plans/:code` | ✅ **Built.** Edit price/name/features. Affects new signups only — see §6.4 |
| `POST` | `/api/v1/admin/plans/:code/apply-to-existing` | Not built. Deliberate, audited bulk re-price of current subscribers — see §6.4. Deferred to Phase 3 (§12) since until there are paying artists on a plan, there is nothing to re-price. |

`confirm` is the single most consequential endpoint in the system: it takes
money as real and extends service. It is admin-only, audited with the acting
user ID, and idempotent in the sense that matters — **implemented as a 409
conflict on re-confirmation, not a silent no-op** as originally described
here. That's a deliberate deviation from this doc's original wording: a
second confirm attempt on an already-paid invoice is far more likely to be
an admin double-clicking or retrying after a network hiccup than a real
second payment, and an explicit error surfaces that clearly instead of
silently swallowing it - verified directly: re-confirming returns
`INVOICE_NOT_SUBMITTED` and the period is not extended twice.

---

## 6. The three hard problems

### 6.1 Enforcement — what actually happens when someone doesn't pay

The temptation is a hard lockout. **Don't do that**, for a reason specific to
this product: an artist with unpaid dues may have confirmed bookings with
customers who paid real deposits. Locking the artist out punishes the customer,
who did nothing wrong, and generates exactly the kind of "B-Edge cancelled my
wedding makeup" story that kills a launch in a market this small and this
word-of-mouth driven.

Graduated enforcement instead:

| State | Artist dashboard | Discover / new bookings | Existing bookings |
|---|---|---|---|
| `trialing` | Full | Visible | Honored |
| `active` | Full | Visible | Honored |
| `grace` (0–7 days overdue) | Full + warning banner | Visible | Honored |
| `past_due` (8–21 days) | Full + urgent banner | **Hidden** | Honored |
| `suspended` (22+ days) | Read-only + banner | Hidden | Honored, no new ones |

Hiding from Discover at `past_due` is the real lever: it stops *future* revenue
without breaking *existing* commitments. It is reversible the instant an admin
confirms payment, and it is invisible to the artist's existing customers.

**The enforcement points already exist and are precisely two lines.**
`internal/discovery/repository.go:64` builds its filter as:

```go
conds := []string{"s.is_active = TRUE", "u.deleted_at IS NULL", "a.status = 'active'"}
```

and line 149 has the matching `AND a.status = 'active'`. Both need the
subscription condition ANDed in. Getting only one of them is the bug to watch
for — an artist hidden from search but still reachable by direct profile link, or
vice versa.

For write-blocking at `suspended`, add `middleware.RequireActiveSubscription()`
composed alongside the existing `RequireAuth()`/`RequireRole()`, applied to
mutating artist routes only. Read routes stay open so a suspended artist can
still see their calendar and their outstanding invoice — someone who cannot see
what they owe cannot pay it.

### 6.2 Seats

`subscriptions.seats` is the allowance; the count of staff artists under the
salon is the usage. Enforce at the point of adding a staff artist: if usage would
exceed the allowance, return a clear "upgrade to add another artist" error rather
than silently allowing it and reconciling later.

Do **not** auto-charge for overage. With manual OMT collection there is no
mechanism to actually capture an unexpected mid-month charge, and an invoice the
artist didn't agree to in advance is a support conversation, not revenue.

### 6.3 No scheduler — derive status, generate invoices lazily

This is the piece most likely to be got wrong, because every SaaS billing tutorial
assumes a cron.

**Subscription status is a pure function of dates, not a stored column.** That's
why `subscriptions` has no `status` field. Compute it on read:

```go
// DeriveStatus returns the current billing state from dates alone.
// There is no stored status column and no job that flips one - the value
// is always correct the moment it is read, including for an artist who
// has not logged in for three months.
func DeriveStatus(s Subscription, now time.Time) Status {
    switch {
    case s.CancelledAt != nil:                      return Cancelled
    case s.PlanCode == "comped":                    return Active
    case s.TrialEndsAt != nil && now.Before(*s.TrialEndsAt): return Trialing
    case s.CurrentPeriodEnd == nil:                 return PastDue
    case now.Before(*s.CurrentPeriodEnd):           return Active
    case now.Before(s.CurrentPeriodEnd.AddDate(0, 0, 7)):  return Grace
    case now.Before(s.CurrentPeriodEnd.AddDate(0, 0, 21)): return PastDue
    default:                                        return Suspended
    }
}
```

No cron can be late, no state can be stale, and a machine that was down for a
week wakes up with correct billing state. This is the same reasoning that put
`ReleaseExpiredHolds` on the read path.

**Invoice generation** genuinely has to write, so it can't be purely derived. Use
lazy generation with the same trigger discipline: an `EnsureInvoicesUpTo(now)`
call at the top of the artist billing read path and the admin overview read path.
It inserts any missing period invoices and relies on `idx_invoices_period` to make
concurrent calls safe. At the current scale — one launch partner and early
signups — the admin loading the billing screen is more than frequent enough to
keep invoicing current, and correctness does not depend on that frequency.

The honest caveat: an artist who never logs in generates no invoice until an
admin opens the overview screen. That is fine while an admin checks weekly, and
it is the point at which a real ticker becomes worth adding — **but not before
there is revenue to justify it.**

### 6.4 Changing a price after artists are already on it

The concrete scenario that motivated the plans table: **Starter launches at $7,
and six months later you want it to be $10.** The question the UI has to answer
unambiguously is *what happens to the artists already paying $7* — and this is a
trust decision, not a technical one.

| Approach | Effect |
|---|---|
| **Grandfather** ✅ | Existing subscribers stay at $7 forever. New signups pay $10. |
| Migrate everyone | Every Starter artist's next invoice is $10. |
| Per-artist | Admin moves individual accounts to the new price. |

**Grandfathering is the default and requires no extra work** — it falls out of
`subscriptions.monthly_price` being a snapshot written at signup. Editing
`plans.starter.monthly_price` changes what the pricing page advertises and what
the *next* signup gets; it does not touch a single existing subscription row.
You have to deliberately act to break grandfathering, which is the correct
default for anything involving money.

Raising the price on existing artists is therefore a **separate, explicit,
audited action** (`POST /admin/plans/:code/apply-to-existing`), never a
side-effect of editing a number in a form. The reasons are not merely technical:

- Silently raising what someone already agreed to pay is the kind of thing that,
  in a market this small and this word-of-mouth-driven, costs more in reputation
  than the extra $3/month earns. B-Edge's entire early growth is Rania's network
  talking to each other.
- Practically, these artists pay by manually sending an OMT transfer. If the
  amount changes without warning, they send the old amount, and now every one of
  them is a partial-payment reconciliation problem for whoever is confirming
  invoices by hand.
- The Artist Agreement (§10) has to state a notice period for price changes.
  Doing it without one is a contract problem regardless of how the code behaves.

So the rule: **a price change never affects an already-issued invoice, and never
silently changes what an existing subscriber owes.** If applied to existing
subscribers, it takes effect from their *next* billing period, after a
notification (recommend 30 days' notice, and a `plan_price_changed` WhatsApp
template alongside the six in §9).

The admin UI must show this plainly — after editing a price, the screen should
say *"New price applies to new signups. 12 existing artists remain on $7."* with
the bulk-migrate action as a separate, clearly-labelled button. An admin who
cannot tell which of the three behaviours they just triggered will eventually
trigger the wrong one.

---

## 7. Frontend

### 7.1 The pricing page has no home today — decide this first

`b-edge-web` has exactly three Angular projects: `artist-dashboard`,
`customer-pwa`, and `shared`. **There is no marketing site.** `customer-pwa`'s
`''` route is the Discover screen (the customer's front door, per its own route
comment), and `artist-dashboard`'s `''` redirects to `/dashboard`. Nowhere is
there a page whose job is to explain B-Edge to an artist who hasn't signed up.

| Option | Cost | Trade-off |
|---|---|---|
| **Public route in `artist-dashboard`** ✅ | Lowest | Pricing sits beside `/register` where signup already lives; no new build target, no new deploy, reuses `bedge-*` primitives immediately. Ships an SPA bundle to a marketing visitor — irrelevant at this scale. |
| New `projects/marketing` app | Medium | Clean separation, better SEO. New build config, new deploy target, duplicated shell. Right answer later, overkill now. |
| Static HTML | Lowest | Fast, but forks the design system and drifts from `style-guide.html` immediately. |

**Recommendation: a public `/pricing` route in `artist-dashboard`**, unguarded
(no `authGuard`), rendering from `GET /api/v1/billing/plans` so prices are not
hardcoded in two places. Revisit a dedicated marketing app when SEO or a real
content site starts mattering — not before going online.

**✅ Built Aug 29, 2026.** `artist-dashboard/src/app/features/pricing/`, wired
into `app.routes.ts` with no guard, reading live from `GET /billing/plans`
through a new `BillingDataService` in `@bedge/shared`. Verified in an actual
browser (Playwright, not just a clean build): all 4 public tiers render with
real prices, the Growth card carries the "Recommended" badge, `comped` is
correctly absent, and the FAQ answers the OMT/Whish payment question below.

### 7.2 Screens to build

**1. `/pricing` — public, artist-dashboard. ✅ Built.** Tier cards, per-seat
explainer, FAQ covering the one thing every Lebanese artist will ask: *how do I
pay if there's no card?* CTA into `/register`. "What happens after the trial"
was left out of the FAQ for now — there is no trial yet (Phase 3), so answering
it would be describing a feature that doesn't exist.

**2. `/dashboard/billing` — artist, authenticated.** The screen that gets B-Edge
paid:
- Current plan, seats, next payment date, derived status badge
- Outstanding invoice, prominent, with the amount and the **exact OMT/Whish
  details to send to**
- "I've paid" → reference-number input → `POST .../submit`. Model this directly
  on the existing Deposit Queue verification screen at `/dashboard/deposits` —
  same interaction, same optional-free-text-reference field, a flow these users
  will already recognize.
- Invoice history with statuses

**3. The admin console — extend the existing `/admin` route.** *(Decided
Aug 29, 2026.)* Today `/admin` is a single-purpose pending-approval queue, and
its route comment explains why: at most two admin accounts will ever exist and
their whole job is one screen. Monetization makes that four jobs. Add **tabs**
rather than a new top-level route or a fourth Angular app — same actor, same
session, no new build or deploy target, and it keeps the "admin is one place"
premise the existing code was written around.

**Role: stays `admin`. No `super_admin` tier.** `users.role` is CHECK-constrained
to `customer | artist | admin` (`001_initial_schema.up.sql:25`), and `admin` is
*already* effectively a superuser — `RequireRole("artist", "admin")` in
`booking/handler.go:75-77` and `product/handler.go:26` lets it act as any artist.
Adding a tier would mean a migration plus updating every one of those call sites,
to distinguish roles that don't exist yet in an organisation of one. Revisit if
staff admins are ever hired who may approve artists but must not touch pricing.

| Tab | Contents | Phase |
|---|---|---|
| **Approvals** | Today's pending-artist queue, unchanged | exists |
| **Billing** | Confirmation queue (`status=submitted`) — the actual daily work. Roster of every artist with derived status, plan, and outstanding amount. Mark-paid, void. | 1–2 |
| **Plans** | Create/edit tiers and prices. Price edits affect new signups only, with a separate audited bulk-migrate action (§6.4). | 3 |
| **Artists** | Search all artists, edit plan/seats, comp an account, extend a trial, force-unsuspend | 3 |

**Scope reasoning** (this was left to judgement): Billing and Plans are the
answer to the two things actually asked for — *who has paid* and *change the
price later*. Artists is included because operating billing without it is
impossible in practice: the moment one artist disputes a charge or Rania's comp
needs extending, an admin needs a per-account edit screen or the only remedy is
`psql`.

**Platform metrics are deliberately scoped down to a summary strip**, not a
dashboard tab: total MRR, count by derived status, and total outstanding. Those
are three aggregates over tables this spec already creates, so they are nearly
free. Churn rate, trial-conversion rate, and revenue-over-time are **excluded** —
each needs historical state that nothing currently records (a subscription only
knows its present, not that it lapsed twice last spring). Building that means a
subscription-events table and a reason to look at it. With one launch partner
there is no trend to plot. Add it when there are enough artists for the number to
mean something.

**4. Cross-cutting: status banners** in the artist dashboard shell for
`trialing` (days remaining), `grace`/`past_due` (urgent, links to billing), and
`suspended` (read-only explanation).

Use the semantic-token banner components documented in `style-guide.html`, **not**
raw Tailwind colors — that guide explicitly flags this exact drift as the one
known design-system inconsistency, showing both side by side. Adding a fifth
hand-rolled banner variant makes it worse.

### 7.3 Signup

Add plan selection to the onboarding flow (`/onboarding`, artist-dashboard).
Selecting a plan creates the `subscriptions` row with `trial_ends_at` set. It does
**not** touch `artists.status` — the admin approval gate still runs independently,
exactly as §3 requires.

Open question for §11: does the trial start at signup or at admin approval?
Starting at signup burns trial days while the artist waits on a human, which is
unfair and generates support load. **Starting the trial at approval is the
recommendation.**

---

## 8. End-to-end money flow

```
1. Artist signs up, picks a plan          → subscriptions row, trial_ends_at set
2. Admin approves the artist              → artists.status = 'active' (unchanged flow)
3. Trial runs (30 days)                   → DeriveStatus = trialing, full access
4. Trial ends                             → EnsureInvoicesUpTo issues invoice #1
5. WhatsApp: "Invoice #1, $15, due Sep 7" → notifications row, booking_id NULL
6. Artist sends $15 via OMT/Whish         → happens entirely outside B-Edge
7. Artist enters reference in /dashboard/billing → invoice: issued → submitted
8. Admin checks the real OMT account      → confirms money actually arrived
9. Admin clicks Confirm                   → invoice: submitted → paid,
                                             current_period_end += 1 month,
                                             audit entry written
10. WhatsApp receipt to artist            → derived status back to active
```

Steps 6 and 8 are human and cannot be automated without a gateway. **Step 8 is
the control that matters**: an artist submitting a reference is a *claim*, not a
payment. Never let `submitted` alone extend service — that is a free
subscription for anyone who types a plausible-looking transaction code.

Failure path: no payment → grace (banner) → past_due (hidden from Discover) →
suspended (read-only). All reversible at step 9.

---

## 9. Notifications

Six new templates on the existing pipeline. No schema change — `booking_id` is
already nullable.

| Template | Trigger | Channel |
|---|---|---|
| `trial_started` | Admin approves artist | WhatsApp |
| `trial_ending_soon` | 5 days before `trial_ends_at` | WhatsApp |
| `invoice_issued` | Invoice generated | WhatsApp |
| `payment_received` | Admin confirms | WhatsApp |
| `payment_overdue` | Entering `grace` | WhatsApp |
| `account_suspended` | Entering `suspended` | WhatsApp |

`B-Edge-WhatsApp-API-Templates-v1.docx` has the existing 16 templates in EN + AR
— match its wording conventions, and add Arabic from the start rather than
retrofitting.

Date-triggered sends (`trial_ending_soon`, `payment_overdue`) have the same
no-scheduler problem as §6.3 and the same answer: enqueue from the read path that
already computes derived status, with a `sent` guard so reloading the dashboard
doesn't re-send. **This is the weakest part of the design** — an artist who never
logs in and whose admin never opens the billing screen gets no reminder. Accept
it at launch scale, and treat it as the first thing a real ticker fixes.

**Hard dependency: Twilio must be live (`WHATSAPP-SETUP.md`) before the first
invoice.** Meta's WhatsApp Business approval has its own external timeline
measured in days-to-weeks — start it now, not when billing code is ready.

---

## 10. Non-code work required to go online

Charging money makes several already-flagged items non-optional. From
`RELEASE-CHECKLIST.md`'s "not assessed by any engineering work" list, these move
from *should* to *must*:

- **Terms of Service + Artist Agreement with pricing terms** — what's owed,
  billing cadence, what happens on non-payment (naming the graduated states in
  §6.1), refund policy, cancellation. Currently drafted by nobody. **Taking money
  without published terms is the single biggest non-technical risk here.**
- **Privacy policy** — required regardless, overdue.
- **A real OMT/Whish business account** to receive subscriptions, separate from
  any personal account, or reconciliation becomes guesswork.
- **Receipt/record-keeping** for Lebanese tax treatment of recurring revenue.
- **Twilio + WhatsApp Business live** (§9).
- **Production hosting, SSL, secrets, backups, monitoring** — all still
  unassessed. Billing data raises the stakes on backups specifically: losing the
  bookings table is bad; losing who-paid-what is unrecoverable, because the
  source of truth is a bank app and someone's memory.

---

## 11. Decisions needed before implementation starts

### Resolved Aug 29, 2026

- **Admin console shape:** tabs on the existing `/admin` route, not a new Angular
  app (§7.2).
- **Role model:** single `admin` role, no `super_admin` tier (§7.2).
- **Prices editable from the UI**, hence migration 023 (§4) — driven by the
  "$7 Starter becomes $10 later" scenario.
- **Admin console scope:** Billing + Plans + Artists tabs, plus a metrics summary
  strip; full analytics excluded (§7.2).

### Still open

These need the founder, not an engineer:

1. **Final prices.** §1's table is a proposal. Nothing is blocked by it — plans
   are rows now, so launch prices are a seed script and later changes are a form.
2. **Trial length**, and does it start at signup or at approval? (§7.3
   recommends approval.)
3. **Grace/past_due/suspended thresholds.** §6.1 proposes 7/21 days.
4. **Currency.** USD throughout, or USD-priced and LBP-collected? Affects whether
   `invoices.amount` needs a collected-amount-and-rate alongside it. **Decide
   before migration 025 ships** — retrofitting currency onto historical invoices
   is genuinely painful.
5. **Does Rania's comp have an end date**, or is it revisited manually?
6. **Who is the admin that confirms payments daily?** The flow assumes a human
   checks OMT and clicks Confirm. If that person doesn't exist reliably, the
   whole model stalls regardless of the code.
7. **Notice period for price increases on existing artists** (§6.4). 30 days is
   the recommendation. This needs to be written into the Artist Agreement before
   the first artist pays, not when the first increase happens — retrofitting a
   price-change clause onto people who already signed without one is exactly the
   argument you don't want to be having.

---

## 12. Suggested build order

Each phase is independently useful; nothing later is required to make something
earlier work.

**Actual build order deviated from this plan on Aug 29, 2026** — the founder
asked to build pricing first, then to continue straight into "who has paid,
who hasn't." The backend for Phases 1–3 landed in one session, in reverse of
the original UI-before-backend assumption: every API and data model exists
before a single artist-facing or admin screen does. That is a reordering, not
a scope change — the screens below still need to be built.

**Phase 1 — See the money. ✅ Backend fully built.**
- Migrations 023/024/025 (`plans`, `subscriptions`, `invoices`), the full
  `internal/billing` domain, derived status (`DeriveStatus` - trialing→
  active→grace→past_due→suspended→cancelled, computed from dates, never
  stored), lazy invoice generation (`ensureInvoicesUpTo`), public `/pricing`
  page.
- Existing artists backfilled onto `comped` subscriptions directly in
  migration 024 (6 rows: Rania + 5 test accounts) - simpler and more
  reproducible than the originally-planned "manually create rows," same
  effect.
- Verified against the real dev DB (not just a clean build): a full
  trialing-artist scenario walked through subscription read → invoice
  generation → submit → admin confirm → period extension → status flips back
  to active, plus the edge cases - resubmitting an already-submitted invoice,
  a non-owning caller trying to submit someone else's invoice, re-confirming
  an already-paid invoice - all correctly rejected. Confirmed by construction
  (not just tested once): an unpaid subscription never accumulates more than
  one outstanding invoice, because `current_period_end` only advances on
  confirmed payment.
- **Not built:** admin **Billing** tab UI, MRR/outstanding summary strip.
  The data these would render (`GET /admin/billing/overview`) already works -
  this is purely a missing screen, not missing logic.

**Phase 2 — Let artists pay. ✅ Backend fully built, UI not started.**
- `GET /billing/subscription`, `GET /billing/invoices`,
  `POST /billing/invoices/:id/submit` (artist); `GET /admin/billing/invoices`,
  `POST .../confirm`, `POST .../void` (admin) - all live, all auth-checked,
  `confirm`/`void` audited via `internal/audit`.
- **Not built:** `/dashboard/billing` (the artist-facing screen from §7.2)
  and the admin confirmation-queue UI. Money can already be tracked
  end-to-end via the API; nobody has a screen to do it from yet.

**Phase 3 — Sell and self-manage. Partially built.**
- ✅ Admin plan create/edit (`POST`/`PATCH /admin/plans`) and
  `PATCH /admin/billing/subscriptions/:id` (change plan/seats/comp/cancel) -
  both delivered ahead of schedule in this session.
- **Not built:** plan selection in onboarding, trial-starts-on-approval,
  admin **Plans** and **Artists** tab UI, and `apply-to-existing` (§6.4's
  audited bulk re-price) - the last one still correctly deferred, since
  there are no real paying subscribers yet to apply it to.

**Phase 4 — Enforce. Not started.** The two `discovery/repository.go`
filters, `RequireActiveSubscription()` middleware, status banners. Genuinely
last on purpose, unaffected by everything above landing early: enforcement
bugs lock real artists out of a live product, so it should land only once
subscription state is already observably correct in production - which it
now can be verified to be, but enforcement itself still reads none of it yet.

**Phase 5 — Remind. Not started.** Six WhatsApp templates. Requires Twilio
live (see `WHATSAPP-SETUP.md`) - unaffected by this session's backend work.

Legal (§10) runs in parallel from day one and gates Phase 2, not Phase 4 —
the moment money is collected is the moment terms must exist.

---

*B-Edge · Beauty at the Edge · الجمال عند الحافة · August 29, 2026*
*Design proposal — no code written. Grounded against schema v22 and the real
route tables of all three Angular projects.*
