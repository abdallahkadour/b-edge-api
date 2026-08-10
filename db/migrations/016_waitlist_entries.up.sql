-- 016_waitlist_entries.up.sql
--
-- Waitlist (PRD §9.5): a customer joins when a service is fully booked on a
-- given date, gets notified via WhatsApp the moment a slot on that date
-- opens up (a cancellation or no-show), with a configurable window to
-- confirm before the next person in line gets notified instead.
--
-- Lives in the booking domain rather than a separate one - waitlist is
-- fundamentally an extension of slot/cancellation logic booking already
-- owns, not an independent concern. Keeping it here avoids cross-domain
-- service wiring for what the spec describes as a genuinely small feature.
--
-- Deliberately simplified from the full spec in one place, flagged here
-- rather than hidden: this migration/domain does NOT reserve or hold the
-- freed slot for the notified customer specifically - they're notified
-- with a link and go through the normal guest booking funnel like anyone
-- else, first-come-first-served against the real slot availability at that
-- moment. A true reservation would need the notified customer's attempt to
-- take priority over any other customer trying the same slot in that
-- window, which is a meaningfully bigger feature (needs to interact with
-- the GIST exclusion constraint / hold mechanism) than "notify people in
-- order," and wasn't asked for as part of "the small version."
--
-- Expiry is lazy, not proactive: there's no background job scanning for
-- expired 'notified' entries on a timer. Cascading to the next person in
-- line happens the next time a NEW slot opens for that same
-- (artist, store, service, date) combination - at that point, any stale
-- 'notified' entry past its confirm_deadline is expired first, then the
-- next 'waiting' entry (if any) is notified. If no further cancellation
-- ever happens for that exact combination, an expired-but-uncascaded entry
-- can sit in 'notified' state indefinitely. Acceptable for a first version
-- - a real background sweep is a small, separate follow-up once this
-- proves out.

CREATE TABLE IF NOT EXISTS waitlist_entries (
  id               UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  artist_id        UUID         NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
  store_id         UUID         NOT NULL REFERENCES stores(id),
  service_id       UUID         NOT NULL REFERENCES services(id),
  customer_id      UUID         NOT NULL REFERENCES users(id),
  requested_date   DATE         NOT NULL,
  status           VARCHAR(20)  NOT NULL DEFAULT 'waiting'
                     CHECK (status IN ('waiting', 'notified', 'expired', 'cancelled')),
  notified_at      TIMESTAMPTZ,
  confirm_deadline TIMESTAMPTZ,
  created_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  updated_at       TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Powers both the artist-facing queue view and the "find who's next" lookup
-- that fires on a cancellation - both filter by exactly this combination.
CREATE INDEX IF NOT EXISTS idx_waitlist_queue
  ON waitlist_entries (artist_id, store_id, service_id, requested_date, status, created_at);

COMMENT ON TABLE waitlist_entries IS
  'Customers waiting for a fully-booked (artist, store, service, date) '
  'combination to free up. No deposit at entry - matches PRD §9.5. FIFO '
  'within a (artist, store, service, requested_date) group, ordered by '
  'created_at.';
