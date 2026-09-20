# B-Edge — Remediation Plan v1

**Date:** 2026-09-21
**Source:** everything still open after the verification exercise (UC-1,
UC-2/3, UC-5, UC-6), the WF1 council review, and Rania's testing session.
**Ground truth:** every count below was measured against the running stack
on 2026-09-21, not carried over from earlier notes.

---

> **Read the severities with this in mind (added 2026-09-21):** B-Edge has
> **no live customers**. Every account, booking and price in the database is
> a fixture, WhatsApp delivers nothing, and the only human using the product
> is the launch artist testing it. So **nothing here is losing money today**,
> and earlier wording in this file that implied otherwise — "wrong prices to
> real customers", "selling 3am appointments at a premium" — overstated it.
>
> What the severities actually rank is **what would cost something the day
> this goes live**, plus what is blocking that day from arriving. Data
> hygiene items are cheap to fix precisely because the data is disposable;
> they are listed for completeness, not urgency.

## 0. How this is ordered

By **what it costs if left alone**, not by effort. Three of the four P0 items
take under an hour; they are P0 because of consequence, not size.

One thing dominates the list and is not engineering: **the domain**. It is
the sole blocker on Meta verification, which is the sole blocker on every
notification, which is the sole blocker on customer login. It also ends the
tunnel churn and enables a real deployment. **One purchase closes four
problems**, and half of P1 is unreachable until it happens.

---

## P0 — Do before Rania touches it again

### P0.1 · Make development mode impossible to expose — **DONE**
**Cost:** ~30 min · **Risk if skipped:** total authentication bypass

`devBypassOTPCode` authenticates any phone number when
`APP_ENV=development`. On 2026-09-21 that was **live on the public tunnel** —
verified by issuing a token for a number that had never requested a code.
Closed by setting `APP_ENV=production`, which also stopped the public API
returning Go stack traces on panic.

The gate is correctly written and fails closed. The hole was that nothing
connects *"publicly reachable"* to *"not development"*, and `.env` is
machine-local and gitignored, so the next tunnel can reopen it.

**DONE 2026-09-21.** `config.ValidateEnv` now refuses to start when
`APP_ENV=development` and any of `API_PUBLIC_URL`, `CLIENT_URL` or
`ARTIST_DASHBOARD_URL` names a non-local host. `CLIENT_URL` is a
comma-separated allow-list, so each entry is checked — a public host hiding
behind a localhost one is still caught.

Loopback, private and link-local addresses all count as local, so ordinary
development is untouched; a rule that blocked `localhost` would be one people
disable rather than obey. Anything unparseable fails **open**, because this
exists to catch a real hostname, not to be a URL validator.

Verified both ways against the built binary: development + the actual tunnel
hostname exits 1 with the offending host named; development + localhost boots
normally. Six tests, including one pinned to the exact configuration that
leaked.

### P0.2 · Two accounts still hold unnormalised phones
**Cost:** ~20 min · **Risk if skipped:** none today; a latent trap for later

Migration 043 normalised every phone to E.164 except two it deliberately
skipped, because normalising them would have collided with existing rows:

```
Sarah            70123456   (collides with an existing +96170123456)
Abdallah Kadour  70000000
```

One phone number is currently shared by two users.

This matters more than it did before: **the refund payer-mismatch check
normalises both sides and returns "cannot tell" when either fails to
parse**. An unnormalised phone therefore silently disables the refund
warning for that customer — the guard is off for exactly the accounts
nobody cleaned up.

**Investigated 2026-09-21, and the severity is lower than first written.**
Both accounts have **zero bookings**, so neither can ever be owed a refund
and the disabled guard is inert for them today. That correction belongs here
rather than quietly in a commit.

They are not empty duplicates either: Sarah holds **1 order** and Abdallah
Kadour **2**. In a live system that would make merging a judgement call
about someone's purchase history — but these are **fixtures in a test
database**, so the honest position is that either resolution is fine and it
simply needs someone to pick one. Left open only because it is not mine to
pick, not because it is delicate.

What was done instead: `verify-uc2` gained **M11**, which fails when any
customer *with a booking* has a phone the guard cannot parse — the exact
population the rule protects. The two accounts are reported in the passing
line so they stay visible without turning the suite permanently red, which
is how checks get ignored.

**Done when:** a decision is made per account — merge into the existing
holder, or assign the number actually belonging to them.

### P0.3 · Rania's opening hours are wrong
**Cost:** 2 min, hers · **Risk if skipped:** none today — it is test data

Three days open before 08:00 — Sunday **07:08**, Monday **03:35**, Tuesday
**03:00**. Almost certainly stray taps.

