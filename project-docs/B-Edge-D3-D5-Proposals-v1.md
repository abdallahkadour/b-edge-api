# D3 and D5 — proposals to approve or reject

**2026-09-03.** Both decisions were marked "engineering can propose, founder
signs off" in `B-Edge-Decision-Register-v1.md`. This is the proposal. Nothing
here is built; each section ends with the one thing to say yes or no to.

Where a benchmark could be confirmed it is cited. Where it could not, this
document says so rather than inventing one.

---

## D3 — Discount precedence

Six sub-questions. Answering them is the whole of Sprint 9's design; the
resolver itself is a pure function and small.

### D3.1 Does a discount reduce the deposit, or the balance?

**Proposal: the balance. Never the deposit.**

The deposit is not a payment step here — it is a commitment device, collected
out of band over OMT or Whish and confirmed by hand. Rania reads a transfer
notification and matches it against an expected amount.

If a promo changed the deposit, that expected amount would differ per booking
by an amount neither party can see in the transfer. Every discounted booking
becomes a reconciliation question in a flow that has **no automation to absorb
it** — and there is exactly one admin account doing this (register D19).

A discount that shrinks the commitment also weakens precisely what the deposit
exists for. Cheaper booking, same no-show cost to the artist.

> `deposit_amount` stays as configured on the service. The discount comes off
> what is owed on the day.

### D3.2 Can discounts stack?

**Proposal: one customer-applied code per booking.**

This matches Fresha, which permits a single discount code per online booking.
It is also the only rule that is explainable in one sentence to a customer and
auditable in one row by the artist.

Automatic rules (first-time-client, off-peak) are **not** codes and may combine
with a code — see the order below. That is a business-set price, not a second
coupon.

### D3.3 Percentage before fixed, or fixed before percentage?

**Proposal: fixed first, then percentage on the remainder.** Matching Fresha,
which applies fixed-amount discounts to individual items first and percentage
discounts to the remaining total.

Worth stating because it is counter-intuitive: fixed-then-percentage gives the
customer **less** than percentage-then-fixed. Taking $10 off $100 then 20% off
leaves $72; 20% off $100 then $10 off leaves $70. Choosing the benchmark's
order is deliberate, not accidental, and the resolver must have a named test
for exactly this pair so nobody "fixes" it later.

### D3.4 How does it interact with `early_bird_fee`?

`stores.early_bird_fee` is the only existing price modifier, and it is a
**surcharge**, not a discount.

**Proposal: the surcharge applies first; the discount comes off the
post-surcharge subtotal.**

The customer is quoted one number. "20% off" means off the price they were
shown. Discounting before the surcharge would let the fee silently eat the
promotion, so the customer pays more than the advertised discount implies and
cannot tell why.

### D3.5 What happens to a discount when a booking is refunded?

**Proposal: mirror the existing cancellation asymmetry rather than inventing a
second one.**

| Who cancelled | Money | The code |
|---|---|---|
| **Artist** | refund due on the deposit actually paid | **released** — reusable |
| **Customer, >24h before** | refund due | **released** |
| **Customer, <24h before** | deposit forfeited (existing policy) | **consumed** |

The principle: a customer is not penalised for a booking they did not break.
This falls out of the rules already in `CancelBooking`, so it adds a column to
`discount_redemptions` rather than a new policy.

### D3.6 Are subscription invoices in scope?

**Proposal: no. Explicitly out.**

Keep the artist-billing money model and the customer-booking money model
separate. A resolver that can touch `invoices` means a bug in customer
discounting can corrupt B-Edge's own revenue record — and invoices are already
the subject of two known-wrong behaviours pinned by characterization tests.

Plan prices are editable rows already (migration 023). Discounting an artist's
subscription is a price change, not a promo code.

### The resulting pipeline

```
  base            = service.price
  + early-bird surcharge, if the slot qualifies      → subtotal
  − automatic rules (first-time, off-peak):  fixed, then percentage
  − one customer promo code:                 fixed, then percentage
  = final_price                              (floored at 0, never negative)

  deposit  = service.deposit_amount           ← UNCHANGED by any of the above
  balance  = final_price − deposit            ← where the discount is felt
```

`bookings.discount_amount` already exists and is hardcoded to zero at exactly
two sites, so the booking half of this is replacing two constants.

### ✅ Say yes or no to

> One code per booking · fixed before percentage · discount off the balance,
> never the deposit · surcharge applied before discount · code released unless
> the customer cancels late · subscription invoices out of scope.

---

## D5 — Review attribution

Most of this was already settled in
`B-Edge-Feature-Feasibility-Assessment-v1.md` §2.2, which named the three
failure modes (dilution, misattributed blame, gaming) and recommended one
review per **visit**: a salon rating plus a specialist rating for the primary
stylist, the two scores kept independent and never derived from each other.

**That recommendation stands and is not re-argued here.** It leaves exactly one
question open.

### D5.1 How is the "primary" stylist resolved?

§2.2 says "the longest **or** highest-value service" without choosing.

**Proposal: highest `final_price`, ties broken by longest duration.**

Price is the platform's own proxy for what the customer came for. Duration is
not: a 15-minute bridal touch-up at $200 matters more to the client than an
hour-long blow-dry at $40, and it is the touch-up they are rating. Duration
survives as the tie-break because it is deterministic and always present.

**This costs nothing today and that is the point.** A booking currently has one
service and one artist, so the rule is unreachable — every booking's primary
stylist is its only stylist. It should still be written as a **named pure
function with its own tests**, because the alternative is that the rule gets
inlined at the call site and is quietly lost the day split bookings land
(Sprint 13), which is the exact moment it starts to matter.

### D5.2 Two things to keep from §2.2, restated so they are not relaxed

- **Never derive one score from the other.** Not "salon rating = average of its
  stylists". That is the gaming vector: an owner inflates the venue by hiring
  one star.
- **One review per visit, not per service.** Per-service ratings produce survey
  fatigue and you end up with *less* data than the single review you started
  with.

`UNIQUE(booking_id)` on `reviews` already enforces one-per-visit for
single-service bookings, so today's schema is already correct.

### ✅ Say yes or no to

> Primary stylist = highest `final_price`, tie-broken by longest duration,
> implemented as a tested pure function even though it is unreachable until
> split bookings exist.

---

## What is deliberately not proposed here

- **Discount UI and admin screens.** Sprint 9's T9.9/T9.10. Nothing to decide;
  they follow the resolver.
- **Which promotions to actually run.** That is marketing, and it is a
  different question from what the engine permits.
- **Whether reviews should be editable after submission.** Not raised by D5 and
  not blocking anything; worth its own decision later.
