# 02 — Software Architect

**2026-09-06.** Structure, coupling, separation of concerns, data flow.

## Scope & files analysed

17 Go domains under `internal/`, 8 leaf packages under `internal/pkg/`,
`cmd/main.go`, and the Angular monorepo's three projects.

---

## Measured coupling

```
17 domains · 8 leaf packages · 20 cross-domain import edges

booking     -> billing      (2)   composition root + consumer interface
admin       -> audit        (2)
domain/auth -> audit        (2)
billing     -> audit        (2)
notification-> inbox        (1)
discovery   -> billing      (1)
*           -> middleware   (6)   handler registration only
```

**20 edges across 17 domains is low.** For comparison, a domain importing every
other would be 272. The architecture is genuinely modular, and the leaf-package
pattern is doing the work it was introduced for.

---

## What is right, and should not be "improved"

### A1 — Dependency inversion is applied correctly, not ceremonially

`booking` needs to know whether an artist's subscription permits bookings.
It does **not** import `billing.Repository`. Instead
`internal/booking/service.go:55` declares the interface it needs:

```go
// Defined here rather than depending on billing.Repository's entire
// surface … just to satisfy a type. *billing.pgRepo (via
// billing.NewRepository) satisfies this structurally with no changes.
```

and `handler.go:53` wires the concrete type at the composition root. The
consumer owns the interface; the provider doesn't know it exists. That is the
textbook shape, and the one cross-domain import is confined to wiring.

### A2 — Leaf packages break cycles rather than decorating the tree

`internal/pkg/{subscription,openinghours,bidi,money,optional,validation}` each
exist for a stated, checkable reason: `middleware` cannot import `billing`, so
the shared rule moved down. This is documented in `CLAUDE.md` as an enforced
convention.

### A3 — The database is treated as part of the architecture

The GIST exclusion constraint is the concurrency guard, not an application
mutex. Migration 001 calls it *"the final atomic guard — no application-level
check can replace it"*, and a live 8-way race confirmed it (one 201, seven
409s). Most codebases put this in a service and get it wrong.

---

## Findings

### F1 — `booking/service.go` is 2,015 lines and 46 functions · **P1**

```
2015  internal/booking/service.go     ← 8% of all non-test Go in one file
1452  internal/booking/repository.go
 738  internal/booking/handler.go
 711  internal/booking/model.go
 658  internal/artist/repository.go
```

The booking domain is legitimately the largest — it holds slot generation, the
state machine, holds, waitlist cascade, travel buffers and bulk shift. The
problem is not the domain's size, it is that one file holds **six unrelated
responsibilities**.

**Proposed split**, along seams that already exist in the code:

```
internal/booking/
├── service.go            orchestration + lifecycle transitions   (~700)
├── slots.go              GetAvailableSlots + the 5 steps         (~450)
│                         (occupancy.go already extracted the algebra)
├── holds.go              guest hold, lazy expiry, submit         (~250)
├── waitlist.go           cascade + notify-next                   (~250)
│                         (waitlist_worker.go already separate)
└── shift.go              bulk shift preview/apply                (~350)
```

No interface changes, no behaviour change, no test changes — every function
keeps its receiver and name. This is a file move, and it is safe precisely
because `statematrix_test.go` (154 cells) and `slots_golden_test.go` pin the
behaviour.

**Why it matters:** the state machine and slot generation are the two things a
new contributor must reason about, and they currently share a file with four
other concerns.

### F2 — The repository interface is the widest abstraction in the codebase · **P2**

`internal/booking/repository.go` declares a single `Repository` interface. The
service depends on all of it; a test must mock all of it.

`internal/booking/waitlist_worker.go` already demonstrates the fix and states
the reasoning:

```go
// waitlistSweepRepo is the narrow slice of the booking repository this
// worker needs. Declared here rather than taking the full Repository so the
// worker's dependencies are visible at a glance and its test needs a
// two-method fake rather than the whole interface.
type waitlistSweepRepo interface {
	FindStaleWaitlistGroups(...)
	NotifyNextWaitlistEntry(...)
}
```

**Recommendation:** apply that pattern as F1's split proceeds — each new file
declares the slice it needs. Do **not** do it as a separate refactor; the value
comes from pairing it with the split, and doing it alone is churn.

### F3 — `middleware` is imported by 6 domains · **P2, structural not accidental**

Every domain's `RegisterRoutes` takes middleware. That is route registration,
not business coupling. The alternative — a central router that knows every
domain — trades six thin edges for one fat one.

**No action.** Recorded so it is not "fixed" into a worse shape.

### F4 — Two aggregates are caches with no self-healing · **P2**

`artists.rating` and `stores.rating` are maintained by
`recomputeArtistRatingTx` / `recomputeStoreRatingTx` inside the write
transaction. Correct for every write that goes through the repository, and
**stale forever** after a direct SQL write.

This is consistent with the project's "derived, never stored" philosophy being
*violated deliberately* here for read performance — but unlike `open_status`
and subscription status, these two cannot be recomputed on read cheaply.

**Recommendation:** a reconciliation query in the ops runbook, not a scheduler.
The project has no scheduler by design and should not gain one for this.

```sql
-- drift check; expects zero rows
SELECT a.id, a.rating AS cached,
       COALESCE((SELECT AVG(rating) FROM reviews r
                 WHERE r.artist_id=a.id AND r.is_visible),0) AS actual
FROM artists a
WHERE a.rating <> COALESCE((SELECT AVG(rating) FROM reviews r
                            WHERE r.artist_id=a.id AND r.is_visible),0);
```

### F5 — No edge layer, and the architecture assumes one · **P1 (infra, not code)**

`B-Edge-Security-Test-Plan-v1.md` §0.1 states it plainly: no CDN, no gateway,
no WAF — one Go binary. The in-process concurrency limiter (300 in-flight) and
per-IP rate limiter are standing in for infrastructure that does not exist.

This is an architectural fact with a deployment answer, not a code change. It
belongs in front of launch. See `03-security-auditor.md` EDGE-01.

---

## Data flow, end to end

```
Angular (signals, zoneless)
   │  JSON — money as decimal STRING, ids as UUID STRING, times RFC3339
   ▼
Fiber middleware  recover → SecurityHeaders → requestid → logger
                  → CORS → rate limit → concurrency limit
   ▼
handler   BodyParser → validation.MapBodyError        (type errors → 422+field)
   ▼
service   validate.Struct → validation.MapError       (value errors → 422+field)
          money.Parse / optional.Field                (three-state semantics)
          domain rules, ownership → errXNotFound()    (404, never 403)
   ▼
repository  pgx, $n placeholders, explicit transactions
   ▼
PostgreSQL  CHECK constraints · GIST exclusion (the real concurrency guard)
```

The layering is clean and each boundary has one job. The one place it leaks is
F1: `service.go` holds orchestration *and* the slot algorithm.

---

## Recommended action

| # | Action | Priority |
|---|---|---|
| F1 | Split `booking/service.go` into 5 files along existing seams | **P1** |
| F5 | Put a CDN/WAF in front before launch | **P1** (infra) |
| F2 | Narrow repository interfaces *as part of* F1 | P2 |
| F4 | Add an aggregate-drift query to the runbook | P2 |
| F3 | Leave middleware imports alone | no action |
