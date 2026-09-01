-- 032_drop_deposit_pending_status.down.sql
--
-- Restores 'deposit_pending' to the status CHECK constraint.
--
-- Nothing to backfill: no row ever held the value, which is why it was
-- removed. This only widens the constraint again.

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_status_check;

ALTER TABLE bookings ADD CONSTRAINT bookings_status_check
  CHECK (status IN (
    'pending', 'approved', 'held', 'deposit_pending', 'deposit_paid',
    'confirmed', 'completed', 'cancelled', 'expired', 'no_show',
    'refund_due', 'refunded'
  ));
