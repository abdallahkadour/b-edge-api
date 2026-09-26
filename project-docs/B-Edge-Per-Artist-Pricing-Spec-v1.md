# B-Edge — Per-Artist Service Pricing · Spec v1

**Status:** Built, 2026-09-26. Verified end to end: Go tests (plain, `devbypass`,
`dbtest`), the chaos-booking suite (3.7/3.7b), and a WebKit/Chromium UI pass at
390px (`b-edge-web/scripts/verify-offerings-ui.mjs`). One defect found during
that pass — a pending member's mobile bottom nav rendered nothing at all (the
bar was itself conditioned on having a "primary" item, which a pending member
never has), so the "More" sheet that would reveal My services never appeared,
and the desktop sidebar copy of the link was CSS-hidden below `md:` — is
**fixed** as of 2026-09-26, commit `5836f48` (`b-edge-web`): the bar now falls
back to showing the filtered nav itself as its primary items whenever none of
it matches the usual primary set, so it is never empty. Re-verified with the
same script: all checks pass, including the previously-unreachable pending-
member path. Only the mobile viewport was ever affected; desktop and the join
step's inline services screen were unaffected throughout.
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

As built (`db/migrations/052_artist_services.up.sql`):

```sql
CREATE TABLE artist_services (
    artist_id      UUID          NOT NULL REFERENCES artists(id)  ON DELETE CASCADE,
    service_id     UUID          NOT NULL REFERENCES services(id) ON DELETE CASCADE,
    price          NUMERIC(10,2),                                -- NULL = the salon's price
    deposit_amount NUMERIC(10,2),                                -- NULL = the salon's deposit
    updated_by     UUID          REFERENCES users(id) ON DELETE SET NULL,
    created_at     TIMESTAMPTZ   NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ   NOT NULL DEFAULT now(),
    PRIMARY KEY (artist_id, service_id),
    CONSTRAINT artist_services_price_valid
        CHECK (price IS NULL OR (price >= 0 AND price <> 'NaN')),
    CONSTRAINT artist_services_deposit_valid
        CHECK (deposit_amount IS NULL OR (deposit_amount >= 0 AND deposit_amount <> 'NaN'))
);

-- The PK serves "this artist's services"; this serves the reverse, used by
-- the cascade and by "who offers this service".
CREATE INDEX idx_artist_services_service ON artist_services (service_id);
```

- **A row means "offers it"; no row means she does not.** There is no
  `is_offered` column, so "offered" can never disagree with "has a row".
- The **cross-table rule** — the service is in the artist's *current* salon —
  cannot be a CHECK. It is held where rows are written and re-checked where
  they are read: the offering `Upsert` statement itself writes only when the
  service is on her current salon's menu (`INSERT … SELECT … WHERE EXISTS`,
  0 rows → 404 `SERVICE_NOT_FOUND`); the owner seed on service creation and
  onboarding only ever seeds the owner of that same salon; `DetachArtist`
  deletes a leaver's rows in the same statement; the two customer-facing
  lists (`discovery.GetArtistServices`, `artist.GetOfferedServicesByArtist`)
  join `artists` on `a.salon_id = s.salon_id`, so a stray row could not
  surface; and every booking re-checks through `validateBookingParties`.
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

**Readers on it, as built:** discovery's artist-profile service list
(`discovery.GetArtistServices`); the artist domain's public service list
(`artist.GetOfferedServicesByArtist`); booking's `GetOfferedService`, which
every booking entry point resolves through `validateBookingParties` — the
guest hold, `POST /bookings`, the slots endpoint and the waitlist; and the
offering settings screen (`offering` repository, via `pricing.Detail`). The
discount resolver is **not** a separate reader: it is handed the price the
booking stores (the effective price from `GetOfferedService` at
`POST /bookings`, the held row's `final_price` at guest submit and at
preview), so it never reads `services.price` at all.

**A build-breaking guard test** (`internal/pkg/pricing/noleak_test.go`)
parses `internal/` and fails on the **common shapes** of SQL reading the
`price` or `deposit_amount` of the `services` table outside
`internal/pkg/pricing`: `services` named after `FROM`/`JOIN` or in a comma
join, bare, `public.`-qualified or quoted, together with `price` /
`deposit_amount` or a `SELECT *` / `s.*` / `services.*`. It matches one
string literal at a time, so **SQL split across two literals is not seen** —
two constants concatenated at the call site (booking's `enrichedSelectCols` +
`enrichedFrom` are already built that way), a table name spliced in with
`fmt.Sprintf`, or `UPDATE … RETURNING`. It is a tripwire, not a proof.

