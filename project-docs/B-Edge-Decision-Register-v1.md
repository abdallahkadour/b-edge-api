# B-Edge — Decision register

**v1, 2026-09-02.** The single list of decisions this product is waiting on,
and of the ones already made and where they landed in code.

## Why this exists

The D1–D9 numbering was invented in a planning session and lived only in that
session's plan file. **Nothing in either repository mentioned D3 or D9.** A
decision that exists only in a chat transcript is not a decision the project
has — it is one that has to be re-derived, differently, by whoever needs it
next.

Meanwhile the one decision log that *did* exist,
`B-Edge-Monetization-Implementation-Spec-v1.md` §11, had gone stale in the
worse direction: it still listed currency and the enforcement thresholds as
open questions months after both were decided **and shipped to the database**.
Two lists, one incomplete and one wrong, is how a resolved question gets
re-litigated and a live one gets forgotten.

This file supersedes both. §11 now points here.

**Keeping it true:** when a decision is made, move it to §1 with the date and
the file it landed in. A resolved decision with no landing site is not
resolved, it is an intention.

---

## 1. Resolved

| # | Decision | Landed in | Date |
|---|---|---|---|
| **D1** | **Currency: USD only.** No LBP collection. Every money column stays single-currency; `invoices` needs no collected-amount/FX-rate columns. | Migration 026 — `CHECK (currency = 'USD')` on `plans`, `subscriptions`, `invoices` | 2026-08-30 |
| **D2** | **Enforcement windows: 21 / 45 days.** Grace 0–21 full access; past_due 21–45 hidden from Discover and unbookable but still editable; suspended 45+ also blocks writes. | `internal/pkg/subscription/status.go` — `GraceDays = 21`, `PastDueDays = 45`, one `Enforce` function read by three call sites | 2026-08-31 |
| **D10** | **Share previews: a Go pre-render endpoint, not Angular SSR.** Only two URL shapes need crawlable metadata; SSR would be a workspace-wide change with permanent build cost to serve non-human clients. | `internal/share`, `GET /a/:handle` · `B-Edge-Share-Previews-Decision-v1.md` | 2026-08-31 |
| **D11** | **Calendar events ship as a LINK, not an attachment.** Twilio does not accept `text/calendar` on WhatsApp — calendar files are MMS-only there, and WhatsApp document support is PDF/vCard/Office. So the message carries a URL and the API answers it. | Migration 031, `internal/calendar`, `GET /c/:token` | 2026-09-01 |
| **D12** | **Two paths to `confirmed` both stay.** The two-step route is not legacy — it is the partial-payment and disputed-transfer case, deliberately built and wired into the Deposits screen. The notification moved to the transition instead. | `announceConfirmed` in `internal/booking/service.go` · matrix §4.2 | 2026-09-01 |
| **D13** | **Confirming a past appointment is allowed but silent.** The bookkeeping is legitimate (a transfer that landed late); "You're all confirmed for last Tuesday. See you then!" plus a calendar link for a date that has gone is not. | `announceConfirmed` early return · matrix §4.1 | 2026-09-01 |
| **D14** | **`deposit_pending` dropped from the schema; `refunded` kept.** The first had no writer and no rows, ever. The second got one when the refund dead end was closed. | Migration 032 · matrix §4.3 | 2026-09-01 |
| **D15** | **Cancel-after-start blocked for `confirmed` only** — not as a blanket rule. Only `held` and `approved` have an expiry sweep, so blocking cancel on `pending`/`deposit_paid` would strand them permanently. | `CancelBooking` guard · matrix §4.4 | 2026-09-01 |
| **D7** | **Stripe: still unavailable in Lebanon — confirmed, and stays excluded.** But the *reason* recorded for the exclusion was wrong. See the note below: "no card rails in Lebanon" is not true, and the boundary moved rather than being confirmed. | This register §2.1 · new question D20 | 2026-09-02 |
| **D20** | **Tap is out — they do not onboard Lebanese merchants.** Their own support documentation: *"We currently do not accept any new merchants from Egypt, Jordan, or Lebanon."* Payouts go only to GCC-domiciled businesses. **But the answer split the excluded scope in half** — see §2.2. | This register §2.2 · new question D21 | 2026-09-03 |
| **D22** | **Interval algebra modelled before the features that need it.** A booking owns a typed `Occupancy` set; half-open intervals matching the GIST constraint; `booking_intervals` child table chosen for Sprint 13 over replacing the parent constraint. | `internal/booking/occupancy.go` · `B-Edge-Interval-Algebra-Decision-v1.md` | 2026-09-03 |
| **D23** | **Cleanup time is a stored `blocked_until` column, not a computed expression.** Forced: `timestamptz + interval` is only STABLE, so no index expression or generated column can carry it. The redundancy is what makes the buffer *releasable* on early completion. Ranges are still rejected — "ship the buffer, not the range". | Migration 033 header | 2026-09-03 |
| **D24** | **Waitlist slot RESERVATION is not built.** Notifying someone a slot opened does not hold it for them; they race for it like anyone else. | This register §3 | 2026-09-03 |

