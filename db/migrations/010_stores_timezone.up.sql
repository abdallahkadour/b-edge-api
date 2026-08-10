-- 010_stores_timezone.up.sql
--
-- Adds an IANA timezone identifier to each store.
--
-- Why this is needed:
--   business_hours.open_time / close_time and stores.early_bird_cutoff are
--   PostgreSQL TIME columns - a wall-clock time with no date and no zone.
--   The Go layer previously interpreted them as UTC, which forced whoever
--   entered the data to pre-convert Beirut local time into its UTC
--   equivalent (6am Beirut stored as "03:00:00").
--
--   That pre-conversion is only correct for half the year. Lebanon observes
--   DST: Beirut is UTC+3 from late March, UTC+2 from late October. A stored
--   "03:00:00" means 6am Beirut in summer and 5am Beirut in winter, so every
--   store silently opens an hour early the moment the clocks change.
--
--   The fix is the standard one used across the booking industry (Wix
--   Bookings, Calendly-style schedulers, etc.): store the wall-clock LOCAL
--   time as the artist actually means it, store the location's IANA zone
--   alongside it, and resolve the two into a real instant at query time.
--   IANA zone names encode their own DST rules, so the conversion stays
--   correct across clock changes without any data migration.
--
--   A raw offset ("+03:00") is NOT sufficient - an offset does not know
--   when it changes. Only a named zone does.
--
-- Default:
--   Every current store is in Lebanon, so 'Asia/Beirut' is the correct
--   backfill. NOT NULL with a default means existing rows are handled
--   without a separate UPDATE and new stores cannot be created zone-less.
--   MENA expansion sets this per store (e.g. 'Asia/Dubai', which has no DST).

ALTER TABLE stores
  ADD COLUMN IF NOT EXISTS timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Beirut';

COMMENT ON COLUMN stores.timezone IS
  'IANA timezone identifier (e.g. Asia/Beirut, Asia/Dubai). business_hours '
  'open_time/close_time and stores.early_bird_cutoff are wall-clock LOCAL '
  'times in this zone, resolved to instants at query time. Never store a raw '
  'UTC offset here - offsets do not encode DST transitions.';
