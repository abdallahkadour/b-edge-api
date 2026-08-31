-- 030_user_notifications.down.sql
--
-- Drops the in-app notification centre.
--
-- Destructive: every unread notification is lost, including any
-- action_required item nobody has dealt with yet (an unpaid refund, a
-- customer who was never reached). Nothing else depends on this table, so
-- the application degrades to having no in-app channel - which is the
-- state it was in before migration 030.

DROP INDEX IF EXISTS idx_user_notifications_feed;
DROP INDEX IF EXISTS idx_user_notifications_group_open;
DROP TABLE IF EXISTS user_notifications;
