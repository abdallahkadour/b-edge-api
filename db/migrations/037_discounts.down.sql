-- 037_discounts.down.sql
--
-- DESTRUCTIVE. Dropping discount_redemptions discards the record of who used
-- which code, which is the only place that history exists - bookings and
-- orders keep the AMOUNT and the CODE, but not the per-customer usage that
-- enforces "once each".
--
-- The columns on bookings/orders are dropped too, so any discount already
-- applied stops being visible on its receipt while the price it produced stays
-- in final_price / total_amount. That asymmetry is why this is a real
-- down-migration and not a casual one: run it and the numbers no longer
-- explain themselves.

ALTER TABLE bookings DROP COLUMN IF EXISTS discount_code;

ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_discount_amount_sane;
ALTER TABLE orders DROP COLUMN IF EXISTS discount_code;
ALTER TABLE orders DROP COLUMN IF EXISTS discount_amount;

DROP INDEX IF EXISTS idx_discount_redemptions_discount;
DROP INDEX IF EXISTS idx_discount_redemption_once;
DROP TABLE IF EXISTS discount_redemptions;

DROP INDEX IF EXISTS idx_discounts_salon_code;
DROP TABLE IF EXISTS discounts;
