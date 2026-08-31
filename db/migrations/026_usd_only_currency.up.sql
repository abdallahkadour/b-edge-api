-- 026_usd_only_currency.up.sql
--
-- Constrains every money column in the billing domain to USD.
--
-- Why this is needed:
--   B-Edge-Monetization-Implementation-Spec-v1.md section 11 listed the
--   currency question as still-open, and flagged that it had to be settled
--   "before migration 025 ships" because retrofitting a second currency
--   onto historical invoices is genuinely painful. 025 shipped with it
--   still open.
--
--   Decided 2026-08-30: USD throughout. No LBP collection. Prices are
--   quoted in USD and paid in USD, matching how professional services are
--   already transacted in Lebanon (see B-Edge-Pricing-Strategy-v1.docx,
--   which notes the LBP collapse made USD the de facto currency).
--
--   plans.currency, subscriptions.currency and invoices.currency were all
--   VARCHAR(3) DEFAULT 'USD' with nothing stopping a second currency
--   arriving. A CHECK constraint is what turns that decision into
--   something the database enforces rather than something a future
--   contributor has to know.
--
-- What this deliberately does NOT do:
--   It does not drop the currency columns. Keeping them costs nothing and
--   means a future multi-currency decision is a constraint change plus the
--   collected-amount/fx-rate columns, rather than re-adding a column to
--   three tables and backfilling it. The constraint records the decision;
--   the column preserves the option.
--
-- Consequence for the application layer:
--   internal/billing's parseNonNegativeDecimal and CreatePlanRequest still
--   accept a currency field and uppercase it. A non-USD value now fails at
--   the database with a constraint violation rather than being silently
--   stored, which surfaces as a 500 rather than a 400. Rejecting it in the
--   service layer is the follow-up; this migration is the backstop that
--   guarantees the invariant regardless.

ALTER TABLE plans
  ADD CONSTRAINT plans_currency_usd_only
  CHECK (currency = 'USD');

ALTER TABLE subscriptions
  ADD CONSTRAINT subscriptions_currency_usd_only
  CHECK (currency = 'USD');

ALTER TABLE invoices
  ADD CONSTRAINT invoices_currency_usd_only
  CHECK (currency = 'USD');

COMMENT ON COLUMN plans.currency IS
  'Always USD - enforced by plans_currency_usd_only. Decided 2026-08-30; see migration 026.';
COMMENT ON COLUMN subscriptions.currency IS
  'Always USD - enforced by subscriptions_currency_usd_only. Decided 2026-08-30; see migration 026.';
COMMENT ON COLUMN invoices.currency IS
  'Always USD - enforced by invoices_currency_usd_only. Decided 2026-08-30; see migration 026.';
