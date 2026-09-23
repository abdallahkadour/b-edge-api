-- 051_artist_identity_for_invitations.down.sql
--
-- Reverses 051. The category CHECK returns to the original five values,
-- which means any artist recorded as 'brows', 'hair_removal' or 'barber'
-- would violate it - so those are folded back to their nearest old value
-- FIRST, rather than letting the constraint fail and take the migration
-- with it.
--
-- The fold is lossy and that is unavoidable: the old taxonomy has no way to
-- say "brow specialist". It is recorded here so anyone running this knows
-- what they are giving up rather than discovering it afterwards.

BEGIN;

UPDATE artists SET category = 'lashes'   WHERE category = 'brows';
UPDATE artists SET category = 'skincare' WHERE category = 'hair_removal';
UPDATE artists SET category = 'hair'     WHERE category = 'barber';

ALTER TABLE artists
    DROP CONSTRAINT IF EXISTS artists_category_check;

ALTER TABLE artists
    ADD CONSTRAINT artists_category_check
    CHECK (category IS NULL OR category IN (
        'makeup', 'hair', 'nails', 'lashes', 'skincare'
    ));

ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_phone_verified_needs_phone;

ALTER TABLE users
    DROP COLUMN IF EXISTS phone_verified_at;

COMMIT;
