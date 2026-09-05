# 00 — Executive Synthesis

**2026-09-06.** Reconciliation of six specialist reviews, and the action plan.

| # | Domain | Findings | Headline |
|---|---|---|---|
| 01 | Standards & Efficiency | 5 | 106 lines of dead code, orphaned by a refactor two days ago |
| 02 | Software Architect | 5 | Coupling is genuinely low; one 2,015-line file holds six jobs |
| 03 | Security Auditor | 4 fixed, 4 open | Plan **executed live**: 42 passed, 4 defects, all fixed |
| 04 | Database Optimizer | 6 | One real N+1; **decline Redis**, buy a CDN instead |
| 05 | Design System & UI | 4 | Tokens exist and are good; **no dark mode at all** |
| 06 | Performance & Vitals | 5 | 91 kB initial is good; images bypass every optimisation |

---

## The finding that outranks everything below

**The product cannot send a single message to a customer.** `D8` /
`TWILIO_WHATSAPP_FROM` is blocked on Meta business verification, which is
blocked on whether B-Edge is a legally registered business in Lebanon. That
gates customer login, booking approvals, confirmations, cancellations, review
requests and the calendar link.

Every P1 below is engineering polish on a product that currently cannot
transact. **This is not an engineering task and no amount of the work in this
document substitutes for it.**

---

## Reconciling the panel

### C1 — "Add a component library" vs "keep the bundle at 91 kB"

Agents 05 and 06 appear to collide: one wants more shared UI, the other guards
a small payload.

**They do not actually conflict, and agent 05 pre-empted it** by explicitly
declining Angular Material / PrimeNG / Spartan. The five proposed components
are *extractions of code that already ships* — `bedge-star-rating` replaces
four inline copies. Net bundle effect is **neutral to negative**.

The dark-mode migration (05 F2) adds **zero JavaScript**: it moves hex values
behind CSS custom properties. The cost is diff size, not bytes.

> **Resolved:** build the five components, ship dark mode, adopt no third-party
> UI kit. Add agent 06's budgets *first* so the claim is enforced rather than
> asserted.

### C2 — "Add Redis" vs the project's derive-on-read architecture

The brief asked about a caching layer. **Agent 04 recommends declining it**, and
agent 02 independently supports that: the two things already cached
(`artists.rating`, `stores.rating`) are the two things that can go stale, and
agent 02 F4 has to propose a drift-check query for exactly that reason.

Adding Redis would multiply that problem across a codebase whose central design
is "derived, never stored, no scheduler".

> **Resolved:** no Redis. Agents 02, 04 and 06 all converge independently on
> the same alternative — **a CDN in front of `/discovery/*`**, which removes
> more load, has no invalidation problem for public slow-changing data, and
> simultaneously closes the missing-edge-layer risk. One purchase, three
> findings.

### C3 — "Constant-time responses" vs latency

Agent 03 R3 notes AUTH-02 asks for identical *timing class*, not just identical
bodies. Achieving it means padding responses to a fixed duration.

> **Resolved: decline for now.** Status, code and message are already
> identical. Timing analysis across the public internet, against a Lebanese
> mobile network, to distinguish "foreign booking" from "no booking", is not a
> credible threat next to R1 (no WAF) and R2 (unverified payment confirmation).
> Recorded as a known gap rather than fixed.

### C4 — "Split the 2,015-line file" vs "don't churn working code"

> **Resolved: do it, and do it now rather than later.** The split is safe
> *specifically because* `statematrix_test.go` (154 cells) and
> `slots_golden_test.go` pin the behaviour, and it is a file move with no
> signature changes. That safety net will not get stronger, and the file will
> only get bigger.

Pair it with agent 02 F2 (narrow repository interfaces per file) rather than
doing that separately.

---

## A cross-cutting risk no single agent owned

**The frontend has effectively no test coverage.** The `ng test` targets were
broken from the day the real apps replaced their scaffolding and were only
repaired on 2026-09-05; there are now **8 tests across ~38 routes**, six of
them for one utility.

Every frontend guarantee to date comes from `ng build` (types only) and manual
E2E passes. Agent 05 proposes migrating ~38 templates for dark mode and
extracting five components — **both are large refactors landing on a codebase
with no automated safety net.**

> **Sequencing consequence:** write component tests for the five extracted
> components *as they are extracted*, and do the dark-mode migration as its own
> reviewable commit. Do not bundle them.

---

## Action plan

### P0 — Blocker

