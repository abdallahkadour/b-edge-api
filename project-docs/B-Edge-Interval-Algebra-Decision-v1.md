# B-Edge — Interval algebra: decisions

**v1, 2026-09-03.** Sprint 5. Records the decisions behind
`internal/booking/occupancy.go`, and the database decision that Sprint 13
will implement.

**No user-visible behaviour changed.** That is the point — the golden-output
tests in `slots_golden_test.go` pass unmodified before and after.

---

## Why now, before anything needs it

The feasibility assessment §4 names this by name:

> *"If two or more of [resources, buffers, splits] are genuinely on the
> roadmap, model the interval algebra once, deliberately, before building any
> of them."*

All three are. Each modifies the same calculation — which spans of time make
an artist unavailable — and each done alone would bend that calculation
around its own case. The third one to arrive would then be unimplementable
without unpicking the first two.

Doing it now costs one refactor of a subsystem with 609 tests behind it.
Doing it later costs the same refactor plus every feature built on the
single-interval assumption in the meantime.

---

## 1. Decisions

### 1.1 A booking owns a SET of intervals, not a range

Today it owns exactly one. `Occupancy` is a set from the start so that
adding a second interval is data, not a signature change through every
caller.

### 1.2 Intervals are half-open, `[start, end)`

Two intervals that merely touch do **not** overlap. This is what makes
back-to-back appointments possible — a slot ending 11:00 and a booking
starting 11:00 coexist.

It is also what the database already believes:
`tstzrange(start_time, end_time, '[)')` in the GIST exclusion constraint. The
application and the constraint now agree **by construction** rather than by
coincidence, which matters because the constraint is the final arbiter and
disagreeing with it produces a `23P01` the user sees as a mysterious
failure.

`TestGolden_BookingBlocksOverlapButNotTouching` pins this end-to-end; the
type's own test pins it directly.

### 1.3 Every interval carries a kind

| Kind | Blocks the artist? | Produced today |
|---|---|---|
| `service` | yes | yes |
| `travel` | yes | yes |
| `buffer` | yes | **no** — Sprint 6 |
| `processing_gap` | **no** | **no** — Sprint 13 |

Today every produced kind blocks, so the kind is documentation. **It stops
being documentation the moment resources arrive**: a chair being occupied
blocks the chair, not the artist, and an untyped set cannot express that
difference at all.

Declaring the two unproduced kinds now is deliberate. Their meaning is the
thing that needs settling, and settling it while nothing depends on it is
free.

### 1.4 `BlocksArtist` and `OverlapsAny` are separate questions

They return the same answer today. They are separate methods because *"is
the artist busy"* and *"is the room in use"* diverge the moment resources
exist, and one method serving both would quietly answer whichever the caller
did not mean.

### 1.5 Merging is for describing time, never for deciding bookability

`Merged()` coalesces touching intervals: `[10,11)` and `[11,12)` become
`[10,12)`. That is correct for *"when is she busy"* and **wrong** for *"can I
book this"* — a candidate ending at 11:00 overlaps the merged span but
neither original.

The set is therefore never merged on insert. `TestOccupancy_MergingMustNot
BeUsedForOverlapDecisions` exists solely to catch someone adding a merge for
tidiness.

### 1.6 Empty and inverted intervals are nothing, not errors

A zero-minute buffer is the *default* configuration, so zero-length has to be
ordinary rather than exceptional. An inverted interval can only be a caller
bug; treating it as empty makes it occupy nothing, where treating it as a
span would block from its end until the end of time.

---

## 2. The database decision — for Sprint 13, not now

Today one booking is one row with one `start_time`/`end_time`, guarded by a
GIST exclusion constraint that migration 001 calls *"the final atomic guard —
no application-level check can replace it."*

Split bookings need one booking to occupy several disjoint spans. Two ways:

**(a) Keep `bookings.start/end` as an envelope, add a `booking_intervals`
child table with its own GIST constraint.** ✅ **Chosen.**

**(b) Replace the parent constraint entirely.**

Reasons for (a):

- Every existing query keeps working. `start_time`/`end_time` remain the
  outer bounds and every report, calendar view and index built on them stays
  correct.
- The child constraint becomes the real guard, and it is *narrower* — it
  excludes on the actual occupied spans rather than the envelope, so a
  haircut genuinely becomes bookable inside a colour-processing gap.
- The envelope stays useful as a cheap first filter.
- Migration is additive: backfill every existing booking as exactly one
  interval.

The cost of (a) is two sources of truth about a booking's bounds, which have
to be kept consistent. That is a real cost and it is accepted deliberately —
the alternative rewrites every query in the domain.

> **Not implemented here.** Sprint 13 builds it, and its migration is
> irreversible in practice: write and test a real `.down.sql` against a copy
> first.

---

## 3. A task in the plan turned out not to exist

The Sprint 5 plan listed **T5.6 — "route conflict detection through
`Occupancy`."**

There is no application-level conflict detection to route. Booking writes are
guarded by the GIST exclusion constraint in Postgres; `ErrSlotUnavailable`
comes from catching SQLSTATE `23P01`. The only `Overlaps` call in the entire
codebase was in slot generation.

That is a better design than the plan assumed — the guard is atomic and
cannot be raced — and it strengthens §2: since conflict detection *is* the
constraint, multi-interval bookings require a child table with its own
constraint. There is no application path to change instead.

**T5.6 is closed as not-applicable rather than done.**

---

## 4. Coupling discovered while writing the golden tests

`GetArtistBookingsForDate` has **no store filter**. It returns every booking
for that artist on that date, across all stores. `GetArtistCrossStoreBookings`
returns the subset at *other* stores.

So a cross-store booking appears in **both** results, and that is load-bearing:

- Step 4 blocks the booking's own span.
- Step 5 adds only the travel buffers **around** it.

`TravelIntervals` therefore deliberately excludes the booking's own duration.
An "optimisation" that filtered step 4 by store would silently stop
cross-store bookings blocking their own time — the artist would be offered
slots at store A while she is booked at store B.

Both the coupling and the reason are now stated in the code at step 4, and a
golden test builds its fixture with the booking in both lists precisely
because setting one without the other describes a state the database cannot
produce.

---

## 5. What shipped

| Task | Status |
|---|---|
| T5.1 design note | this document §1 |
| T5.2 DB decision | §2 — decided, implemented in Sprint 13 |
| T5.3 golden-output tests | `slots_golden_test.go`, 10 tests |
| T5.4 `Occupancy` type | `occupancy.go`, **100% coverage** |
| T5.5 slot generation routed through it | done, golden unchanged |
| T5.6 conflict detection | **not applicable** — §3 |
| T5.7 travel buffers as typed intervals | `TravelIntervals`, and it fit without special-casing |

**T5.7 was the real test of the abstraction** — an already-shipped
complication that had to express cleanly or prove the design wrong. It
reduced to two `KindTravel` intervals and a zero-buffer case that falls out
of `Add` dropping empties, with no branch in the caller.

`TimeRange` was deleted rather than deprecated. It had one caller and leaving
a second, shorter way to say "a span of time" beside a typed one is how the
typing gets bypassed.

**Sprints 6, 12 and 13 are now unblocked from an architecture standpoint.**
Sprint 6 (buffers) needs no further design. Sprints 12 and 13 still need
demand evidence — register D9.
