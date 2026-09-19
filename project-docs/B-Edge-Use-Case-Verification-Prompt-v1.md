# B-Edge — Use-Case Verification Prompt v1

> A reusable prompt for verifying that B-Edge does what it is supposed to do.
> Paste the section marked **THE PROMPT** into a fresh session. Everything
> before it explains why the prompt is shaped the way it is; everything after
> it is reference material the prompt depends on.
>
> Written 2026-09-19, after a testing session with the launch artist surfaced
> defects that earlier testing had not — despite a lot of earlier testing.

---

## 1. Why the previous testing missed things

This matters more than the prompt itself, because a prompt that does not
target the actual failure modes just produces more of the same testing.

Every defect that reached the artist had one of five shapes. **None of them
was a broken happy path.** The happy path has been exercised many times and
it works.

| # | Shape | What it looked like here |
|---|---|---|
| 1 | **Alternate flow nobody modelled** | A deposit paid from a number other than the customer's. The refund path assumed payer = customer, silently, everywhere. |
| 2 | **Exception flow with no observation** | Every WhatsApp message failed for six weeks. `notifications.status` said `sent`, because `sent` meant "Twilio returned 2xx". The system could not see its own total failure. |
| 3 | **Environment variation** | Hour labels rendered as a single letter "M" — on a phone, in WebKit. Desktop Chrome, where all the development happens, was fine. |
| 4 | **A test that asserted the wrong thing** | A UI sweep reported four routes clean. Three of them did not exist and were the 404 page. A render check is not an identity check. |
| 5 | **Cross-screen inconsistency** | Hours listed the week Sunday-first (because the database counts `day_of_week` from 0), Calendar Monday-first. Each screen is correct alone; together they are wrong. |

Two structural causes sit underneath all five:

