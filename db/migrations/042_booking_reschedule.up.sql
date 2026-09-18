-- Lets a client move an appointment instead of destroying it.
--
-- THE TRAP THIS REMOVES
--
-- A client who needed a different time had exactly one option: cancel and book
-- again. That loses the slot to whoever books it first, and inside 24 hours it
-- also forfeits the deposit - so the product punished someone for the ordinary
-- act of having something come up. Every competitor surveyed offers
-- self-service rescheduling.
--
-- WHY A COUNTER RATHER THAN A FLAG
--
-- Unlimited rescheduling is its own abuse: a slot held and moved repeatedly is
-- a slot nobody else can book, with no deposit ever at risk. A count is also
-- the honest thing to show an artist deciding whether to trust a client, which
-- a boolean could not do.
ALTER TABLE bookings
  ADD COLUMN IF NOT EXISTS reschedule_count INTEGER NOT NULL DEFAULT 0
    CHECK (reschedule_count >= 0 AND reschedule_count <= 10);
