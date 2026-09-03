# B-Edge — Feature Feasibility &amp; Architecture Assessment v1

> Written 2026-08-30. Benchmarks a proposed feature set against Fresha, Booksy,
> GlossGenius, Vagaro, Phorest and Zenoti, and scores each item's feasibility
> **against B-Edge's actual codebase** rather than as a greenfield build.
>
> Scoring is grounded in a direct audit of the `b-edge-api` and `b-edge-web`
> working trees on the date above (route registrations parsed from source, tests
> executed, migrations and components enumerated) — not asserted from memory or
> from the other docs in this folder. Where a competitor capability could not be
> independently confirmed, this document says so rather than asserting it.
>
> Companion to `B-Edge-Competitor-Analysis-v1.docx` (the feature inventory) and
> `B-Edge-Monetization-Implementation-Spec-v1.md` (the money model). This document
> is the *what should we build next, and what will it cost us* view.

---

## 0. Two findings that come before the matrix

### 0.1 There is no discount capability of any kind

Confirmed against real code: no promo codes, no percentage or fixed discounts, no
first-time-client pricing, no off-peak rates. The money model runs through
`services.price`, `services.deposit`, `products`, and the `invoices` table, and
none of them support a reduction. Every benchmark competitor has had this for
years.

This was already listed as an open gap in
`B-Edge-Targets-vs-Actual-Analysis-v1.md` ("real discount system") as of Aug 7 and
remains open.

### 0.2 Multi-merchant payouts are blocked by geography, not complexity

The architecture Fresha, GlossGenius, Booksy and Vagaro use for booth-renter split
payments rests on Stripe Connect or an equivalent (Adyen, Square). **Stripe does
not operate in Lebanon**, and neither do the alternatives those platforms depend
on.

This is not a gap to engineer around — it is *why* the OMT/Whish manual deposit
flow exists (see `RELEASE-CHECKLIST.md` § "Deliberate, not gaps"). That flow is a
deliberate adaptation to a market without card rails, and it is one of the few
genuinely defensible differentiators B-Edge has.

**Consequence:** card-on-file, automated cancellation-fee capture, and split
payouts should be treated as a *scope boundary*, not a backlog. Re-verify Stripe's
Lebanon status directly against their supported-countries list before any payments
planning — it is the load-bearing assumption here.

> **Re-verified 2026-09-02 (register D7), and this section's reasoning was
> half wrong.** Stripe's Lebanon status is unchanged — 44 supported countries,
> Lebanon not among them, UAE the only MENA entry. But the inference above,
> that "neither do the alternatives those platforms depend on", only checked
> the processors those platforms happen to use. It did not check MENA-native
> ones.
>
> **Tap Payments serves Lebanon**, and publishes both card-on-file tokenization
> and a Marketplace API with split payouts and sub-merchant KYC — Stripe
> Connect's shape. Areeba, PayTabs and Bank of Beirut's gateway also operate
> there.
>
> That does **not** reopen the scope on its own. Tap's marketplace product is
> eligibility-gated with no published country coverage, settlement into
> Lebanese banks under capital controls is a separate and likelier blocker,
> and 60–70% of Lebanese e-commerce is still cash on delivery. The boundary
> holds — but "Lebanon has no card rails" must stop being cited as the reason,
> because it is false and it suppressed a question worth asking. That question
> is register **D20**: one email to Tap.

---

## 1. Feature feasibility &amp; complexity matrix

Complexity is scored **relative to the existing codebase**. "Partly built" means
the underlying data or component already exists and the remaining work is
surfacing it.

### Client storefront &amp; discovery

