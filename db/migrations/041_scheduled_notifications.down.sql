-- Removes future-dated sending. Reminders become impossible again.
DROP INDEX IF EXISTS idx_notifications_one_reminder_per_booking;
DROP INDEX IF EXISTS idx_notifications_sendable;
CREATE INDEX IF NOT EXISTS idx_notifications_pending
  ON notifications (created_at) WHERE status = 'pending';
ALTER TABLE notifications DROP COLUMN IF EXISTS scheduled_at;
