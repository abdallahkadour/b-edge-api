# Execution report — every executable suite, 2026-09-28

**Date:** 2026-09-28 · **Run autonomously**
**Against:** the running stack — real API (`air`, `-tags devbypass`), real PostgreSQL (migration 054), WebKit and Chromium at 390px and 320px
**Plans:** `B-Edge-Security-Test-Plan-v1.md` · `E2E-TEST-PLAN.md` · the executable suites in both repos

---

## 0. Result

**Every executable suite was run, and every one ends green — but only after
fixing five of the suites themselves.** No product defect was found in
behaviour. What the run did find:

1. **One open security risk, now measured (FRAUD-20).** A promo code can be
   guessed through the public previews at **≈ 5,800 guesses per hour per
   address**; the real code slipped into 482 random guesses was picked out.
2. **The suites had gone stale, and the plan had an unobserved claim.** Three
   product changes on 23–25 September (invitees must be registered and
   phone-verified; a joining member offers nothing until she switches
   services on; a refund is owed only for a deposit actually received) left
   five harnesses testing the old rules. They produced **false failures**,
   **two false passes**, and **one case that had skipped silently on every
   run since 25 September**. Each time, the product was right and the
   harness was wrong. All are fixed and re-run. E2E 16.1 claimed a waitlist
   cascade on hold expiry that never happened before 26 September; it is
   corrected and now executable (16.5).
3. **Running the suites back to back from one machine trips the API's own
   rate limit** (600 requests / 5 min / address) and turns into failures that
   look like defects. Five UI checks failed that way and all passed when
   re-run alone.

| Group | Result |
|---|---|
| Go build, vet | clean |
| Go unit tier — 41 packages | **1,140 pass** |
| Go database tier (`-tags dbtest`) | **1,219 pass** — the counted 1,231 is 5 `TestMain` + 7 tests that only build with `-tags devbypass` |
| Go `-tags devbypass` packages | 2 packages, 7 tests pass |
| Web — `@bedge/shared` 42 · customer-pwa 21 · artist-dashboard 14 | **77 pass**, 16 spec files |
| Builds — shared, customer-pwa, artist-dashboard | clean |
| Documentation check | 1 drift (customer help) → fixed; see §5 |
| UC-1 guest booking | 20 pass |
| UC-2 money *(after harness fix)* | 11 pass, 1 honest skip |
| UC-6 subscription enforcement | 7 pass |
| UC-7 owner/member boundary | 7 pass |
| Chaos booking | 25 pass, 2 informational (features B-Edge deliberately lacks) |
| Security §3.4b batch 1 | 7 pass |
| Security §3.4b batch 2 | 6 pass, 1 informational |
| Security §3.4d salon *(after harness fix)* | 12 pass, 3 undecided, 1 informational, 1 skip |
| E2E 22 multi-artist *(after harness fix)* | 14 pass |
| E2E 23 salon over time *(after harness fix)* | 17 pass, 2 to decide |
| **E2E 27 + 16.5 + FRAUD-18/19/21/22** *(new, `make e2e-suite27`)* | **15 pass** |
| UI — phone verification *(made state-aware)* | 6 pass, 1 skip |
| UI — per-artist services | 23 pass |
| UI — E2E 22.6 at 390px and 320px | 14 pass |
| **FRAUD-20** code guessing | **open — measured** |

**184 live checks pass, 0 fail.** Every run that created data tore it down;
every residual count was 0.

**Addendum, later the same day — FRAUD-20 fixed.** 20 promo previews per
address per 10 minutes, shared by both preview routes, refused with 429
`TOO_MANY_CODE_ATTEMPTS`. Re-measured: 14 answered (6 already spent in the
window), then refused on both routes, the rest of the API unaffected — 120
guesses an hour, down from ≈ 5,800. The app now shows that reason under the
code field, and its "too quickly" banner is kept for the general limit only
(it had also been shown, wrongly, for the 2-hold limit). See §1.

---

## 1. The security finding — FRAUD-20, promo codes can be guessed

Both previews — `POST /orders/discount-preview` (added 2026-09-27) and
`POST /bookings/:id/discount-preview` — are public by design and must answer
`valid: true` for a real code. So the only defence is how many guesses an
address gets.

