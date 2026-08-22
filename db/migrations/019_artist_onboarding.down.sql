-- 019_artist_onboarding.down.sql
ALTER TABLE artists DROP CONSTRAINT IF EXISTS artists_user_id_unique;
DROP INDEX IF EXISTS idx_artists_status;
ALTER TABLE artists DROP COLUMN IF EXISTS status;
