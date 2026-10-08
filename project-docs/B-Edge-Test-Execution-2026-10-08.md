# Execution report — both test plans extended, then everything run

**Date:** 2026-10-08 · **Run autonomously**
**Against:** the running stack — real API (`air`, `-tags devbypass`), PostgreSQL (migration 054), both Angular dev servers, WebKit and Chromium at 390px and 320px
**Code:** api `030ab59` and web `a14ad77` (the last commits, 2026-09-29), plus this run's changes
**Plans:** `E2E-TEST-PLAN.md` (b-edge-web) · `B-Edge-Security-Test-Plan-v1.md`

---

## 0. Result

**Both plans were extended for what changed since the last run, and every
executable suite was run.** The extension is E2E **Suite 28** (13 cases) and
security **§3.4f** (7 cases), aimed at the 28–29 September changes and at
what the 2026-10-08 status review found untested.

| | Result |
|---|---|
| Live checks, all suites | **333 pass, 8 fail** — every failure is in the new Suite 28, and they are 3 distinct security defects |
| Go — build, vet | clean |
| Go — unit tier, 41 packages | **1,153 pass** |
| Go — database tier (`-tags dbtest`) | **1,238 pass**, including the 6 new database tests |
| Go — `-tags devbypass` packages | 2 packages pass |
| Web — builds | clean (9 compiler lint warnings, NG8113/NG8102, none new) |
| Web — unit tests | **90 pass** (shared 49, customer-pwa 23, artist-dashboard 18) |
| Documentation check | 1 drift (Go tests 1,243 → 1,252, this run's 9 new tests) → updated |

**Found:** three security defects (§1), one latent booking defect, fixed
(§2), and one missing screen (§3). **No regression** in anything that passed
on 28–29 September.

---

> **Addendum, later the same day — all three security defects FIXED** (the
> founder approved). §8 has what changed and the full regression run that
> followed: the main fix changes what every signed-in request does.

## 1. Security defects found — fixed later the same day (§8)

### AUTH-22 — a frozen or suspended account keeps itself signed in

Login refuses a frozen account (`ACCOUNT_FROZEN`) and a suspended one
(`ACCOUNT_SUSPENDED`). **Refresh refuses neither.** `Refresh` checks the
refresh token and fetches the user but never reads `users.status`, and
changing the status does not revoke refresh tokens. Measured: both accounts
got a new, working session; the suspended one renewed three more times in a
row. A refresh token lives 7 days and rotates on use, so **an account
suspended while signed in stays signed in for as long as it keeps using the
app.** Suspension only stops someone who signs out.

Found by reading `internal/domain/auth/service.go` for this pass, then
measured — it was not on any list.

**Fix:** `Refresh` refuses a non-active status, and a status change revokes
the account's refresh tokens. **One decision first:** unfreezing is itself a
signed-in call, so if refresh also refuses a *self*-frozen account, a user
who froze herself can only come back through support. Suspension has no such
question.

### AUTH-14b — what a removed member's old login can still do, measured in full

On 2026-09-29 the old token was shown to ship an order. This run tried all
14 things a member can do, each **twice**: with the old token, and with a
fresh login after removal. A pass for the old token where the fresh login is
refused is the stale token's doing.

**5 of 14 get through:** reading the salon's **orders** (customer names,
phones, delivery pins), its **service menu** and its **team**, and
**shipping** and **delivering** an order (both changed the order). The other
9 are refused because their service code re-reads membership or ownership
from the database. The fix that was proposed on 2026-09-29 (re-check
membership on salon-scoped routes) now has an exact scope: the order queue,
ship, deliver, the services list and the members list.

### AUTH-20 — one invitation, two people

An invitation addressed to the **phone of artist A and the email of artist
B** is sent (201). B is refused (403 `INVITATION_NOT_FOR_YOU`) and A joins.
The invite and accept lookups both take "the first user matching phone
**or** email", `LIMIT 1` with no `ORDER BY`, so who the invitation is for is
decided by row order, not by the owner. **Fix:** refuse at invite when the
two contacts resolve to different accounts.

