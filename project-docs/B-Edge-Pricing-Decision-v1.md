# B-Edge — Pricing and go-to-market
## Decision record · v1

**Date:** 2026-09-23 · **Status:** ladder applied, prices unvalidated
**Inputs:** competitor research (2026), two independent advisory reviews — a
SaaS pricing strategist and a sales manager, briefed separately and not shown
each other's work.

---

## 1. What prompted this

The founder asked Rania — launch artist, ~300K Instagram followers, two
studios — what she would pay. She said **$250 a month**, and that other
artists would too.

The ladder at the time topped out at **$80** and started at **$7**.

That gap is what this document resolves. It is not resolved by choosing a
number; it is resolved by establishing what B-Edge has to earn, what the
market charges, and what will actually arrive as money.

---

## 2. The constraint everything else follows from

**There are no card rails in Lebanon.** B-Edge cannot auto-charge anyone. It
holds no money, takes no commission, and earns nothing on payment
processing. Customers pay deposits directly to the salon's own OMT or Whish
number; B-Edge never touches them.

Every subscription payment is therefore **a human making a manual transfer,
and a human confirming it arrived.** There is no card on file, no auto-renew,
no dunning, no retry.

Three consequences, and they drive every decision below:

1. **The subscription carries 100% of revenue.** There is no second line.
2. **Each invoice has a real, recurring human cost** on both sides.
3. **Doubling the number of invoices doubles the failure surface.**

---

## 3. What the market actually charges

Researched 2026-09-23. Normalised to **one solo artist billing $3,000/month**:

| Platform | Subscription | + processing | all-in | per extra staff |
|---|---|---|---|---|
| Fresha | $19.95 | $83.70 | **$104** | $14.95 (+20% marketplace commission) |
| Booksy | $29.99 | $77.70 | **$108** | $20 (+30% on boosted) |
| Vagaro | $23.99 | $85.50 | **$109** | $10/calendar, caps ~$90 |
| GlossGenius | $24.00 | $78.00 | **$102** | **none — unlimited staff** |
| Square | $0–49 | ~2.4–3.5% | — | none on paid plans |
| Boulevard | $158/location | 2.65% | ~$425 typical | 5 included |
| Phorest | ~$150/licence | — | $250–450 multi-location | staff-count based |
| Zenoti | $200–600+/location | — | + $5k–20k implementation | staff-count based |
| Blyssbook (Saudi) | ~$26 | zero commission | — | — |

**The striking result is how tight the all-in figure is: ~$105 per artist per
month, about 3.5% of booking volume — and only ~20% of it is subscription.**
The other 80% is payment processing and marketplace commission, both of which
B-Edge structurally cannot have.

The old `starter` at $7 was **0.23% of booking volume**. `growth` at $15 was
0.5%.

---

## 4. The ladder (applied, migration 050)

| Tier | Monthly | Artists | Locations |
|---|---|---|---|
| **Solo** | $45 | 1 | 1 |
| **Studio** | $109 | 4 | 1 |
| **Salon** | $189 | 12 | 2 |
| **Multi** | $249 | 25 | 5 |
| *comped* | $0 | 999 | — |

`starter`, `growth` and `enterprise` are **retired, not deleted** —
`subscriptions.plan_code` is a foreign key and `internal/admin` assigns a
plan on approval. Deleting the rows would dangle the key and break approval.

**Target: ~1.5% of booking volume**, roughly half the Western take. Still the
cheapest serious option in the category.

### The floor is an invoice, not a price

> *"Each invoice costs 10–15 minutes and carries failure risk. A $7 invoice is
> negative-margin against your own time before you count chasing it."*

Both reviewers independently said to delete the cheap tiers. A cheap tier
also anchors the category cheap and generates the most collection labour per
dollar earned.

### Per-seat billing: rejected

Both reviewers rejected it, for different and complementary reasons.

**Commercial:** a per-seat bill changes amount on every hire and departure.
Under manual collection each change is a fresh transfer, a fresh
confirmation, a fresh dispute. Banded flat pricing keeps the invoice amount
stable for a year at a time.

**Product — the stronger argument:**

> *"Salon staffing in Lebanon is fluid — chair renters, part-timers, family on
> Saturdays. Charge per artist and the owner registers two of six to keep the
> bill down. The calendar is then wrong, the product looks broken, and she
> churns blaming you. Your entire value is a COMPLETE calendar. Don't tax the
> thing that makes the product work."*

This was not in the original design and it overturns it. `included_seats` is
now a **ceiling** — the most artists a salon on that tier may have — and
`seat_price` is 0 on every plan. Crossing the ceiling moves a salon up a
band; it never adds a line to an invoice.

### One ladder, not two products

A soloist is a salon with one person in it. Separate "Solo Artist" and
"Salon" products would turn the highest-value event in the business — a
soloist hiring her first artist, $45 → $109 at zero acquisition cost — into a
migration between product lines. Market it as two things; keep the billing
object as the salon.

---

## 5. Where the two reviews disagreed

Recorded because the disagreement is real and unresolved by argument.

