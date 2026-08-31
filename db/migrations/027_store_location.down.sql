-- 027_store_location.down.sql
--
-- Drops the store map pin columns.
--
-- Destructive: any pins artists have dropped are lost and cannot be
-- recovered by re-applying the up migration. That is acceptable here
-- because a pin is re-droppable in seconds from the store edit screen and
-- nothing depends on it (a store with no pin simply omits the map), but it
-- is worth knowing before running this against a database with real data.

ALTER TABLE stores
  DROP COLUMN IF EXISTS longitude,
  DROP COLUMN IF EXISTS latitude;
