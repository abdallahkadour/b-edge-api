-- 050_price_ladder.up.sql
--
-- Replaces a price ladder that was never charged, and removes per-seat
-- billing before it is built.
--
-- ── Why ──────────────────────────────────────────────────────────────────
--
-- The old ladder was starter $7 / growth $15 / studio $25 / multi $50 /
-- enterprise $80, with `seat_price` and `included_seats` populated for a
-- per-artist charge that no code ever applied. Nobody has ever been billed:
-- every live subscription is `comped`.
--
-- Two outside reviews on 2026-09-23 - a pricing strategist and a sales
-- manager, briefed separately - reached the same conclusions independently:
--
--   1. The bottom of the ladder was 6-8x too low. Normalised to one solo
--      artist doing $3,000/month, Fresha, Booksy, Vagaro and GlossGenius all
--      extract ~$105/month, about 3.5% of booking volume - but only ~20% of
--      that is subscription. The rest is payment processing (2.2-3.5%) and
--      marketplace commission (Fresha 20% of a new client's first visit,
--      Booksy up to 30% on boosted bookings).
--
--      B-Edge has NO processing margin and NO commission. There are no card
--      rails in Lebanon; customers pay deposits directly to the salon's own
--      OMT or Whish number and B-Edge never touches the money. So the
--      subscription has to carry alone what competitors spread over three
--      revenue lines.
--
--   2. Per-seat pricing is wrong here, for a reason that has nothing to do
--      with money: salon staffing in Lebanon is fluid - chair renters,
--      part-timers, family on Saturdays. Charge per artist and the owner
--      registers two of six to keep the bill down. The calendar is then
--      wrong, the product looks broken, and she churns blaming B-Edge. The
--      entire value of this platform is a COMPLETE calendar. Taxing the
--      thing that makes the product work is self-defeating.
--
--      Collection makes it worse. Every payment is a human OMT transfer
--      confirmed by hand; a per-seat bill changes amount on every hire and
--      departure, and each change is a fresh transfer, confirmation and
--      dispute. Banded flat pricing keeps the invoice amount stable for a
--      year at a time.
--
-- ── What this does ───────────────────────────────────────────────────────
--
-- included_seats stops being "seats you have paid for" and becomes a CEILING
-- - the most artists a salon on this tier may have. seat_price goes to 0
-- everywhere: there is no per-artist charge at any tier.
--
-- ── Prices are quotable, not validated ───────────────────────────────────
--
-- These are the higher of two recommended ladders ($45 entry vs $19),
-- deliberately: you can come down from a quoted price and you cannot go up.
-- They are unvalidated. The test is to quote five real salons and count how
-- many actually transfer money - three of five means the ladder holds, zero
-- means drop a band. Nothing here is charged to anyone yet; every live
-- subscription remains `comped`.
--
-- ── What is NOT removed, and why ─────────────────────────────────────────
--
-- starter, growth and enterprise rows stay, marked non-public. subscriptions
-- .plan_code is a foreign key to this table, and internal/admin hands every
-- newly approved artist `starter`. Deleting the rows would dangle the key
-- and break approval. They are retired from view, not erased.

-- The ladder. Solo is the floor: below about $45/month an invoice costs more
-- in founder time to chase and confirm than it returns.
INSERT INTO plans (code, name, monthly_price, currency, seat_price, included_seats,
                   description, is_public, sort_order)
VALUES
  ('solo',  'Solo',  45.00,  'USD', 0, 1,
   'One artist, one location. Everything included.', TRUE, 10),
  ('salon', 'Salon', 189.00, 'USD', 0, 12,
   'Up to 12 artists across 2 locations.', TRUE, 30)
ON CONFLICT (code) DO UPDATE
  SET name = EXCLUDED.name, monthly_price = EXCLUDED.monthly_price,
      seat_price = EXCLUDED.seat_price, included_seats = EXCLUDED.included_seats,
      description = EXCLUDED.description, is_public = EXCLUDED.is_public,
      sort_order = EXCLUDED.sort_order, updated_at = NOW();

-- Repriced. Same codes so nothing referencing them dangles.
UPDATE plans SET monthly_price = 109.00, included_seats = 4,  seat_price = 0,
                 name = 'Studio', description = 'Up to 4 artists, one location.',
                 is_public = TRUE, sort_order = 20, updated_at = NOW()
 WHERE code = 'studio';

UPDATE plans SET monthly_price = 249.00, included_seats = 25, seat_price = 0,
                 name = 'Multi', description = 'Up to 25 artists across 5 locations.',
                 is_public = TRUE, sort_order = 40, updated_at = NOW()
 WHERE code = 'multi';

-- Retired from the public list. Rows kept so the foreign key holds.
UPDATE plans SET is_public = FALSE, seat_price = 0, updated_at = NOW()
 WHERE code IN ('starter', 'growth', 'enterprise');

-- comped keeps its 999 ceiling. It is how the launch artist and the internal
-- test roster run, and it must never be a limit.
UPDATE plans SET seat_price = 0, updated_at = NOW() WHERE code = 'comped';

COMMENT ON COLUMN plans.included_seats IS
    'CEILING: the most artists a salon on this plan may have. NOT a quantity '
    'billed for - seat_price is 0 on every plan and there is no per-artist '
    'charge. Crossing the ceiling moves a salon up a band; it never adds a '
    'line to an invoice.';

COMMENT ON COLUMN plans.seat_price IS
    'Retired 2026-09-23 and 0 on every plan. Per-artist billing was designed '
    'and never implemented; two independent reviews recommended against it. '
    'The column is kept rather than dropped only because nothing else depends '
    'on its absence - do not reintroduce a per-seat charge without revisiting '
    'why it was rejected (see this migration''s header).';
