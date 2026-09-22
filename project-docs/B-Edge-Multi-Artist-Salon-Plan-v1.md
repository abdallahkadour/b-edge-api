# B-Edge — Multi-Artist Salon Support
## Implementation Plan · v1

**Date:** 2026-09-21 · **Status:** Draft for approval
**Implements:** BRD · HLD · LLD · Database Structure (all `-v1`)
**Shape:** 6 phases, 34 tasks. Phases 0–2 are independently shippable and carry most of
the value. Phases 3–5 change money and can be deferred without blocking Rania's second
artist.

---

## Status — 2026-09-21

**Phase 1 is complete and verified against the running stack. Phase 0 engineering is
done apart from the slot baseline, which is still capturing; three founder decisions
remain open and block only Phase 3.**

| Task | State | Evidence |
|---|---|---|
| **T0.1–T0.3** decisions | Open | D-MS1, D-MS3, D-MS6 need the founder. Nothing before Phase 3 depends on them. |
| **T0.4** verify I2, resolve the orphan | **Done** | All 5 salons pass I2; `owner_id` referentially clean, so migration 048 needs no repair pass. Orphan is `testartist@bedge.com` — inert, left alone, **but its 1 subscription blocks migration 050**. See the database doc §2.6. |
| **T0.5** slot baseline | **Recapturing (2nd attempt)** | `scripts/capture-slot-baseline.py` → `project-docs/slot-baseline-pre-046.json`. 31 triples × 90 days = 2,790 samples at 1.2/s, ~39 min. **Gates T2.6, nothing earlier.** Two earlier attempts were discarded — see below. |
| **T1.1** `internal/pkg/salonrole` | **Done** | 17 capabilities × 2 roles, leaf package, `Can`/`Resolve`/`All`/`Valid`. |
| **T1.2** matrix tests | **Done** | 14 tests: completeness both ways, owner ⊇ member, `None` holds nothing, the BR-8 list restated independently of the map. |
| **T1.3** anti-drift guard | **Done** | `TestNoInlineOwnerChecks` parses `internal/`. **Proven to fire** — an injected probe was caught and named. |
| **T1.4** `SalonRole` claim | **Done** | Derived at token issue from `salons.owner_id`; both user queries pick up `owner_id` in the same round-trip. |
| **T1.5** `RequireSalonCapability` | **Done** | 403 `SALON_ROLE_FORBIDDEN`; 10 middleware tests including fail-closed without `RequireAuth`. |
| **T1.6** apply guards | **Done** | 18 salon writes guarded across `artist`, `promo`, `payout`, `product`, `media`. |
| **T1.7** route-coverage test | **Done** | Parses route registrations; **proven to fire** on an injected unguarded route. |
| **T1.8** frontend | **Done** | `salonRole` / `isSalonOwner` signals, `salonOwnerGuard`, 4 routes guarded, nav filtered. |

**Live verification:** `make verify-uc7` — 7 of 7 pass. Same account, same routes, role
toggled by moving `salons.owner_id`: 10 owner-only writes refused, the refused write left
the row byte-identical, no stray rows created, the owner unaffected, the member still able
to work. Ownership restored and re-verified.

**Suite:** 35 Go packages pass (34 + `salonrole`) · 42 frontend tests · `go vet` clean ·
`make swagger` regenerated.

### Phase 2 — COMPLETE (14 of 14)

