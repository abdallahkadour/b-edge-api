-- 019_artist_onboarding.up.sql
--
-- Adds a review gate before a self-service artist profile goes live.
--
-- Backward compatibility is the one thing that MUST NOT break here: Rania's
-- existing row must not silently vanish from Discover the moment this
-- migration runs. Default is 'active', not 'pending' - every artist that
-- exists today is grandfathered in as already-reviewed. Only the NEW
-- self-service onboarding flow explicitly sets 'pending' at creation time;
-- this column's default is a safety net for every artist that came before
-- that flow existed, not the value new signups get.
--
-- 'rejected' is a genuine terminal state, not a synonym for deleted - an
-- admin's decision should stay visible and auditable, not disappear.
ALTER TABLE artists
  ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'active'
    CHECK (status IN ('pending', 'active', 'rejected'));

-- Every public-facing artist query filters on this - an index keeps that
-- filter cheap as the artist count grows past the handful it is today.
CREATE INDEX IF NOT EXISTS idx_artists_status ON artists (status);

COMMENT ON COLUMN artists.status IS
  'pending = self-service signup awaiting admin review, not shown on '
  'Discover and not bookable. active = reviewed and live. rejected = '
  'admin declined the application; kept for audit, not deleted.';

-- artists.user_id had no uniqueness constraint at all until now - nothing
-- stopped one user ending up with two artist profiles (a double-submitted
-- onboarding form, a retried request after a timeout). The onboarding
-- flow's application-level idempotency check needs this backing it up;
-- without a database constraint there is always a race window between
-- checking "does this user already have an artist row" and inserting one.
ALTER TABLE artists
  ADD CONSTRAINT artists_user_id_unique UNIQUE (user_id);
