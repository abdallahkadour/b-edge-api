-- 035_dual_layer_reviews.up.sql
--
-- A review rates two things: the specialist, and the venue.
--
--
-- WHY TWO SCORES AND NOT ONE
--
-- B-Edge-Feature-Feasibility-Assessment-v1.md §2.2 names three failure modes a
-- single rating produces once a salon has more than one artist:
--
--   * DILUTION      - a strong stylist's average is dragged down by the venue,
--                     or a weak one is propped up by it.
--   * MISATTRIBUTED  - "the parking was awful" lands on a person's score, and
--     BLAME           "she was wonderful" lands on the building's.
--   * GAMING        - if the venue score is derived from its stylists, an owner
--                     inflates the venue by hiring one star.
--
-- The third is the reason for the hard rule below.
--
--
-- THE TWO AGGREGATES ARE INDEPENDENT. NEVER DERIVE ONE FROM THE OTHER.
--
-- stores.rating is the average of salon_rating over that store's visible
-- reviews. artists.rating is the average of rating. Neither reads the other,
-- and no view or query should make "salon rating = the average of its
-- stylists" true - that is precisely the gaming vector §2.2 warns about, and
-- it is a one-line change for someone who has not read this.
--
-- The scores are ALLOWED to disagree. A great stylist in a tired room is a
-- real and useful thing for a customer to learn, and a display that hides the
-- disagreement throws away the only reason to collect two numbers.
--
--
-- WHY THE STORE, NOT THE SALON
--
-- §2.2 says the venue. A salon can have several stores - Rania has Beirut
-- Downtown and Tripoli - and they are different rooms, different parking,
-- different neighbourhoods. Attributing a venue review to the salon would
-- merge two physical places a customer experiences separately, which is the
-- dilution failure again one level up.
--
-- bookings.store_id already records which one the appointment was at, so the
-- attribution needs no new input from anybody.
--
--
-- WHY BOTH COLUMNS ARE NULLABLE
--
--   * store_id  - every existing review predates this migration and has no
--                 venue recorded. Backfilled below where the booking still
--                 exists, but left nullable because the FK is to a store that
--                 may later be deleted, and losing the specialist rating
--                 because the room closed would be worse than a NULL.
--   * salon_rating - the venue question is OPTIONAL. §2.2's own warning about
--                 survey fatigue applies: a required second question on a form
--                 that currently asks one is how response rates fall. A review
--                 with a specialist score and no venue score is complete.
--
-- Because it is optional, the store aggregate counts only rows where
-- salon_rating IS NOT NULL - otherwise every specialist-only review would
-- silently count as a zero against the venue.
--
--
-- ONE REVIEW PER VISIT IS ALREADY ENFORCED
--
-- UNIQUE(booking_id) exists since migration 001. Nothing here relaxes it, and
-- per-service ratings are deliberately NOT introduced: §2.2 is explicit that
-- they produce survey fatigue and end up yielding less data than the single
-- review you started with.

-- ── The venue half of the review ─────────────────────────────────────────────

ALTER TABLE reviews
  ADD COLUMN IF NOT EXISTS store_id     UUID REFERENCES stores(id),
  ADD COLUMN IF NOT EXISTS salon_rating INTEGER;

ALTER TABLE reviews
  ADD CONSTRAINT reviews_salon_rating_check
  CHECK (salon_rating IS NULL OR (salon_rating >= 1 AND salon_rating <= 5));

COMMENT ON COLUMN reviews.store_id IS
  'The venue this visit happened at, copied from bookings.store_id at submit. '
  'Nullable: reviews predating migration 035 have none, and a deleted store '
  'must not cost us the specialist rating.';

COMMENT ON COLUMN reviews.salon_rating IS
  'Rating of the VENUE, 1-5, independent of reviews.rating which rates the '
  'specialist. Optional - a review with no venue score is complete. Never '
  'derive one score from the other; see migration 035.';

-- Backfill the venue from the booking that produced each review. Existing rows
-- keep salon_rating NULL: nobody was asked the question, and inventing an
-- answer would corrupt the first aggregate this feature ever computes.
UPDATE reviews r
SET    store_id = b.store_id
FROM   bookings b
WHERE  b.id = r.booking_id
  AND  r.store_id IS NULL;

-- ── The venue aggregate ──────────────────────────────────────────────────────
--
-- Mirrors artists.rating/review_count exactly - same types, same defaults, same
-- recompute-on-write approach - so the two halves stay symmetrical and neither
-- looks like the special case.

ALTER TABLE stores
  ADD COLUMN IF NOT EXISTS rating       NUMERIC(3,2) NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS review_count INTEGER      NOT NULL DEFAULT 0;

ALTER TABLE stores
  ADD CONSTRAINT stores_rating_range CHECK (rating >= 0 AND rating <= 5);

COMMENT ON COLUMN stores.rating IS
  'Average salon_rating over this store''s VISIBLE reviews that answered the '
  'venue question. 0 means no ratings yet, not a bad venue - render the '
  'absence, never a zero.';

-- Partial index matching how the aggregate is actually computed: visible rows
-- that answered the venue question, for one store.
CREATE INDEX IF NOT EXISTS idx_reviews_store_visible
  ON reviews (store_id)
  WHERE is_visible = TRUE AND salon_rating IS NOT NULL;

-- Recompute for any store that already has backfilled reviews. Currently a
-- no-op in every sense that matters - no row has a salon_rating yet - but it
-- makes the migration correct rather than merely correct-today.
UPDATE stores s
SET    rating = COALESCE((
           SELECT AVG(r.salon_rating) FROM reviews r
           WHERE r.store_id = s.id AND r.is_visible = TRUE AND r.salon_rating IS NOT NULL
       ), 0),
       review_count = (
           SELECT COUNT(*) FROM reviews r
           WHERE r.store_id = s.id AND r.is_visible = TRUE AND r.salon_rating IS NOT NULL
       ),
       updated_at = NOW();
