-- Reverts 055. Deposits already raised by the rule stay as they are; only
-- the record of why, and each salon's setting, are lost.
DROP INDEX IF EXISTS idx_bookings_no_show_history;
ALTER TABLE bookings DROP COLUMN IF EXISTS no_show_deposit;
ALTER TABLE salons DROP COLUMN IF EXISTS no_show_deposit_after;
