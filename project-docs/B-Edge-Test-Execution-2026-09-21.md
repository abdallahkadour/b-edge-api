# Execution report — Security plan §3.4b and E2E suites 17–21

**Date:** 2026-09-21 · **Run autonomously**
**Against:** the running stack — real API, real PostgreSQL, real Twilio, WebKit
**Plans:** `B-Edge-Security-Test-Plan-v1.md` §3.4b · `E2E-TEST-PLAN.md` 17–21

---

## 0. Result

**33 of 33 executed checks pass.** One defect was found and fixed during the
run. Five of my own test harnesses produced false results before producing
true ones, and all five are recorded in §4 — a report that hides its own
false positives cannot be trusted on its passes.

| Group | Result |
|---|---|
| Security §3.4b batch 1 — AUTH/DATA/INJ | 7 pass |
| Security §3.4b batch 2 — FRAUD/SPAM/EDGE | 6 pass, 1 informational |
| E2E 17–19 via `make verify` | 37 checks, 1 honest skip |
| E2E 20–21 in WebKit at 390px and 320px | 10 pass |

---

## 1. The defect this run found

### Two more copies of the cancelled-artist rule · **fixed**

Executing **FRAUD-10** asked a question the plan had marked *"unverified —
check this"*: is an unsellable artist hidden from the **share preview** too,
not just from the surfaces that take money?

It was not. And the cause was the same class this project keeps producing:
`subscriptionVisibleCond` exists in **three** files —
`internal/discovery`, `internal/artist` and `internal/share` — each a
hand-written copy of one rule. Discovery's was corrected on 2026-09-20.
**The other two were not**, so a cancelled artist stayed reachable through
their share link and through handle lookup, while being hidden from Discover
and refused bookings.

`internal/share`'s own comment states the requirement it was violating:

> *a link preview must not render for an artist whose profile is itself
> hidden from Discover.*

Both copies now match, each carrying a note that the rule lives in three
places and moves together. Verified: the share URL for a cancelled artist
returns **302**, not a card.

---

## 2. Security plan §3.4b — results

| Test | Verdict | Evidence |
|---|---|---|
| **AUTH-08** dev OTP bypass outside development | PASS | `326321` refused under `APP_ENV=production` → `OTP_NOT_FOUND` |
| **AUTH-11** development build on a public host | PASS | Refused **4** public configurations including one hidden second in a comma-separated `CLIENT_URL`; all **3** local configurations still boot |
| **AUTH-12** delivery flag as a registration oracle | PASS | Byte-identical response for an artist's number, a customer's, and one never seen |
| **DATA-01** Twilio SID exposure | PASS | 4 SIDs in the database, none in any of 4 authenticated responses |
| **DATA-02** payer phone cross-tenant | PASS | Another artist's booking returns 404 |
| **INJ-05** bidi override in an outbound body | PASS | Override survives in the queued payload (expected) and is stripped at send |
| **INJ-06** format/newline injection | PASS | No evaluation; a forged `B-Edge:` prefix stays inert text |
| **FRAUD-08** refund gate bypass | PASS | **4 bypass shapes refused** — omitted, `false`, the string `"true"`, `null`. A real boolean `true` works. A second refund is `409` |
| **FRAUD-09** format-equivalent payer | PASS | `70 555 123` and `+96170555123` treated as one number; a genuinely different number still blocked |
| **FRAUD-10** unsellable artist | PASS *(after fix)* | Cancelled artist hidden from discovery, slots, hold **and now share (302)** |
| **SPAM-06** discount enumeration + redemption | PASS | Valid and unknown codes both `200` — no enumeration signal. `max_redemptions=1` honoured under concurrent submission |
| **SPAM-07** cross-tenant hours write | PASS | Writing another artist's hours → `404` |
| **EDGE-05** 90-day horizon amplification | PASS | Single date 6 ms; 90 dates at 10-way concurrency 0.1 s total (**1 ms each**) — no disproportionate cost |

**CLIENT-06** (PII in browser storage) is covered by E2E 21 below rather than
duplicated here.

---

## 3. E2E suites 17–21 — results

**17 (payer capture and refund gate)** and **18 (delivery truth)** are
executable as `make verify-uc2` and the notification tests; **19
(enforcement)** as `make verify-uc6`. All pass. FRAUD-08/09/10 above exercise
the same ground more aggressively.

**20 — hours, calendar, horizon**, in WebKit at both widths:

| | 390px | 320px |
|---|---|---|
| Hours: page overflow | 0 | 0 |
| Hours: clipped time values | 0 | 0 |
| Hours: bulk control present | yes | yes |
| Calendar: hour labels | 17 | 17 |
| Calendar: labels off-screen | **0** | **0** |

All three defects that reached the launch artist stay fixed at both widths.

**20.3 — horizon:** 90 dates, `2026-09-21 … 2026-12-19`, chips Sep/Oct/Nov/Dec.
Tapping a chip scrolls and **does not change the selection** — "show me
November" is not "book the 1st".

**21 — draft survival:** draft survives a reload; the funnel returns to the
**profile**, not a dead hold; drafts are keyed per artist with no
cross-contamination; and with `sessionStorage` throwing on access the funnel
**still works**.

---

## 4. My own false results

Five, all mine, none a product defect. Recorded because §3.4c exists
precisely because this keeps happening.

| What it reported | What was true |
|---|---|
| **AUTH-11 FAIL** — "timed out" | A *valid* config boots and runs forever. The harness treated the absence of an exit as failure. A timeout is the success signal here. |
| **AUTH-12 FAIL** — "responses differ, oracle" | Artist and customer were byte-identical. The third request tripped the **rate limiter** — the limiter working, read as a leak. Now clears the phone's OTP history between probes. |
| **FRAUD-10** — "share preview renders" | It returns **302**. `urlopen` follows redirects by default and the harness saw the final 200. Now refuses to follow redirects. |
| **SPAM-06 FAIL** — foreign-key violation | I invented a guest placeholder UUID of all zeros. The real one ends `…ff`. |
| **E2E 20.2 FAIL** — "0 labels, overflow 0" | The pages had **bounced to /login** and the harness measured the login page. `overflow=0` was vacuous. Cause: the refresh cookie is `Secure`, so WebKit drops it over plain HTTP — the suite now runs over HTTPS, which is also what the artist uses. |

The last one is the most useful: an identity assertion turned a silent false
pass into an honest failure, which is exactly what rule 1 is for.

---

## 5. Not executed — do not read these as passes

- **AUTH-13** (squatting a number via guest booking) — needs a decision on
  what the intended behaviour *is* before it can have a verdict. A guest row
  for someone else's number is currently by design.
- **EDGE-01 / SPAM-01** volumetric load — still require written
  authorisation and an environment that is not a laptop.
- **CLIENT-02 / CLIENT-03** (CSRF, CORS) — unchanged since the 2026-09-18
  run; not re-executed.
- **INJ-01** SQLmap sweep — not re-run; no new query surface was added that
  interpolates input.
- The **delivered** path of the reconciler has still never executed. Every
  observation to date is of failure. Watch it the day Meta verification
  clears before trusting a green number.

---

## 6. State after the run

Everything the run touched was restored: 0 probe notifications, 0 test
discounts, 0 probe users, 0 held bookings, 0 renamed services, mkup4 back to
`comped`, and the OTP rows the run generated cleared.

34 Go packages pass · 38 frontend tests · `make verify` 37 checks.