Beirut Downtown surcharges before 09:00, so those slots also show +$15.
**No real customer sees this** — it is a test environment — so the actual
cost is that her own testing happens against nonsense hours, and any demo
shows 3am availability. Worth two taps, not worth calling a pricing
incident, which an earlier version of this entry did.

**Fix:** she opens Hours → *Set the same hours for every day* → Apply. Her
data, her call — deliberately not changed for her.

### P0.4 · `request-otp` reports success for a message that never arrives — **DONE**
**Cost:** ~2 h · **Risk if skipped:** every customer login is a dead end

**0 of 92** notifications have ever been delivered. The API answers
*"Verification code sent"* regardless, so a customer waits for a code that
does not exist and nothing anywhere says otherwise.

Delivery itself is blocked on Meta. **Honesty is not.** The reconciler now
records `delivery_status`, so the truth exists in the database and is simply
not surfaced.

**DONE 2026-09-21.** `request-otp` now returns `delivery_looks_broken` and,
when true, tells the customer WhatsApp is not reaching them and that they
can still book without signing in — which is true, and is the path that
works today. The code is queued either way; only the wording changes.

**The check is PLATFORM-WIDE, not per number, and that is load-bearing.**
The first implementation asked "have recent messages to *this* number
failed", which would have handed back an enumeration oracle the endpoint was
explicitly built to avoid: `request-otp` returns an identical response for
an artist's number as for a customer's, so that it cannot be used to
discover who is registered, and a per-number delivery signal is only
answerable for numbers the system has messaged before. Rewritten to ask
whether the last five reconciled attempts across all recipients failed. Five
rather than one, because a single undelivered message is a flat phone, not
an outage.

A health-check error means "cannot tell" and is reported as healthy — the
alternative is announcing an outage because a query timed out.

Verified live against the real 0% delivery rate: the API now answers *"We're
having trouble reaching WhatsApp right now, so the code may not arrive. You
can still book without signing in."* Four tests, one of which asserts the
artist and customer responses stay indistinguishable.

---

## P1 — Blocked on one purchase

### P1.1 · Buy the domain
**Cost:** ~$10/yr + ~30 min

The keystone. It unblocks, in order of value:

1. **Meta business verification** → WhatsApp delivery → customer login,
   booking confirmations, the morning reminder, review requests. All built,
   all inert.
2. **A stable URL for Rania.** Quick tunnels have been reclaimed three times
   in two days; each one costs a message and a broken link.
3. **A real deployment** — the €4/mo host in the runbook, which does not
   sleep when the laptop closes.
4. **P0.1 becomes structural** rather than conventional.

`environment.prod.ts` still points at `b-edge.com`, which someone else owns;
it needs updating to whatever is bought.

### P1.2 · Deploy per the runbook
**Cost:** ~half a day · **Blocked by P1.1**

`B-Edge-Deployment-Runbook-v1.md` is written and unexecuted. Its restore
drill is the part worth honouring: it inserts a booking and then an
overlapping one and requires the second to be **rejected**, because a
restore that quietly lost `btree_gist` looks complete and has silently lost
the only thing preventing double-booking.

### P1.3 · Watch the delivery reconciler against real traffic
**Cost:** ~1 h · **Blocked by P1.1**

Verified end-to-end on 2026-09-21: SID captured, Twilio asked, `undelivered`
recorded, `WARN NOTHING is reaching recipients` logged. But it has only ever
seen failures. **The delivered path has never executed.** Watch it the day
Meta clears, before trusting a green number.

---

## P2 — Real gaps, no external blocker

### P2.1 · The booking funnel loses everything on reload — **DONE**
**Cost:** ~3 h

Funnel state is in-memory signals on a single route; nothing in
`customer-pwa` persists. A reload mid-booking discards the step, the slot,
the live hold, and the name and phone just typed.

`overscroll-behavior-y: contain` removed the accidental trigger
(pull-to-refresh). It did not make the funnel durable against a deliberate
reload, an iOS tab eviction, or a crash.

**DONE 2026-09-21.** The typed details — name, phone, notes — are mirrored
to `sessionStorage` as they change and restored on init. Verified in WebKit:
type, reload, walk back to the details step, and all three are still there.

**Only the typed details.** Not the slot, the hold id or the step. Restoring
those would resurrect a reservation that has very likely expired and put the
customer on a screen describing a booking the server no longer has — exactly
the staleness the no-URL-routing decision exists to prevent. Retyping a name
is an annoyance; being shown a slot you no longer hold is a broken promise.
After a reload the funnel correctly returns to the artist profile.

