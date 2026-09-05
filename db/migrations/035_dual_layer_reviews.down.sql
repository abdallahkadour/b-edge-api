-- 035_dual_layer_reviews.down.sql
--
-- DESTRUCTIVE, and not symmetrically so.
--
-- Dropping reviews.salon_rating discards every venue rating customers have
-- given. Unlike most of this project's down-migrations, that data cannot be
-- recomputed from anything else - it was collected from people, once, and
-- there is no second source.
--
-- store_id and the stores aggregate are safe: the first is a copy of
-- bookings.store_id and the second is derived. salon_rating is the one that
-- matters. Take a dump before running this if any venue rating exists:
--
--   SELECT count(*) FROM reviews WHERE salon_rating IS NOT NULL;

DROP INDEX IF EXISTS idx_reviews_store_visible;

ALTER TABLE stores  DROP CONSTRAINT IF EXISTS stores_rating_range;
ALTER TABLE stores  DROP COLUMN IF EXISTS rating;
ALTER TABLE stores  DROP COLUMN IF EXISTS review_count;

ALTER TABLE reviews DROP CONSTRAINT IF EXISTS reviews_salon_rating_check;
ALTER TABLE reviews DROP COLUMN IF EXISTS salon_rating;
ALTER TABLE reviews DROP COLUMN IF EXISTS store_id;
