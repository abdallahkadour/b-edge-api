-- 051_artist_identity_for_invitations.up.sql
--
-- A salon may only invite a registered, identified beauty professional.
--
-- ── The problem this fixes ───────────────────────────────────────────────
--
-- Today a salon owner invites a phone number. Nothing on the other end has
-- to exist. internal/membership creates an invitation row for any normalised
-- contact, and whoever opens the link first becomes an artist in that salon.
--
-- Three consequences, in ascending order of seriousness:
--
--   1. The salon cannot see who they are inviting. A typo'd digit invites a
--      stranger, and the owner finds out when that stranger appears on their
--      team page with access to the salon's client list.
--
--   2. There is no identity behind the invitation. The token is the only
--      credential, so whoever holds the link is the artist - which is
--      AUTH-16, recorded as an open decision since the feature shipped.
--
--   3. Nothing establishes the invitee is a beauty professional at all.
--
-- ── What this migration adds ─────────────────────────────────────────────
--
-- users.phone_verified_at    when this number was proven to be theirs
-- artists.category           extended from 5 values to 8
--
-- The invitation rules themselves live in internal/membership - this only
-- provides the columns they read. A rule enforced in SQL alone would be
-- invisible to the error messages the owner needs to see.
--
-- ── The category taxonomy, and why these eight ───────────────────────────
--
-- The column and its CHECK already existed with five values: makeup, hair,
-- nails, lashes, skincare. Three are added.
--
-- Checked against how the industry actually divides this work. US state
-- licensing recognises cosmetology (hair/skin/nails), esthetics (skincare,
-- waxing, lash and brow), nail technology and BARBERING as separate
-- credentials - barbering is a distinct licence in most states, not a
-- subset of cosmetology. Fresha's service taxonomy runs cutting/styling/
-- colouring, acrylics/gels/mani-pedis, facials/skincare/makeup, and
-- waxing/body treatments, and its specialty-salon list names lash studios
-- and BROW STUDIOS as categories in their own right.
--
--   brows         Threading, shaping and tinting. Folded into 'lashes'
--                 until now, which is wrong for this market specifically:
--                 threading is a standalone service people book by itself,
--                 and a brow specialist is not a lash specialist.
--
--   barber        Male grooming is a separate clientele, a separate price
--                 point and - in most licensing regimes - a separate
--                 qualification. A salon hiring a barber is not hiring a
--                 hairdresser.
--
--   hair_removal  Waxing, threading, sugaring. A major service category
--                 with no home in the old five; it was being recorded as
--                 'skincare', which conflates it with facials.
--
-- DELIBERATELY NOT ADDED:
--
--   massage       Spa rather than salon. Different session lengths, often a
--                 separate regulatory regime, and it would pull the product
--                 toward a market B-Edge has not chosen.
--   henna         Culturally significant here, and a real specialisation for
--                 bridal work - but it is better modelled as a SERVICE a
--                 makeup artist offers than as a person's whole category.
--                 Revisit if bridal artists ask for it by name.
--   permanent     Microblading and PMU sit between skincare and brows and
--                 need a decision about medical-adjacent liability first.
--
-- The column stays NULLABLE. Six artists exist and four have a category;
-- making it NOT NULL would require inventing one for the other two. The
-- invitation rule enforces its presence instead, so the requirement lands on
-- new members without rewriting anyone's profile.

BEGIN;

-- ── phone verification ───────────────────────────────────────────────────
--
-- NULL means "never verified", which is every artist today: artists register
-- with email and password, phone is optional, and 0 of 6 have even supplied
-- one. Customers already verify by OTP (customer_otps.verified_at); this is
-- the same fact recorded on the user, so both paths can be read uniformly.
--
-- Nothing can set this until outbound delivery works - verifying a number
-- means sending to it. The invitation rule that reads it is therefore behind
-- a config switch, default OFF. Turning it on before delivery works would
-- block every invitation, including to the launch artist.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS phone_verified_at TIMESTAMPTZ;

COMMENT ON COLUMN users.phone_verified_at IS
    'When this phone was proven to belong to the user. NULL = never verified. '
    'Set by OTP confirmation. Read by membership.Invite when '
    'REQUIRE_VERIFIED_PHONE_FOR_INVITE is on.';

-- A verified timestamp without a number is incoherent, and would let a
-- verification survive the number being cleared.
ALTER TABLE users
    DROP CONSTRAINT IF EXISTS users_phone_verified_needs_phone;

ALTER TABLE users
    ADD CONSTRAINT users_phone_verified_needs_phone
    CHECK (phone_verified_at IS NULL OR phone IS NOT NULL);

-- ── the category taxonomy ────────────────────────────────────────────────

ALTER TABLE artists
    DROP CONSTRAINT IF EXISTS artists_category_check;

ALTER TABLE artists
    ADD CONSTRAINT artists_category_check
    CHECK (category IS NULL OR category IN (
        'makeup',
        'hair',
        'nails',
        'lashes',
        'brows',
        'skincare',
        'hair_removal',
        'barber'
    ));

COMMENT ON COLUMN artists.category IS
    'The professional specialty. NULL is permitted for artists who predate '
    'migration 051; membership.Invite requires it on anyone being invited.';

COMMIT;
