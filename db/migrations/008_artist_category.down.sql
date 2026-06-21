-- Reverse migration 008.
DROP INDEX IF EXISTS idx_artists_category;
ALTER TABLE artists DROP COLUMN IF EXISTS category;
