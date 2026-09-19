-- Lets the system find out whether a message was actually DELIVERED.
--
-- THE PROBLEM THIS SOLVES
--
-- `notifications.status = 'sent'` does not mean sent. It means Twilio's API
-- returned a 2xx when we handed the message over. The worker records that and
-- discards the response body.
--
-- Those are very different facts, and the gap between them cost six weeks.
-- On 2026-09-19 the table showed 19 OTP codes as `sent` while Twilio's own
-- API reported, for the entire account lifetime: 31 undelivered, 6 failed,
-- 3 inbound, and ZERO delivered. Not one message has ever reached a person.
-- Every dashboard, every log line and every status query in this system said
-- the notification pipeline was working.
--
-- Nothing detected it because nothing could. The application had no memory of
-- which Twilio message a row became, so there was no question it could ask.
--
-- WHAT THESE COLUMNS ARE
--
--   provider_message_id  the Twilio message SID (SM…), captured from the
--                        response we already receive and currently throw away.
--                        This is the join key to the provider. Without it no
--                        reconciliation of any kind is possible.
--
--   delivery_status      what the PROVIDER says happened: queued, sent,
--                        delivered, undelivered, failed, read. Deliberately
--                        separate from `status`, which stays the record of
--                        what THIS system did. Conflating them would destroy
--                        the distinction that matters - "we handed it over"
--                        and "it arrived" must remain separately visible, or
--                        the same blindness returns in a new column.
--
--   delivery_checked_at  when we last asked. Lets the reconciler poll only
--                        what it has not recently seen, and makes a stale
--                        answer obvious rather than silently authoritative.
--
-- WHY POLLING RATHER THAN A STATUS CALLBACK
--
-- Twilio can POST delivery updates to a StatusCallback URL, which is the
-- better design and is what this should become. It needs a public HTTPS
-- endpoint, and B-Edge has no domain yet - the quick tunnels used for testing
-- change hostname on every restart, so a callback registered against one is
-- dead within hours.
--
-- Polling needs no inbound address and works today. When the domain lands,
-- add the callback and keep the reconciler as the backstop for messages whose
-- callback was missed; the columns do not change.
--
-- NULL means "never checked", which is honestly different from "we checked
-- and it is queued". Every existing row starts NULL.

ALTER TABLE notifications
  ADD COLUMN IF NOT EXISTS provider_message_id  VARCHAR(64),
  ADD COLUMN IF NOT EXISTS delivery_status      VARCHAR(20),
  ADD COLUMN IF NOT EXISTS delivery_checked_at  TIMESTAMPTZ;

COMMENT ON COLUMN notifications.provider_message_id IS
  'Twilio message SID. The join key to the provider; without it delivery '
  'cannot be reconciled at all.';
COMMENT ON COLUMN notifications.delivery_status IS
  'What the PROVIDER reports: queued/sent/delivered/undelivered/failed/read. '
  'Distinct from status, which records what this system did. NULL = never asked.';

-- The reconciler asks for rows we handed over but have not confirmed. Partial,
-- because that set is small and shrinks as answers arrive, while the table
-- grows forever.
CREATE INDEX IF NOT EXISTS idx_notifications_awaiting_delivery
  ON notifications (delivery_checked_at NULLS FIRST, sent_at)
  WHERE provider_message_id IS NOT NULL
    AND delivery_status IS DISTINCT FROM 'delivered';
