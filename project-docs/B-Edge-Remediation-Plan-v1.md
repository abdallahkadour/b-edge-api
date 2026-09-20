# B-Edge — Remediation Plan v1

**Date:** 2026-09-21
**Source:** everything still open after the verification exercise (UC-1,
UC-2/3, UC-5, UC-6), the WF1 council review, and Rania's testing session.
**Ground truth:** every count below was measured against the running stack
on 2026-09-21, not carried over from earlier notes.

---

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

### P0.1 · Make development mode impossible to expose
**Cost:** ~30 min · **Risk if skipped:** total authentication bypass

`devBypassOTPCode` authenticates any phone number when
`APP_ENV=development`. On 2026-09-21 that was **live on the public tunnel** —
verified by issuing a token for a number that had never requested a code.
Closed by setting `APP_ENV=production`, which also stopped the public API
returning Go stack traces on panic.

The gate is correctly written and fails closed. The hole was that nothing
connects *"publicly reachable"* to *"not development"*, and `.env` is
machine-local and gitignored, so the next tunnel can reopen it.

**Fix:** refuse to boot when `APP_ENV=development` and a public base URL is
configured (`API_PUBLIC_URL` set, or a non-localhost `CLIENT_URL`). Fail at
startup with a named error, not a warning — a warning in a log nobody reads
is how this happened.

**Done when:** the API exits non-zero on that combination, and a test asserts
it.

### P0.2 · Two accounts still hold unnormalised phones
**Cost:** ~20 min · **Risk if skipped:** silent auth and refund mismatches

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

**Fix:** decide per account whether it is a duplicate to merge or a distinct
person to renumber, then normalise. Two rows; this is a judgement call, not
a migration.

**Done when:** `SELECT count(*) FROM users WHERE phone NOT LIKE '+%'` is 0
and no phone is shared.

### P0.3 · Rania's opening hours are wrong, and they are also a pricing bug
**Cost:** 2 min, hers · **Risk if skipped:** wrong prices to real customers

Three days open before 08:00 — Sunday **07:08**, Monday **03:35**, Tuesday
**03:00**. Almost certainly stray taps.

It is not only cosmetic. Beirut Downtown charges an early-bird surcharge
before 09:00, so **every pre-9am slot is advertised at +$15**. She is
currently selling 3am appointments at a premium.

**Fix:** she opens Hours → *Set the same hours for every day* → Apply. Her
data, her call — deliberately not changed for her.

### P0.4 · `request-otp` reports success for a message that never arrives
**Cost:** ~2 h · **Risk if skipped:** every customer login is a dead end

**0 of 92** notifications have ever been delivered. The API answers
*"Verification code sent"* regardless, so a customer waits for a code that
does not exist and nothing anywhere says otherwise.

Delivery itself is blocked on Meta. **Honesty is not.** The reconciler now
records `delivery_status`, so the truth exists in the database and is simply
not surfaced.

**Fix:** after queueing, if recent notifications to that number are
`undelivered`, tell the customer WhatsApp is not reaching them and offer the
guest booking path, which works today.

**Done when:** a customer whose last three codes were undelivered sees a
truthful message rather than "sent".

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

### P2.1 · The booking funnel loses everything on reload
**Cost:** ~3 h

Funnel state is in-memory signals on a single route; nothing in
`customer-pwa` persists. A reload mid-booking discards the step, the slot,
the live hold, and the name and phone just typed.

`overscroll-behavior-y: contain` removed the accidental trigger
(pull-to-refresh). It did not make the funnel durable against a deliberate
reload, an iOS tab eviction, or a crash.

**Recommended scope:** persist **only the typed details** (name, phone,
notes) to `sessionStorage`. Never the slot or booking id — the funnel's own
header explains why a live 10-minute hold must not be resurrectable, and
that reasoning still holds.

### P2.2 · Surcharge-vs-discount ordering has no test
**Cost:** ~1 h

Decision **D3** settled that a surcharge applies before a discount. Nothing
asserts it. This is a money rule with a named decision and no guard — the
same shape as every defect this exercise has found.

Blocked only by there being no active discount code to test against; create
one in the fixture.

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
