-- 012_artists_handle.down.sql
DROP INDEX IF EXISTS artists_handle_unique;
ALTER TABLE artists DROP COLUMN IF EXISTS handle;
