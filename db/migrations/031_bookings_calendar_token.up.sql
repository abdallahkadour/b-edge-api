-- 031_bookings_calendar_token.up.sql
--
-- Adds an "add to calendar" token and an iCalendar sequence number to
-- bookings, so a guest customer can put their appointment into their own
-- phone calendar from a link in a WhatsApp message.
--
-- Why a link and not an attachment: Twilio does NOT accept text/calendar on
-- the WhatsApp channel - calendar files are restricted to MMS. WhatsApp's
-- document support is PDF, vCard and Office formats only. So an .ics can
-- never be attached to the message; it has to be fetched from a URL the
-- message points at, which is what these two columns exist to serve.
--
--
-- calendar_token
--
-- Deliberately a sibling of review_token (migration 013) rather than a
-- reuse of it. Same trust model - possession of a private random token
-- proves "this is the person who has this specific appointment", because
-- B-Edge's booking funnel is guest-first and a customer never receives a
-- session to prove identity any other way. But review_token is minted when
-- a booking COMPLETES, and a calendar entry is for an appointment that has
-- not happened yet, so the two can never share a lifecycle.
--
-- Minted in ApproveBooking. That is the single choke point: both routes to
-- 'confirmed' (approved -> confirmed, and approved -> deposit_paid ->
-- confirmed) descend from 'approved', so one call site covers every booking
-- that will ever be worth putting in a calendar. It is minted at approval
-- but the link is only SENT once the booking is confirmed - an approved
-- booking whose deposit never arrives should not leave a ghost event in
-- someone's calendar.
--
--
-- calendar_sequence
--
-- RFC 5545's SEQUENCE. A calendar client decides whether a re-imported .ics
-- UPDATES the event it already has or creates a second one by looking at
-- UID plus SEQUENCE: same UID and a HIGHER sequence means update, same
-- sequence means ignore. The UID needs no column - it is derived from the
-- booking id, which is already stable and unique.
--
-- This column exists now, before the bulk-shift write path is built, on
-- purpose. Shifting a whole day of bookings without incrementing SEQUENCE
-- would give every one of those customers a SECOND appointment in their
-- calendar at the new time while the old one stays put - the exact failure
-- the bulk-shift feature is meant to prevent. Retrofitting it later does
-- not help the events already imported.
--
--     ANY code that changes a booking's start_time or end_time MUST also
--     increment calendar_sequence in the same statement.
--
-- Honest limit, recorded so nobody later believes otherwise: even done
-- correctly this is best-effort. A customer who imported the event once and
-- never opens the updated link keeps the stale time. The calendar entry is
-- a convenience, never the source of truth, so a reschedule message must
-- still state the new time in words.

ALTER TABLE bookings
  ADD COLUMN IF NOT EXISTS calendar_token VARCHAR(64),
  ADD COLUMN IF NOT EXISTS calendar_sequence INTEGER NOT NULL DEFAULT 0;

-- Partial, matching bookings_review_token_unique: most rows are NULL, and a
-- plain unique index would index every one of them.
CREATE UNIQUE INDEX IF NOT EXISTS bookings_calendar_token_unique
  ON bookings (calendar_token)
  WHERE calendar_token IS NOT NULL;

ALTER TABLE bookings
  ADD CONSTRAINT bookings_calendar_sequence_non_negative
  CHECK (calendar_sequence >= 0);

COMMENT ON COLUMN bookings.calendar_token IS
  'Private random token letting the guest who owns this appointment fetch an '
  '.ics for it without an account. Minted at approval; the link is only sent '
  'once the booking is confirmed. NULL for bookings never approved.';

COMMENT ON COLUMN bookings.calendar_sequence IS
  'RFC 5545 SEQUENCE. MUST be incremented in the same statement as any '
  'change to start_time or end_time, or a rescheduled booking creates a '
  'second calendar event instead of moving the first.';
