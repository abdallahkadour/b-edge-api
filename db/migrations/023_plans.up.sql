-- 023_plans.up.sql
--
-- Plan catalogue for B-Edge subscription billing. Prices live in a table,
-- not a Go constant, specifically so an admin can change one from a UI
-- without a code change, build, or deploy - the concrete driving case is
-- "Starter launches at $7, gets raised to $10 six months later."
--
-- Deliberately NOT referenced by foreign key from subscriptions/invoices
-- (planned next - see B-Edge-Monetization-Implementation-Spec-v1.md). Both
-- of those tables will snapshot their own price at signup/issue time. If an
-- invoice instead read its amount through a join to this table, editing a
-- price here would retroactively rewrite the value of every invoice ever
-- issued at the old price - including ones already marked paid. plans.code
-- is a label other tables store for reference; it is never the source of
-- truth for what was actually charged.
CREATE TABLE IF NOT EXISTS plans (
  code           VARCHAR(30)   PRIMARY KEY,
  name           VARCHAR(80)   NOT NULL,
  monthly_price  NUMERIC(10,2) NOT NULL CHECK (monthly_price >= 0),
  currency       VARCHAR(3)    NOT NULL DEFAULT 'USD',
  seat_price     NUMERIC(10,2) NOT NULL DEFAULT 0 CHECK (seat_price >= 0),
  included_seats INTEGER       NOT NULL DEFAULT 1 CHECK (included_seats >= 1),
  description    TEXT          NOT NULL DEFAULT '',
  features       JSONB         NOT NULL DEFAULT '[]',
  is_public      BOOLEAN       NOT NULL DEFAULT TRUE,
  sort_order     INTEGER       NOT NULL DEFAULT 0,
  created_at     TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
  updated_at     TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- The public pricing page lists is_public=TRUE plans ordered by sort_order -
-- this index backs exactly that query.
CREATE INDEX IF NOT EXISTS idx_plans_public_sort ON plans (is_public, sort_order);

COMMENT ON COLUMN plans.code IS
  'Stable slug (starter/growth/studio/multi/comped). Never reused for a '
  'different plan once any subscription may reference it by this code.';
COMMENT ON COLUMN plans.is_public IS
  'FALSE hides a plan from GET /billing/plans (the public pricing page) '
  'without deleting it - used for comped/legacy tiers that must still '
  'exist for admins to assign, but were never meant to be sold.';
COMMENT ON COLUMN plans.seat_price IS
  'Cost per staff artist beyond included_seats. 0 for solo-only tiers.';

-- Seed the tiers proposed in B-Edge-Monetization-Implementation-Spec-v1.md
-- section 1. These prices are launch placeholders pending the founder's
-- final call (spec section 11), not a technical decision - editable from
-- the admin Plans tab once built, per that same spec's section 6.4 on
-- grandfathering existing subscribers when a price changes later.
INSERT INTO plans (code, name, monthly_price, seat_price, included_seats, description, features, is_public, sort_order) VALUES
  ('starter', 'Starter', 7.00, 0, 1,
    'New and independent artists just getting started.',
    '["1 artist account","Booking calendar & availability","Customer OTP login","WhatsApp booking notifications"]',
    TRUE, 1),
  ('growth', 'Growth', 15.00, 0, 1,
    'Solo bridal and event makeup artists with a higher average ticket.',
    '["Everything in Starter","Product store & orders","Waitlist for fully booked dates","Client CRM"]',
    TRUE, 2),
  ('studio', 'Studio', 25.00, 6, 2,
    'Small salons with a few staff artists.',
    '["Everything in Growth","Up to 2 staff artists included","+$6/mo per additional seat","Earnings breakdown per artist"]',
    TRUE, 3),
  ('multi', 'Multi-Location', 50.00, 6, 5,
    'Multiple studios or a larger team.',
    '["Everything in Studio","Up to 5 staff artists included","+$6/mo per additional seat","Priority support"]',
    TRUE, 4),
  ('comped', 'Comped', 0.00, 0, 999,
    'Launch partners and internal accounts - not sold, assigned by an admin only.',
    '[]',
    FALSE, 99)
ON CONFLICT (code) DO NOTHING;