| Task | State | Evidence |
|---|---|---|
| **T2.1–T2.3** migrations 046–048 | **Done** | Applied up → down → up on a scratch database **and on a `pg_dump` copy of the real database** (5 salons, 6 artists, 38 bookings, all intact after both directions). All 16 new constraints exercised individually and asserted **by constraint name**. |
| **T2.4/T2.5** `internal/pkg/schedule` | **Done** | Pure interval algebra, 20 tests, **100% coverage**. First test in the file is the identity property. |
| **T2.6** intersection wired into slots | **Done** | Step 1.5 of `GetAvailableSlots` + `internal/booking/artist_rota.go`. 10 golden-output tests in the `slots_golden_test.go` style. |
| **T2.7** baseline comparison | **Done — PASSED** | 2,790 samples compared against `slot-baseline-pre-046.json`. **0 genuine slot-content differences.** All 24 reported differences were on the capture date, which is not comparable across runs (below). The 2,766 stable samples were byte-identical. |
| **T2.8** `internal/membership` | **Done** | model, repository, service, handler, routes, adapters. 33 tests. |
| **T2.9** `CompleteIntoExistingSalon` | **Done** | A branch inside onboarding's transaction sharing `insertArtist`, not a second path. |
| **T2.10/T2.11** lifecycle + removal/transfer | **Done** | Covered by the 33 above, including phone-equivalence, token reuse, and both-parties token revocation on transfer. |
| **T2.12** rota CRUD | **Done** | `GET/PUT /artists/me/schedule` + exceptions, `own_schedule:write`. 11 tests. |
| **T2.13** frontend | **Done** | `MembershipDataService`, Team, My hours, public `/join/:token`. Both apps build; 42 frontend tests pass. |
| **T2.14** E2E suite 22 | **Done** | Five cases in `E2E-TEST-PLAN.md`, written so **22.5 is read first**. |

**Not yet applied to the live database.** Migrations 046–048 are held until the
baseline capture completes, so that `slot-baseline-pre-046.json` is literally what its
name says.

### Migration 048 was unnecessary, and the scratch run is what found it

The database document stated that `salons.owner_id` had no foreign key and proposed
adding one. **It already has one** — `001_initial_schema.up.sql:40` declares it inline as
`REFERENCES users(id)`, which Postgres named `salons_owner_id_fkey`, and
`002_indexes.up.sql:20` already indexes the column. Running the full migration set against
a scratch database returned `constraint "salons_owner_id_fkey" already exists`.

048 is now comment-only. The integrity work was done in 001; what was genuinely missing
was the statement of intent — nothing in the schema said this column decides who may
change a salon's prices.

### A duplication worth recording, not fixing here

`apperror.Forbidden("NO_SALON", …)` appears at **38 sites across 8 domains**, and it had
**already drifted into two messages** before this work started: 37 said *"You are not
associated with a salon"* and the middleware I added said *"No salon is associated with
this account"*. Mine are now aligned to the existing wording, so no third variant exists.

Extracting all 38 into one constructor is the right end state and is **deliberately not
being done mid-feature**: touching 38 call sites across 8 domains in the same change as a
new authorisation boundary is how unrelated regressions arrive. `NO_SALON` describes the
caller rather than an object, so unlike the 403-vs-404 case it is not an enumeration
oracle — this is a consistency defect, not a security one. Worth a small, separate change.

### T2.7 passed, and found a bug on the way

**The gate: 0 content differences across 2,766 stable samples.** An artist with
no rota gets byte-identical availability before and after migration 047. That is the
property the whole feature rests on, and it is now measured rather than argued.

Before committing 39 minutes to the run, the snapshot binary was checked to actually
contain the change: inserting a 14:00-16:00 rota took one artist's day from **36 slots
to 8**, and removing it restored 36. A comparison against an unchanged binary passes
having tested nothing.

**All 24 reported differences were on day 0**, and they were two things:

1. **Day 0 is not comparable across runs.** Same-day minimum notice makes today's
   availability shrink with the clock, so a baseline taken at 05:00 and a comparison at
   07:00 disagree about today whatever the code does. `compare()` now skips the capture
   date and says so loudly - a comparison that quietly drops samples stops being a gate.

2. **A real pre-existing bug, surfaced by the shape of the disagreement rather than its
   content.** `slots.go` built its result with `var slots []*TimeSlot`, a nil slice,
   which marshals to JSON `null`. CLAUDE.md states `make([]*T, 0)` as an enforced
   convention and this function was the exception. It had stayed invisible because the
   two early returns already send `[]*TimeSlot{}` - so a **closed store answered `[]`
   while an open store with nothing left answered `null`** - and `customer-pwa` never
   saw it because `ApiService.getArray` coalesces the difference away. Which is exactly
   the fragility the convention warns about: correctness depending on which client
   helper the caller happened to pick.

   Fixed, and pinned by `TestGoldenRota_EmptyResult_IsAnEmptySliceNotNil` across all
   four ways a day can come back empty. Verified live: the endpoint now returns
   `{"data":[]}`.

