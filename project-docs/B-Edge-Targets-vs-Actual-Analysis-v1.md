# B-Edge — Targets vs. Actual: Gap Analysis v1

> Compares the "locked" spec documents (PRD v7 Final, Booking Domain Spec, Technical Decisions) against the verified current build state, cross-checked against real code (not just memory or docs) wherever possible.
> Written August 7, 2026. Covers the core product/technical docs in full; competitor intelligence, GTM, pricing strategy, and infra docs not yet analyzed (see "Not Yet Reviewed" at the end).

---

## Headline finding

**Two of your own target documents disagree with each other**, and the actual build followed a third, undocumented path on at least one major decision. Before anything else, these need reconciling:

| Item | `B-Edge-Product-Roadmap.docx` says | `B-Edge-PRD-v7-Final.docx` says | What's actually built |
|---|---|---|---|
| Product Store | Phase 3 | **Phase 1 — Launch** | Not started. No `products`/`orders` tables. |
| Waitlist | Phase 2 | **Phase 1 — Launch** | Not started. No schema, no logic. |
| Deposit confirmation flow | — | Two-tap: "Mark deposit received" → separate "Confirm booking" tap | **One-tap**, deliberately collapsed (see below) |

PRD v7 describes itself as "Final," reviewed against three AI models with "34 gaps identified and resolved," and is the more detailed, more recent document — it's likely the one that should win. But since the actual build has been treating Product Store and Waitlist as clearly-deferred (matching the *older* Roadmap doc, not the PRD), worth explicitly deciding which document is ground truth going forward, and updating the loser to match.

---

## Part 1 — Confirmed matches (the good news)

Verified directly against real code, not just docs:

| Area | PRD target | Actual | Verdict |
|---|---|---|---|
| **Multi-store travel buffer** | Weekday 150min / weekend 90min, per-artist-per-store-pair override | `artist_store_buffers` table, exact same defaults, override logic in `service.go` applies weekday/weekend split correctly | ✅ **Exact match** |
| **Early bird cutoff** | Beirut 9am / Tripoli 7:30am | Live data confirms `09:00:00` / `07:30:00`, timezone-correct after this session's DST fix | ✅ **Exact match** |
| **Deposit verification (manual)** | Artist checks Wish Money, marks received in dashboard | Deposit Queue screen (built this session) does exactly this | ✅ **Matches conceptually** — see state-machine deviation below for the one real difference |
| **Slot holding / GIST exclusion** | 10-min hold, first-write-wins at DB level | Confirmed implemented, tested live this session | ✅ **Match** |
| **Cancellation → refund_due tracking** | Cancellation sets a refund-due state, artist sees it, customer sees status | `StatusRefundDue` exists and is set correctly on qualifying cancellations | ✅ **Partial match** — status exists; no dedicated Refund Queue *screen* or tracking table (see gaps) |
| **Business hours exceptions** | Per-store holiday/blackout dates | `business_hours_exceptions` table exists and is used | ✅ **Match** |

---

## Part 2 — Real, verified gaps (Phase 1 per PRD v7)

Everything below is explicitly listed as **Phase 1 — Launch** in PRD v7 §14.1, and confirmed absent by direct code search (not assumption):

| Feature | PRD detail | Status | Notes |
|---|---|---|---|
| **Waitlist** | Queue-based, notify first-in-line, configurable confirm window | ❌ Not started | Zero code references anywhere |
| **Product Store** | Full catalogue + order state machine (`placed→confirmed→shipped→delivered`) | ❌ Not started | No `products`/`orders` tables exist |
| **Rescheduling** | Single + bulk reschedule, cascading conflict detection, auto full refund | ❌ Not started | Not mentioned in any session |
| **Home visit / outside-Lebanon bookings** | Separate pricing model, additional-person fees, reimbursable expense line items | ❌ Not started | Current schema assumes in-store only |
| **Discounts & promotions** | Percentage, fixed, promo codes, seasonal | ⚠️ **Column exists, no system** | `bookings.discount_amount` is a raw number field — no promo-code table, no redemption logic, no seasonal-pricing engine |
| **SMS fallback** | WhatsApp primary, automatic SMS fallback on delivery failure | ❌ Not started | No Twilio SMS integration exists — WhatsApp-only, and WhatsApp itself has no live credentials yet either |
| **Notification reliability spec** | Retry 3x w/ backoff, dead-letter queue, 5-min dedup, customer opt-out prefs, versioned templates | ⚠️ **Partial** | Retry/backoff exists in the worker (confirmed earlier this session); dedup, opt-out preferences, and template versioning not confirmed built |
| **Customer profiles** | History, favourites, refund status, visible to a logged-in customer | ❌ Deliberately parked | Same root cause as no customer accounts — already documented and reasoned about |
| **Admin dashboard** | Owner sees all stores/staff/bookings/refund queue across the whole platform | ❌ Not started | Only role-gating checks exist (`RequireRole("admin")`), no actual admin views |
| **Arabic RTL interface** | Full bilingual UI, both PWAs | ❌ Not started | Confirmed zero app-level RTL/Arabic code — English only throughout |
| **Staff roles and permissions (5-role hub-and-spoke)** | Owner/Manager/Artist/Front-desk/etc. with location-level permissions | ⚠️ **Needs verification** | Current auth only has `artist`/`admin` roles observed — the fuller 5-role model from PRD §3.2 not confirmed implemented |

