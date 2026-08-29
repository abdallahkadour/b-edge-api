-- 024_subscriptions.up.sql
--
-- One row per artist, holding their current plan/seat assignment. This is
-- the second half of B-Edge's own accounts-receivable system - since there
-- is no payment gateway, B-Edge has to track subscription state itself
-- rather than delegating it to Stripe/Chargebee. See
-- B-Edge-Monetization-Implementation-Spec-v1.md for the full design.
--
-- Deliberately has NO status column. Whether an artist is trialing/active/
-- past_due/suspended is a pure function of the date columns below, computed
-- at read time (internal/billing.DeriveStatus) - not stored and flipped by
-- a background job. B-Edge has no scheduler by deliberate choice elsewhere
-- in this codebase (RELEASE-CHECKLIST.md: ExpireDeadlineBookings and
-- friends are called lazily from read paths, not a cron/ticker); a stored
-- status column would be the first thing here that actually needed one, so
-- this avoids that entirely. A machine that was down for a week wakes up
-- with correct billing state with zero catch-up logic required.
CREATE TABLE IF NOT EXISTS subscriptions (
  id                  UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  artist_id           UUID NOT NULL UNIQUE REFERENCES artists(id) ON DELETE CASCADE,

  -- References plans.code for LABELLING only - monthly_price/currency/seats
  -- below are snapshotted at signup/change time, never read through this
  -- FK. If an invoice or a subscription's charge amount were computed by
  -- joining to plans, editing a price there would retroactively change
  -- what an existing subscriber is being charged - see
  -- B-Edge-Monetization-Implementation-Spec-v1.md section 6.4. The FK
  -- exists only so plan_code can never point at a code that was never real.
  plan_code           VARCHAR(30) NOT NULL REFERENCES plans(code),
  seats               INTEGER      NOT NULL DEFAULT 1 CHECK (seats >= 1),
  monthly_price       NUMERIC(10,2) NOT NULL CHECK (monthly_price >= 0),
  currency            VARCHAR(3)   NOT NULL DEFAULT 'USD',

  -- Lifecycle dates - the actual source of truth for DeriveStatus.
  trial_ends_at       TIMESTAMPTZ,
  current_period_end  TIMESTAMPTZ,

  -- Set only by an explicit admin action (or a future self-service
  -- cancel), never by the passage of time - distinguishes "chose to leave"
  -- from "stopped paying," which matters for re-engagement later.
  cancelled_at        TIMESTAMPTZ,

  created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Backs the admin billing overview's sort/scan and the lazy invoice
-- generator's "who might need a new invoice" pass.
CREATE INDEX IF NOT EXISTS idx_subscriptions_period_end
  ON subscriptions (current_period_end);

COMMENT ON COLUMN subscriptions.plan_code IS
  'Label only, matching plans.code - never the source of truth for what '
  'this artist is actually charged. See monthly_price/seats/currency.';
COMMENT ON COLUMN subscriptions.cancelled_at IS
  'Explicit cancellation only. NEVER set by a scheduled job - lapsing into '
  'suspended for non-payment is a derived STATUS (see DeriveStatus), not '
  'this column. This distinguishes "chose to leave" from "stopped paying."';

-- Backfill: every artist that exists today was onboarded before this table
-- did, so none of them ever "signed up" for a plan in any real sense -
-- billing them retroactively for months they never agreed to pay for would
-- be wrong, not just awkward. Grandfathered onto 'comped' (free, no
-- trial/period dates needed - DeriveStatus treats comped as always active
-- regardless of dates) rather than left with no subscription row at all,
-- so the admin billing overview shows every real artist with an explicit,
-- correct status instead of an all-artists-missing gap that looks like a
-- bug. An admin can move any of them onto a real paid plan later via
-- PATCH /admin/billing/subscriptions/:id once that endpoint exists.
INSERT INTO subscriptions (artist_id, plan_code, seats, monthly_price, currency)
SELECT id, 'comped', 1, 0, 'USD'
FROM artists
ON CONFLICT (artist_id) DO NOTHING;
