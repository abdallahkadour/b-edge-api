# Execution report — E2E Suites 1–16, and the invitation decision

**Date:** 2026-09-29 · **Run autonomously**
**Against:** the running stack — real API (`-tags devbypass`), PostgreSQL (migration 054), the two Angular dev servers, Chromium at 390px
**Plans:** `E2E-TEST-PLAN.md` Suites 1–16 · `B-Edge-Security-Test-Plan-v1.md` AUTH-14/16, FRAUD-11

---

## 0. Result

**Suites 1–16 were executed end to end, and are now executable.** Until
today they existed only as manual journeys.

| Half | How | Result |
|---|---|---|
| Behaviour — Suites 2–16 | `make e2e-journeys` (`scripts/e2e-journeys.py`) | **93 pass, 0 fail**, 4 stated skips, residual 0 |
| Screens — Suites 1, 2, 4, 5, 9, 10, 13, 14, 15 | `node scripts/e2e-journeys-ui.mjs` (b-edge-web) | **28 pass, 0 fail** after one fix, residual 0 |

**One defect found and fixed** (1.3). **One security gap measured** that the
plan had recorded as safe (AUTH-14). **One founder decision implemented**
(D26). Three things found and not fixed are in §3.

---

## 1. The defect — onboarding said nothing about why it would not submit (1.3) · **fixed**

"Submit for review" stays disabled until the handle is 3+ characters, the
price is a plain amount and the duration is 15–480 minutes. For an empty
handle, a negative price or a zero duration, the button simply greyed out —
nothing on screen said which field was wrong, which is exactly what 1.3
asks for. The form now says it under the field ("At least 3 characters…",
"Enter a duration from 15 to 480 minutes.", "Enter a price like 50 or
50.00."). A taken handle already showed a proper field error.

---

## 2. Decisions implemented

**D26 — an invitation is for the person it was sent to.** Only the account
the invitation's phone names may accept it, and declining needs that
account (it was public). Anyone else: 403 `INVITATION_NOT_FOR_YOU`.
Security FRAUD-11 and AUTH-16 move from UNDECIDED to **PASS**; E2E 23.7d
likewise. The join page no longer offers "Decline" to someone who is not
signed in, shows a refused decline instead of swallowing it, and the
artist help gained the two topics the team feature never had.

---

## 3. Found, not fixed

### AUTH-14 is worse than recorded — a removed member can still WRITE for 15 minutes

The plan (2026-09-22) says a removed member's token "still READS … **No
writes get through**." That checked five endpoints. Measured today with the
two that are open to every member:

- her old token lists the salon's orders — customers' names, phones and
  delivery pins — **200**;
- and **marks an order shipped — 200, the order became `shipped`**.

Why: login tokens live 15 minutes and carry the salon; nothing re-checks
membership against the database per request, and removal revokes only the
refresh token. The same applies to deliver. Recommended fix: check that the
caller is still in the salon the token names on salon-scoped routes (one
indexed query, cacheable for seconds). Awaiting the founder.

### The waitlist cascade keys on the server's time zone — unverified

`cascadeWaitlist` takes the freed booking's calendar date in the process's
time zone, while a waitlist `requested_date` is a salon-local date. On this
machine the process runs in Beirut time, so it cannot misbehave here. On a
server in UTC, a booking between 00:00 and ~03:00 Beirut would notify the
wrong day's queue. Found by reading the code; not measured.

### No automated test covers the failed-delivery alert (13.5), and none covered inbox bundling

13.5 needs a forced Twilio failure through the running worker and was not
staged. Inbox bundling (13.1) had no test either; the journey now runs the
repository's own `ON CONFLICT` statement 100 times and concurrently, but a
Go test in `internal/inbox` would be the proper home.

---

## 4. Results by suite

| Suite | Result | Notes |
|---|---|---|
| 1 onboarding | 11 pass *(after the 1.3 fix)* | 1.4's redirect target is Profile by design; plan corrected |
| 2 stores, hours, services | 9 pass | open-after-close and `09:00` refused cleanly; deleting a service keeps its history |
| 3 guest booking | 14 pass | hold 10 min, double hold 409, expired hold 409 `HOLD_EXPIRED`, travel buffer: 0 times inside 150 min, 19 after |
| 4 shop | 7 API + 3 screen pass | a shell script named `.jpg` refused `INVALID_IMAGE`; shipped orders not cancellable |
| 5 customer account | 6 pass | sign-in through the screens; a lapsed pending booking is under Past |
| 6 clients · 7 earnings | 3 + 1 pass | notes replace, not append; earnings equal a hand sum, cancelled excluded |
| 8 billing | 10 pass | the whole invoice cycle; past_due hidden but editable; suspended blocked (403) |
| 9 open/closed | 7 API + 3 screen pass, 1 skip | badge, New York device shows Beirut time, unknown hours → no badge; DST 07:00Z/06:00Z |
| 10 portfolio tags | 6 API + 1 screen pass | another salon's service refused without naming it; ownership is 404 |
| 11 share previews | 6 pass, 1 skip | suspended → 302; escaping; 11.7 needs a real crawler |
| 12 bulk shift | 8 pass | the deferrable constraint in raw SQL; 50 bookings; 20 concurrent agree |
| 13 notifications | 7 pass + bell screen, 1 skip | read-all is per user; titles over 200 refused; cascade on delete |
| 14 calendar links | 8 pass + page, 1 skip | CRLF, 75-octet folding with Arabic and emoji; reschedule bumps SEQUENCE; cancelled still resolves |
| 15 buffer | 5 pass + screen | the database refuses a booking inside cleanup; early completion frees it at once |
| 16 waitlist | 3 pass | every freeing event notifies; the **live** stall sweep cleared a stuck queue in 91 s |

---

## 5. My harness's own mistakes, in the order made

Recorded because a report that hides its false results cannot be trusted
on its passes. Each was caught by reading the evidence before believing it:
reading the URL mid-redirect (1.1/1.2); reusing a soft-deleted sign-up
email (1.1); clicking a disabled button (1.3); expecting the audit action
`approve` instead of `approved` (1.5); holding start times that overlap a
60-minute booking (3.3, twice); two "next weekday" dates landing on the
same Monday (3.5); a new artist's live trial kept her visible (11.4, and
8.1's premise); psql's words instead of the error code (12.1); blocked
bookings live in `blockers`, not `movable` (12.4); the feed is an object
(13.3); rescheduling is the customer's action (14.4); the store name is in
SUMMARY, not LOCATION (14.2); the robots rule is a meta tag (14.6); the
waitlist is keyed to the freed booking's day (16.1); uppercase-styled text
and whitespace in button labels (5.2, 9.1); and the API's own rate limit
when the whole script ran twice inside five minutes — it now waits out
`RATE_LIMIT_EXCEEDED` (and only that code).

---

## 6. Not executed — do not read these as passes

11.7 and 14.8 (a real WhatsApp/Instagram crawler; real Apple/Google/Outlook
calendars) · 13.5 (forced delivery failure) · 9.1 "ended earlier today" and
12.4 "starts exactly now" (time of day) · 11.5's database-outage variant ·
4.1's real image upload and the 15 MB resize offer (the upload would go to
Cloudinary) · the screens of 3.3 (expired hold) and 3.7 (fee messaging),
whose behaviour is covered · 12.5's 23:30 UTC case · 16.3's "run twice"
(covered by the waitlist worker's mocked tests).
