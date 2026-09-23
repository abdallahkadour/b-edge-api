# B-Edge Reliability Plan v1 — 62 → 78

**Written 2026-09-23** · against `api eaeba2d` / `web 6073c94`
**Companion to:** the Reliability Scorecard (metrics M1–M9 and the re-score worksheet)
**This file is canonical.** The published page is a rendering of it; when they
disagree, this one is right.

---

## The principle this plan is built on

Three defect *classes* produce almost every bug this project has found. Adding
coverage without closing a class buys a number that decays. **The plan attacks
classes, not instances.**

### Class A — a rule written correctly in one place and not consulted in another

Confirmed instances: `subscriptionVisibleCond` in 3 files (discovery fixed,
share and artist left drifted for a day); `RevenueStatuses` declared and read by
nothing; `included_seats` declared a ceiling and enforced nowhere (FRAUD-14);
`mapValidationError` in 10 copies already drifted into 4 variants; the
`NO_SALON` message across 38 sites in 8 domains in 3 variants; `GraceDays` /
`pastDueDays` / `suspendedAfterDays` as three hand-copied numbers; enforcement
policy in 3 places disagreeing about what `past_due` blocks; `deposit_amount`
read where `deposit_paid_at` was meant.

**Remedy:** a leaf package, plus a source-parsing test that breaks the build.

### Class B — a zero read as a guard

Confirmed instances: a rate-limiter 429 read as "no availability"; an empty
`business_hours` read as a status guard; a missing subscription read as a status
guard; DATA-03 passing against an empty `notifications` table; a grep pattern
mismatch read as "nothing happening"; `psql` returning 0 without
`ON_ERROR_STOP`; a clipping check that reported "none" while the screenshot
showed `09:00 AN`.

At least seven times a coincidental empty result looked like correct behaviour.

**Remedy:** every check establishes its precondition *before* measuring, and
carries a positive control.

### Class C — a test that asserts the bug

Confirmed instances: `TestCancelBooking_ArtistCancelAlwaysRefund` and the
waitlist fixture, both green, both encoding the `refund_due` defect.

**Remedy:** mutation testing. A test that survives a mutation constrains nothing.

---

## Phase 0 — This week · unblock the cap

Delivery caps the score at 62 regardless of what else lands. Nothing in Phase 1–4
is worth as much as M1 coming off zero.

| # | Task | Moves |
|---|---|---|
| **W0.1** | **Provision `TWILIO_SMS_FROM`.** Not blocked on Meta verification — a separate purchase. ~$1–2/mo for the number, $0.36/SMS to Lebanon. This alone can take M1 off zero without waiting on ticket 63051. | M1 |
| **W0.2** | **DONE 2026-09-23.** `make verify-delivery` — queue one notification to your own handset, poll to a terminal state, assert `delivered`, print the transport actually used. **It must FAIL today.** That failure is the positive control for the most important metric on the scorecard; a check that has only ever been run after the fix proves nothing. | M1 |
| **W0.3** | **Prove the SMS fallback has ever executed.** It was written last week and has never run. Force WhatsApp to fail with a bad `TWILIO_WHATSAPP_FROM`, assert the row lands with `channel='sms'` and a real SID. Untested fallback code is not a fallback. | M1 |
| **W0.4** | ~~Server-side booking horizon~~ **DONE 2026-09-23.** See the decision below — the recommendation in the first draft of this plan (120 days) was **wrong** and would have refused the launch artist's highest-value bookings. | — |

**Decision needed from you:** W0.1 is procurement. Everything else in this plan
is engineering and needs nothing from you.

### W0.4 — resolved · the booking horizon

**Decision: picker 400 days, server bound 550 days.** Recorded 2026-09-23.

The first draft of this plan recommended **120 days**. That was wrong, and the
reason it was wrong is worth keeping: I picked a number that sounded far away
without checking it against what the business sells. **Bridal is booked 11-12
months ahead and is roughly double the price of anything else the launch artist
does.** A 120-day cap would have refused exactly the bookings B-Edge most wants.

Measured before deciding:

| | Before | After |
|---|---|---|
| Server cap | **none** - a hold 365 days out returned `201` | `550` days, `BOOKING_TOO_FAR_AHEAD` |
| `CreateBooking` past check | **none at all** - parsed the timestamp and inserted | shares the guard |
| Customer picker | `STRIP_DAYS = 90` | `400` |

The 90 was not arbitrary - its own comment claimed a quarter "covers the things
people book far ahead - weddings, graduations, Eid." **It does not cover
weddings.** And it had already been raised once, from 28, after a customer
reported "booking is only available for this month." The number moved twice; the
reasoning was never checked against the business either time.

**Consequence while it said 90: a bride could not book through the funnel at
all.** Those bookings arrived by DM and were keyed in by hand, so the
highest-value work on the platform never touched the product. This was filed as
the smallest item in Phase 0 and was in fact a live revenue defect.

Two things fell out of fixing it:

