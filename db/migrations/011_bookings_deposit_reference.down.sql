-- 011_bookings_deposit_reference.down.sql
ALTER TABLE bookings DROP COLUMN IF EXISTS deposit_reference;
