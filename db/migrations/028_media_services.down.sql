-- 028_media_services.down.sql
--
-- Drops the photo-to-service tags.
--
-- Destructive: every tag an artist has applied is lost. Re-applying the up
-- migration gives an empty table, not the previous tags. Nothing else
-- depends on this table, so dropping it degrades the profile gallery back
-- to an untagged list rather than breaking anything.

DROP INDEX IF EXISTS idx_media_services_service;
DROP TABLE IF EXISTS media_services;