`sessionStorage`, not `localStorage`: this should survive a reload of *this
tab* and nothing more. A name and phone in `localStorage` would outlive the
booking, the session, and the phone being handed to someone else. The draft
is keyed per artist and cleared once the booking is placed or abandoned.

**It uncovered a pre-existing latent bug.** `GuestDetailsScreenComponent`
seeded its editable fields from `initialName()` / `initialPhone()` /
`initialNotes()` **in its constructor**, where a signal `input()` is not yet
bound and returns its declared default. Those four inputs were wired end to
end and **silently inert** — invisible because the container's values were
always empty too, so `""` was indistinguishable from working. Moved to
`ngOnInit`. The same pitfall is already documented on the container's own
`ngOnInit` for the router-bound `artistId`.

### P2.2 · Surcharge-vs-discount ordering — **DONE, and the item was wrong**
**Cost:** ~1 h

**This entry was inaccurate and is corrected rather than quietly deleted.**
D3.4 *is* tested, and well: `internal/pkg/discount` has 16 tests at 94.7%
coverage including `TestSurchargeAppliesBeforeDiscount`, which asserts 20%
of 120 rather than of 100. Writing another unit test would have duplicated
existing work.

The real gap was one layer up. `applyDiscount` passes `subtotal` as the base
with a **zero surcharge**, on the stated assumption that subtotal already
includes the early-bird fee. The resolver stays correct and the booking
becomes wrong if a caller ever passes the raw price instead, and no test
covered that coupling.

**DONE 2026-09-21.** `verify-uc1` gains **A5**, which books a real
early-bird slot with a real percentage code through the API and checks the
arithmetic end to end: base 200 + fee 15 = 215, 20% of **215** = 43, final
172. It creates the discount code and the early-bird cutoff it needs and
restores both.

A1 and A5 also stopped skipping. Both previously depended on the test day
happening to have a slot before the store's cutoff, which most days do not —
a check that skips is a check that never catches anything. The suite now
forces the condition. First attempt still skipped because the cutoff was
computed from a slot list the earlier holds had already consumed; it is now
derived from what is free at that moment.

### P2.3 · Payer capture has never been exercised
**Cost:** observation only

Four deposits have been confirmed; **zero** recorded a payer number. The
refund warning has therefore never fired in anger. The field is optional by
design — most deposits do come from the customer's own number — but until
one mismatch goes through the whole path end to end, it is verified only by
test.

### P2.4 · Bottom sheets cannot be swiped away
**Cost:** ~4 h

Eleven dialogs, tap-to-dismiss only. Flagged by the UI test plan; the
largest remaining item from it.

---

## P3 — Deliberately deferred

| Item | Why it waits |
|---|---|
| Customer profile onboarding | Named in the workflow brief, absent from code and from any spec. Not a defect — decide whether it is wanted before building it. |
| Explicit find-or-create on `users.phone` | Concurrent registration is currently serialised by a unique-index violation. It holds; it is just arbitration by accident rather than design. |
| Off-scale font sizes (13/15px) | **Closed.** Reviewed and accepted as-is. |
| Rania's verified badge | `artists.is_verified` was set by direct DB edit with no audit trail. Harmless at one artist, wrong as a process. |
| Salon registration, staff model, payouts, payment gateway | **Do not exist.** Recorded in the WF1 review as Unimplemented rather than broken. Build only if the product wants them. |

---

## What is already closed

Stated because a remediation plan that lists only problems misrepresents the
state of the system.

- **Guest booking** — 18 checks, `make verify-uc1`. Six concurrent holds on
  one slot resolve to exactly one winner.
- **Deposits and refunds** — 10 checks, `make verify-uc2`. Found and applied
  migration 038's two constraints, which had never been applied.
- **Subscription enforcement** — 7 checks, `make verify-uc6`. Found and fixed
  a cancelled artist still being sold.
- **Delivery observability** — the system can now see its own 0% delivery
  rate. It could not, for six weeks.
- **Duplicated rules** — subscription enforcement, the cancellable set,
  blocking statuses, payer comparison, the column/scanner triple and money's
  bounds are each now single-definition or guarded by a test that fails on
  divergence.
- **Documentation drift** — `make docs-check`, failing, with a committed
  baseline.

34 Go packages, 38 frontend tests, 35 live verification checks.

---

## Suggested sequence

1. **P0.1** — 30 minutes, closes an authentication bypass for good.
2. **P0.3** — tell Rania about her hours today; she fixes it in two taps.
3. **P0.2** — two accounts, a judgement call each.
4. **P1.1** — buy the domain. Everything below it unblocks together.
5. **P0.4** while Meta verification is pending.
6. **P1.2**, then **P1.3** on the first day delivery works.
7. **P2** in order.