Neither was caused by the multi-artist work. The comparison was built to detect a
regression and instead found a latent defect nothing else was looking for, which is a
fair argument for the cost of building it.

### The slot baseline took three attempts — both failures were mine

Recorded because a baseline is only worth what the process behind it is worth, and this
is the artefact the whole of Phase 2 is gated on.

1. **Attempt 1 — 2,191 of 2,790 samples returned 429.** The harness fired at 8-way
   concurrency against a limiter of 600 requests per 5 minutes
   (`middleware/register.go`). Every one of those would have been recorded as a day with
   no availability by a harness that treated a failed call as an empty list. This one did
   not, which is the only reason it was caught. Fixed by pacing to 1.2/s with backoff.

2. **Attempt 2 — 2,445 errors, because I killed the API four minutes in.** `make dev`
   runs `air`, and air's `bin` is `./tmp/main`. I had built a second instance to the same
   path to test Phase 1 on port 3999, and `pkill -f "tmp/main"` then matched **both** —
   taking the main development API down for roughly 37 minutes without my noticing,
   because the capture was the only thing talking to it. Kill by port or by PID, never by
   a binary path something else also uses.

   The 345 samples that did succeed all returned zero slots, which looked alarming and
   was not: they were all `mkup3`'s triples, and **the stores belonging to `mkup3` and
   `mkup4` have no open `business_hours` rows at all**. Those triples are empty by
   configuration, so they compare vacuously and contribute nothing to the gate. The
   capture script now reports how many triples actually produced availability, so a weak
   baseline cannot be mistaken for a strong one.

3. **Attempt 3** — running against the restored API.

### Two deliberate deferrals, recorded so they are not mistaken for omissions

1. **Billing carries no capability guard yet.** `subscriptions.artist_id` means those
   routes act on the *caller's own* subscription, not the salon's. An owner-only guard
   today would stop a member paying for themselves. It lands with T3.3, when the grain
   actually changes. Recorded in `exemptRoutes` with that reason.
2. **`PATCH …/orders/:id/ship` and `/deliver` stay open to members.** They move no money,
   they are reversible, and a member holding the parcel is the right person to mark it
   shipped. `confirm-payment` *is* guarded — it asserts money reached the salon's OMT or
   Whish account, which only the owner can see.

### What Phase 1 deliberately does not do

Nothing changes for anyone today. Every artist on the platform owns their own one-member
salon and therefore resolves to `Owner`, which holds every capability. The boundary exists
and is tested; it has nobody to apply to until Phase 2 makes members possible.

---

## 0. Sequencing rationale

Three rules set the order.

**1. Unify before you regrain.** `subscriptionVisibleCond` exists as three hand-written
copies in `discovery`, `artist` and `share`. On 2026-09-20 one was corrected; on
2026-09-21 the other two were found still wrong, leaving a cancelled artist bookable
through their share link. Phase 3 regrains subscriptions to the salon and touches all
three. **Unifying them is a prerequisite, not a cleanup task** — three parallel edits to a
rule that has already drifted once is the highest-probability way to ship this feature
broken.

**2. Capture the slot baseline before touching the scheduler.** Phase 2 changes the
revenue path. The proof that nothing regressed is a byte-identical comparison against
output captured beforehand. Once migration 047 ships, that baseline can no longer be
taken.

**3. Authorisation before membership.** Building the invite flow first would create a
second artist who can immediately edit the owner's prices. Phase 1 closes G1; only then
does Phase 2 make a second artist possible.

**Deliberately deferred:** seat billing (Phase 3) and earnings splits (Phase 4) can lag the
membership work. Rania can onboard a second artist and operate correctly while the salon
still pays two subscriptions — annoying, not broken. Reversing that order ships money
changes before the authorisation boundary that protects them.

---

## Phase 0 — Decisions and ground truth

Parallel with Phase 1. No code.

