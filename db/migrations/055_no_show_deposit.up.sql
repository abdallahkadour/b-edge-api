-- 055: a customer who keeps missing appointments pays a deposit to book.
--
-- WHY
--
-- A no-show costs the artist the slot and, for a service that takes no
-- deposit, everything: nothing was paid and nothing is kept. Without card
-- rails a salon cannot charge a no-show fee after the fact, so the only
-- protection is asking for money BEFORE the next booking. The PRD's Phase 2
-- list names it ("auto-require deposit from clients with multiple
-- no-shows"); Fresha, Booksy and Vagaro all offer it per business.
--
-- THE RULE (decision D29, 2026-10-09)
--
-- When a customer submits a booking for a service that takes NO deposit,
-- and she has missed at least `no_show_deposit_after` appointments at THIS
-- salon in the last 12 months, the booking asks for half its price as a
-- deposit. Another salon's no-shows are that salon's business; a service
-- that already takes a deposit is already protected and is left alone; a
-- miss from years ago does not follow her forever.
--
-- no_show_deposit_after: 2 by default (one miss is an accident), 0 = off,
-- at most 10. Set by the owner, beside the salon's payment details.
-- bookings.no_show_deposit records that a booking's deposit came from this
-- rule, so the artist and the customer can both be told why.

ALTER TABLE salons
  ADD COLUMN no_show_deposit_after SMALLINT NOT NULL DEFAULT 2
  CONSTRAINT salons_no_show_deposit_after_range CHECK (no_show_deposit_after BETWEEN 0 AND 10);

ALTER TABLE bookings
  ADD COLUMN no_show_deposit BOOLEAN NOT NULL DEFAULT false;

-- The history the rule reads: one customer's no-shows at one salon.
CREATE INDEX IF NOT EXISTS idx_bookings_no_show_history
  ON bookings (salon_id, customer_id, start_time)
  WHERE status = 'no_show' AND deleted_at IS NULL;
