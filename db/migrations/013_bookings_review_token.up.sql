-- 013_bookings_review_token.up.sql
--
-- Adds a one-time review token to completed bookings, enabling a guest
-- customer to leave a review without ever needing an account or a login
-- session - the token itself proves "this is the person who had this
-- specific appointment", the same way a magic link works.
--
-- Why this exists: every /reviews endpoint requires Bearer auth
-- (middleware.RequireAuth()), but B-Edge's booking funnel is deliberately
-- guest-first - a customer who books never receives a JWT or session at
-- all. Building a review screen against the authenticated endpoints would
-- produce a form that always 401s. Building full customer accounts first
-- (login/register/reset) is a much larger, separate piece of work, tracked
-- but deferred. This is the guest-appropriate middle path: no account, no
-- password, just a private link tied to one specific booking.
--
-- Generated once, when a booking transitions to 'completed' (see
-- internal/booking's CompleteBooking). Nullable because bookings created
-- before this migration, and bookings that never reach 'completed', have
-- none.

ALTER TABLE bookings
  ADD COLUMN IF NOT EXISTS review_token VARCHAR(64);

CREATE UNIQUE INDEX IF NOT EXISTS bookings_review_token_unique
  ON bookings (review_token)
  WHERE review_token IS NOT NULL;

COMMENT ON COLUMN bookings.review_token IS
  'One-time token letting the guest who had this appointment leave a review '
  'with no login required. Generated when the booking is marked completed. '
  'The review domain''s existing CreateReview business logic (booking must '
  'be completed, one review per booking) still applies - this only changes '
  'how the caller''s identity is established, from a JWT to a token lookup.';