Measured on a throwaway salon with one real code (`SAVE10`) slipped in at
position 300 among random six-character guesses:

- **482 guesses answered in 0.3 s**, then 429 — the rest of that address's
  600-request window.
- **The real code came back `valid: true`.**
- The limiter is a fixed budget per 5-minute window, not a pace, so a burst
  spends it at once: **≈ 5,800 guesses per hour per address**, and nothing
  limits the total across addresses.

**Recommendation:** a separate, tight per-address limit on the two preview
routes (e.g. 20 per 10 minutes), leaving the general limit alone; and codes
long enough that 5,800 an hour is hopeless. **Done the same day** — see the
addendum above and FRAUD-20 in the security plan.

SPAM-06 (21 September) is not contradicted: it asks whether a nonexistent
code and a real-but-unusable one look alike — they do — and cannot see this.

---

## 2. The suites that had gone stale

Three product changes landed after the suites were last run:

| Change | Commit | What it broke in the suites |
|---|---|---|
| A salon may only invite a **registered** artist whose phone is **verified** | `6771b77`, `c0b4b6f` (2026-09-23) | Salon security, E2E 22, E2E 23 invited bare or unverified numbers and got the correct 409/404 |
| A joining member **offers nothing** until she switches services on (PP-7) | `adf1d5a` (2026-09-25) | Joined members had no bookable times at all |
| A refund is owed only for a deposit **actually received** | `eaeba2d` (2026-09-23) | UC-2's "paid" fixture set an amount, never `deposit_paid_at` |

What each produced, and the fix:

| Suite · case | Measured | Why | Fix |
|---|---|---|---|
| UC-2 **M1, M3** | FAIL — artist cancel → `cancelled`, expected `refund_due` | The fixture modelled "paid" with `deposit_amount` alone — the very confusion `eaeba2d` fixed | Fixture stamps `deposit_paid_at` when it means paid; new **M1b** pins that an unpaid deposit is not refunded |
| Salon security — every invitation case | harness crash after 409 `PHONE_NOT_VERIFIED` | Invitees never verified | Register real artists at every invited number; verify through the real endpoint with the dev bypass |
| Salon security **SPAM-08** | **false PASS** — "refused after 0 invitations" | Refused as `ARTIST_NOT_REGISTERED`, but any refusal was accepted | Asserts that exact reason and says the daily invitation cap is not reached by this run |
| Salon security **FRAUD-14** | **false PASS** — "refused at the ceiling" | Refused as `ARTIST_NOT_REGISTERED`, not by the ceiling | Invites a registered, verified artist; now refused **409 `PLAN_LIMIT_REACHED`** |
| E2E 22 **22.1a** | FAIL — 409 `PHONE_NOT_VERIFIED` | Invitee never verified | Verified through the real endpoint |
| E2E 22 **22.3b** | **SKIP on every run since 2026-09-25**, unnoticed | The member had no times to narrow (PP-7) | Member switches her services on; now **57 → 8 times, all inside 14:00–16:00** |
| E2E 23 — harness | crash after 409 `PHONE_NOT_VERIFIED` | Same | `join_salon` verifies first |
| E2E 23 **23.3b** | FAIL — "carine=0" | No times to narrow (PP-7); the narrowing itself leaked nowhere | Joiner switches services on |
| E2E 23 **23.7e** | FAIL — worded "an unapproved artist can take a deposit" | The **positive control** (an approved artist) was refused `SERVICE_NOT_FOUND`; pending and rejected were correctly refused | Joiner switches services on; the check now reports a failed control as a failed control |
| UI phone verification **2, 5** | FAIL | `rania@` was verified on 2026-09-23, the day the screen shipped; the script assumed unverified | State-aware: checks the verified screen; the send-code flow is a stated SKIP (see §6) |

**Lesson worth keeping:** a SKIP is not a pass, and neither is a refusal
without its reason. 22.3b skipped quietly for three days, and SPAM-08 /
FRAUD-14 would have gone on passing whatever broke.

---

## 3. The rate limit and back-to-back runs