**There is no written model of what B-Edge is supposed to do.** Testing has
been "open screens and look for anything odd", which finds only what someone
happens to look at. There is no list of actors, no list of use cases, and no
enumeration of alternate and exception flows — so there is nothing to test
*against*, and no way to know what was not tested. The research consensus is
blunt about this: a use case with no extensions is **incomplete**, because
real systems have exceptions ([Visual Paradigm](https://www.visual-paradigm.com/guide/the-comprehensive-guide-from-use-case-to-test-case-via-uml/)).
Almost every defect above lived in an extension.

**The frontend regression net is thin.** `ng test` gives **33 passing tests
across 41 routes** — real, but nowhere near enough to hold the behaviour
this product depends on. Most fixes are held by nothing, so they are
verified once by hand and then decay.

> An earlier version of this section said the suite was *broken* — "19
> failed / 6 passed". That was wrong, and instructively so: it came from
> running `npx vitest run` directly, which bypasses Angular 21's
> `@angular/build:unit-test` builder. The tool reported real errors about a
> setup it had loaded incorrectly, and I read them as facts about the
> project. **Running the wrong command and believing its output is the same
> mistake as trusting a status column** — see rule 4 below, which now
> applies to the verifier as much as to the system.

The prompt below is built to attack exactly these.

---

## 2. What the prompt produces

Three artefacts, in this order. Each one is input to the next.

1. **A use-case model** — actors, use cases, and for every use case its
   basic flow, alternate flows and exception flows. UML use-case and
   activity diagrams as Mermaid, so they live in the repo as text and diff
   like code.
2. **A test derived from every flow** — not every use case. A use case with
   four flows yields at least four tests. This is the step that converts
   "we tested the booking flow" into "we tested the eleven ways the booking
   flow can go".
3. **A traceability matrix** — flow → test → result → evidence. Its job is
   to make *untested* visible. A flow with no test is the finding.

---

## 3. THE PROMPT

> Copy everything between the rules into a fresh session.

---

You are acting as a **requirements and verification engineer** on B-Edge, a
Lebanese beauty booking platform. Two repositories, side by side:
`b-edge-api` (Go 1.26 / Fiber / PostgreSQL, no ORM) and `b-edge-web`
(Angular 21 monorepo: `customer-pwa`, `artist-dashboard`, `shared`).

Your job is **not** to find bugs by looking at screens. It is to build a
model of what this system is supposed to do, derive tests from that model
systematically, execute them, and report what is verified, what is broken,
and — most importantly — **what is not covered**.

### Ground rules, in priority order

**1. Derive, do not guess.** Read the code, the migrations, the handlers and
the templates to establish what the system actually does. Where behaviour is
ambiguous, say so and pick the reading a careful reader would; never invent
a requirement to test against.

**2. A use case without alternate and exception flows is incomplete.** For
every use case you identify, enumerate:
- the **basic flow** (everything works),
- every **alternate flow** (a different but valid path),
- every **exception flow** (something fails, is missing, is refused, times
  out, arrives twice, or arrives from the wrong party).

If you list a use case with only a basic flow, you have not finished it.
Historically, every defect that reached a real user of this product lived in
an alternate or exception flow.

**3. Assert identity before measuring anything.** Before you record that a
screen is correct, assert you are on that screen — check a heading, a route,
a known element. A previous audit reported four pages clean; three of them
were the 404 page. A render check is not an identity check.

**4. Verify the effect, not the status field.** Never accept the
application's own report of success as evidence. If a notification says
`sent`, ask the provider whether it was delivered. If a save returns 200,
read the row back. The most expensive defect in this product's history was a
status column that said `sent` while the delivery rate was zero.

**5. Test on the surface the user actually holds.** This is a phone product
for Lebanon. Exercise every user-facing flow in **WebKit at 390px** as the
default, not desktop Chrome. Playwright's WebKit is installed. Check 320px
for anything dense. Desktop is the secondary case, not the primary one.

**6. Check across screens, not only within them.** Two screens that are each
correct alone can disagree with each other — week start, status vocabulary,
a price shown one way here and another there. Consistency is a property of
the set.

**7. Timezone, money and phone numbers are where this system breaks.** Every
time is `Asia/Beirut` wall-clock and must be tested near midnight. Every
money value crosses the wire as a decimal string. Every phone number must be
E.164-normalised before comparison. Where you test one of these, test the
boundary, not the middle.

### Method

**Step 1 — Actors and use cases.** Identify every actor (guest customer,
registered customer, artist, admin, background worker, external provider)
and every use case each one participates in. Produce a **UML use-case
diagram as Mermaid**, grouped by actor, with `include` and `extend`
relationships shown where they exist.

**Step 2 — Flow enumeration.** For each use case, write the basic flow as
numbered steps, then every alternate and exception flow. For the six
critical paths listed in §4 below, also produce a **UML activity diagram**
(Mermaid `flowchart`) showing every branch, and a **sequence diagram**
(Mermaid `sequenceDiagram`) across Angular → Go → PostgreSQL → external
provider where more than one system is involved.

**Step 3 — Derive tests.** One test per flow, minimum. For each, state:
- preconditions (data that must exist),
- the exact steps,
- the expected result **as an observable fact**, not a feeling,
- how you will verify it (which query, which API call, which assertion).

**Step 4 — Execute.** Run them. Against the running stack, with real data,
on WebKit at phone width for anything user-facing. Record actual results
verbatim, including the ones that pass.

**Step 5 — Report.** A traceability matrix: use case → flow → test → result
→ evidence. Then, separately and prominently:
- **defects**, each with the flow it belongs to and the failing evidence,
- **flows with no test**, and why (blocked, out of scope, needs hardware),
- **flows that cannot be verified from here** (real WhatsApp delivery, real
  device gestures, a real payment), stated as unverified rather than
  approximated.

### Hard rules on honesty

- Report what you did **not** cover as prominently as what you did. An
  incomplete matrix that says so is worth more than a complete-looking one
  that quietly skipped things.
- If a test passes for a reason you did not expect, investigate before
  recording it as a pass.
- If you cannot reach a flow (no data, no credentials, no device), say so.
  Do not substitute a weaker test and let it stand for the real one.
- Distinguish **defect** (it does not do what it should) from **gap** (there
  is no requirement covering this) from **decision** (it behaves this way on
  purpose and here is where that is recorded).

### Deliverable

A single markdown document in `b-edge-api/project-docs/`, containing the
diagrams as Mermaid, the flow enumeration, the traceability matrix, and the
findings. Do not put it in `docs/` — that directory is gitignored swagger
output and anything written there is lost.

---

## 4. The six paths to model first

Ordered by what it costs when they fail. Work down, not in parallel.

| # | Path | Why first | Known exception flows to cover |
|---|---|---|---|
| 1 | **Guest books an appointment** | The only revenue path that works today. Customer login is blocked on WhatsApp, so guest booking IS the funnel. | slot taken between view and submit; double-submit; store closed that day; same-day notice; booking spans closing time; early-bird surcharge; discount code; browser reload mid-funnel (state is in memory and is lost) |
| 2 | **Artist approves → deposit → confirm** | Where money enters. | deposit never arrives; arrives partially; arrives from a different number; arrives after the deadline; artist confirms twice; booking cancelled between approve and deposit |
| 3 | **Cancel and refund** | Where money leaves, irreversibly. OMT/Whish have no chargeback. | cancel inside 24h; cancel by artist vs customer; refund to a payer ≠ customer; refund marked twice; refund owed but never sent |
| 4 | **Availability and hours** | Wrong here means double-booking or invisible availability. | DST boundary; near midnight Beirut; special-hours override; store inactive; buffer/cleanup time; cross-store travel buffer; a booking that no longer fits its own window |
| 5 | **Notifications** | Currently 0% delivered and the system cannot see it. | provider rejects; provider accepts then fails to deliver; recipient has no phone; duplicate on retry; scheduled reminder for a booking since cancelled; message outside WhatsApp's 24h window |
| 6 | **Subscription and access** | Determines whether an artist can work at all. | grace → past_due → suspended transitions; artist hidden from discovery; write blocked while read allowed; comped account; clock at the exact boundary |

---

## 5. Reference: what is already known

Give this to the session so it does not rediscover it.

**Already fixed, do not re-report:** the same-day-notice UTC bug (now
`openinghours.IsSameDayIn`); iOS zoom on sub-16px inputs; sub-44pt tap
targets; the 28-day booking horizon (now 90 days); the Sunday/Monday week
inconsistency; calendar hour labels clipping on mobile.

**Known and deliberate:** B-Edge holds no money — deposits move
customer-to-artist over OMT/Whish directly; state is derived, never stored;
there is no scheduler beyond supervised sweep workers; ownership failures
return 404 rather than 403 so ids cannot be enumerated; repository-layer
tests are out of scope because no DB test infrastructure exists.

**Known and broken, no need to re-verify:** WhatsApp delivery is 0% pending
Meta business verification, so customer OTP login cannot work.

**Known and NOT broken, despite an earlier claim:** the frontend test suite
runs. Use `ng test <project>` — 33 tests, all passing. `npx vitest run` is
the wrong entry point for Angular 21 and its failures are meaningless.

**Traps that have already cost time:**
- `@bedge/shared` resolves to `./dist/shared`, so `ng build shared` must run
  before the apps or changes are silently ignored.
- `bookingSelectCols` and both scan functions in `internal/booking/repository.go`
  are hand-maintained; a new column added to one and not the others reads as
  a zero value forever.
- Playwright matches the **most recently registered** route first, so a
  catch-all registered after a specific stub swallows it.
- The customer discovery page is at `/`, not `/discover`. `/bookings` and
  `/profile` do not exist.
- `GET /bookings/slots` returns **only bookable slots** — there is no
  `available` field to filter on. Filtering for one yields zero results and
  looks like "no availability".
- The hours exception table is `business_hours_exceptions` with an
  `exception_date` column. There is no `special_hours` table.
- Run test SQL through something that **surfaces stderr**. A helper that
  swallowed it turned a failed INSERT into a non-existent table into a
  confident false finding.

---

## 6. Definition of done

This work is finished when:

1. Every actor and use case is listed, with diagrams.
2. Every use case has its alternate and exception flows enumerated.
3. Every flow has a test, or an explicit recorded reason why it does not.
4. Every test has been executed and its actual result recorded.
5. The traceability matrix shows coverage, and the gaps are named.

It is **not** finished when the screens look fine.
