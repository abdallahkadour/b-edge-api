-- 032_drop_deposit_pending_status.up.sql
--
-- Removes 'deposit_pending' from the bookings status CHECK constraint.
--
-- It is a status nothing can produce. Enumerating the state machine
-- (B-Edge-Booking-State-Machine-Matrix-v1.md) found that no write path in
-- the codebase ever sets it: it appears only in two membership lists
-- (BlockingStatuses and the cancellable set), never in an UPDATE. Zero rows
-- have ever held it.
--
-- Why bother removing a value that costs nothing at runtime: it is not free
-- to readers. Every row of the state-machine matrix has a `deposit_pending`
-- entry that someone has to reason about and then discover is unreachable,
-- and the execution script would spend 9 of its 108 cells proving that a
-- status which cannot exist rejects everything. A schema that advertises a
-- state the application cannot enter is documentation that lies.
--
-- 'refunded' was in the same position when the matrix was written and is
-- deliberately NOT dropped here - migration 031's sibling work gave it a
-- writer (MarkRefunded, closing the refund_due dead end), so it is now a
-- real state rather than a vestigial one.
--
-- Safe to re-run: the constraint is dropped and recreated wholesale.
--
-- Reversible: the down migration restores the value. Nothing needs
-- backfilling in either direction because no row uses it.

ALTER TABLE bookings DROP CONSTRAINT IF EXISTS bookings_status_check;

ALTER TABLE bookings ADD CONSTRAINT bookings_status_check
  CHECK (status IN (
    'pending',
    'approved',
    'held',
    'deposit_paid',   -- the two-step partial-payment path; see 4.2
    'confirmed',
    'completed',
    'cancelled',
    'expired',
    'no_show',
    'refund_due',
    'refunded'        -- written by MarkRefunded since 2026-09-01
  ));
