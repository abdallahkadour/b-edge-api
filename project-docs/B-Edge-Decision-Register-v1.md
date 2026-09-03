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
| **D20** | **Is B-Edge eligible for Tap Payments' Marketplace product in Lebanon?** One email decides whether card-on-file, automated cancellation fees and multi-merchant payouts reopen — see D7. Tap's docs do not publish country coverage for the marketplace product and say to contact them for eligibility. | Sprints not yet written: card-on-file, cancellation-fee capture, artist payouts | **Founder, ~15 minutes.** One email to Tap. Highest value-per-minute item on this list |
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

---

## 3. Explicitly not decisions

Recorded so they stop being re-raised as open questions:

- **Multi-merchant payouts, card-on-file, automated cancellation-fee capture.**
  Still excluded — but **the stated reason was wrong**, see §2.1. Lebanon does
  have card rails; what it may not have is marketplace eligibility and workable
  settlement. Excluded pending **D20**, not excluded on principle.
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

**D20 replaces D7 as the free one** — and it is now the highest
value-per-minute item here. Fifteen minutes and one email to Tap decides
whether card-on-file, automated cancellation fees and artist payouts reopen.
D7 itself is answered (§2.1): Stripe is still out, but the reason recorded for
the exclusion was wrong, and the real blocker is unknown until Tap replies.

**D3 and D5 are now proposals, not blanks** — `B-Edge-D3-D5-Proposals-v1.md`.
Each ends with a single line to accept or reject. D5 turned out to be nearly
settled already: the feasibility assessment §2.2 had recommended everything
except the primary-stylist tie-break.

**D19 deserves more attention than its size suggests.** Every other money
decision assumes a human is reliably clicking Confirm.
