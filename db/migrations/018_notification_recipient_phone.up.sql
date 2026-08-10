-- 018_notification_recipient_phone.up.sql
--
-- Lets a notification be queued for a phone number that has no `users` row
-- yet. Required to stop RequestOTP creating an account for any arbitrary
-- phone number an unauthenticated caller submits (security audit, Aug 2026):
-- previously identity had to exist before the OTP could be queued, because
-- notifications.user_id was NOT NULL and the worker resolved the recipient's
-- phone by joining to users.
--
-- After this, the OTP flow queues against a bare phone string and the
-- customer's `users` row is only created once they actually prove control of
-- that number by verifying the code.
--
-- The CHECK keeps the table honest: every notification must still have SOME
-- deliverable recipient. Dropping NOT NULL without it would allow rows that
-- can never be delivered to anyone.

ALTER TABLE notifications
  ALTER COLUMN user_id DROP NOT NULL;

ALTER TABLE notifications
  ADD COLUMN IF NOT EXISTS recipient_phone VARCHAR(20);

ALTER TABLE notifications
  ADD CONSTRAINT notifications_recipient_present
  CHECK (user_id IS NOT NULL OR recipient_phone IS NOT NULL);

COMMENT ON COLUMN notifications.recipient_phone IS
  'Direct recipient for notifications with no associated users row (currently '
  'only pre-verification customer OTP codes). When NULL, the worker resolves '
  'the phone from user_id as before.';