| Feature | Competitor precedent | Complexity | Verdict | Primary bottleneck |
|---|---|---|---|---|
| Real-time Open/Closed status | Universal | Low | **Easy** | None of substance. Weekly hours, special-hours exceptions and `store.is_active` all already exist — this is a derived read over data already stored. Timezone handling is the only real care point. |
| Maps embed + one-tap routing | Universal | Low | **Partly built** | The MapLibre `location-map` component already ships, and order delivery pins are stored (migration 022). Remaining work is a deep-link to the device's native maps app. |
| OG share cards (WhatsApp/IG) | Fresha, Booksy | Medium | **Challenging** | The Angular PWA is client-rendered. WhatsApp and Instagram crawlers do not execute JavaScript, so runtime-injected meta tags are invisible to them. Needs SSR or a small pre-render service for artist/booking URLs. |
| Dual-layer reviews (salon + specialist) | Phorest, Zenoti; partial in Fresha | Medium-High | **Challenging** | Attribution across a multi-stylist visit — see §2.2. |
| Portfolio galleries tagged to services | GlossGenius, Fresha | Low-Medium | **Easy** | Both halves already exist independently (media reorder/cover, services catalogue). A join table plus UI. Best value-per-effort item in this document. |
| Discount engine (codes, first-time, off-peak) | Universal | Medium-High | **Challenging** | Not the arithmetic — the *interaction surface*. A discount must resolve against deposits, product carts, subscription invoices and refunds. Precedence rules must be decided before code. |

### Provider ergonomics

| Feature | Competitor precedent | Complexity | Verdict | Primary bottleneck |
|---|---|---|---|---|
| Pre-populated service templates | GlossGenius (best-in-class), Booksy | Low | **Easy** | Seed catalogue plus a toggle. Highest onboarding-friction reduction per unit of effort on this list. Curation is the work, not code. |
| Variable / range durations | **None ship true ranges** | High | **High risk** | Combinatorial explosion in slot generation — see §2.1. The absence of precedent is itself the finding. |

### Calendar &amp; scheduling engine

| Feature | Competitor precedent | Complexity | Verdict | Primary bottleneck |
|---|---|---|---|---|
| Split-booking / processing downtime | Fresha, Zenoti (native) | High | **High risk** | The slot engine and `BlockingStatuses` assume one contiguous block per booking. Splitting means a booking occupies a *set* of intervals — a core model change, not an add-on. |
| Automated waitlist auto-fill | Fresha, Zenoti | Medium | **Partly built** | The waitlist table and notification worker both exist. Blocked on two things: Twilio going live, and the deliberate absence of a background scheduler — this is the first genuine case lazy-on-read cannot serve. |
| Physical resource management | Zenoti (core), Phorest; Vagaro via workaround | High | **Challenging** | Adds a third dimension (artist × time × resource) to availability. Zenoti treats this as core architecture; Vagaro bolted it on and reviewers report double-booked rooms as a result. |

### Payments &amp; financial safeguards

| Feature | Competitor precedent | Complexity | Verdict | Primary bottleneck |
|---|---|---|---|---|
| Multi-merchant booth-renter payouts | Fresha, GlossGenius, Vagaro | Very High | **Blocked** | No card rails in Lebanon; Stripe Connect unavailable. Also carries money-transmitter and 1099-equivalent obligations that don't map to Lebanese law. See §2.3. |
| Card-on-file / cancellation fee capture | Universal | High | **Blocked** | Same rail constraint. Deposit-based no-show protection is the correct local substitute and already works. |
| Upfront deposit enforcement | Universal | — | **Built** | Already shipped, including a two-step verification queue no benchmark competitor offers. Ahead, not behind. |

---

## 2. Deep dives

### 2.1 Variable duration ranges

Start with the most informative fact: **none of the six benchmark platforms ship
true range-based durations.** When category leaders with far larger engineering
teams haven't built something this conceptually simple, the reason is usually that
it breaks something downstream.

It does. A range forces every slot calculation to answer an unanswerable question:
when generating availability, reserve the optimistic 10 minutes or the pessimistic
20?

- **Reserve the minimum** → double-booking whenever the service runs long, with a
  cascading delay through the rest of the day.
- **Reserve the maximum** → the system silently discards capacity. A stylist doing
  eight 10-to-20-minute services loses eighty minutes of bookable time daily to
  padding that usually isn't needed.

B-Edge's cross-store travel buffers compound the pessimistic case further.

**Recommended compromise — and what the market has converged on:** keep a single
committed duration for scheduling, and add a separate, optional
*buffer/cleanup* value that is reserved but marked releasable. The provider sets
"Gel Full Set: 45 min + 10 min buffer." The calendar blocks 55, shows 45 to the
client, and the buffer becomes reclaimable when the provider marks the service
complete early.

**Ship the buffer, not the range.**

