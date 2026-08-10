-- 011_bookings_deposit_reference.up.sql
--
-- Adds an optional free-text reference for the artist's own deposit
-- reconciliation - e.g. an OMT/Wish transaction code, or a short note.
-- Purely for the artist's own record-keeping; never validated or shown to
-- the customer. Matches the "Transaction Ref or notes (Optional)" field on
-- the Deposit Queue verification screen.

ALTER TABLE bookings
  ADD COLUMN IF NOT EXISTS deposit_reference VARCHAR(255);

COMMENT ON COLUMN bookings.deposit_reference IS
  'Optional artist-entered note captured when confirming a deposit - e.g. '
  'an OMT/Wish transaction code. Free text, not validated, not customer-facing.';
