-- 049_store_default_hours.up.sql
--
-- A store's default opening hours, and the end of the empty calendar.
--
-- ── The problem this fixes ───────────────────────────────────────────────
--
-- internal/onboarding creates a salon, an artist, a store and a first
-- service in one transaction, and NO business_hours rows. A newly onboarded
-- artist is therefore completely unbookable: openinghours.Resolve finds no
-- row for the weekday, reports the store closed, and slot generation returns
-- an empty list for every date forever. Nothing tells them. They appear on
-- Discover, a customer opens their profile, and there is simply nothing to
-- book.
--
-- Two stores on the development database are in that state right now
-- (mkup3's and mkup4's), and it is why 7 of the 31 artist/store/service
-- combinations in the slot baseline produced zero availability across a
-- 90-day horizon.
--
-- The launch artist raised this directly: "a makeup artist should have a
-- default for opening hours, she should not go day by day."
--
-- ── Why a column rather than a constant in Go ────────────────────────────
--
-- The bulk hours editor already exists in the dashboard. What was missing
-- was a starting point, and a starting point that lives in the schema can
-- be read back, shown in the UI as "your default", used to reset a week,
-- and changed per store. A constant buried in the onboarding transaction
-- could do exactly one of those.
--
-- ── What gets seeded, and what does not ──────────────────────────────────
--
-- Onboarding seeds all seven days open at these hours, so a new artist is
-- bookable the moment they are approved. Closing Sunday is one tap in the
-- bulk editor.
--
-- That direction is deliberate. Being open on a day you do not work costs a
-- cancellation; being closed on a day you do work costs every booking you
-- never hear about, silently, which is the state every new artist is in
-- today.
--
-- EXISTING STORES ARE NOT BACKFILLED. These columns take their defaults, but
-- no business_hours row is written for a salon that is already trading. A
-- migration that invents opening hours for a live salon can put an artist on
-- Discover at a time they are not there, and the first they would know is a
-- customer arriving. mkup3 and mkup4 are left as they are, deliberately.

ALTER TABLE stores
    ADD COLUMN default_open_time  TIME NOT NULL DEFAULT '09:00',
    ADD COLUMN default_close_time TIME NOT NULL DEFAULT '18:00';

ALTER TABLE stores
    ADD CONSTRAINT stores_default_hours_check
    CHECK (default_open_time < default_close_time);

COMMENT ON COLUMN stores.default_open_time IS
    'Opening time used to seed business_hours when the store is created, and '
    'offered as the starting point in the bulk hours editor. Not itself a '
    'trading rule - business_hours is what slot generation reads.';

COMMENT ON COLUMN stores.default_close_time IS
    'Closing time counterpart to default_open_time. See that comment.';