---

## Part 3 — The state-machine deviation (deliberate, but undocumented)

This is the most structurally significant difference between spec and reality, so it gets its own section.

**PRD v7 §8.1 and the dedicated Booking Domain Spec both describe:**
```
approved → deposit_paid → confirmed
         (artist taps      (artist taps
        "Mark received")  "Confirm booking")
```
Two separate artist actions, two separate states, both persisted.

**What's actually built and live:**
```
approved → confirmed
         (artist taps "Verify Payment" — ONE action)
```
`ConfirmDepositReceived` deliberately collapses both steps into one atomic transition. This was a reasoned decision made earlier in the build ("she checks OMT/Wish and confirms the moment the transfer lands — collapsing two clicks into one"), not an oversight — the old two-step endpoints (`MarkDepositReceived`/`ConfirmDeposit`) still exist in the code for edge cases (partial payment, disputed transfer), they're just not what the primary UI calls.

**This is a legitimate UX improvement** (fewer taps for the artist's most common action) — but it means the PRD and Booking Domain Spec are now describing a flow that doesn't match what ships. Worth a deliberate choice: update the docs to match reality, or reconsider whether the two-step flow should actually be the primary path after all.

---

## Part 4 — Everything built this session that has no corresponding spec at all

Not gaps — genuine additions beyond the original design, worth noting since they weren't asked for by any doc:

- **Guest review-link flow** (`/review/:token`) — the PRD's notification table has no "leave a review" trigger tied to booking completion at all; this session added one, plus the manual "Send review request" WhatsApp stopgap
- **Artist public handles** (`/book/rania` instead of a UUID) — not mentioned anywhere in the UI Spec or PRD
- **Discover/browse screen redesign** — matches the PRD's discovery requirement, but the actual visual design deviated from the Stitch mockup in two ways (documented in-code): no price-per-card, no bottom tab bar

---

## Part 5 — Documents not yet reviewed

To keep this analysis honest about its own coverage — these are uploaded but not yet read in this pass:

- `B-Edge-Slot-Algorithm-Spec-v1.docx` — worth checking the full 7-step algorithm against the actual Go implementation in detail (only spot-checked the travel-buffer step so far)
- `B-Edge-WhatsApp-API-Templates-v1.docx` — the 16 templates; worth checking whether tonight's 4 built messages match the specced wording/tone or diverge
- `B-Edge-Angular-PWA-Architecture-v1.docx`, `B-Edge-UI-Spec-v2.md` — full screen-by-screen inventory cross-check against what's actually built (~15 screens confirmed built across both apps; the doc claims 40 screens total)
- `B-Edge-Auth-API-Docs.docx`, `B-Edge-API-Contract-v1.docx` — extracted but not yet diffed line-by-line against real endpoint behavior
- `B-Edge-Test-Strategy-v1.docx`, `B-Edge-DevOps-Infrastructure-v1.docx`, `B-Edge-INFRA.docx` — testing/infra targets vs. actual (e.g., no `bedge_test` DB exists despite being specced — already known)
- Competitor intelligence (7 docs), GTM, Pricing Strategy, Rania Onboarding Runbook — business/strategy documents, lower priority for a build-status gap analysis but worth a pass if useful

---

*B-Edge · Gap Analysis v1 · August 7, 2026*
