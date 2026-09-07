-- A deposit may never exceed the price of the service it secures.
--
-- WHY THIS IS A SAFETY CONSTRAINT, NOT A TIDINESS ONE
--
-- B-Edge has no card rails. A client sends the deposit directly to the artist
-- by OMT or Whish, and the artist confirms it arrived. There is no escrow, no
-- chargeback and no intermediary holding the money, which means the deposit
-- figure shown in the funnel is the ONLY thing standing between a client and
-- an unbounded up-front transfer to a stranger.
--
-- Nothing enforced a ceiling. A service priced at $50 could carry a $500
-- deposit, and the booking funnel would have shown that number and asked the
-- client to send it. That is the shape of an advance-fee scam, and the
-- platform was capable of rendering it.
--
-- The rule is deliberately "deposit <= price" rather than a percentage cap.
-- A full-price deposit is legitimate here - bridal work and travel jobs are
-- routinely paid up front in this market - but a deposit LARGER than the
-- service can never be anything but an error or an attack.
--
-- EXISTING ROWS ARE CLAMPED, NOT REJECTED
--
-- Adding the constraint alone would fail the migration on any offending row
-- and take the deploy with it. Clamping first is the honest fix: a deposit
-- above the price was never collectable as stated, so lowering it to the
-- price loses nothing real. The UPDATE is logged by its own row count.
UPDATE services
   SET deposit_amount = price,
       updated_at     = NOW()
 WHERE deposit_amount > price;

ALTER TABLE services
  ADD CONSTRAINT services_deposit_not_above_price
  CHECK (deposit_amount <= price);

-- bookings.deposit_amount is a SNAPSHOT taken when the booking was created,
-- and is compared against original_price for the same reason. It is
-- constrained too, because a booking row is what an artist points at when
-- asking a client for money.
--
-- Compared against original_price, not final_price: a discount reduces what
-- is owed, and the discount resolver already refuses to cut below the
-- deposit, so final_price >= deposit holds without a second constraint that
-- could contradict it.
UPDATE bookings
   SET deposit_amount = original_price
 WHERE deposit_amount > original_price;

ALTER TABLE bookings
  ADD CONSTRAINT bookings_deposit_not_above_price
  CHECK (deposit_amount <= original_price);