| # | Action | Owner | Source |
|---|---|---|---|
| 1 | **Answer: is B-Edge a legally registered business in Lebanon?** Everything customer-facing waits on it | Founder | D8 |
| 2 | **CDN + WAF in front of the API before real traffic.** No edge layer exists; in-process limiters are standing in for infrastructure | Infra | 02 F5, 03 R1, 04 F5 |
| 3 | **Out-of-band verification on payment confirmation.** One admin account confirms all money against a self-reported reference; `FRAUD-06` is a plausible fake transfer | Founder + eng | 03 R2, D19 |

### P1 — High

| # | Action | Effort | Source |
|---|---|---|---|
| 4 | Delete 7 dead `validationMessage` functions (~106 lines) | 10 min | 01 F1 |
| 5 | Batch `GetOrderItems` with `ANY($1)` — kills the N+1 on both order lists | 1 h | 04 F1 |
| 6 | `NgOptimizedImage` + Cloudinary `f_auto,q_auto` loader — LCP **and** CLS | 2 h | 06 F1 |
| 7 | Lighthouse baseline on `/discover` and `/a/:handle`, throttled mobile | 1 h | 06 |
| 8 | Split `booking/service.go` into 5 files; narrow interfaces as you go | 3 h | 02 F1, F2 |
| 9 | Tokenise colours behind CSS variables; ship dark mode | 1 d | 05 F2 |
| 10 | Extract 5 shared components, **with tests**, migrate call sites | 1 d | 05 F1 |
| 11 | Re-run AUTH-05 / AUTH-06 over HTTPS — currently untestable, not passing | 1 h | 03 |

### P2 — Tech debt

| # | Action | Source |
|---|---|---|
| 12 | Bundle budgets in `angular.json` (warn 110 kb, error 150 kb) | 06 F5 |
| 13 | `provideZonelessChangeDetection()` explicitly — the property is currently invisible | 06 zoneless |
| 14 | `@defer (on viewport)` for map, gallery, notification body | 06 F2 |
| 15 | Preload the artist's travel buffers once per slot request | 04 F2 |
| 16 | Add waitlist composite index + `order_items.product_id`; **skip the other 8** | 04 F3 |
| 17 | `strings.ToUpper` in place of `money.upper` | 01 F2 |
| 18 | `cdk/overlay` for the notification panel; add `LiveAnnouncer` | 05 F3 |
| 19 | Aggregate-drift query into the ops runbook (not a scheduler) | 02 F4 |
| 20 | Stub Twilio, then run AUTH-07 / SPAM-02 | 03 |
| 21 | Seed fixtures for FRAUD-04 / FRAUD-05 | 03 |
| 22 | Measure timing class on ownership branches | 03 R3 |

### P3 — Nice to have

| # | Action | Source |
|---|---|---|
| 23 | Move micro-interactions onto components; add `prefers-reduced-motion` | 05 F4 |
| 24 | Suite 10 needs one Cloudinary upload to become runnable at all | E2E plan |

---

## Explicitly recommended *against*

Recorded so these are not revisited as oversights:

- **shadcn/ui, Radix, React Aria, Base UI** — React-only. This is Angular 21.
  `@angular/cdk` is the equivalent and is already a dependency.
- **Angular Material / PrimeNG / Spartan** — would cost more bundle than the
  entire current initial payload and force a visual redesign.
- **Redis** — see C2. No measured hot read; contradicts derive-on-read.
- **A scheduler for aggregate drift** — the project has none by design.
- **Indexes on 8 of the 11 unindexed FKs** — low-cardinality parents, rarely
  deleted, small tables. Write cost for no measured read.
- **Constant-time ownership responses** — see C3.
- **`DisallowUnknownFields`** — already assessed and declined; it would turn a
  forward-compatible client into a broken one, and ignoring unknown fields is
  what makes mass assignment impossible.

---

## Overall assessment

This codebase is **substantially better than the review brief assumed**. The
brief anticipated reinvented wheels, messy styling and fragmented components;
what the panel found was a hand-rolled-SQL Go backend with genuinely low
coupling, a considered semantic palette, 100% lazy routes, a sub-100 kB
payload, and a database doing real work — the GIST exclusion constraint is the
concurrency guard, verified under an 8-way race.

The weaknesses are concentrated and specific rather than systemic:

1. **Infrastructure that does not exist yet** — no edge layer, no field data.
2. **A frontend with no automated tests**, about to receive two large refactors.
3. **A product blocked on a business-registration question**, not on code.

The single highest-leverage engineering purchase is the **CDN** — three agents
reached it independently, and it resolves a security risk, a scaling question
and a caching debate at once.
