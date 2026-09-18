-- Drops the payment destinations. Clients go back to being told an account
-- number over WhatsApp, with no authoritative value to check it against.
DROP INDEX IF EXISTS idx_salon_payment_methods_lookup;
DROP TABLE IF EXISTS salon_payment_methods;