---

## 2. Open — and who can actually settle each

The column that matters is the last one. Most of these are waiting on
information only the founder has; two (D3, D5) are waiting on nothing but an
hour of engineering, and one (D20) on a single email.

| # | Decision | Blocks | Settled by |
|---|---|---|---|
| **D3** | **Discount precedence.** Deposit vs balance, stacking, percentage-before-fixed ordering, interaction with `stores.early_bird_fee` (the only existing price modifier, and it is a *sur*charge), refunds. Subscription invoices in scope or not. | Sprint 9 — the whole discount engine | **Proposal written 2026-09-03** — `B-Edge-D3-D5-Proposals-v1.md`. Awaiting a yes/no |
| **D4** | **Service catalogue content.** ~40–80 services with durations, prices, categories and `name_ar`. | Sprint 4 entirely | **Founder / Rania only.** Domain knowledge. **Longest lead time in the plan — start it before anything else on this list** |
| **D5** | **Review attribution.** One review per visit; salon rating + primary-specialist rating; the tie-break for "primary" when a visit has several services (longest, or highest value). | Sprint 8 | **Proposal written 2026-09-03** — `B-Edge-D3-D5-Proposals-v1.md`. Only the tie-break was genuinely open. Awaiting a yes/no |
| **D6** | **Arabic-first priority.** A prioritised sprint, or after Phase 3? | Sprint 11 | **Founder.** And **D4 forces it** — you cannot curate `name_ar` and then decide Arabic does not matter |
| **D8** | **`TWILIO_WHATSAPP_FROM`.** `TWILIO_ACCOUNT_SID` and `TWILIO_AUTH_TOKEN` are set; the sender is not. | Customer login, booking approvals/confirmations/cancellations, review requests, the calendar link, bulk-shift notifications | **Procurement.** Founder — targeted end of week 2026-09-06 |
| **D21** | **Does B-Edge ever want to collect customer money at all** — i.e. move from subscription to commission? **Not a payments question.** As posed originally ("does Areeba do splits?") it was the wrong question: B-Edge has no split to perform. See §2.3. | Every card-payment capability, including the two D20 appeared to unblock | **Founder.** A business-model decision, not an engineering or vendor one |
| **D9** | **Do resources / split bookings have demonstrated demand** from paying salons? | Sprints 12–13 | **Founder.** Needs market evidence |
| **D16** | **Final launch prices.** Six plans are seeded (`starter` $7 … `multi` $50, plus `comped`), but they are a proposal. Nothing is blocked — plans are rows, so launch prices are a seed edit and later changes are a form. | Nothing technically | **Founder** |
| **D17** | **Trial length, and does it start at signup or at approval?** `subscriptions.trial_ends_at` exists and is settable, but no trial is configured and **no subscription currently has one** — all six live subscriptions are `comped`. | Self-service onboarding | **Founder.** The monetization spec §7.3 recommends starting at approval |
| **D18** | **Does Rania's comp have an end date**, or is it revisited by hand? | Nothing technically | **Founder** |
| **D19** | **Who confirms payments daily?** The whole model assumes a human checks OMT/Whish and clicks Confirm. **There is exactly one admin account** (`abdallah.kadour@b-edge.com`). If that person is not reliably available, the revenue model stalls regardless of the code. | Revenue collection in practice | **Founder.** The sharpest of the money questions, and the least technical |


### 2.1 D7 in full — the boundary moved

