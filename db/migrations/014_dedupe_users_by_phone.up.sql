-- 014_dedupe_users_by_phone.up.sql
--
-- Fixes a real data-integrity gap: CreateGuestUser (booking domain) has
-- always done a blind INSERT with no lookup-by-phone first, so the same
-- real person booking as a guest multiple times ends up as several
-- different `users` rows sharing one phone number, with their bookings
-- scattered across all of them instead of one canonical identity. This
-- blocks customer OTP login, which needs "this phone number = this one
-- account" to actually be true.
--
-- One-time data fix: for every phone number shared by more than one active
-- user, keeps the EARLIEST-created row as the real identity, repoints every
-- booking/review/client_note from the newer duplicate(s) onto the keeper,
-- then soft-deletes the duplicates. Nothing is lost - booking history moves
-- to the keeper, and the duplicate rows remain inspectable via deleted_at
-- rather than being hard-deleted.
--
-- After this runs, CreateGuestUser is updated (separate Go change, same
-- session) to look up an existing user by phone before inserting, so this
-- situation can't recur. The partial unique index below enforces the same
-- rule at the DB level too - matches the exact WHERE-deleted_at-IS-NULL
-- pattern already used for artists.handle in migration 012.

-- Materialize the keeper/loser mapping once, reused by every repoint below.
-- A CTE only scopes to a single statement, so a real table is needed to
-- share this mapping across the four statements that follow.
CREATE TEMP TABLE _phone_dedupe AS
WITH ranked AS (
  SELECT id, phone,
         ROW_NUMBER() OVER (PARTITION BY phone ORDER BY created_at ASC, id ASC) AS rn
  FROM users
  WHERE phone IS NOT NULL AND deleted_at IS NULL
)
SELECT r.id AS loser_id, k.id AS keeper_id
FROM ranked r
JOIN ranked k ON k.phone = r.phone AND k.rn = 1
WHERE r.rn > 1;

-- Repoint every table that references a customer's users.id.
UPDATE bookings b
SET customer_id = d.keeper_id
FROM _phone_dedupe d
WHERE b.customer_id = d.loser_id;

UPDATE reviews r
SET customer_id = d.keeper_id
FROM _phone_dedupe d
WHERE r.customer_id = d.loser_id;

UPDATE client_notes c
SET customer_id = d.keeper_id
FROM _phone_dedupe d
WHERE c.customer_id = d.loser_id;

-- Soft-delete the now-orphaned duplicates. Never hard-deleted - matches the
-- existing soft-delete convention already used on this table.
UPDATE users u
SET deleted_at = NOW(), updated_at = NOW()
FROM _phone_dedupe d
WHERE u.id = d.loser_id;

DROP TABLE _phone_dedupe;

-- Enforce uniqueness among ACTIVE users going forward. A partial index, not
-- a plain UNIQUE constraint, so the soft-deleted duplicates above - which
-- still carry their old phone value for audit purposes - don't block a
-- future signup from reusing that same number.
CREATE UNIQUE INDEX IF NOT EXISTS users_phone_unique
  ON users (phone)
  WHERE phone IS NOT NULL AND deleted_at IS NULL;
