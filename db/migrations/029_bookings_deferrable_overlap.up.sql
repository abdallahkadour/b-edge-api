-- 029_bookings_deferrable_overlap.up.sql
--
-- Makes the booking overlap constraint deferrable, so a transaction can
-- move several bookings at once.
--
-- Why this is needed:
--   Exclusion constraints are enforced row-by-row, mid-statement. Shifting
--   a day of bookings forward therefore fails even when the committed
--   state would be perfectly valid: booking A is moved into booking B's
--   OLD slot before B has moved out of it.
--
--   Reproduced against this exact schema with two adjacent bookings
--   (09:00-10:00 and 10:00-11:00) and a single UPDATE adding 10 minutes:
--
--     ERROR: conflicting key value violates exclusion constraint
--            "bookings_artist_id_tstzrange_excl"
--     DETAIL: Key ...["09:10","10:10") conflicts with existing ...["10:00","11:00")
--
--   Every ordering fails the same way. Ascending order collides forward,
--   descending collides backward when shifting earlier, and a
--   booking-at-a-time loop is just the same collisions one row later.
--   There is no application-level ordering that avoids this.
--
-- What actually changes (measured, and NOT what was first assumed):
--
--   A non-deferrable exclusion constraint is checked as each row enters the
--   index - mid-statement. A DEFERRABLE one, even INITIALLY IMMEDIATE, uses
--   the deferred-trigger machinery and is checked at END OF STATEMENT.
--
--   So this migration alone is enough for a single-statement bulk shift.
--   SET CONSTRAINTS ... DEFERRED is only required when the shift spans
--   SEVERAL statements. All three behaviours were measured on this schema:
--
--     mode                                | per-row | 1-stmt bulk | N-stmt bulk
--     ------------------------------------|---------|-------------|------------
--     NOT DEFERRABLE (before)             | checked | FAILS       | FAILS
--     DEFERRABLE INITIALLY IMMEDIATE (now)| stmt end| SUCCEEDS    | FAILS
--       + SET CONSTRAINTS ... DEFERRED    | txn end | SUCCEEDS    | SUCCEEDS
--
--   The safety guarantee is intact where it matters: inserting a booking
--   that genuinely overlaps an existing one still fails immediately, and a
--   real overlap created inside a deferred transaction is still rejected
--   when constraints are finally checked. Both verified.
--
--   The honest caveat: this is NOT a no-op for existing writes. Checking
--   moves from per-row to per-statement. Every write in this codebase today
--   touches one booking row, so the two are indistinguishable in practice -
--   but a future multi-row statement would now be validated on its final
--   state rather than row by row, which is a real semantic change and is
--   the reason it is written down here rather than assumed.
--
--   See project-docs/B-Edge-Bulk-Schedule-Operations-Spec-v1.md section 1.
--
-- Why DROP + ADD rather than ALTER:
--   ALTER TABLE ... ALTER CONSTRAINT can only change deferrability of
--   FOREIGN KEY constraints. An exclusion constraint must be recreated.
--   This rebuilds the GIST index under an ACCESS EXCLUSIVE lock - trivial
--   at current volume, worth scheduling deliberately once bookings is
--   large.
--
-- The constraint body below is byte-identical to migration 001's apart
-- from the added DEFERRABLE clause. If 001's predicate ever changes, this
-- must change with it.

ALTER TABLE bookings
  DROP CONSTRAINT bookings_artist_id_tstzrange_excl;

ALTER TABLE bookings
  ADD CONSTRAINT bookings_artist_id_tstzrange_excl
  EXCLUDE USING gist (
    artist_id WITH =,
    tstzrange(start_time, end_time, '[)') WITH &&
  ) WHERE (status NOT IN ('cancelled', 'expired', 'no_show', 'refunded'))
  DEFERRABLE INITIALLY IMMEDIATE;

COMMENT ON CONSTRAINT bookings_artist_id_tstzrange_excl ON bookings IS
  'Prevents overlapping active bookings per artist. DEFERRABLE INITIALLY IMMEDIATE: normal writes still fail fast; bulk schedule operations opt in with SET CONSTRAINTS ... DEFERRED. See migration 029.';