**Checked 2026-09-02 against `stripe.com/global`.** Stripe supports 44
countries. **Lebanon is not among them**, and the UAE is the only MENA
country on the list (Cyprus is listed but is geographically European). India
and Indonesia are the only "coming soon" entries. So the Stripe half of the
answer is: *no change, still excluded.*

**But the assumption that justified the exclusion does not survive checking.**
The excluded scope — card-on-file, automated cancellation-fee capture,
multi-merchant payouts — was recorded as out of reach because *"Lebanon has no
card rails."* That is not accurate. **Tap Payments explicitly serves Lebanon**,
and Areeba, PayTabs and Bank of Beirut's gateway all operate there.

Worse for the old assumption, Tap publishes exactly the two capabilities the
excluded scope needs:

- **Card-on-file / tokenization** — saved cards attached to a customer, with
  retrieve/verify/list/delete, for recurring and one-click checkout. That is
  what automated cancellation-fee capture requires.
- **A Marketplace API** — split a charge between the marketplace and a
  sub-merchant via a `destinations` object, with sub-merchant onboarding, KYC,
  and payouts to the business's own bank account. That is Stripe Connect's
  shape, which is what artist payouts would need.

**What is NOT established, and must not be assumed:**

1. **Whether the marketplace product is available for Lebanon.** Tap's
   developer docs publish no country coverage for it and say plainly:
   *"Contact Tap team to confirm if you are eligible to use Tap's Marketplace
   solution."* It is gated, not self-serve. One source describes Tap's core
   coverage as "GCC and Egypt" while their own Lebanon page advertises
   acceptance there — acceptance, marketplace eligibility, and settlement are
   three different permissions.
2. **Whether payouts can settle into Lebanese bank accounts** at all, given
   capital controls and the dollar situation. This is a banking question, not
   an API one, and it is the more likely blocker.
3. **Whether it would be used.** Roughly 60–70% of Lebanese e-commerce is
   still cash on delivery. Card rails existing does not mean customers reach
   for them.

**So the correct posture is narrower than either the old assumption or the new
finding.** Stripe stays excluded. "No card rails" should stop being cited as
the reason, because it is false and it has been suppressing a question worth
asking. The real blocker is unknown until someone asks Tap — which is D20, and
is fifteen minutes of founder time for a potentially large change in scope.

Nothing should be built against this until D20 comes back.


### 2.2 D20 in full — half the excluded scope was never actually blocked

**Tap: no.** Their support documentation is explicit — *"We currently do not
accept any new merchants from Egypt, Jordan, or Lebanon."* Payouts are made
only to businesses domiciled in GCC countries. Their `/en-lb` landing page
advertising Lebanese acceptance refers to accepting payments *from* Lebanon,
not to onboarding Lebanese merchants. No email needed; the question is closed.

**But answering it split the excluded scope in two, and only one half is
actually blocked.**

| Capability | Status |
|---|---|
| **Card-on-file / tokenization / recurring charges** | **Available.** Areeba is Beirut-based, founded 2017, onboards Lebanese merchants, and offers tokenization (storing a card as an encrypted token for later charges) and scheduled recurring billing. |
| **Split / marketplace payouts to sub-merchants** | **Unconfirmed.** Tap has it and will not take us. Areeba markets to marketplaces but split payouts specifically could not be confirmed. → **D21** |

So **automated cancellation-fee capture and card-on-file deposits are not
blocked by the market.** They have been excluded since 2026-08-30 on a
premise — "Lebanon has no card rails" — that was never true, and neither D7
nor this document is claiming otherwise now.

**What has not changed, and should temper any enthusiasm:**

1. **Settlement under capital controls.** Whether money reaches a usable
   Lebanese account is a banking question, not a processor one, and remains
   the likeliest practical blocker.
2. **Adoption.** Roughly 60–70% of Lebanese e-commerce is still cash on
   delivery. Card rails existing is not customers using them.
3. **Onboarding cost.** Areeba's onboarding is reported to take longer and
   require more paperwork than the GCC processors.
4. **The OMT/Whish flow stays either way.** It is the majority path and one of
   the few genuinely defensible things about this product. Nothing here argues
   for replacing it — only that a card option is *possible* alongside it.

**Nothing should be built on this yet.** It reopens a planning question, not a
sprint.


### 2.3 D21 reframed — B-Edge has no split to perform

