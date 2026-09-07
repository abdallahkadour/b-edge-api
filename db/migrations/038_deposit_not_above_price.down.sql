-- Drops the ceiling. The clamped values are NOT restored - the original
-- amounts were never collectable as stated and are not worth recovering.
ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_deposit_not_above_price;
ALTER TABLE services DROP CONSTRAINT IF EXISTS services_deposit_not_above_price;
