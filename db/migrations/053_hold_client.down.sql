-- Reverts 053. Lossless apart from the hashes themselves, which only ever
-- describe unfinished holds and are cleared when each one ends.
DROP INDEX IF EXISTS idx_bookings_active_hold_client;
ALTER TABLE bookings DROP COLUMN IF EXISTS hold_client;