- `validateBookingTime` is now **one function called by both entry points**.
  They had disagreed: the guest hold rejected the past and nothing rejected the
  far future, while `POST /bookings` checked **neither**. Class A, found while
  fixing something else.
- The month-jump chips now carry a year. A strip longer than 365 days renders
  the same month twice, and labelling by month alone gave two chips both reading
  "Sep" - distinct keys, identical labels.

**Still open, deliberately:** per-service `max_advance_days`, so a regular
session can go back to ~90 days while bridal keeps the long horizon. One
nullable column and one check. `MaxBookingHorizon` stays as the outer bound it
cannot exceed.

**Recorded, not fixed:** a bridal booking made a year out locks its price at
booking time. For the bride that is a feature - the quote is honoured. For the
artist it means bridal cannot be repriced for anyone already booked. Keeping it;
it should be a decision rather than an accident.

---

## Phase 1 — Weeks 1–2 · finish the DB test infrastructure that is 80% built

**The finding that rewrites this phase:** `TEST_DB_NAME` is already in `.env`,
`make migrate-test` already exists, and `cmd/migrate/main.go:24` already
switches on `TEST_DB=true`. CLAUDE.md calls this vestigial and says building the
infrastructure is "a separate project." It is not — **what is missing is a Go
helper of roughly sixty lines.**

| # | Task |
|---|---|
| **W1.1** | **DONE 2026-09-23.** `internal/pkg/testdb`: migrate once into `bedge_test_tmpl`, then each package does `CREATE DATABASE bedge_test_<pkg> TEMPLATE bedge_test_tmpl`. Template cloning is a file copy in Postgres — about 100ms **per package**, not per test. No testcontainers; Postgres is already running as `bedge-postgres`. |
| **W1.2** | **DONE 2026-09-23.** Tag them `//go:build dbtest` so `go test ./...` stays fast and only CI pays. |
| **W1.3** | Cover the SQL that touches bookings and money **first**, in order: `booking/repository.go`, `billing/repository.go`, `membership/repository.go`. Target **≥25% on those three**, not on all 264 files. Coverage spread thin across every repository is worth less than depth on the three that can lose money. |
| **W1.4** | **DONE 2026-09-23.** The argument for the whole phase: **two things only a real database can prove.** The GIST exclusion constraint — the chaos suite tests it through HTTP, which cannot distinguish "the constraint held" from "the service got lucky about ordering." And the `CASE WHEN $2 THEN $3 ELSE col END` clearable-field SQL, where the old `COALESCE` silently ignored `{"bio": null}`. A mock cannot test either. |

---

## Phase 2 — Weeks 2–4 · close Class C, stop the green suite from lying

| # | Task |
|---|---|
| **W2.1** | Adopt `gremlins` for Go mutation testing. `internal/booking` and `internal/billing` first — the two packages where a wrong answer costs money. |
| **W2.2** | **Expect survivors. They are the deliverable, not a failure.** Every survivor is a line whose behaviour no test constrains. `refund_due` would have been caught here: mutating `b.DepositAmount.IsPositive()` to a constant `true` survives the old suite silently. |
| **W2.3** | Add mutation score per package to the ledger as **M10**. Gate: booking + billing ≥ 70% killed. This replaces statement coverage as the real measure — coverage says a line ran, mutation says a line is *constrained*. |
| **W2.4** | **DONE 2026-09-23.** **Retire the LLD's "mutation-tested" claim** about `statematrix_test.go`. There is no mutation tooling in the repo and there never was. The test itself is the best thing in the suite — 154 cells asserting the exact error code *and* that a rejected action did not write the row. The claim around it is Class A drift that got into the documentation, and it produced a false sense of assurance that reached the scorecard. |

**Why Phase 2 precedes Phase 3:** mutation testing is what *finds* the remaining
Class A instances. An unconstrained line is exactly what a duplicated-and-drifted
rule looks like from the test suite's side.

---

## Phase 3 — Weeks 3–5 · close Class A, the duplication register

| # | Task |
|---|---|
| **W3.1** | Inventory every rule that exists in more than one place. Start from the confirmed list above; the mutation survivors from W2.2 will add to it. |
| **W3.2** | For each entry: extract to a leaf package **or** add a source-parsing guard. Not both, not neither. A register entry with no resolution is how `included_seats` stayed unenforced. |
| **W3.3** | **Every new guard must be proven to fire on a deliberately injected violation, with the proof recorded in the test's header.** This is already the house standard for the four that exist — W3.3 makes it a rule rather than a habit. |
| **W3.4** | Target **M7 ≥ 6**. |

---

## Phase 4 — Weeks 4–6 · close Class B, and fix what "handler coverage" measures