### 2.2 Dual reviews — salon vs. specialist

The data model is the easy half. The hierarchy already supports it
(`salons → stores → artists`, with reviews currently hanging off the artist).
Adding a second review target is small schema work.

The hard half is **attribution across a multi-service visit** — a client who has
colour with one stylist and a blow-dry with another, in one appointment, at one
salon. Three failure modes:

- **Review dilution.** Asking for a rating per stylist per visit produces survey
  fatigue; completion rates collapse and you end up with less data than the single
  review you started with.
- **Misattributed blame.** One weak stylist drags the salon score, or a dirty
  salon drags an excellent stylist's score. Both are unfair, and both create
  support disputes a solo founder has to arbitrate personally.
- **Gaming.** If salon scores aggregate their stylists', an owner inflates the
  venue by hiring one star. If fully independent, the two scores will visibly
  disagree and confuse clients.

**Recommendation:** one review per *visit*, not per service. A single salon-level
rating (venue, cleanliness, timeliness) plus a single specialist rating directed at
the *primary* stylist — the one who performed the longest or highest-value
service, resolved automatically. Keep the two scores independent; never derive one
from the other.

The existing token-based guest review flow already provides the delivery
mechanism. This is a change to what the form asks, not how it is sent.

### 2.3 Split bookings and multi-merchant payments

Two separate problems, commonly bundled because both involve "splitting."

**Split bookings (processing downtime)** is a scheduling problem, and a real one.
The booking model currently assumes a contiguous interval. Supporting a haircut
inside a colour-processing window means a booking must own a *set* of intervals,
touching slot generation, conflict detection, blocking-status logic, travel
buffers and the calendar UI simultaneously. This is the most invasive change in
this document — a rewrite of the most-tested subsystem in the codebase (~2,100
lines of booking tests would need revisiting). Feasible, but never attempt it
opportunistically.

**Multi-merchant payouts** is a regulatory and market problem before an
architectural one. Even in a Stripe-supported market the bottlenecks are:

- Onboarding friction — every booth renter completes full KYC before they can be
  paid.
- Chargeback liability on a split transaction.
- Tax reporting obligations (1099 in the US; no clean Lebanese equivalent).
- Money-transmitter classification risk if funds route through the platform rather
  than directly.

In Lebanon none of this is reachable — the prerequisite rails don't exist. Treat
it as out of scope, and treat the manual OMT/Whish flow not as a placeholder for
it but as the market-correct answer.

---

## 3. Phased blueprint

Sequenced by value-per-unit-of-risk against the current architecture.

### Phase 1 — Surface what already exists

*High feasibility, high visible value, near-zero architectural risk.*

- **Open/Closed status indicator** — derived from hours + exceptions + `is_active`.
- **Maps embed &amp; one-tap routing** — extend the existing MapLibre component.
- **Service templates library** — curated seed data with toggles.
- **Portfolio photos tagged to bookable services** — a join between two working
  systems; the shortest path to "browse the look, book the look."
- **Finish Twilio delivery** — precondition for anything notification-driven in
  Phase 2. See `WHATSAPP-SETUP.md`.
- **Tests for the billing domain** — 1,889 lines of money code currently at zero
  test coverage, and it only gets harder to retrofit under later phases.

### Phase 2 — Revenue mechanics and retention

*Custom logic, contained blast radius. Each item needs a decision before code.*

- **Discount engine** — promo codes, first-time-client pricing, off-peak rates.
  Specify precedence against deposits, carts and invoices first; the rules are the
  deliverable, the arithmetic is trivial.
- **Dual-layer reviews** — one per visit, salon + primary specialist, independent
  scores.
- **Automated waitlist auto-fill** — the first feature that genuinely requires a
  background scheduler. Accept that, or accept the feature stays manual.
- **Service buffer/cleanup time** — the deterministic answer to the
  variable-duration request (§2.1).
- **OG share cards** — needs SSR or pre-rendering. Given Instagram is the primary
  distribution channel, this may outrank everything above it.

### Phase 3 — Structural scheduling changes

*Deep, invasive, and only justified by demonstrated demand from paying salons.*

- **Physical resource management** — chairs, rooms, stations as a third
  availability dimension. Build it as core scheduling logic or not at all;
  Vagaro's bolt-on approach is the documented failure case.
