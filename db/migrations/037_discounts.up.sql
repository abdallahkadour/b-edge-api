-- 037_discounts.up.sql
--
-- Promotions: the rows behind internal/pkg/discount.
--
--
-- WHAT THIS DOES NOT DO
--
-- It stores no computed price. `bookings.discount_amount` already exists
-- (migration 001) and has been hardcoded to zero at exactly two sites since;
-- this migration gives it something to hold, and adds the same pair to
-- `orders` so both money paths stay symmetrical.
--
-- The arithmetic lives in internal/pkg/discount, which is pure and has no
-- database access at all. Nothing here decides a price.
--
--
-- THE UNIQUENESS CONSTRAINT IS THE POLICY
--
-- Sprint 9's plan put it precisely: the join table's "uniqueness constraint is
-- what enforces first-time and per-customer limits". That is deliberate rather
-- than incidental.
--
-- idx_discount_redemption_once is a PARTIAL unique index on
-- (discount_id, customer_id) WHERE consumed. It gives three things at once:
--
--   * one use per customer per code, enforced atomically. Two concurrent
--     checkouts with the same code cannot both succeed - one gets a
--     constraint violation rather than both reading "0 uses so far".
--   * first-time-only codes, since the customer's first redemption is their
--     only one.
--   * D3.5's release rule for free. Cancelling early sets consumed = FALSE,
--     the row leaves the index, and the code becomes usable again. No
--     deletion, so the history of what was offered survives.
--
-- An application-level "SELECT count(*) then INSERT" would be a race, and the
-- race is worth money.
--
--
-- WHY A REDEMPTION POINTS AT A BOOKING *OR* AN ORDER, NEVER BOTH
--
-- The two money paths are separate everywhere else in this schema and stay
-- separate here. num_nonnulls(...) = 1 makes a row that belongs to both, or to
-- neither, unrepresentable rather than merely discouraged.
--
--
-- SUBSCRIPTION INVOICES ARE OUT OF SCOPE (D3.6)
--
-- There is deliberately no invoice_id column. Keeping the artist-billing and
-- customer-booking money models apart means a bug in customer discounting
-- cannot corrupt B-Edge's own revenue record - and invoices already carry two
-- known-wrong behaviours pinned by characterization tests. Discounting an
-- artist's subscription is a price change on an editable plan row, not a promo
-- code.

-- ── The promotions themselves ────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS discounts (
    id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Salon-scoped. A code belongs to the salon that honours it; there is no
    -- platform-wide promotion concept, because B-Edge holds no money and
    -- therefore cannot fund one.
    salon_id         UUID NOT NULL REFERENCES salons(id) ON DELETE CASCADE,

    code             VARCHAR(32)  NOT NULL,
    description      TEXT,

    kind             VARCHAR(16)  NOT NULL,
    value            NUMERIC(10,2) NOT NULL,

    -- Eligibility. All optional: a code with none of these set is simply
    -- always valid while active.
    starts_at        TIMESTAMPTZ,
    ends_at          TIMESTAMPTZ,
    max_redemptions  INTEGER,
    first_time_only  BOOLEAN      NOT NULL DEFAULT FALSE,

    is_active        BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT discounts_kind_check
        CHECK (kind IN ('fixed', 'percentage')),

    -- A percentage is 0-100; a fixed amount is bounded by the money column.
    -- Enforced here because a 500% code is a configuration error that would
    -- otherwise be silently clamped by the resolver and never noticed.
    CONSTRAINT discounts_value_range
        CHECK (
            value > 0 AND (
                (kind = 'percentage' AND value <= 100) OR
                (kind = 'fixed'      AND value <= 99999999.99)
            )
        ),

    -- Migration 034's rule, applied to a new money column.
    CONSTRAINT discounts_value_not_nan CHECK (value <> 'NaN'::numeric),

    CONSTRAINT discounts_window_ordered
        CHECK (ends_at IS NULL OR starts_at IS NULL OR ends_at > starts_at),

    CONSTRAINT discounts_max_redemptions_positive
        CHECK (max_redemptions IS NULL OR max_redemptions > 0)
);

-- Case-insensitive uniqueness per salon: customers type codes by hand and
-- "SUMMER20" and "summer20" must not be two different promotions.
CREATE UNIQUE INDEX IF NOT EXISTS idx_discounts_salon_code
    ON discounts (salon_id, UPPER(code));

COMMENT ON COLUMN discounts.first_time_only IS
    'Restricts the code to a customer with no prior completed booking at this '
    'salon. Evaluated in the service layer; the redemption index enforces the '
    'once-per-customer half.';

-- ── Who used what ────────────────────────────────────────────────────────────

CREATE TABLE IF NOT EXISTS discount_redemptions (
    id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    discount_id  UUID NOT NULL REFERENCES discounts(id) ON DELETE CASCADE,
    customer_id  UUID NOT NULL REFERENCES users(id),

    booking_id   UUID REFERENCES bookings(id) ON DELETE CASCADE,
    order_id     UUID REFERENCES orders(id)   ON DELETE CASCADE,

    -- What the code actually took off, which is NOT always what it advertises:
    -- the resolver caps a discount at the deposit, so a $40 code on a $50
    -- service with a $20 deposit records $30. The receipt and this row agree.
    amount       NUMERIC(10,2) NOT NULL,

    -- D3.5. FALSE means the booking was cancelled in a way that does not
    -- penalise the customer, and the code is theirs to use again.
    consumed     BOOLEAN      NOT NULL DEFAULT TRUE,

    created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

    CONSTRAINT discount_redemptions_one_target
        CHECK (num_nonnulls(booking_id, order_id) = 1),

    CONSTRAINT discount_redemptions_amount_sane
        CHECK (amount >= 0 AND amount <> 'NaN'::numeric)
);

-- The policy, as an index. See the header.
CREATE UNIQUE INDEX IF NOT EXISTS idx_discount_redemption_once
    ON discount_redemptions (discount_id, customer_id)
    WHERE consumed;

-- Counting total redemptions against max_redemptions.
CREATE INDEX IF NOT EXISTS idx_discount_redemptions_discount
    ON discount_redemptions (discount_id) WHERE consumed;

COMMENT ON INDEX idx_discount_redemption_once IS
    'Enforces one use per customer per code ATOMICALLY - two concurrent '
    'checkouts cannot both succeed. Partial on `consumed` so releasing a '
    'redemption (D3.5) frees the code without deleting the history.';

-- ── The order side, mirroring bookings ───────────────────────────────────────

ALTER TABLE orders
    ADD COLUMN IF NOT EXISTS discount_amount NUMERIC(10,2) NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS discount_code   VARCHAR(32);

ALTER TABLE orders
    ADD CONSTRAINT orders_discount_amount_sane
    CHECK (discount_amount >= 0 AND discount_amount <> 'NaN'::numeric);

COMMENT ON COLUMN orders.discount_amount IS
    'Mirrors bookings.discount_amount so both money paths have the same shape. '
    'total_amount is the amount actually owed, AFTER this reduction.';

ALTER TABLE bookings
    ADD COLUMN IF NOT EXISTS discount_code VARCHAR(32);

COMMENT ON COLUMN bookings.discount_code IS
    'The code that produced discount_amount, denormalised so a receipt renders '
    'without a join and survives the discount row being deleted.';
