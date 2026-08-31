-- 029_bookings_deferrable_overlap.down.sql
--
-- Restores the non-deferrable overlap constraint from migration 001.
--
-- Safe to run: the constraint predicate is unchanged, so no existing row
-- can violate it. Any transaction relying on SET CONSTRAINTS ... DEFERRED
-- will start failing again with 23P01, which is the intended effect of
-- rolling this back.

ALTER TABLE bookings
  DROP CONSTRAINT bookings_artist_id_tstzrange_excl;

ALTER TABLE bookings
  ADD CONSTRAINT bookings_artist_id_tstzrange_excl
  EXCLUDE USING gist (
    artist_id WITH =,
    tstzrange(start_time, end_time, '[)') WITH &&
  ) WHERE (status NOT IN ('cancelled', 'expired', 'no_show', 'refunded'));
