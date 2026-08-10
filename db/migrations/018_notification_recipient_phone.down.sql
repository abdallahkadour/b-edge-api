-- 018_notification_recipient_phone.down.sql
--
-- Rows with a recipient_phone but no user_id cannot satisfy the restored
-- NOT NULL, so they are removed first. They are undeliverable OTP codes,
-- not business records - safe to drop.
DELETE FROM notifications WHERE user_id IS NULL;

ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_recipient_present;
ALTER TABLE notifications DROP COLUMN IF EXISTS recipient_phone;
ALTER TABLE notifications ALTER COLUMN user_id SET NOT NULL;
