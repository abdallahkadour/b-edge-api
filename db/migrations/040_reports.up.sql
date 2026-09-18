-- Somewhere for a client to go when something goes wrong.
--
-- WHY THIS IS PART OF THE PAYMENT STORY, NOT A SUPPORT FEATURE
--
-- B-Edge has no card rails. There is no chargeback, no escrow and no
-- intermediary holding the money, so when a deposit goes to the wrong person
-- the platform is the ONLY recourse a client has. Migration 039 gave them an
-- authoritative payment destination to check against; this gives them
-- somewhere to go when the check fails. Without it, "we hold a record of the
-- correct number" is a fact nobody can act on.
--
-- REPORTS REQUIRE AN ACCOUNT, DELIBERATELY
--
-- reporter_user_id is NOT NULL. An unauthenticated report endpoint is a spam
-- cannon, and the natural alternative - proving identity with a booking id
-- plus a phone number - is exactly the credential-stuffing surface this
-- product does not need. A client already has to sign in with the phone they
-- booked with to see the booking at all, so requiring it here costs nothing
-- and ties every report to a real, reachable person.
--
-- THE CATEGORIES ARE THE THREAT MODEL
--
-- They are not generic support topics. Each names something the trust & safety
-- review identified as able to happen here, and 'wrong_payment_details' is the
-- important one: it is the tripwire for T1, the substituted-account-number
-- attack that migration 039 exists to prevent. A cluster of those against one
-- artist is the signal that someone is impersonating them.
CREATE TABLE IF NOT EXISTS reports (
  id               UUID        PRIMARY KEY DEFAULT gen_random_uuid(),

  reporter_user_id UUID        NOT NULL REFERENCES users(id),
  reporter_role    VARCHAR(20) NOT NULL CHECK (reporter_role IN ('customer', 'artist')),

  -- What it is about. A report usually concerns a booking, but not always -
  -- an impersonated profile has no booking behind it, which is precisely the
  -- case worth hearing about early.
  booking_id       UUID        REFERENCES bookings(id) ON DELETE SET NULL,
  artist_id        UUID        REFERENCES artists(id)  ON DELETE SET NULL,

  category         VARCHAR(40) NOT NULL CHECK (category IN (
                     'wrong_payment_details',
                     'payment_not_received',
                     'deposit_not_returned',
                     'did_not_attend',
                     'impersonation',
                     'inappropriate_behaviour',
                     'other'
                   )),
  description      TEXT        NOT NULL CHECK (length(trim(description)) >= 10),

  status           VARCHAR(20) NOT NULL DEFAULT 'open'
                   CHECK (status IN ('open', 'reviewing', 'resolved', 'dismissed')),

  -- What was done about it. Kept even after resolution, because the value of a
  -- report is mostly in the pattern across many of them.
  resolution_note  TEXT,
  resolved_by      UUID        REFERENCES users(id),
  resolved_at      TIMESTAMPTZ,

  created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

  -- A report must be about something.
  CONSTRAINT reports_has_a_subject CHECK (booking_id IS NOT NULL OR artist_id IS NOT NULL)
);

-- One OPEN report per person per booking. Not a unique constraint on the whole
-- table: the same client may legitimately report the same artist again months
-- later, and a resolved report must not block a new one about a new incident.
-- This only stops the same complaint being filed twice while it is still being
-- looked at.
CREATE UNIQUE INDEX IF NOT EXISTS idx_reports_one_open_per_booking
  ON reports (reporter_user_id, booking_id)
  WHERE status IN ('open', 'reviewing') AND booking_id IS NOT NULL;

-- The admin queue reads open reports oldest-first: someone waiting on an
-- answer about money they have already sent should not be overtaken.
CREATE INDEX IF NOT EXISTS idx_reports_queue
  ON reports (created_at ASC)
  WHERE status IN ('open', 'reviewing');

-- Detection, not support: "how many payment-details reports against this
-- artist this month" is the query that catches an impersonation campaign.
CREATE INDEX IF NOT EXISTS idx_reports_by_artist
  ON reports (artist_id, category, created_at DESC);
