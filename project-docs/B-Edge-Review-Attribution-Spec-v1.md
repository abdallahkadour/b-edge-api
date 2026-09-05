# B-Edge — Review attribution

**v1, 2026-09-06.** Sprint 8. What a review rates, who it belongs to, and the
three rules that must not be relaxed.

> **Decision D5 is closed by this document.** The proposal in
> `B-Edge-D3-D5-Proposals-v1.md` was accepted as written: primary stylist =
> highest `final_price`, tie-broken by longest duration, implemented as a
> tested pure function even though it is unreachable until split bookings
> exist.

---

## 1. One review, two scores

A review rates **two independent things**:

| Score | Column | Rates | Required |
|---|---|---|---|
| Specialist | `reviews.rating` | the person | yes |
| Venue | `reviews.salon_rating` | the room | **no** |

One review per **visit**, not per service. `UNIQUE(booking_id)` has enforced
that since migration 001 and nothing here relaxes it.

The venue score is attributed to a **store**, not a salon. Rania has Beirut
Downtown and Tripoli; they are different rooms, different parking, different
neighbourhoods. `bookings.store_id` already records which one, so the
attribution needs no new input from anybody.

---

## 2. The three rules

These come from `B-Edge-Feature-Feasibility-Assessment-v1.md` §2.2, which named
three failure modes a single rating produces the moment a salon has more than
one artist. They are restated here because each is one small, reasonable-looking
change away from being undone.

### 2.1 Never derive one score from the other

`stores.rating` is the average of `salon_rating`. `artists.rating` is the
average of `rating`. **Neither reads the other.**

"Salon rating = the average of its stylists" is the **gaming** vector: an owner
inflates the venue by hiring one star. It is also a one-line change for someone
who has not read this, which is why `recomputeStoreRatingTx` carries the
warning in its own doc comment rather than relying on this file being found.

### 2.2 The two scores are allowed to disagree

A great stylist in a tired room is a real thing, and it is useful to a customer
deciding where to go. A display that hides the disagreement — averaging them,
or showing only the higher — throws away the only reason to collect two numbers.

Both UIs therefore render the two rows **labelled and visually distinct**
("Artist" / "Salon", "You" / "Salon"). An unlabelled pair of star rows would
make disagreement read as a bug.

### 2.3 An unanswered venue question is absent, not zero

The venue question is optional. A review with only a specialist score is
**complete**.

This has three consequences, and all three are load-bearing:

- `salon_rating` is `NULL`, never `0`. The `CHECK` allows `NULL OR 1..5`.
- The store aggregate filters `salon_rating IS NOT NULL`. Without it, every
  specialist-only review would count as a zero and a well-reviewed venue would
  trend to nothing as it collected more reviews.
- The UI **omits the venue row entirely** when there is no score, and the API
  omits the field (`omitempty`). "Not rated" and "rated badly" are different
  things and must not render the same.

§2.2 of the assessment warns about survey fatigue directly: a required second
question is how response rates fall, and per-service ratings produce *less*
data than the single review you started with.

---

## 3. Who the specialist score belongs to

**Highest `final_price`; ties broken by longest duration; further ties by the
smallest artist ID.**

Price is the platform's own proxy for what the customer came for. Duration is
not: a 15-minute bridal touch-up at $200 matters more to the client than an
hour-long blow-dry at $40, and it is the touch-up they are rating. Duration
survives as the tie-break because it is deterministic and always present.

The third level exists only so the answer does not depend on the order the
caller built the slice in. Which artist wins an exact tie is arbitrary and is
not a judgement about them; determinism is the only property being bought.

`final_price`, not `original_price`: it is what the customer actually paid and
the number they themselves saw.

### Why it is written before it can run

A booking today has one artist and one service, so `PrimaryStylist` always
returns the only stylist and its second branch is unreachable.

It is written anyway, as a named function with seven tests, because the
alternative is that the rule gets inlined at the call site as
`booking.ArtistID` and is quietly lost the day split bookings land (Sprint 13)
— which is the exact moment it starts to matter. `CreateReview` calls it today
even though the answer is known, for the same reason.

---

## 4. What shipped

| Task | Where |
|---|---|
| T8.1 attribution spec | this document |
| T8.2 migration | `035_dual_layer_reviews` — `reviews.store_id`, `reviews.salon_rating`, `stores.rating`, `stores.review_count` |
| T8.3 primary-stylist rule | `internal/review/attribution.go`, 7 tests |
| T8.4 two independent aggregates | `recomputeArtistRatingTx` + `recomputeStoreRatingTx`, both in the write transaction |
| T8.5 the form asks the venue question | `leave-review.page` — optional, clearable, absent when unanswered |
| T8.6 both scores displayed | customer `reviews.page` and artist `reviews.component`, labelled |
| *(beyond the plan)* venue score on the profile | `discovery.StoreCard.rating` + `review_count`, per store, omitted when unrated — §5 |

Verified end to end against the running stack: a review submitted with
specialist 5 and venue 3 produced `stores.rating = 3.00` and
`artists.rating = 5.00` — two different numbers from one review, neither
derived from the other.

### Aggregates are a cache, and only the repository maintains it

Both are recomputed inside the same transaction as the row that changed —
create, delete, and hide/show — so a reader can never see a review its
average does not account for.

They do **not** self-heal. A row deleted by direct SQL leaves both aggregates
stale until something writes through the repository again. That is the same
posture as `artists.rating` before this sprint and is stated here so it is not
discovered as a surprise.

---

## 5. Deliberately not built

- **Per-service ratings.** §2.2 is explicit: survey fatigue, and less data than
  a single review.
- **Editing a review after submission.** Raised in the D5 proposal as out of
  scope and still is; it needs its own decision.
- **A salon-level average across stores.** Decided against, 2026-09-06. The
  venue score is published **per store** on `discovery.StoreCard` and is never
  averaged across a salon's locations. Fresha and Booksy both rate the venue
  per location, and more to the point, averaging Beirut Downtown with Tripoli
  is the *dilution* failure of §2.2 one level up — the exact thing choosing the
  store grain in migration 035 was meant to avoid. Undoing it at display time
  would make the schema decision pointless.

  `rating` is **omitted** from the card when `review_count` is 0, so an unrated
  venue publishes no score rather than "0.00". Same rule as everywhere else in
  this feature, and the same shape as `ReasonUnknown` rendering no badge.
- **Backfilling `salon_rating` for old reviews.** Nobody was asked the
  question. Inventing an answer would corrupt the first aggregate this feature
  ever computes.