It carries an **explicit allowlist, each entry with its reason** — the same
shape as the route-coverage guard's `exemptRoutes` — of exactly three
entries: the owner's own menu, `artist.GetServicesBySalon` and
`artist.GetServiceByID` (reading and writing the *salon* price is the point
of that screen); and `offering`'s `Upsert`, which *writes* her override
(`artist_services.price`) and joins `services` only for the current-salon
predicate. Every shape was proven to fire before it was trusted: each was
injected into a throwaway file and named by the failing test, and a
split-constant probe beside them was not.

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
  `offered` is **required** — absent or `null` is `422 VALIDATION_ERROR` on
  `offered`, because `false` deletes her row and her custom price, and a
  body that only meant to change the deposit must not be read as "switch it
  off". `price` and `deposit_amount` are `optional.Field`, so an explicit
  `null` **clears** an override back to the salon's value and an absent key
  keeps it — the house pattern for clearable fields. Money goes through
  `internal/pkg/money`.
- **Switching a service off deletes her row — including her custom price.**
  "No row" is what "not offered" means (PP-5), so there is nowhere to keep it.
  Switching back on starts again from the salon's values. The screen says so
  before she confirms turning off a service she has priced.
- **Refused:** her own deposit above her own price (422, on `deposit_amount`); a
  service outside her current salon (404 `SERVICE_NOT_FOUND` — refused by
  the lookup and again by the `Upsert` statement itself); a member of another
  salon (404 `MEMBER_NOT_FOUND`).
- **The own-services routes re-check salon membership against the database,
  not just the token.** `salon_id`/`salon_role` come off the access token,
  which stays valid until it expires even after `membership.Leave` revokes
  refresh tokens — so a departed member's still-valid access token would
  otherwise still carry her old salon_id. `ListMine`/`UpdateMine` call
  `ArtistInSalon(artistID, salonID)` against the database before doing
  anything, and answer `404 MEMBER_NOT_FOUND` if she is no longer in it.
  Without this, a since-revoked artist could write `artist_services` rows for
  her former salon in the window before her token expires — rows that can
  never be booked, but would silently switch services back ON if she later
  rejoins, breaking PP-7's "joining a salon: all switches off."
- **Audited:** every change, as an `audit_events` row (`entity_type`
  `artist_service`, action `offering.update` or `offering.off`) with the
  actor, `actor_role` `artist` and the client IP. Old and new values both
  carry `{artist_id, service_id, offered, price, deposit_amount}`, so the
  row says **whose** service changed: the owner changing Maya's price records
  the owner as the actor and Maya's `artist_id` in the values.
- The route-coverage guard enforces a capability on both `PUT` routes. *(The
  artist's own routes were first specified as `/artists/me/services`; they
  moved under `/artists/salon/` during planning precisely so that
  `routecoverage_test.go`, which only inspects that prefix, enforces them.)*
- Money errors are `400 INVALID_PRICE` / `INVALID_DEPOSIT_AMOUNT` from
  `internal/pkg/money`; a deposit above the price is `422 VALIDATION_ERROR`.

**Lifecycle hooks:**

| Event | Effect on `artist_services` |
|---|---|
| Owner creates a service (`artist` `CreateService`; the statement is `createServiceWithOwnerOfferSQL` in `artist/owner_seed.go`) | row for the **owner**, same statement |
| Onboarding creates the first service (`onboarding` `Complete`) | row for the **owner**, same transaction |
| A member joins | **none** — she chooses in the join step |
| A member leaves (`membership` `DetachArtist`) | **her rows deleted**, same statement |
| A service is deactivated | rows kept; the resolver filters `is_active`, so reactivating restores every artist's choice |
| A service or artist is deleted | cascade |

## 6. What the customer sees

- **Whose price is never ambiguous.** Every customer flow starts from one
  artist (`/book/:artistId`); there is no salon-wide page. No "from $X" is
  needed. Marketplace cards show no price today and do not change.
- **Her profile** lists only services she has switched on, at her price and
  deposit, cheapest first. Both customer-facing readers of an artist's menu —
  `artist.GetOfferedServicesByArtist` and `discovery.GetArtistServices` —
  `ORDER BY effective_price ASC, s.name ASC`; neither lists a service she has
  not switched on (the `JOIN artist_services` excludes it), nor one of
  another salon (both join `artists` on `a.salon_id = s.salon_id`).
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
values as placeholders; a "Use salon price & deposit" button that clears
**both** overrides (it always did; the label now says so); a note when the
deposit cap applied. Switching a service OFF asks for confirmation first whenever she has
a custom price OR a custom deposit set on it (either alone is enough), naming
what will be lost, because switching off deletes the row (§5) — nothing to
confirm when neither is set, since there is nothing to lose.

