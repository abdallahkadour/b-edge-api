-- Reverses 050, restoring the never-charged original ladder.
UPDATE plans SET monthly_price = 7.00,  included_seats = 1, seat_price = 0.00,
                 is_public = TRUE, sort_order = 10 WHERE code = 'starter';
UPDATE plans SET monthly_price = 15.00, included_seats = 1, seat_price = 0.00,
                 is_public = TRUE, sort_order = 20 WHERE code = 'growth';
UPDATE plans SET monthly_price = 25.00, included_seats = 2, seat_price = 6.00,
                 name = 'Studio', is_public = TRUE, sort_order = 30 WHERE code = 'studio';
UPDATE plans SET monthly_price = 50.00, included_seats = 5, seat_price = 6.00,
                 name = 'Multi-Location', is_public = TRUE, sort_order = 40 WHERE code = 'multi';
UPDATE plans SET monthly_price = 80.00, included_seats = 5, seat_price = 8.00,
                 is_public = TRUE, sort_order = 50 WHERE code = 'enterprise';
DELETE FROM plans WHERE code IN ('solo', 'salon')
  AND NOT EXISTS (SELECT 1 FROM subscriptions WHERE plan_code = plans.code);
COMMENT ON COLUMN plans.included_seats IS NULL;
COMMENT ON COLUMN plans.seat_price IS NULL;