D21 was originally *"does Areeba support split payouts to sub-merchants?"*
Checking the codebase before drafting that email showed the question does not
apply, and that **§2.2 above overstated what D20 unblocked.** Both are
corrected here.

**B-Edge never touches money.** Verified against the schema and source, not
assumed:

- **Zero** `payouts`, `transfers`, `ledger`, `settlements` or `balances`
  tables.
- **Zero** occurrences of `commission`, `platform_fee`, `take_rate` or
  `payout` anywhere in `internal/`.
- Deposits move **customer → artist directly** over OMT/Whish.
  `bookings.deposit_reference` is documented in the schema as *"optional
  artist-entered note … an OMT/Wish transaction code … not customer-facing"* —
  the platform records that Rania says money arrived; it never receives it.
- `internal/earnings` computes `SUM(final_price)` **for the artist**. It is her
  revenue report, not a balance B-Edge owes her.
- B-Edge's revenue is **subscriptions only** — $7 to $80 a month.

Split payouts exist to solve Fresha's problem: the platform collects the
customer's money, keeps a commission, and remits the rest. **B-Edge does not
collect, so there is nothing to split.** The capability is missing from a
model that has no use for it.

**And the same reasoning narrows §2.2.** Card-on-file is genuinely available in
Lebanon through Areeba — that part stands. But B-Edge *using* it requires
answering who the merchant is, and both answers lead back here:

| Merchant is… | Consequence |
|---|---|
| **B-Edge** | The platform now collects customer money and owes it onward — it is holding other people's funds, which is a regulatory question in Lebanon long before it is an engineering one |
| **Each artist** | Every artist needs her own Areeba merchant account, and B-Edge automating charges against someone else's account is precisely the payment-facilitator role Areeba may not offer |

So the honest position is narrower than §2.2 implied: **card-on-file is
available in the market, and still unavailable to B-Edge's architecture
without a deliberate change to what B-Edge is.**

**The real question, and it is not about processors:** does B-Edge want to
become a platform that holds and forwards money — a commission model — or stay
subscription software that never touches a transaction?

Staying subscription-only is the current design and a defensible one. It is
why the OMT/Whish flow exists, it keeps B-Edge out of money transmission
entirely, and it means the artist is paid the instant the customer transfers
rather than on a payout cycle. Everything excluded under "payments" follows
from that single choice, not from Lebanon's rails.

**Nothing in the payments area should be planned until D21 is answered**, and
the answer is a business decision.

---

## 3. Explicitly not decisions

Recorded so they stop being re-raised as open questions:

- **Multi-merchant payouts.** Still excluded, now for the *right* reason: the
  processor that has the capability (Tap) will not onboard Lebanese merchants,
  and the one that will (Areeba) has unconfirmed split support — **D21**.
- **Card-on-file, automated cancellation-fee capture.** Available *in the
  Lebanese market* (Areeba), but not available to B-Edge without deciding
  whether the platform collects money — see §2.3. Excluded by B-Edge's own
  architecture, which is a defensible choice, rather than by the market.
- **Waitlist slot reservation.** Deferred until the GIST exclusion-constraint
  question settles in Sprint 13. Migration 016's header explains why.
- **Repository-layer tests.** No database test infrastructure exists
  (`TEST_DB_NAME` is vestigial). Building it is a separate project; recording
  this stops someone spending a sprint on testcontainers instead of coverage.

---

## 4. What to do next with this list

**D4 first**, and not because it is the most important — because it is the only
one with a lead time measured in days rather than minutes, and it is not
engineering work, so it can run in parallel with everything else.

**The payments thread is now one question, not four.** D7 and D20 are
answered: Stripe is out, Tap is out, and Areeba does offer card-on-file to
Lebanese merchants. But §2.3 shows the boundary was never really about
processors — **B-Edge holds no money and has no payout ledger**, so a split
payout has nothing to split and a stored card has no merchant to charge
against. All of it collapses into **D21**: does B-Edge want to collect
customer money at all? That is a business-model decision and needs no vendor
research.

**D3 and D5 are now proposals, not blanks** — `B-Edge-D3-D5-Proposals-v1.md`.
Each ends with a single line to accept or reject. D5 turned out to be nearly
settled already: the feasibility assessment §2.2 had recommended everything
except the primary-stylist tie-break.

**D19 deserves more attention than its size suggests.** Every other money
decision assumes a human is reliably clicking Confirm.
