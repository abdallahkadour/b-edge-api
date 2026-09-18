-- Lets a notification be written now and sent later.
--
-- WHY THIS COLUMN DID NOT EXIST, AND WHY THAT MATTERED
--
-- The notification worker drains 'pending' rows immediately. There was no way
-- to express "send this tomorrow at 09:00", which meant B-Edge could not send
-- an appointment reminder at all - the single most common no-show tool in
-- every competing product. The codebase already knew: pkg/subscription's
-- status.go says "Revisit these numbers once reminders actually send."
--
-- The compensating control was the deposit, and it is a blunter instrument: it
-- protects the artist's income but does nothing to get the client through the
-- door.
--
-- NULL MEANS NOW
--
-- Every existing row and every event-driven notification leaves this NULL and
-- behaves exactly as before. That is deliberate: the alternative, backfilling
-- created_at into scheduled_at, would make an outage-recovery replay of old
-- rows indistinguishable from a scheduled send.
ALTER TABLE notifications
  ADD COLUMN IF NOT EXISTS scheduled_at TIMESTAMPTZ;

-- The worker's claim query orders by created_at over pending rows. It now also
-- filters on scheduled_at, so the partial index has to cover both or every
-- claim degrades into a scan as scheduled rows accumulate ahead of their time.
DROP INDEX IF EXISTS idx_notifications_pending;
CREATE INDEX IF NOT EXISTS idx_notifications_sendable
  ON notifications (scheduled_at NULLS FIRST, created_at)
  WHERE status IN ('pending', 'failed');

-- ONE REMINDER PER BOOKING PER KIND, ENFORCED
--
-- The scheduler is a sweep, so it will see the same booking on every tick
-- until the appointment passes. Idempotency by unique index rather than by
-- "check then insert", which races against itself the moment there is more
-- than one process - and the fix for a duplicate reminder is an apology, not
-- a retry.
CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_one_reminder_per_booking
  ON notifications (booking_id, template_name)
  WHERE booking_id IS NOT NULL AND template_name LIKE 'booking_reminder%';
