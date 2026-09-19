-- Reverses 044.
--
-- Dropping the column destroys every recorded payer number, and those cannot
-- be reconstructed - they were read off a transfer receipt by a human at
-- confirmation time and exist nowhere else. Any refund still outstanding
-- loses the only record of where its money must go back to.
DROP INDEX IF EXISTS idx_bookings_deposit_payer_phone;

ALTER TABLE bookings
  DROP COLUMN IF EXISTS deposit_payer_phone;