- **Split bookings / processing downtime** — the interval-set rewrite. High value
  to full-service hair salons, near-zero to solo makeup artists. Let the real
  customer mix decide.
- **True variable durations** — recommended *against*; revisit only if the Phase 2
  buffer model demonstrably fails in practice.

### Excluded — a scope boundary, not a backlog

- **Multi-merchant booth-renter payouts**, **card-on-file**, **automated
  cancellation-fee capture** — all require card rails Lebanon lacks. Revisit only
  alongside a decision to enter a card-enabled market (UAE, KSA), and recognise
  that such a move is a market-entry decision with its own regulatory work, not a
  feature.

---

## 4. Strategic risk &amp; viability

### The central tension

**Feature parity with Fresha is not a winnable objective for a solo founder, and
pursuing it is the most common way platforms in this category die.** Fresha has
130,000 businesses, $80M from KKR, and hundreds of engineers. Every month spent
closing one feature gap is a month they spend widening another.

What *is* winnable is being unambiguously better for a market they treat as
marginal. B-Edge already has three things Fresha cannot easily copy: cross-city
travel buffers, a cash-economy deposit workflow, and — once built — genuine
Arabic-first UX. Those are the strategy, not consolation prizes.

### Why booking startups fail in this category

- **The feature-parity treadmill.** Teams benchmark against the leader, build
  breadth instead of depth, and ship a worse version of everything rather than a
  better version of something.
- **Marketplace cold-start.** Discovery is worth nothing with twenty salons and
  everything with thirty thousand. Building marketplace mechanics before supply
  density burns the runway that would have bought the density.
- **Monetisation ceiling.** Fresha's real revenue is payment processing, not
  subscriptions — the software is close to bait. In a market where payments can't
  be processed, the ceiling is subscription revenue alone. The billing domain is
  therefore not a side feature but the entire business model, which is a further
  reason its zero test coverage is disproportionately serious.
- **Scheduling complexity debt.** Every flexible-scheduling feature multiplies the
  state space of the availability engine. Platforms that accepted ranges, splits,
  resources and buffers without a coherent model end up with an engine nobody
  dares modify — and double-bookings that erode trust faster than any missing
  feature.
- **Churn from trust, not features.** Salons rarely switch software for a better
  feature list; they switch after a double-booking that cost them a client, or a
  payment that went missing.

### Architectural traps to avoid now

**Overloading status enums.** `B-Edge-Monetization-Implementation-Spec-v1.md` §3
already names this and got it right by refusing to overload `artists.status` with
subscription state. Hold that line as discounts, resources and split bookings
arrive — each will tempt an extra enum value rather than a new concept.

**Storing derived state.** Deriving subscription status from dates rather than
persisting it was correct. The same applies to Open/Closed status, discount
eligibility and waitlist position — compute, never store, or they drift from the
facts underneath.

**Retrofitting currency.** The monetization spec §11 flags this as open and
urgent: decide USD-throughout vs. USD-priced-and-LBP-collected *before* historical
invoice data accumulates. A discount engine makes this strictly worse — every
reduction needs to know which currency it reduces and at what rate.

**Treating the availability engine as accidentally extensible.** Resources,
buffers, splits and travel time all modify the same calculation. Adding them
incrementally as special cases produces the unmaintainable engine described above.
If two or more are genuinely on the roadmap, model the interval algebra once,
deliberately, before building any of them.

---

## 5. Sourcing and confidence

- **B-Edge feasibility scoring**: direct audit of both working trees, 2026-08-30.
  High confidence.
- **Competitor capabilities**: `B-Edge-Competitor-Analysis-v1.docx` (May 2026
  research) supplemented with current web research. Fresha's exact handling of
  processing gaps and off-peak pricing could **not** be independently confirmed —
  the matrix reflects that uncertainty rather than asserting a capability.
- **Stripe's Lebanon availability**: not verified against Stripe's own
  supported-countries list in this pass. It is the load-bearing assumption of §0.2
  and §2.3 and should be confirmed directly before any payments planning.

---

*B-Edge · Beauty at the Edge · الجمال عند الحافة · August 30, 2026*
