-- Reverses 049.
--
-- Drops the columns only. Any business_hours rows seeded from them while
-- this migration was in place are left alone: by the time anyone rolls back,
-- those are a salon's real trading hours, and deleting them would take the
-- salon off the market.
ALTER TABLE stores DROP CONSTRAINT IF EXISTS stores_default_hours_check;
ALTER TABLE stores DROP COLUMN IF EXISTS default_open_time;
ALTER TABLE stores DROP COLUMN IF EXISTS default_close_time;