| Task | Description | Owner |
|---|---|---|
| **T0.1** | Confirm **D-MS1** (per-salon seat billing). Changes revenue per salon; everything in Phase 3 depends on it. | Founder |
| **T0.2** | Confirm **D-MS3** (salon-wide client notes) and **D-MS6** (one payment account per salon). Both are privacy/fairness postures that belong in the artist terms, not in code comments. | Founder |
| **T0.3** | Record all eleven D-MS decisions in `B-Edge-Decision-Register-v1.md` in its existing resolved/open format. | Eng |
| **T0.4** | **Verify invariant I2** — every salon's `owner_id` is an artist in that salon — and resolve the one artist with `salon_id IS NULL` **by hand**. Do not automate. Migration 048 depends on this, and forcing a schema state after spot-checking a sample is precisely how migration 038 went unapplied and unnoticed. | Eng |
| **T0.5** | Capture the slot baseline across all 5 artists × 90 days into `project-docs/slot-baseline-pre-046.json`. **Cannot be done after Phase 2 ships.** | Eng |

---

## Phase 1 — The authorisation boundary (closes G1)

**Goal:** a second artist cannot damage the salon. Ships with zero user-visible change —
every artist today is an owner and gains nothing and loses nothing.

| Task | Description | Depends |
|---|---|---|
| **T1.1** | `internal/pkg/salonrole`: `Role`, `Capability`, the matrix, `Can`, `Resolve`. Leaf package, no `internal/` imports. | — |
| **T1.2** | `role_test.go`: matrix completeness, owner ⊇ member, `None` grants nothing, all three `Resolve` branches. | T1.1 |
| **T1.3** | **`TestNoInlineOwnerChecks`** — parses `internal/` and fails on any `owner_id` comparison outside `salonrole` and `membership/repository.go`. Same technique as `booking/columns_test.go`, which parses its own source to keep `bookingSelectCols` and `scanBooking` in agreement. **This is the guard that makes P1 a constraint rather than a comment.** | T1.1 |
| **T1.4** | Add `SalonRole` to `jwt.Claims`; resolve at token issue. Old tokens decode as `None` and are refused by any guard — the safe direction. | T1.1 |
| **T1.5** | `middleware.RequireSalonCapability(cap)` → 403 `SALON_ROLE_FORBIDDEN`. Fiber test pattern: `middleware/concurrency_limiter_test.go`. | T1.4 |
| **T1.6** | Apply guards across `artist` (services, stores, hours), `promo` (discounts), `product`, `billing`, payment methods. Reads stay open to all members. | T1.5 |
| **T1.7** | **Route-coverage test**: enumerate registered routes; every salon-scoped mutating route must carry a capability guard. A new unguarded route fails the build. Forgetting one guard is the likeliest way to ship G1 half-closed, and it is invisible in review. | T1.6 |
| **T1.8** | Frontend: `salonRole` signal from the token; owner-only nav hidden **and** route-guarded. Seed in `ngOnInit`, **never in a constructor** — a signal `input()` is unbound until after construction, so a constructor read returns the default and every member renders briefly as an owner. That exact bug made four inputs inert in `guest-details-screen.component.ts`. | T1.4 |

**Exit:** `make verify-uc7` MS1–MS2 pass. Every existing test passes unchanged. A solo
artist notices nothing.

---

## Phase 2 — Membership and per-artist hours (closes G2, G3)

**Goal:** Rania can invite a second artist who works their own hours. This is the phase
that delivers the actual business request.

| Task | Description | Depends |
|---|---|---|
| **T2.1** | Migration **046** `salon_invitations` (§2.2 of the DB doc), with partial unique indexes on live invitations. | — |
| **T2.2** | Migration **047** `artist_schedules` + `artist_schedule_exceptions`. Header must state that **absence of a row means available for the whole store window**, so the semantics survive the next reader. | — |
| **T2.3** | Migration **048** — FK on `salons.owner_id`. | T0.4 |
| **T2.4** | `internal/pkg/schedule`: `Window`, `Intersect`, `IntersectDay`. Pure, no clock, no DB. | — |
| **T2.5** | The seven intersection tests (LLD §5.1). **`TestIntersect_NoArtistRota_ReturnsStoreWindowUnchanged` first** — it is the identity property every existing artist depends on. | T2.4 |
| **T2.6** | Wire the intersection into `booking.GetAvailableSlots`. | T2.5, T0.5 |
| **T2.7** | **Diff live slot output against the T0.5 baseline. Byte-identical or the phase stops.** | T2.6 |
| **T2.8** | `internal/membership`: model, repository, service, handler, routes. | T1.5 |
| **T2.9** | Extract `CompleteIntoExistingSalon` from `onboarding/repository.go:63` — **one transaction with a branch, not a second onboarding path.** | T2.8 |
| **T2.10** | Invitation lifecycle tests: valid, expired, revoked, reused, cross-salon, phone-equivalent (`70555123` ≡ `+96170555123`). | T2.8 |
| **T2.11** | Removal and transfer, with BR-3/BR-5 guards and token invalidation on transfer. | T2.8 |
| **T2.12** | Artist rota CRUD: `PUT /artists/me/schedule`, personal exceptions. | T2.2 |
| **T2.13** | Frontend: Team screen, invite flow, accept screen, My Schedule. **The invitation link must be visible and copyable in the owner's dashboard** — `TWILIO_WHATSAPP_FROM` is unset and 61 of 61 notifications ever queued are `dead`, so a WhatsApp-only invite is unusable today. | T2.8, T2.12 |
| **T2.14** | E2E suite 22 in `E2E-TEST-PLAN.md`, including **22.5: a solo artist's dashboard is unchanged**. | T2.13 |

