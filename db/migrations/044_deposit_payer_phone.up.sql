-- Records WHICH NUMBER a deposit was actually sent from.
--
-- THE PROBLEM
--
-- B-Edge holds no money. Deposits move customer-to-artist directly over OMT
-- and Whish, and a refund is the same movement in reverse. Both rails are
-- addressed by PHONE NUMBER, and OMT in particular is collected in person at
-- an agent counter.
--
-- Until now the only phone on a booking was the customer's own, and the
-- system quietly assumed the deposit came from it. In practice it very often
-- does not:
--
--   * someone books on their own number and pays from a spouse's or
--     parent's Whish wallet,
--   * or they hand cash to an OMT agent, in which case the sender of record
--     is the counter, not the customer at all.
--
-- That assumption is harmless right up to the moment of a refund, which is
-- exactly when it costs real money. A refund pushed back to the booking's
-- phone when the deposit came from a different wallet does not reach the
-- person owed it, and on these rails it is not trivially reversible - there
-- is no chargeback, no card network, and no support queue to appeal to. The
-- artist is simply out of pocket and the customer is still owed.
--
-- WHAT THIS COLUMN IS
--
-- The number the money CAME FROM, captured by the artist at the moment they
-- confirm the deposit arrived - the one moment a human is already looking at
-- the transfer receipt and can read it off.
--
-- Stored as E.164 so it can be compared with users.phone by equality rather
-- than by guessing at formatting (migration 043 normalised every other phone
-- in the schema for the same reason). NULL means "not recorded", which is
-- deliberately distinct from "same as the customer's": every booking that
-- predates this column is NULL, and treating those as matching would assert
-- something nobody checked.
--
-- WHAT IT IS NOT
--
-- Not a second contact number. Nothing should ever CALL or message this
-- number - it may well be an OMT counter. It exists to answer one question
-- at refund time: is the person we are about to send money to the person who
-- sent it? The mismatch flag is derived in the service layer rather than
-- stored, because it is a comparison of two live values and a stored copy
-- would go stale the moment either changed.

ALTER TABLE bookings
  ADD COLUMN IF NOT EXISTS deposit_payer_phone VARCHAR(20);

COMMENT ON COLUMN bookings.deposit_payer_phone IS
  'E.164 number the deposit was sent from, when it differs from or is not '
  'known to match the customer''s own. NULL = not recorded. Never contact '
  'this number: it may be an OMT counter, not a person.';

-- Partial index: the refund workflow asks "which refund_due bookings were
-- paid from somewhere else", never "find me a payer number". Indexing only
-- the recorded rows keeps it small - the column is NULL for every historical
-- booking and will stay NULL whenever the payer is the customer.
CREATE INDEX IF NOT EXISTS idx_bookings_deposit_payer_phone
  ON bookings (deposit_payer_phone)
  WHERE deposit_payer_phone IS NOT NULL;
