# B-Edge — Per-Artist Service Pricing · Spec v1

**Status:** design approved section by section, 2026-09-25 · awaiting spec review
**Overturns:** D-MS9 (*"Can a member set their own prices? No, v1."*) and the
services half of BR-8 (*"owner-write, member-read"*)
**Depends on:** `validateBookingParties` (FRAUD-16, commit `cc9d5be`), which this
feature extends

---

## 1. The goal

In a salon with several artists, the same service can cost different amounts
depending on who does it. The founder's example: **Rania's Bridal is $200,
another artist's is $100**, and the customer pays the salon either way.

The salon stays the single entity that sells and collects. What changes is
that the price of a service now depends on the artist as well as the service.

## 2. Decisions

Each was put to the founder as a question and answered; none is inferred.

| # | Question | Decision |
|---|---|---|
| **PP-1** | What can differ per artist? | **Price and deposit.** Duration stays the salon's for now. The deposit has to be included, because `bookings_deposit_not_above_price` refuses a deposit above its price — a $25 junior price under a $30 salon deposit would be unbookable. Duration is deferred, not refused: it touches slot generation, the calendar and the day shift. |
| **PP-2** | Is a custom price required? | **No — optional.** An artist who sets nothing uses the salon's price and deposit. |
| **PP-3** | Who sets it? | **Both.** The artist sets her own; the salon owner can change or remove any member's. *"The owner of the salon is admin."* |
| **PP-4** | Can an artist not offer a service at all? | **Yes.** Each artist has an on/off switch per service. This also closes a real gap: today every artist offers every service, so a customer can book the salon's nail artist for Bridal makeup. |
| **PP-5** | How is it stored? | **One row per artist per service she offers** (`artist_services`). Chosen over price levels (cannot express "Rania is $200") and over copying the menu per artist (breaks the salon's single menu, discounts and reporting). |
| **PP-6** | Deposit higher than an artist's price after the owner raises the salon deposit? | **Capped at the price when read.** Only ever lowers what a customer pays upfront; a booking must never fail because two numbers set by two people drifted apart. Shown on her screen so the cap is not silent. |
| **PP-7** | Defaults | Joining a salon: **all switches off**, she chooses. Existing artists at release: **all on, at the salon price** — nobody disappears. The owner's own services: **on**. A service the owner adds later: **on for the owner, off for members.** |
| **PP-8** | Solo salons | **Hidden.** With one artist, her price is the salon's price. The screen appears when a second artist joins. |
| **PP-9** | What does a change affect? | **New bookings only.** A booking stores its price when made, so a bride booked at $200 stays at $200 (the bridal price lock recorded 2026-09-23). Switching a service off cancels nothing. |

**How Fresha does it**, for reference: per-team-member **price and duration**,
configured by admins in the business's catalogue; *"clients will automatically
see the price and duration when booking with a specific team member."* No
documented way for staff to edit their own prices, and no per-person deposit.
B-Edge deliberately goes further on who may edit (PP-3) and on deposits (PP-1),
and deliberately not as far on duration.
([Fresha help](https://www.fresha.com/help-center/knowledge-base/catalog/76-set-advanced-pricing-and-durations-))

## 3. Data — migration 052

```sql
CREATE TABLE artist_services (
    artist_id      UUID NOT NULL REFERENCES artists(id)  ON DELETE CASCADE,
    service_id     UUID NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    price          NUMERIC(10,2),          -- NULL = the salon's price
    deposit_amount NUMERIC(10,2),          -- NULL = the salon's deposit
    updated_by     UUID REFERENCES users(id),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (artist_id, service_id),
    CHECK (price          IS NULL OR (price >= 0          AND price <> 'NaN')),
    CHECK (deposit_amount IS NULL OR (deposit_amount >= 0 AND deposit_amount <> 'NaN'))
);
```

- **A row means "offers it"; no row means she does not.** There is no
  `is_offered` column, so "offered" can never disagree with "has a row".
- The **cross-table rule** — the service is in the artist's salon — cannot be a
  CHECK. It is enforced where rows are written (§5) and re-checked on every
  booking by `validateBookingParties`.
- **Backfill** in the same migration: one row per existing artist per service
  of her salon — **active and inactive** — prices NULL. Behaviour on release day
  is identical. Inactive services are included because the resolver already
  filters `is_active`; leaving them out would mean a service reactivated after
  release comes back offered by nobody.
- **Down migration** drops the table. Nothing else references it, so it is
  lossless apart from the overrides themselves, and the header says so.
- **Room for duration:** a future `duration_min NULL` column with the same
  "NULL means the salon's" rule.

## 4. The price is worked out in one place

A leaf package, `internal/pkg/pricing`, owns one SQL fragment used by every
reader — the same shape as `subscription.VisibilityCond`:

```sql
COALESCE(os.price, s.price)                                     AS price,
LEAST(COALESCE(os.deposit_amount, s.deposit_amount),
      COALESCE(os.price, s.price))                              AS deposit_amount,
COALESCE(os.deposit_amount, s.deposit_amount)
      > COALESCE(os.price, s.price)                             AS deposit_capped
-- FROM services s JOIN artist_services os
--   ON os.service_id = s.id AND os.artist_id = $artist
```

**Readers moved onto it:** discovery's artist-profile service list, the artist
domain's `GetPublicServicesByArtist`, the hold, `POST /bookings`, and the base
price handed to the discount resolver.

**A build-breaking guard test** (M7 → 7) parses `internal/` and fails if any SQL
reads the `price` or `deposit_amount` of the `services` table outside
`internal/pkg/pricing`. It carries an **explicit allowlist, each entry with its
reason** — the same shape as the route-coverage guard's `exemptRoutes`. Known
legitimate entries: the owner's own menu (reading and writing the *salon*
price is the point of that screen), and admin reporting if it reports the
salon menu rather than what customers paid. Proven to fire before it is
trusted.

**`validateBookingParties` gains one condition:** she has a row for the service.
A switched-off service returns `errServiceNotFound()` — indistinguishable from
one that does not exist, same as a foreign service.

## 5. Who can change what

Two capabilities in the existing owner/member matrix:

| Capability | Owner | Member |
|---|---|---|
| `own_services:write` — her own switches, price, deposit | ✅ | ✅ |
| `member_services:write` — any member's | ✅ | ❌ |

| Endpoint | Capability |
|---|---|
| `GET /artists/salon/my-services` | `own_services:write` |
| `PUT /artists/salon/my-services/:serviceId` | `own_services:write` |
| `GET /artists/salon/members/:artistId/services` | `member_services:write` |
| `PUT /artists/salon/members/:artistId/services/:serviceId` | `member_services:write` |

- `GET` returns the **whole salon menu**: per service, whether it is on, her
  price and deposit (possibly null), the salon's, the **effective** values a
  customer will pay, and `deposit_capped`.
- `PUT` body: `{ "offered": bool, "price": money|null, "deposit_amount": money|null }`.
  `price` and `deposit_amount` are `optional.Field`, so an explicit `null`
  **clears** an override back to the salon's value — the house pattern for
  clearable fields. Money goes through `internal/pkg/money`.
- **Switching a service off deletes her row — including her custom price.**
  "No row" is what "not offered" means (PP-5), so there is nowhere to keep it.
  Switching back on starts again from the salon's values. The screen says so
  before she confirms turning off a service she has priced.
- **Refused:** her own deposit above her own price (422, on `deposit_amount`); a
  service outside her salon (404); a member of another salon (404).
- **Audited:** every change, with the actor. The owner changing Maya's price
  records the owner.
- The route-coverage guard enforces a capability on both `PUT` routes. *(The
  artist's own routes were first specified as `/artists/me/services`; they
  moved under `/artists/salon/` during planning precisely so that
  `routecoverage_test.go`, which only inspects that prefix, enforces them.)*
- Money errors are `400 INVALID_PRICE` / `INVALID_DEPOSIT_AMOUNT` from
  `internal/pkg/money`; a deposit above the price is `422 VALIDATION_ERROR`.

**Lifecycle hooks:**

| Event | Effect on `artist_services` |
|---|---|
| Owner creates a service (`artist/repository.go:488`) | row for the **owner** |
| Onboarding creates the first service (`onboarding/repository.go:143`) | row for the **owner** |
| A member joins | **none** — she chooses in the join step |
| A member leaves (`DetachArtist`) | **her rows deleted**, same transaction |
| A service is deactivated | rows kept; the resolver filters `is_active`, so reactivating restores every artist's choice |
| A service or artist is deleted | cascade |

## 6. What the customer sees

- **Whose price is never ambiguous.** Every customer flow starts from one
  artist (`/book/:artistId`); there is no salon-wide page. No "from $X" is
  needed. Marketplace cards show no price today and do not change.
- **Her profile** lists only services she has switched on, at her price and
  deposit, cheapest first.
- **Checkout:** her price, plus the early-bird fee where it applies; discounts
  come off her price; the deposit is hers, capped.
- **Shown = charged, made structural.** Today the last funnel screen displays
  `service().price` — the value remembered from when the profile opened — and
  the hold response carries no price at all. The hold now returns what it
  charged (price, early-bird fee, discount, final, deposit) and the last screen
  displays **those**. A price changed mid-funnel is seen before confirming.
- After booking, nothing moves: "My bookings" shows the stored price.

## 7. Screens

**One component, `ServiceOfferingsComponent`, in three places** — built once:

1. **Joining a salon** — a step after accepting the invitation. Switching
   nothing on is allowed, with a plain notice that she cannot be booked yet.
2. **"My services"** in her dashboard.
3. **Owner → Team → a member → "Services & prices"** — "on her behalf" mode,
   showing who last changed each price.

Each row: the switch; when on, price and deposit pre-filled with the salon's
values as placeholders; "use salon price" to clear; a note when the deposit cap
applied. Hidden for solo salons (PP-8).

## 8. Testing

| What | Proves |
|---|---|
| Migration up → down → up on a scratch DB | reversible, idempotent |
| DB test: backfill | every existing artist × active service got a row — **nobody disappears** |
| DB tests: resolver | override wins; NULL means the salon's; deposit capped; no row means not offered; inactive service excluded |
| Guard test: no stray `services.price` | a new reader cannot bypass the resolver — **proven to fire** |
| Service tests | capabilities, money validation, `null` clears, deposit above price refused, audit written with the actor |
| `validateBookingParties` | switched-off service → `404 SERVICE_NOT_FOUND`, identical to missing |
| **End to end** | profile price = hold price = stored booking price, with and without an override |
| Chaos suite | two members of one salon at $200 and $100 both charged correctly; switched-off service refused |
| WebKit, 390px | the switch screen, including the join step |
| `make mutation` | on the resolver and the new service code |

**Knock-on:** members who join after release offer nothing until they switch
services on (PP-7). The chaos suite and E2E scripts book with newly joined
members, so they must switch services on first — as they now verify phones
first.

## 9. Out of scope

- **Duration per artist** (PP-1) — a later column, same rule.
- **Price levels** (Junior / Senior / Master).
- **A salon-wide page** that would need "from $X".
- **Telling a member when the owner changes her price** — visible on her
  screen and in the audit log; a notification waits for outbound delivery.
- **Member-proposed prices needing owner approval** — PP-3 chose direct edit
  plus owner override instead.

## 10. Observed while designing — not part of this work

**Leaving a salon does not remove the member's `artist_stores` links**
(`DetachArtist` only sets `salon_id = NULL`), and Discover joins on
`artist_stores`. Whether a departed member can still surface on Discover
depends on the visibility condition, which was not checked. **To verify
separately**; not asserted as a bug here.