---

## 2. Fixed in this run — the waitlist told the wrong day on a UTC server (E2E 28.11)

A waitlist entry asks for a day on the salon's calendar. When a slot freed,
the cascade took the day from the booking's start instant in whatever zone
that instant carried — and pgx returns `timestamptz` in the process's zone.

- **Live, on this machine (Beirut time):** a booking at 01:30 Beirut on
  13 October was cancelled with people waiting for the 12th and the 13th;
  the 13th was told. Correct, by luck of the machine's zone.
- **As a UTC server would see it:** the new test hands the cascade the same
  instant in UTC and the cascade told the **12th**. Watched failing before
  the fix.

Fixed in `internal/booking/waitlist.go`: the day is taken in the store's time
zone (falling back to the old behaviour if the store cannot be read).
Deployment servers default to UTC, so this would have shipped.

---

## 3. A gap — customers cannot move an appointment from the app (E2E 28.10)

`PATCH /bookings/:id/reschedule` works (E2E 14.4 passes) but no screen in
either app calls it. A customer who cannot make it can only cancel. Added to
the plan's coverage map as **NO UI PATH**.

---

## 4. Results by suite

| Suite | Result | Notes |
|---|---|---|
| UC-1 guest booking | 20 pass | |
| UC-2 money | 11 pass, 1 skip | M4 skips by design: the booking's customer has an unusable phone, so the guard correctly stays silent |
| UC-6 enforcement · UC-7 owner/member | 7 + 7 pass | |
| Chaos booking | 25 pass, 2 informational | features B-Edge deliberately lacks |
| Security batch 1 · batch 2 | 7 pass · 6 pass, 1 informational | |
| Security salon (§3.4d/e) | 14 pass, 1 undecided, 1 informational, 1 skip | undecided = AUTH-14 (measured in full by AUTH-14b); skip = DATA-05, SMS not provisioned |
| E2E 22 multi-artist | 14 pass, 1 informational | |
| E2E 23 salon over time | 18 pass, 1 to decide | 23.7f is AUTH-14's reads |
| E2E 27 + 16.5 + FRAUD-18/19/21/22 | 15 pass *(after a harness fix, §5)* | |
| FRAUD-20 (opt-in) | pass | 14 codes answered (6 already spent by 27.7), then 429 on both previews; the rest of the API 200 |
| **E2E 28 + security §3.4f** *(new)* | **20 pass, 8 fail, 1 gap** | the 8 failures are AUTH-14b (5), AUTH-22 (2), AUTH-20 (1); identical in three runs |
| FRAUD-23 (opt-in, new) | pass | budget spent, all three address headers forged → still 429 `TOO_MANY_CODE_ATTEMPTS` |
| E2E 2–16 journeys | 93 pass, 4 skip | 9.1 "later today" (run at 23:04 Beirut), 11.7, 13.5 (now covered by 28.12), 14.8 |
| UI — phone verification | 6 pass, 1 skip | the account was verified on 2026-09-23 |
| UI — per-artist services | 23 pass *(alone)* | 13 pass, 2 fail when run straight after the API suites |
| UI — E2E 22.6 at 390/320px | 14 pass *(alone)* | 0 pass, 2 fail (login timed out) back to back |
| UI — Suites 1–16 screens | 28 pass | |
| **UI — Suite 28.1–28.3** *(new)* | **3 pass** | the join page logged out, as someone else, and as the invitee |
| **Go — 28.11, 28.12, 28.13** *(new)* | 7 tests pass | 1 unit (waitlist day), 3 database (failed-delivery alert), 3 database (inbox bundling); each watched failing first |
| **Go — mutation gaps** *(new)* | 2 tests pass | membership: Leave revokes tokens; the daily cap's 24-hour window (§6) |

