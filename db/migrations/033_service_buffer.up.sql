-- 033_service_buffer.up.sql
--
-- Cleanup / turnaround time after an appointment.
--
-- Feasibility assessment §2.1 considered variable duration RANGES (a service
-- that takes "60-90 minutes") and recommended against them: none of the six
-- benchmarked competitors ship them, and a range makes every downstream
-- calculation ambiguous. The verdict there was "ship the buffer, not the
-- range." Recorded here so it is not re-litigated as a missing feature.
--
--
-- WHY A STORED blocked_until COLUMN RATHER THAN AN EXPRESSION
--
-- The obvious design is to leave the exclusion constraint computing the
-- buffer inline:
--
--     EXCLUDE USING gist (artist_id WITH =,
--       tstzrange(start_time, end_time + make_interval(mins => buffer_min), '[)') WITH &&)
--
-- Postgres rejects it: "functions in index expression must be marked
-- IMMUTABLE". make_interval IS immutable, but `timestamptz + interval` is
-- only STABLE — adding an interval depends on the session TimeZone, because
-- day and month components cross DST boundaries differently per zone. A
-- GENERATED column fails for exactly the same reason. Both were tried
-- against this database before settling on the column.
--
-- So blocked_until is stored, and the constraint ranges over it. That keeps
-- the guard where migration 001 insisted it belongs — "the final atomic
-- guard, no application-level check can replace it" — rather than demoting
-- cleanup time to an application-only rule that a direct API call or a race
-- could walk straight through.
--
-- The redundancy is real and is accepted for a second reason: it is what
-- makes cleanup time RELEASABLE. When an appointment finishes early, setting
-- blocked_until = NOW() hands the rest of the buffer back and the slot
-- reopens immediately. A computed expression could never do that — it would
-- always insist on the full buffer regardless of what actually happened,
-- which is the pessimistic-reserve failure §2.1 warns about.
--
-- CHECK (blocked_until >= end_time) is the guard against drift. The exact
-- arithmetic cannot be checked immutably for the same reason as above, but
-- the direction can, and a buffer that ends before the appointment does is
-- the only corruption that would silently free a busy slot.
--
--
-- WHY bookings.buffer_min IS A SNAPSHOT
--
-- Same reasoning as original_price / final_price. The service's buffer is
-- configuration and will change; a booking made under a 15-minute buffer
-- must not silently re-plan itself when the artist later sets 30.
--
--
-- end_time IS UNCHANGED AND STAYS CUSTOMER-FACING
--
-- It remains the moment the appointment finishes. The customer is never
-- shown duration+buffer — they did not buy the cleanup. Everything the
-- customer sees reads end_time; only scheduling reads blocked_until.

-- ── Configuration: the buffer an artist sets per service ─────────────────────

ALTER TABLE services
  ADD COLUMN IF NOT EXISTS buffer_min INTEGER NOT NULL DEFAULT 0;

ALTER TABLE services
  ADD CONSTRAINT services_buffer_min_range CHECK (buffer_min BETWEEN 0 AND 120);

COMMENT ON COLUMN services.buffer_min IS
  'Cleanup/turnaround minutes reserved after this service. 0 = none (the '
  'default). Never shown to the customer; see migration 033.';

-- ── The booking: a snapshot, and the span actually reserved ──────────────────

ALTER TABLE bookings
  ADD COLUMN IF NOT EXISTS buffer_min INTEGER NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS blocked_until TIMESTAMPTZ;

-- Backfill before NOT NULL: every existing booking reserved exactly its own
-- span, so blocked_until = end_time reproduces today's behaviour precisely.
UPDATE bookings SET blocked_until = end_time WHERE blocked_until IS NULL;

ALTER TABLE bookings
  ALTER COLUMN blocked_until SET NOT NULL;

ALTER TABLE bookings
  ADD CONSTRAINT bookings_blocked_until_after_end CHECK (blocked_until >= end_time);

COMMENT ON COLUMN bookings.buffer_min IS
  'Snapshot of services.buffer_min when this booking was created. Frozen so '
  'a later configuration change cannot re-plan a booking already made.';

COMMENT ON COLUMN bookings.blocked_until IS
  'End of the span this booking reserves on the artist calendar, including '
  'cleanup: normally end_time + buffer_min. The exclusion constraint ranges '
  'over THIS, not end_time. Set to NOW() on early completion to hand the '
  'remaining buffer back. Never customer-facing.';

-- ── Move the guard onto the reserved span ────────────────────────────────────
--
-- Recreated rather than altered; an exclusion constraint's expression cannot
-- be changed in place. Every other property is preserved deliberately:
--
--   * the same excluded statuses (cancelled/expired/no_show/refunded), so
--     terminal bookings keep releasing their slot;
--   * DEFERRABLE INITIALLY IMMEDIATE, added by migration 029 so a
--     single-statement bulk shift can move a whole day without tripping
--     row-by-row. Losing that would silently break bulk scheduling.
--
-- With blocked_until = end_time for every existing row, this constraint is
-- byte-for-byte equivalent to the old one until something sets a buffer.

ALTER TABLE bookings
  DROP CONSTRAINT IF EXISTS bookings_artist_id_tstzrange_excl;

ALTER TABLE bookings
  ADD CONSTRAINT bookings_artist_id_tstzrange_excl
  EXCLUDE USING gist (
    artist_id WITH =,
    tstzrange(start_time, blocked_until, '[)') WITH &&
  )
  WHERE (status NOT IN ('cancelled', 'expired', 'no_show', 'refunded'))
  DEFERRABLE INITIALLY IMMEDIATE;
