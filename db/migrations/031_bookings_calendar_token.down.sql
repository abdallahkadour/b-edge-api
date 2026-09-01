-- 031_bookings_calendar_token.down.sql
--
-- Drops the calendar columns.
--
-- Destructive in a way worth naming: every already-issued calendar link
-- stops resolving, and re-running the up migration mints NEW tokens, so the
-- old links stay dead. Events customers already imported are unaffected -
-- an .ics is a copy, not a subscription - but those events can then never
-- be updated or cancelled, because the UID's issuing row no longer has a
-- reachable token.

DROP INDEX IF EXISTS bookings_calendar_token_unique;

ALTER TABLE bookings
  DROP CONSTRAINT IF EXISTS bookings_calendar_sequence_non_negative;

ALTER TABLE bookings
  DROP COLUMN IF EXISTS calendar_token,
  DROP COLUMN IF EXISTS calendar_sequence;
