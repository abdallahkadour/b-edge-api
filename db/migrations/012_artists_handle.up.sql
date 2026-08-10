-- 012_artists_handle.up.sql
--
-- Adds a human-readable public handle for artists, so booking links can be
-- "b-edge.app/book/rania" instead of the raw artist UUID
-- ("b-edge.app/book/378cd76e-6c75-4c63-9d38-6f8fa211f1e5"). The UUID route
-- keeps working - this is an additive alias, not a replacement - so any
-- link already shared before this migration still resolves.
--
-- Format is enforced at the database level (CHECK), not just in application
-- code: lowercase letters, digits, and single hyphens, 3-50 characters,
-- must start and end with an alphanumeric character. This blocks handles
-- like "-rania-" or "rania--beauty" that would look broken in a URL, and
-- blocks uppercase/spaces/special characters that would need URL-encoding.
--
-- Nullable: existing artists have no handle until they set one. Every new
-- booking link generated in the UI should prefer the handle when present
-- and fall back to the UUID when it's not.

ALTER TABLE artists
  ADD COLUMN IF NOT EXISTS handle VARCHAR(50)
    CONSTRAINT artists_handle_format
    CHECK (handle IS NULL OR handle ~ '^[a-z0-9]([a-z0-9-]*[a-z0-9])?$');

CREATE UNIQUE INDEX IF NOT EXISTS artists_handle_unique
  ON artists (handle)
  WHERE handle IS NOT NULL;

COMMENT ON COLUMN artists.handle IS
  'Public, human-readable identifier for booking links (e.g. "rania"). '
  'Nullable, unique when set. Lowercase alphanumeric + hyphens only, '
  'enforced by artists_handle_format. The UUID route remains valid '
  'regardless of whether a handle is set.';
