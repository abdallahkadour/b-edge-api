-- Reverses 045.
--
-- Dropping provider_message_id discards the only link between a notification
-- row and the Twilio message it became, so no past send can ever be
-- reconciled again. The delivery answers already collected are lost with it.
DROP INDEX IF EXISTS idx_notifications_awaiting_delivery;

ALTER TABLE notifications
  DROP COLUMN IF EXISTS delivery_checked_at,
  DROP COLUMN IF EXISTS delivery_status,
  DROP COLUMN IF EXISTS provider_message_id;
