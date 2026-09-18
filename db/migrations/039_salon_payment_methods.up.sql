-- Where a client should send money, held by the platform rather than by a
-- WhatsApp message.
--
-- THE PROBLEM THIS CLOSES
--
-- B-Edge has no card rails. A client sends the deposit directly to the artist
-- by OMT or Whish. Until now the platform held NO record of where that money
-- should go, so the account number necessarily reached the client over
-- WhatsApp - the one channel B-Edge does not control.
--
-- That is the highest-value attack in the trust & safety review (T1):
-- anyone who can insert themselves into that conversation, or who clones an
-- artist's profile, substitutes their own number. The client has no
-- authoritative number to compare against, and B-Edge holds no record of the
-- correct one, so the dispute is unresolvable by anyone afterwards - not by
-- the client, not by the artist, not by support.
--
-- Holding the destination makes three things possible that were not:
--   - the funnel can show the client where to send, before they are asked
--   - "only ever send to the number shown in the app" becomes honest advice
--   - a later dispute has a fact to refer to
--
-- SALON-SCOPED, NOT ARTIST-SCOPED
--
-- Services, prices, deposits and product orders are all salon-scoped, so the
-- money being collected belongs to the salon. One destination therefore
-- serves both bookings and product orders, and a solo artist is simply a
-- salon of one. Scoping this to the artist would have split the two money
-- paths for no gain.
--
-- ONE ACCOUNT PER METHOD, ENFORCED
--
-- The UNIQUE constraint is a safety property, not tidiness. Two Whish numbers
-- for one salon means the app cannot say which is correct, and an ambiguous
-- answer to "where do I send my money" is the whole vulnerability restated.
CREATE TABLE IF NOT EXISTS salon_payment_methods (
  id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  salon_id     UUID         NOT NULL REFERENCES salons(id) ON DELETE CASCADE,

  -- The two ways money actually moves in Lebanon. Deliberately a CHECK rather
  -- than a lookup table: the list is short, changes rarely, and a typo here
  -- would send a client to a payment method that does not exist.
  method       VARCHAR(20)  NOT NULL CHECK (method IN ('whish', 'omt')),

  -- The name the transfer is made out to. Load-bearing for the client: both
  -- OMT and Whish show a recipient name at confirmation, so this is what
  -- lets someone notice they are about to pay the wrong person.
  account_name VARCHAR(200) NOT NULL CHECK (length(trim(account_name)) >= 2),

  -- Phone number for Whish, account reference for OMT. Stored as text, never
  -- as a number: leading zeros are significant and these are identifiers, not
  -- quantities.
  account_ref  VARCHAR(40)  NOT NULL CHECK (length(trim(account_ref)) >= 4),

  -- Lets an artist retire a number without deleting it. A deleted row would
  -- orphan the explanation of where past deposits were sent.
  is_active    BOOLEAN      NOT NULL DEFAULT TRUE,

  created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),

  CONSTRAINT salon_payment_methods_one_per_method UNIQUE (salon_id, method)
);

-- The read is always "the active destinations for this salon", on the deposit
-- step of the funnel - a hot, latency-sensitive path.
CREATE INDEX IF NOT EXISTS idx_salon_payment_methods_lookup
  ON salon_payment_methods (salon_id)
  WHERE is_active = TRUE;
