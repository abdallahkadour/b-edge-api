-- 034_money_not_nan.down.sql
--
-- Drops the NaN guards. Reversible without data loss: the constraints only
-- ever prevented writes, so nothing was altered on the way in and there is
-- nothing to restore.
--
-- Dropping these does NOT reintroduce the 2026-09-05 defect on its own -
-- internal/pkg/money still rejects NaN before it reaches SQL. It removes the
-- second line only, which is exactly what a new unvalidated endpoint would
-- then be able to walk through.

ALTER TABLE artists       DROP CONSTRAINT IF EXISTS artists_rating_not_nan;

ALTER TABLE services      DROP CONSTRAINT IF EXISTS services_price_not_nan;
ALTER TABLE services      DROP CONSTRAINT IF EXISTS services_deposit_not_nan;

ALTER TABLE bookings      DROP CONSTRAINT IF EXISTS bookings_original_price_not_nan;
ALTER TABLE bookings      DROP CONSTRAINT IF EXISTS bookings_final_price_not_nan;
ALTER TABLE bookings      DROP CONSTRAINT IF EXISTS bookings_deposit_not_nan;
ALTER TABLE bookings      DROP CONSTRAINT IF EXISTS bookings_discount_not_nan;

ALTER TABLE products      DROP CONSTRAINT IF EXISTS products_price_not_nan;

ALTER TABLE orders        DROP CONSTRAINT IF EXISTS orders_total_not_nan;
ALTER TABLE order_items   DROP CONSTRAINT IF EXISTS order_items_unit_price_not_nan;
ALTER TABLE order_items   DROP CONSTRAINT IF EXISTS order_items_subtotal_not_nan;

ALTER TABLE stores        DROP CONSTRAINT IF EXISTS stores_early_bird_fee_not_nan;

ALTER TABLE plans         DROP CONSTRAINT IF EXISTS plans_monthly_price_not_nan;
ALTER TABLE plans         DROP CONSTRAINT IF EXISTS plans_seat_price_not_nan;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_monthly_not_nan;
ALTER TABLE invoices      DROP CONSTRAINT IF EXISTS invoices_amount_not_nan;
