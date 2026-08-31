-- 026_usd_only_currency.down.sql
--
-- Drops the USD-only constraints, restoring the pre-026 state where any
-- three-character currency code could be stored.
--
-- Note this is genuinely reversible with no data loss: the constraints only
-- ever rejected writes, and every existing row is USD either way. Rolling
-- back does not resurrect a multi-currency capability that ever worked - it
-- only removes the guard.

ALTER TABLE invoices      DROP CONSTRAINT IF EXISTS invoices_currency_usd_only;
ALTER TABLE subscriptions DROP CONSTRAINT IF EXISTS subscriptions_currency_usd_only;
ALTER TABLE plans         DROP CONSTRAINT IF EXISTS plans_currency_usd_only;

COMMENT ON COLUMN plans.currency IS NULL;
COMMENT ON COLUMN subscriptions.currency IS NULL;
COMMENT ON COLUMN invoices.currency IS NULL;
