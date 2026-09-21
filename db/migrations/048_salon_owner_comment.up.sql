-- 048_salon_owner_comment.up.sql
--
-- Documents salons.owner_id as an authorisation primitive.
--
-- ── What this migration was going to be, and why it is not ──────────────
--
-- The design document for this work stated that salons.owner_id had no
-- foreign key and proposed adding one, on the reasoning that a column about
-- to gate every salon-scoped write should be referentially enforced first.
--
-- That reasoning was right and the premise was wrong. 001_initial_schema.up.sql
-- line 40 already declares it:
--
--   owner_id  UUID  NOT NULL REFERENCES users(id),
--
-- which Postgres named salons_owner_id_fkey. Adding it again fails with
-- "constraint already exists" - found by running the full migration set
-- against a scratch database before letting any of this near the real one.
-- 002_indexes.up.sql:20 already indexes the column too.
--
-- So the integrity work was done in 001 and needs nothing. What is genuinely
-- missing is the statement of INTENT: nothing in the schema said this column
-- decides who may change a salon's prices, and until 2026-09-21 no code read
-- it at all. A reader had no way to know it mattered.
--
-- Comment-only migrations are cheap and this one earns its number: the next
-- person to consider widening, nulling or repurposing owner_id meets the
-- constraint in the schema rather than discovering it in a 403.

COMMENT ON COLUMN salons.owner_id IS
    'The user who owns this salon. AUTHORISATION PRIMITIVE: '
    'internal/pkg/salonrole.Resolve derives owner-vs-member from it, and '
    'that decides who may write the salon''s services, stores, hours, '
    'discounts, products, payment methods and billing. Do not read this '
    'column anywhere else - TestNoInlineOwnerChecks parses internal/ and '
    'fails the build on any other reader outside a short allowlist.';

COMMENT ON TABLE salons IS
    'A salon. Owns the shared resources (services, stores, discounts, '
    'products, payment methods); artists.salon_id attaches members to it. '
    'Exactly one owner, in owner_id.';