**PP-8's "hidden for solo salons" is three states, not two**, driven by the
owner's own headcount fetch: **loading** (request in flight) hides the link,
to avoid a show-then-hide flicker for the common case of a solo owner;
**unknown** (the fetch failed, or there was nothing to ask — an admin, a
member, or an artist whose token predates any salon) SHOWS it, failing open
so a real multi-artist owner never loses her entry point to one failed
request; a confirmed **count ≤ 1** hides it, count `> 1` shows it. This gate
only ever applies to the salon OWNER — a member's nav is never gated on it.

## 8. Testing

| What | Proves |
|---|---|
| Migration up → down → up | reversible, repeatable. **Measured by hand** at build time on a dump of dev (`bedge_052`): 22 expected pairs, 22 rows, 0 missing, after the first up and again after down → up (Task 1). **Executed as a DB test** since 2026-09-26: `TestMigration052_DownUpDownUp_…` runs the real `.down.sql`/`.up.sql` twice against fixture data |
| DB test: backfill (same test) | every artist × every service of **her** salon, **active and inactive**, got a row with NULL prices; no row for another salon's service; an artist with no salon gets none — **nobody disappears**. Watched it fail with the backfill narrowed to active services and with the backfill removed |
| DB tests: resolver | override wins; NULL means the salon's; deposit capped; no row means not offered; inactive service excluded |
| Guard test: no stray `services.price` | a new reader in the common shapes (FROM/JOIN/comma join, `public.`/quoted names, `SELECT *`/`s.*`) cannot bypass the resolver — **each shape proven to fire**; SQL split across two literals is **not** caught (§4) |
| Service tests | capabilities, money validation, `null` clears, `offered` required, deposit above price refused, audit written with the actor **and whose service** (owner acting on a member records the owner as actor and the member's `artist_id`) |
| DB tests: invariant | `Upsert` of another salon's service writes nothing (`ErrNotFound`), a stray foreign row is not updated, and both customer-facing lists exclude a stray foreign-salon row |
| `validateBookingParties` | switched-off service → `404 SERVICE_NOT_FOUND`, identical to missing |
| **End to end** | profile price = hold price = stored booking price, with and without an override |
| Chaos suite (3.7/3.7b) | owner + two members of one salon: owner at the salon price, one member at $200, one at $100 — listed == held == stored for all three; the switched-off member's service refused with `404 SERVICE_NOT_FOUND` — 3.7b runs only when that member's 3.7 leg passed (a positive control, as in 3.6), so it cannot pass on a member who never offered the service |
| WebKit, 390px (`b-edge-web/scripts/verify-offerings-ui.mjs`) | the switch screen, including the join step (Chromium); since the final-review fixes also: a cancelled turn-off confirm on a priced row keeps the switch ON and the row priced, "Use salon price & deposit" then a deposit-only save keeps the price NULL, the audit names whose service changed (with role and IP), and a PUT without `offered` is 422 — 23 pass, 0 fail, 0 residual out of 33 (2026-09-26). The browser checks were seen passing only; the same behaviours were watched failing in the component spec and Go tests |
| `make mutation` | run on `internal/offering` only (Task 14): 13 killed, 1 lived, 28 not covered — gremlins runs without `-tags dbtest` and the package has no handler test, so repository and handler mutants are invisible to it. **Not run on the resolver** (`internal/pkg/pricing`), whose SQL only DB tests exercise |

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

## 10. Two live defects this plan also fixed

Found while reading the code during planning, unrelated to per-artist pricing
itself but fixed along the way because both would have gotten worse under it:

1. **Approval used to quote the menu's deposit, not the booking's.**
   `ApproveBooking` built *"Please send a $X deposit"* from
   `service.DepositAmount` — the salon menu's *current* value — instead of
   `b.DepositAmount`, the amount stored when the booking was made. With
   per-artist deposits this would have quoted the salon's deposit to a
   customer who booked a member with a different one. Fixed in `2ed7834`
   ("book her service at her price; approval quotes the booking's deposit").
2. **The confirmation screen omitted the early-bird fee.** The picker badges
   early-bird slots and the hold charges `price + early_bird_fee`, but the
   last funnel screen displayed `service().price` — a customer confirming at
   $150 was actually charged $165. Fixed in two halves: the API in
   `b-edge-api` `3240d1a` ("the hold returns what it charged, early-bird fee
   included") — the hold now returns
   `original_price`/`early_bird_fee`/`final_price`/`deposit_amount`; and the
   screen in `b-edge-web` `05d45cb` ("confirm at the price the hold charged,
   early-bird fee included") — the confirmation screen shows those instead of
   the remembered catalogue price.

## 11. Observed while designing — not part of this work

**Leaving a salon does not remove the member's `artist_stores` links**
(`DetachArtist` only sets `salon_id = NULL`), and Discover joins on
`artist_stores`. Whether a departed member can still surface on Discover
depends on the visibility condition, which was not checked. **To verify
separately**; not asserted as a bug here.
