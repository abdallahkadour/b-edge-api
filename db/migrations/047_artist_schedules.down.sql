-- Reverses 047.
--
-- Dropping these restores the pre-047 behaviour exactly: with no rows, slot
-- generation falls back to the store window for every artist, which is what
-- it did before this migration existed. No data outside these tables refers
-- to them.
DROP TABLE IF EXISTS artist_schedule_exceptions;
DROP TABLE IF EXISTS artist_schedules;