**Mutation testing** (`gremlins`, timeout coefficient 100) on the two
packages this period touched most: see §6.

---

## 5. The harness's own mistakes, and what was done about each

- **Suite 27 died two cases in** with an `IndexError`. It ran straight after
  the other API suites, the API's own 600-requests-per-5-minutes budget for
  localhost was spent, the slot list came back as a 429, and the harness
  read it as "no open times". Confirmed with a direct request
  (`RATE_LIMIT_EXCEEDED`). Suite 27 and Suite 28's setup helpers now wait
  out the general limit — and only that code, since `TOO_MANY_HOLDS` and
  `TOO_MANY_CODE_ATTEMPTS` are answers the suites assert on. Re-run: 15 pass,
  after waiting the window out five times.
- **Two UI scripts failed back to back and passed alone.** The same pattern
  the 2026-09-28 run traced to the rate limit; the sign-in worked in both
  engines when probed a few minutes later. These scripts do not log status
  codes, so this time the cause is likely, not proven.
- **Suite 28's first run counted "read clients" as a leak** (200 for both
  tokens). The client list is per artist, so a removed member with no
  clients of her own gets an empty list from any token. Each read now
  counts as a leak only if the salon's own data comes back. Its first run
  also used a route that 404s for anyone not yet onboarded to check
  whether a session worked, and its first phone numbers collided with an
  earlier throwaway run's; both corrected before any result was recorded.