**Exit:** a second artist can be invited, accepted, given a rota, and can take bookings —
and cannot touch the salon's prices, stores, hours, discounts or billing.

---

## Phase 3 — Seat billing — **MOSTLY CANCELLED 2026-09-23**

> **Read `B-Edge-Pricing-Decision-v1.md` before doing anything in this phase.**
>
> Two independent advisory reviews rejected per-seat billing, and the price
> ladder was repriced from $7–$80 to $45–$249 (migration 050). `seat_price`
> is now 0 on every plan and `included_seats` is a **ceiling**, not a
> quantity billed for.
>
> **T3.4 (seat arithmetic) is cancelled.** It would have implemented a charge
> the business has decided not to make.
>
> **What survives:** the subscription regrain from `artist_id` to `salon_id`
> (T3.1–T3.3, T3.6) — still wanted, because the owner should receive one
> bill — plus a single ceiling check on the add-artist path in place of
> T3.5's seat quote.
>
> The product reason for the rejection is worth carrying: salon staffing in
> Lebanon is fluid, and charging per artist makes an owner register two of
> six to keep the bill down. The calendar is then wrong, the product looks
> broken, and she churns. B-Edge's entire value is a complete calendar.

## Phase 3 — Seat billing (closes G4)

Gated on **T0.1**. Deferrable: a two-artist salon paying two subscriptions is wrong but not
broken.

| Task | Description | Depends |
|---|---|---|
| **T3.1** | **Unify `subscriptionVisibleCond` into one leaf function** consulted by `discovery`, `artist` and `share`. Behaviour-preserving, independently shippable, and a **prerequisite** for T3.3 (see §0 rule 1). | — |
| **T3.2** | Migrations **049–051** — add `salon_id`, backfill, `NOT NULL`, partial unique index, relax `artist_id`. Run the duplicate-subscription check (DB doc §2.4) first; abort if it returns rows. | T0.1 |
| **T3.3** | Regrain subscription lookups: `billing`, `middleware/billing.go`, and the now-single visibility function. | T3.1, T3.2 |
| **T3.4** | Seat arithmetic at `billing/service.go:363`: `monthly_price + max(0, active_members − included_seats) × seat_price`. Money through `internal/pkg/money`, never `decimal.NewFromString` on input. Tests for all six plans, including `comped` (999 seats) and `starter` (`seat_price = 0` — extra seats must be **free, not an error**). | T3.3 |
| **T3.5** | `SeatImpact` quote + the `SEAT_CHARGE_REQUIRED` gate on invite (FR-B3). | T3.4, T2.8 |
| **T3.6** | Widen `subscription.Enforce` input to the salon's subscription so a lapse suspends **every** member (FR-B5). `Enforce` itself is unchanged — only what is passed to it. | T3.3 |
| **T3.7** | Owner-only billing UI; seat count and next-invoice preview. | T3.5 |

**Exit:** MS6, MS7 pass. One subscription per salon, priced by seats.

---

## Phase 4 — Earnings and settlement (closes G5)

