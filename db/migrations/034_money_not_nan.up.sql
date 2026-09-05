-- 034_money_not_nan.up.sql
--
-- Forbid NaN in every numeric column.
--
--
-- WHY THIS EXISTS
--
-- On 2026-09-05 a security pass (INJ-04) found that `{"price":"NaN"}` reached
-- Postgres through an endpoint that never parsed the value. NUMERIC accepts
-- 'NaN', so the write committed. From that moment the row was unreadable:
-- scanning NaN into a decimal.Decimal errors, so every read 500'd - including
-- the read the update path performs first, which meant the value could not be
-- corrected through the API at all. Repair required direct SQL.
--
-- It also spread. NaN propagates through arithmetic, so SUM(price) across the
-- salon returned NaN and would have poisoned any earnings or revenue figure
-- that touched the row.
--
-- internal/pkg/money now validates every money string before it reaches SQL,
-- and that is the real fix. This migration is the second line: a parser is
-- code someone can bypass with a new endpoint, whereas a CHECK constraint is
-- enforced for every writer including psql.
--
--
-- WHY `<> 'NaN'` AND NOT THE OBVIOUS ALTERNATIVES
--
-- Two constraints that look correct do nothing here, and both were written and
-- tested against this database before settling on the one below.
--
--   CHECK (price = round(price, 2))
--     Rejects NEITHER NaN nor excess scale.
--     * PostgreSQL defines NaN = NaN as TRUE for numeric - a deliberate
--       departure from IEEE 754 so that NaN can be sorted and indexed. So
--       round(NaN,2) = NaN evaluates to true and the row passes.
--     * A NUMERIC(10,2) column COERCES on input, before any constraint is
--       evaluated. By the time the CHECK runs, 10.999 is already 11.00 and the
--       comparison trivially holds.
--
--   CHECK (price >= 0)
--     Does not catch NaN either. PostgreSQL sorts NaN above every other
--     numeric value, so 'NaN'::numeric >= 0 is TRUE. A non-negativity
--     constraint gives no protection at all here, which is exactly the kind of
--     assumption that survives code review.
--
-- `<> 'NaN'::numeric` works precisely because NaN = NaN is true: the
-- inequality is therefore false, the CHECK fails, and the row is rejected.
-- Ordinary values compare normally and are unaffected.
--
--
-- WHAT THIS DOES NOT DO
--
-- It does not enforce scale. That is not an oversight and it is not
-- fixable here: the column coerces excess decimals before a constraint can
-- object, so "10.999" silently becoming 11.00 can ONLY be prevented in the
-- application. internal/pkg/money is therefore not defence-in-depth for
-- scale - it is the only defence. See
-- project-docs/B-Edge-Cross-Layer-Data-Flow-Audit-v1.md §5.2.
--
-- Infinity needs no constraint: NUMERIC(10,2) already rejects it as a field
-- overflow, because an infinite value cannot fit a declared precision.
--
--
-- SAFETY
--
-- Verified before writing: zero rows across all 16 numeric columns currently
-- hold NaN, so every ADD CONSTRAINT validates against existing data without a
-- rewrite. artists.rating is included even though it is derived from an AVG
-- rather than user input - an aggregate over a poisoned row is exactly how a
-- NaN would spread into it.

ALTER TABLE artists       ADD CONSTRAINT artists_rating_not_nan            CHECK (rating          <> 'NaN'::numeric);

ALTER TABLE services      ADD CONSTRAINT services_price_not_nan           CHECK (price           <> 'NaN'::numeric);
ALTER TABLE services      ADD CONSTRAINT services_deposit_not_nan         CHECK (deposit_amount  <> 'NaN'::numeric);

ALTER TABLE bookings      ADD CONSTRAINT bookings_original_price_not_nan  CHECK (original_price  <> 'NaN'::numeric);
ALTER TABLE bookings      ADD CONSTRAINT bookings_final_price_not_nan     CHECK (final_price     <> 'NaN'::numeric);
ALTER TABLE bookings      ADD CONSTRAINT bookings_deposit_not_nan         CHECK (deposit_amount  <> 'NaN'::numeric);
ALTER TABLE bookings      ADD CONSTRAINT bookings_discount_not_nan        CHECK (discount_amount <> 'NaN'::numeric);

ALTER TABLE products      ADD CONSTRAINT products_price_not_nan           CHECK (price           <> 'NaN'::numeric);

ALTER TABLE orders        ADD CONSTRAINT orders_total_not_nan             CHECK (total_amount    <> 'NaN'::numeric);
ALTER TABLE order_items   ADD CONSTRAINT order_items_unit_price_not_nan   CHECK (unit_price      <> 'NaN'::numeric);
ALTER TABLE order_items   ADD CONSTRAINT order_items_subtotal_not_nan     CHECK (subtotal        <> 'NaN'::numeric);

ALTER TABLE stores        ADD CONSTRAINT stores_early_bird_fee_not_nan    CHECK (early_bird_fee  <> 'NaN'::numeric);

ALTER TABLE plans         ADD CONSTRAINT plans_monthly_price_not_nan      CHECK (monthly_price   <> 'NaN'::numeric);
ALTER TABLE plans         ADD CONSTRAINT plans_seat_price_not_nan         CHECK (seat_price      <> 'NaN'::numeric);
ALTER TABLE subscriptions ADD CONSTRAINT subscriptions_monthly_not_nan    CHECK (monthly_price   <> 'NaN'::numeric);
ALTER TABLE invoices      ADD CONSTRAINT invoices_amount_not_nan          CHECK (amount          <> 'NaN'::numeric);