- **28.9 expected 422 for every bad onboarding field.** The price answers
  400 `INVALID_SERVICE_PRICE` (the money parser's status, not the
  validator's) while still naming the field. The point of the case — the
  server refuses what the form explains — holds; the 400 is recorded as an
  observation, and the plan says so.

---

## 6. Mutation testing

`gremlins`, timeout coefficient 100, on the package the 29 September change
touched (membership) and the one 28.13 now covers (inbox, with `-tags
dbtest` so its new database tests count).

| Package | Killed | Lived | Not covered | Efficacy |
|---|---|---|---|---|
| `internal/membership` — first run | 93 | 11 | 67 | 89.4% |
| `internal/membership` — after 2 new tests | **98** | **6** | 67 | **94.2%** |
| `internal/inbox` | 11 | 1 | 25 | 91.7% |

**No mutant survived in `assertInvitee`**, the D26 rule. Two survivors were
real gaps and are now pinned:

- **Leaving a salon revokes the leaver's refresh tokens** — nothing asserted
  it (`if s.tokens != nil` negated in `Leave` survived). It is the same
  defence AUTH-14 leans on. `TestLeave_Member_LosesHerRefreshTokens`.
- **The daily invitation cap counts exactly the last 24 hours** — the mock
  ignored the window, so `+24h` and `-24ns` survived (3 mutants).
  `TestInvite_DailyCap_CountsTheLast24Hours`.

The 6 left are error-propagation and best-effort branches (a failed
notification queue, a failed redaction, the preview's `needs_signup` lookup
failing, an unexpected repository error on accept) and a pluralisation
helper; the inbox survivor is the empty-level default. Recorded, not chased.

---

## 7. Not executed — do not read these as passes

11.7 and 14.8 (a real crawler; real phone calendars) · 13.5 live (a forced
Twilio failure through the running worker — the behaviour is now covered by
28.12's database tests) · 9.1 "a window later today" (run at 23:04) ·
DATA-05 (no SMS sender is provisioned) · phone-verification step 5 (needs an
unverified roster account) · 4.1's real image upload (it would go to
Cloudinary) · performance against the PRD's targets (nothing is deployed).

---

## 8. Addendum — the three fixed, and everything re-run

**AUTH-14b and AUTH-22 — every signed-in request re-reads the account
(decision D28).** `RequireAuth` still verifies the token, then asks
`middleware.Standing`, installed by `domain/auth.RegisterRoutes`, what the
account is now: the salon and salon role handlers see come from the database
(the same load and `salonrole.Resolve` derivation as login), a suspended
account is refused with 403 `ACCOUNT_SUSPENDED`, a deleted one with 401, and
an unreadable account fails closed (500). One indexed lookup per request.
Chosen over shortening the token, which narrows the window without closing
it, and over per-route checks, which is how five routes came to leak while
nine did not. It also closes AUTH-15 (a transferred owner's old token now
carries member rights at once). `Refresh` additionally refuses a suspended
account.

**A frozen account keeps its session (decision D27).** The case had
expected otherwise; the product's freeze screen says "You can undo this
right here, but once you sign out while frozen, you won't be able to log
back in yourself", and unfreezing is a signed-in call. Login still refuses a
frozen account.

**AUTH-20 — an invitation's two contacts must be one account.** Given both
a phone and an email, `Invite` resolves each separately and refuses with
409 `CONTACTS_DISAGREE` unless they name the same account (a contact nobody
holds is refused too, or a later registration would reopen the ambiguity).

**Web.** Both apps' error interceptors now end the session on 403
`ACCOUNT_SUSPENDED` as on 401 (`endsTheSession`), so a suspended artist is
sent to sign-in instead of a dashboard where every call fails; any other 403
still leaves her signed in.

**Tests added, each watched failing first:** 6 middleware
(`standing_test.go`: removed member, transferred owner, suspended, deleted,
unreadable, nothing installed), 10 auth (`standing_test.go`: the standing
for owner/member/none/suspended/frozen/gone/error, refresh suspended and
frozen, and that `RegisterRoutes` installs the check), 3 membership
(`contacts_test.go`) + 1 database (`ContactOwners`), 6 web
(`auth-error.interceptor.spec.ts`). Swagger regenerated (`/auth/refresh`
documents its 403).

### The regression run after the fixes

D28 changes what every signed-in request does, so everything was run again
on the fixed build, suites spaced for the rate limit.

| Suite | After the fixes | Change from the first run |
|---|---|---|
| Go — unit · database tier | **1,174 · 1,260 pass**, vet clean | +21 · +22 tests: the fixes' 20 (19 unit, 1 database) and §6's two mutation-gap tests |
| Web — unit (shared · customer-pwa · artist-dashboard) | **55 · 23 · 18 pass** | +6 (the interceptor spec) |
| UC-1 · UC-2 · UC-6 · UC-7 | 20 · 11 (+1 skip) · 7 · 7 pass | none — UC-7 swaps a salon's owner, so it exercises the new role derivation |
| Chaos booking | 25 pass, 2 informational | none |
| Security batch 1 · batch 2 | 7 · 6 pass (+1 informational) | none |
| Security salon | **15 pass, 0 undecided** (1 informational, 1 skip) | AUTH-14 **UNDECIDED → PASS**: the old token reaches nothing, writes included |
| E2E 22 · 23 · 27 | 14 · **19** · 15 pass | 23.7f **INFO → PASS**; 0 left to decide |
| **E2E 28 + §3.4f** | **28 pass, 0 fail**, 1 gap (28.10) | 8 failures → 0 |
| E2E 2–16 journeys | 93 pass, 4 skip | none |
| UI — phone · per-artist services · 22.6 · Suites 1–16 screens | 6 (+1 skip) · 23 · 14 · 28 pass | none — and run spaced, the two scripts that failed back to back pass first time |
| **UI — Suite 28** | **4 pass** | +28.8: an owner suspended while signed in is on `/login` after one click (the interceptor change, watched failing in its unit spec; this browser case was not run against the old build) |

Two harnesses had to learn the decision rather than the code changing
under them: `verify-security-salon` and `e2e-suite23` reported a removed
member's old token as UNDECIDED / INFO whatever it reached. They now pass
only when it reaches nothing, and fail on any read as a regression of D28.
