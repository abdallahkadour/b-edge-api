-- 033_service_buffer.down.sql
--
-- Restores the exclusion constraint to ranging over end_time and drops the
-- buffer columns.
--
-- Lossy in one direction worth naming: any buffer an artist configured, and
-- any partially-released buffer on a live booking, is discarded. The slots
-- that cleanup time was reserving become immediately bookable. Re-running
-- the up migration restores the columns but not their values.
--
-- Safe on the constraint itself: with the columns gone the guard reverts to
-- exactly what migration 029 left, DEFERRABLE included.

ALTER TABLE bookings
  DROP CONSTRAINT IF EXISTS bookings_artist_id_tstzrange_excl;

ALTER TABLE bookings
  ADD CONSTRAINT bookings_artist_id_tstzrange_excl
  EXCLUDE USING gist (
    artist_id WITH =,
    tstzrange(start_time, end_time, '[)') WITH &&
  )
  WHERE (status NOT IN ('cancelled', 'expired', 'no_show', 'refunded'))
  DEFERRABLE INITIALLY IMMEDIATE;

ALTER TABLE bookings
  DROP CONSTRAINT IF EXISTS bookings_blocked_until_after_end;

ALTER TABLE bookings
  DROP COLUMN IF EXISTS buffer_min,
  DROP COLUMN IF EXISTS blocked_until;

ALTER TABLE services
  DROP CONSTRAINT IF EXISTS services_buffer_min_range;

ALTER TABLE services
  DROP COLUMN IF EXISTS buffer_min;
