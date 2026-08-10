-- 015_customer_otps.up.sql
--
-- Customer authentication via WhatsApp OTP - no email, no password, matching
-- the phone-first design already used throughout B-Edge (guest booking,
-- artist WhatsApp notifications). Validated against real-world practice
-- before building: WhatsApp OTP is explicitly the standard choice for MENA
-- markets, and Fresha's own account model (guest checkout OR account,
-- linked by the identifier used at checkout) is the pattern this mirrors
-- substituting phone for email since B-Edge's guest funnel never collects
-- one.
--
-- Keyed by phone, not user_id: a phone number requesting its very first OTP
-- has no users row yet at the moment the code is generated (that row gets
-- created - or reused, post migration 014 - as part of the request-OTP
-- flow itself, before the notification is enqueued).
--
-- otp_hash follows the same sha256-hex convention already used for refresh
-- tokens (see hashToken in internal/domain/auth) - appropriate here too,
-- since OTPs are short-lived (5 min) and attempt-limited, not a case that
-- needs bcrypt's deliberate slowness.

CREATE TABLE IF NOT EXISTS customer_otps (
  id          UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  phone       VARCHAR(20)  NOT NULL,
  otp_hash    VARCHAR(255) NOT NULL,
  expires_at  TIMESTAMPTZ  NOT NULL,
  attempts    INTEGER      NOT NULL DEFAULT 0,
  verified_at TIMESTAMPTZ,
  created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- Powers both the rate-limit check (count recent rows for a phone) and the
-- verify lookup (latest active row for a phone).
CREATE INDEX IF NOT EXISTS idx_customer_otps_phone_created
  ON customer_otps (phone, created_at DESC);

COMMENT ON TABLE customer_otps IS
  'One-time WhatsApp login codes for customer accounts. Rate-limited to 3 '
  'requests per phone per 5 minutes and 5 verify attempts per code '
  '(enforced in application code, not the DB). Rows are never deleted - '
  'old codes simply expire and stop matching, which also gives a natural '
  'audit trail of login attempts.';