The API allows 600 requests per 5 minutes per address. Run one after another
from one machine, the suites together exceed it, and the browser scripts
that ran last failed in ways that read as defects:

| Check | First run | Alone, after the window |
|---|---|---|
| offerings `o-offered` | **429** | 422 `[offered]` — correct |
| offerings `o-draft` | deposit not saved | deposit `20.00` saved |
| offerings check 9 | member2 login failed | pass |
| E2E 22.6 at 390px | login redirect timed out | pass |
| phone UI | click timed out | pass |

**Recommendation:** either pace `make verify-all` (a pause between suites),
or add a development-only way to raise the limit for test runs. Not changed
here: it is a security control, and a switch that loosens it must be proven
unable to reach production — the same care the dev OTP bypass got.

The same limiter affects the share links: every remote tester is
`127.0.0.1` to the API, so they share one budget and one 2-hold limit per
artist. Now stated in E2E-TEST-PLAN §0.

---

## 4. The plan's own error — E2E 16.1

16.1 listed hold expiry and deposit-deadline expiry as cascading to the
waitlist "on the read path". They never did before 2026-09-26: the sweep
called repository methods that returned a row count and threw away which
slots had opened. Found checking an external review, fixed in `a206894`,
and the plan still claimed it. The rows are corrected, and **16.5** is the
executable check: a lapsed hold with **nobody reading availability** became
`expired`, and the waiting customer `notified`, **after 51 s**.

---

## 5. What was extended

**Executable:** `make e2e-suite27` (`scripts/e2e-suite27.py`) — E2E 27.1–27.8
and 16.5 plus security FRAUD-18/19/21/22 in one pass; builds and destroys its
own salon. Its first run failed two cases on a harness error of its own —
consecutive start times overlap for a 60-minute service, and the server's
correct 409 was read as a failed control; it now picks times a service apart.
Added to `make verify-all`.

**E2E plan:** 16.1 corrected; **16.5** (the expiry worker); **27.8** (forged
address, someone else's hold); §0 note on share links and the rate limit.

**Security plan:** **FRAUD-20** measured (open); **FRAUD-21** a forged
`CF-Connecting-IP` / `X-Forwarded-For` / `X-Real-IP` does not lift the hold
limit — 429, no header believed; **FRAUD-22** a submitted booking cannot be
released and answers exactly like an unknown id — accepted risk recorded
that an unfinished hold can be released by whoever holds its id.

**Help guides** (flagged by `check-docs.sh`): customer — promo codes in the
cart as well as booking, the hold limit and the 10-minute hold, the discount
line on orders; artist — a code's end date is the salon's day, codes work in
the shop and can take an order to free, the order total is what is owed.

---

## 6. Not executed — do not read these as passes

| What | Why |
|---|---|
| E2E suites 1–15 and most of 16, §2.5 adversarial pass, §3 UI stress pass | Manual journeys; no executable harness. Suites 17–27 are executable and were run |
| Enterprise UI plan | Last executed 2026-09-19; not re-run |
| UI send-code-and-verify flow | Needs an unverified artist; driving it on `rania@` would queue a real code to her number |
| `make verify-delivery` | Sends a real message; expected to fail until Meta verification or `TWILIO_SMS_FROM` |
| Mutation testing | Run per package as code changed (booking, product, promo on 2026-09-26/27); not re-run in full |
| `make lint` | `golangci-lint` is not installed on this machine; `go vet` is clean |
| Security rows outside the executable batches (e.g. D4 security headers) | Unchanged since their last recorded result |

**Awaiting a product decision** (measured, unchanged since 2026-09-22):
FRAUD-11 an unauthenticated caller holding an invitation link can decline
it; AUTH-16 / 23.7d an invitation can be redeemed by a different account;
AUTH-14 / 23.7f a removed member's access token still reads for up to 15
minutes. 23.7d now measures little — the adversary is already a member when
it runs; AUTH-16 asks the same question cleanly.

**Found by the editor's analyzer, not fixed:** `toDecimal` in
`internal/booking/repository.go` has no callers; `columnRe` in
`internal/booking/columns_test.go` is never used; `golang.org/x/image` is
listed indirect but should be direct (`go mod tidy`).
