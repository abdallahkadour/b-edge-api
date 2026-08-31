-- 027_store_location.up.sql
--
-- Adds a pin-dropped map location to stores.
--
-- Why this is needed:
--   The customer-facing artist profile needs to show where a salon actually
--   is and offer one-tap directions. stores.address is free text and, for
--   the same reason migration 022 gave for orders, does not reliably
--   geocode: Lebanese addressing is informal and landmark-based rather than
--   a street-number system, so resolving "near the old bakery, Zahle" to
--   coordinates is not something a geocoder does well.
--
--   So the artist drops a pin once, the same way a customer already drops
--   one at checkout (migration 022). The frontend component for this
--   already exists and already supports both picking and viewing
--   (projects/shared/src/lib/ui/location-map.component.ts, which renders a
--   "Get directions" deep link in view mode) - only the stored coordinates
--   were missing.
--
-- Nullable on purpose:
--   Every store that exists today has no pin, and a store without one must
--   stay fully bookable - the map is an enhancement to the profile, never a
--   precondition for taking a booking. Consumers render the map only when
--   both values are present.
--
-- CHECK ranges mirror migration 022's exactly, so the two coordinate pairs
-- in this schema are validated identically.

ALTER TABLE stores
  ADD COLUMN latitude DOUBLE PRECISION
    CHECK (latitude IS NULL OR (latitude BETWEEN -90 AND 90)),
  ADD COLUMN longitude DOUBLE PRECISION
    CHECK (longitude IS NULL OR (longitude BETWEEN -180 AND 180));

COMMENT ON COLUMN stores.latitude IS
  'Artist-dropped map pin latitude. NULL means no pin set; the profile then omits the map.';
COMMENT ON COLUMN stores.longitude IS
  'Artist-dropped map pin longitude. NULL means no pin set; the profile then omits the map.';