| | Pricing strategist | Sales manager |
|---|---|---|
| Solo | **$45** | **$19** |
| Mid | $109 / $189 | $49/location |
| Top | $249 | $99–149 |
| Cheap tier | none; floor $45 | a genuinely **free** tier (~30 bookings/mo, no deposits) |
| Tier on | artists + locations | locations and volume, **never heads** |

They are answering different questions. The strategist priced from **unit
economics** — what B-Edge must earn with no processing or commission line.
The sales manager priced from **what a stranger will hand over in cash** in a
market where B-Edge is unknown and the reminders do not send yet.

**The applied ladder takes the higher numbers deliberately: you can come down
from a quoted price and you cannot go up.** If the test below returns zero,
drop a band.

---

## 6. The test — this week, no code

Both reviewers proposed the same thing, independently.

> *"You're asking 'what price' when the question is still 'will anyone send
> money at all.' Get $19 out of a stranger's OMT and you'll have learned ten
> times more than that conversation gave you."*

**Quote the five existing salons a real price and count who transfers.**

- **3 of 5 transfer** → the ladder holds.
- **0 of 5** → drop a band and re-quote.

Quote **quarterly prepay** as the default, annual at two months free, monthly
as an exception. Bill everyone on the 1st — one collection day a month, not
thirty anniversary dates. Grace 7 days, then the public booking page goes
dark; data is never deleted. **Put that in writing at signup**, or it becomes
a personal conflict later.

### Rania

**Stop comping her.** She said she would pay; find out. A reference customer
who transfers is worth more than a free one — it validates the price, it is
the strongest social proof available, and it is the first revenue.

Her 300K followers are **clients, not artists** — the wrong asset. The right
asset is the ~30 artists in her phone. Ask for introductions, not selling.

### What Rania's $250 is worth as evidence

> *"It's not a price. It's a compliment."*

She pays nothing today, so there is no budget pain. She is the design partner
and invested in this succeeding. She was asked face to face by the founder
she likes, and answered *"what is this worth"* rather than *"what will I send
by OMT."* She is also the 99th percentile of the market — the median user is
a solo artist billing $1–2K/month, for whom $250 is 15–25% of revenue.

Treat it as a **ceiling anchor for the top tier**, nothing more.

---

## 7. What to sell — the sentence

Not features. No-shows and DM admin.

> *"How many clients a week don't show up? Three? At $40 each that's $480 a
> month walking out the door. With B-Edge they pay a deposit before they book
> — straight to your OMT. I never touch your money. One saved client a month
> pays for this."*

Two supporting lines, both true and both unavailable to Fresha in this
market: **I never hold your money**, and **no commission on your clients —
they're yours.** In a market burned by its own banks, the first is the
strongest sentence available.

---

## 8. The blocker that outranks pricing

> *"If you start charging while reminders don't send, 'it doesn't message my
> clients' becomes the churn reason for every single one. Fix before you
> charge. This is your #1 risk, and it's product, not sales."*

`TWILIO_WHATSAPP_FROM` needs Meta business verification, pending since error
63051. Until it clears, **every notification queued is undeliverable** — 61
of 61 at last count, including customer login codes.

**Addressed 2026-09-23:** the notification worker now falls back to SMS over
the same Twilio account (`TWILIO_SMS_FROM`). WhatsApp is preferred whenever
configured; SMS is the floor, not a preference. The transport actually used
is recorded on the notification, so a WhatsApp outage is visible in the data
rather than looking like normal operation.

**Still required before charging:** provision a Lebanese sender and set
`TWILIO_SMS_FROM`. The code is ready; the number is procurement.

---

## 9. What NOT to build

Both reviewers, unprompted, warned against building billing infrastructure.

Do not build: proration, mid-cycle plan changes, usage metering, dunning,
automated retries (there is nothing to retry), a billing portal, invoice
generation, self-serve downgrade, free-trial infrastructure, LBP or
multi-currency.

For the first 50 customers the billing stack is `current_period_end` on the
subscription, a spreadsheet, and a WhatsApp reminder 7 days before expiry.

> *"Building Stripe-shaped infrastructure for a country with no cards is the
> most expensive mistake available to you right now."*

**This removes most of Phase 3 from `B-Edge-Multi-Artist-Salon-Plan-v1.md`.**
T3.4 (seat arithmetic) is cancelled. What remains is the subscription regrain
to salon level — still wanted, because the owner should receive one bill —
plus a single ceiling check on the add-artist path.

---

## 10. Open, and needing the founder

1. **Validate or drop the ladder.** Quote five salons; count transfers.
2. **Provision `TWILIO_SMS_FROM`.** Blocks charging.
3. **Free tier or not?** The one genuine disagreement between the reviews.
   A free tier costs nothing to collect and keeps marketplace supply alive;
   it also anchors the category cheap. Unresolved.
4. **Revisit `salons.subscription_plan`** — a vestigial column with its own
   CHECK (`free`/`studio`/`salon_plus`), unconnected to the `plans` table.
   All five salons read `free`. It duplicates subscription state and should
   go, but not in the same change as a pricing move.