| # | Task |
|---|---|
| **W4.1** | A shared preamble for the Python harnesses: `require_nonempty()`, `require_status()`, `positive_control()`. Every check must state what has to be true before it measures anything. Seven false results came from skipping this. |
| **W4.2** | Convert the 5 informational rows in the chaos suite into real assertions, or delete them. **An informational row is a check that decided not to decide.** |
| **W4.3** | Reframe handler coverage as a **count, not a percentage**. Extend `routecoverage_test.go` — which already parses route registrations — so every registered route must have at least one test that exercises it, with an exemption list carrying reasons. **"0 unexercised routes of 160" is a better and more achievable target than "20% handler coverage"**, because handlers are thin and statement coverage of a thin layer measures almost nothing. |

---

## Phase 5 — Ongoing · hold the line

- **H1** `make verify-all`: go test, dbtest, chaos, security, E2E in one target. Run on every push.
- **H2** Re-score monthly against the worksheet. The movement is the signal, not the absolute.
- **H3** Two rules into CLAUDE.md, because both were learned the expensive way:
  - *A passing test is not evidence until you have seen it fail.*
  - *Establish the condition before measuring it.*

---

## Deliberately not in this plan

- **testcontainers.** Postgres is already in Docker; template databases are faster and need no new dependency. CLAUDE.md already warns against spending a sprint here, and it is still right about that.
- **Load testing.** Needs written authorisation and an environment that is not a laptop. Unchanged.
- **100% coverage as a target.** Coverage is a proxy. After Phase 2, mutation score is the real measure and coverage becomes a diagnostic.
- **Fixing the two known-wrong billing behaviours** (invoice accumulation; `AddDate` rolling Jan 31 → Mar 3). They are product decisions pinned by tests, and belong in the decision register, not here.
- **Rewriting the 38 `NO_SALON` sites mid-feature.** It goes in the W3.1 register and gets resolved once, deliberately.

---

## Expected movement

| Phase | Effort | M-metrics moved | Score |
|---|---|---|---|
| 0 | ~2 days + procurement | M1 | 62 → **72** |
| 1 | ~4 days | M2, M5 | 72 → **77** |
| 2 | ~3 days | M10 (new) | 77 → **78** |
| 3 | ~3 days | M7, M8 | holds 78 |
| 4 | ~3 days | M3 → route count, M9 | holds 78 |

**Phases 3 and 4 do not raise the number. They are what stops it falling back** —
the classes they close are what produced the 10 fix commits in 44.

---

## Execution log

### 2026-09-23 — W0.2, W0.4, W1.1, W1.2, W1.4, W2.4, H1

**Repository coverage moved off zero for the first time: 0.0% → 1.6%.**
`booking/repository.go` 5.2%, `artist/repository.go` 6.2%. Total 36.4% → 36.0%
(the denominator grew).

That percentage is small and saying otherwise would be dishonest. **The
infrastructure is the deliverable** — the next repository test now costs a few
minutes rather than a sprint. W1.3's "≥25% on three repositories" is NOT met.

**`internal/pkg/testdb`** — migrates once into `bedge_test_tmpl`, then
`CREATE DATABASE … TEMPLATE` per package. Measured: first package ~0.5s
including the migration, each subsequent clone ~40ms. Serialised across
parallel test binaries by a Postgres advisory lock, because without one several
binaries race to create the same template and all but one fail.

**8 tests that a mock cannot express**, and every one was proven to fire:

| Test | Proves |
|---|---|
| Overlapping bookings for one artist | Refused, `ErrSlotUnavailable`, only 1 row |
| **Adjacent** bookings | **Allowed** — `'[)'` bounds; if this broke, artists could not book back-to-back |
| Two artists, same hour | Allowed — the constraint keys on `artist_id`, which is what makes multi-artist work |
| **Cancelled booking** | **Does not block its old slot** — the constraint's `WHERE`. If the predicate were dropped, every cancellation would silently sterilise its own hour |
| Profile field set / absent / `""` / `null` | The `CASE WHEN` presence-value pair, all four cases |

Three of those assert the constraint must **not** fire. A constraint that
refused everything would pass a naive overlap test while making the product
unusable.

**Proven to fire, not assumed.** Disabling the horizon bound failed 2 of 5
tests; reintroducing the old `COALESCE($3, bio)` failed **all 4** profile
tests. Both restored and re-run green.

**`make verify-delivery`** — written while it still fails, which is the point.
Current output: `sent` via whatsapp, never delivered. It also caught a mistake
in its own first run: a fabricated recipient returns Twilio **63024 (invalid
recipient)**, which is *not* the Meta verification block (63051) and proves
nothing about the pipeline. The script now refuses placeholder numbers rather
than producing a confident, wrong diagnosis. The probe row was deleted so M1's
denominator stays honest at 92.

**`make verify-all`** — `test`, `test-db`, `verify`, `chaos-booking`,
`verify-security-salon` in one target.

### Not done, and why

- **W0.1** `TWILIO_SMS_FROM` — procurement. Still the largest single movement available.
- **W0.3** SMS fallback proof — blocked on W0.1. The code has still never executed.
- **W1.3** ≥25% on three repositories — infrastructure landed, depth not built.
- **W2.1–W2.3** mutation testing — `gremlins` not yet adopted.
- **W3.x, W4.x** — untouched.