| Task | Description | Depends |
|---|---|---|
| **T4.1** | **Extract the earnings scope predicate into one function** and call it from all three sites (`earnings/repository.go:69,109,146`). Do not edit three copies — that is the defect class this whole document is organised around. | T1.1 |
| **T4.2** | Owner breakdown query: per-member `GROUP BY` plus salon total. | T4.1 |
| **T4.3** | `GET /salon/earnings/breakdown`, guarded by `earnings:salon:read`. | T4.2 |
| **T4.4** | "Collected on behalf of" per member — deposits that reached the salon account, attributed to the treating artist (FR-E3). | T4.2 |
| **T4.5** | **Copy, and it is not optional:** every earnings surface states that B-Edge does not transfer money and the figure is a record (FR-E4, risk R7). A member who reads a figure as a payment received is a trust failure, and this is the single easiest thing in the plan to skip. | T4.3 |
| **T4.6** | MS3: a member's earnings response contains no other member's booking IDs. Negative test per route. | T4.3 |

---

## Phase 5 — Customer-facing artist choice (closes G6)

| Task | Description | Depends |
|---|---|---|
| **T5.1** | Salon profile lists its artists (FR-C1). | T2.6 |
| **T5.2** | Artist picker in the funnel (FR-C2). | T5.1 |
| **T5.3** | **One-member salons render exactly as today** (FR-C4) — no picker, no salon framing. | T5.2 |
| **T5.4** | *(Deferred — D-MS11)* "No preference" booking. Deterministic assignment must interact with the GIST exclusion constraint, which `001_initial_schema` calls *"the final atomic guard — no application-level check can replace it."* Its own design note first. | — |

---

## Verification

Every task: `go build ./... && go test ./...` clean, plus `ng build shared && ng build
customer-pwa && ng build artist-dashboard` for frontend work. Backend tasks end with
`make swagger` — `docs/` is gitignored, so there is no diff to commit, but a stale
`@Success` type is invisible until someone reads the wrong contract.

New: `make verify-uc7` → `scripts/verify-uc7.py`, MS1–MS11.

Migrations are paired `.up`/`.down` with a prose header stating why; the `.down` is
executed against a copy before the `.up` ships.

**Re-baseline the docs at the end of every phase:**
```bash
./scripts/doc-facts.sh > project-docs/doc-facts.baseline
./scripts/check-docs.sh
```

---

## The one test that decides whether this shipped correctly

> **An artist working alone on B-Edge must not be able to tell that multi-artist support
> exists.**

Every default in this design serves that: a missing rota means full availability; a
soloist is an owner and holds every capability; a one-member salon renders as a solo
profile. E2E 22.5 and integration check MS4 are where it is enforced. If either fails, the
feature is not ready regardless of how well the multi-artist path works — B-Edge's launch
artist is a soloist, and five of five salons on the platform have exactly one member.

---

## Effort

| Phase | Tasks | Scale |
|---|---|---|
| 0 Decisions | 5 | Days — mostly founder |
| 1 Authorisation | 8 | ~1 sprint |
| 2 Membership + hours | 14 | ~1.5 sprints |
| 3 Seat billing | 7 | ~0.5 sprint |
| 4 Earnings | 6 | ~0.5 sprint |
| 5 Customer choice | 4 | ~0.5 sprint |

**Phases 1–2 are the business request.** 3–5 make it commercially coherent and can follow.

---

## Open risks carried into implementation

| Risk | Carried as |
|---|---|
| Capability check re-expressed inline (R1) | T1.3 anti-drift test |
| Stale `SalonRole` after transfer (R2) | T2.11 token invalidation |
| Hours intersection regresses soloists (R3) | T0.5 baseline → T2.7 gate |
| Subscription regrain corrupts billing (R4) | T3.2 additive, verified-first |
| Cross-member earnings leak (R5) | T4.6 negative test per route |
| Seat charge surprises the owner (R6) | T3.5 explicit acknowledgement |
| Member misreads earnings as money received (R7) | T4.5 copy |

---

*Companion documents: `B-Edge-Multi-Artist-Salon-BRD-v1.md` ·
`B-Edge-Multi-Artist-Salon-HLD-v1.md` · `B-Edge-Multi-Artist-Salon-LLD-v1.md` ·
`B-Edge-Multi-Artist-Salon-Database-v1.md`*
