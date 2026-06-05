-- ─────────────────────────────────────────────────────────────────────────────
-- B-Edge Initial Schema
-- Rules applied:
--   • TIMESTAMPTZ everywhere — never TIMESTAMP
--   • NUMERIC(10,2) for all money — never FLOAT
--   • lock_timeout on every migration
--   • Explicit FK indexes (separate migration 002)
--   • GIST exclusion constraint on bookings — prevents double booking at DB level
-- ─────────────────────────────────────────────────────────────────────────────

SET lock_timeout = '5s';

-- Extensions
CREATE EXTENSION IF NOT EXISTS "pgcrypto";   -- gen_random_uuid()
CREATE EXTENSION IF NOT EXISTS "btree_gist"; -- required for GIST constraint on UUID + tstzrange

-- ─────────────────────────────────────────────────────────────────────────────
-- USERS
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS users (
  id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  name          VARCHAR(100) NOT NULL,
  email         VARCHAR(255) NOT NULL UNIQUE,
  password_hash VARCHAR(255) NOT NULL,
  role          VARCHAR(20)  NOT NULL CHECK (role IN ('customer', 'artist', 'admin')),
  phone         VARCHAR(20),
  status        VARCHAR(20)  NOT NULL DEFAULT 'active'
                CHECK (status IN ('active', 'frozen', 'suspended', 'deleted')),
  created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  updated_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  deleted_at    TIMESTAMPTZ
);

-- ─────────────────────────────────────────────────────────────────────────────
-- SALONS
-- A salon is the business entity. One owner, many artists, many stores.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS salons (
  id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_id          UUID         NOT NULL REFERENCES users(id),
  name              VARCHAR(200) NOT NULL,
  name_ar           VARCHAR(200),
  subscription_plan VARCHAR(20)  NOT NULL DEFAULT 'free'
                    CHECK (subscription_plan IN ('free', 'studio', 'salon_plus')),
  plan_expires_at   TIMESTAMPTZ,
  is_active         BOOLEAN      NOT NULL DEFAULT TRUE,
  created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  updated_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  deleted_at        TIMESTAMPTZ
);

-- ─────────────────────────────────────────────────────────────────────────────
-- STORES
-- A physical location belonging to a salon.
-- Rania has two: Beirut Downtown and Tripoli.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS stores (
  id                    UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  salon_id              UUID         NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  name                  VARCHAR(200) NOT NULL,
  name_ar               VARCHAR(200),
  address               TEXT,
  city                  VARCHAR(100) NOT NULL,
  country               VARCHAR(100) NOT NULL DEFAULT 'Lebanon',
  phone                 VARCHAR(20),
  -- same-day minimum notice before booking (hours)
  same_day_notice_hours INTEGER      NOT NULL DEFAULT 4,
  -- early bird: slots before this time are flagged with extra fee
  early_bird_cutoff     TIME,
  early_bird_fee        NUMERIC(10,2) NOT NULL DEFAULT 0.00,
  -- cross-store travel buffers (minutes) — weekday vs weekend
  weekday_buffer_min    INTEGER      NOT NULL DEFAULT 150,
  weekend_buffer_min    INTEGER      NOT NULL DEFAULT 90,
  is_active             BOOLEAN      NOT NULL DEFAULT TRUE,
  created_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  updated_at            TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- BUSINESS HOURS
-- Per store, per day of week. 0=Sunday, 6=Saturday.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS business_hours (
  id          UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
  store_id    UUID    NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
  day_of_week INTEGER NOT NULL CHECK (day_of_week BETWEEN 0 AND 6),
  open_time   TIME    NOT NULL,
  close_time  TIME    NOT NULL,
  is_open     BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (store_id, day_of_week)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- BUSINESS HOURS EXCEPTIONS
-- Holidays, blackout dates, or special-hours days.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS business_hours_exceptions (
  id             UUID  PRIMARY KEY DEFAULT gen_random_uuid(),
  store_id       UUID  NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
  exception_date DATE  NOT NULL,
  is_closed      BOOLEAN NOT NULL DEFAULT TRUE,
  -- if not closed, these override regular hours
  open_time      TIME,
  close_time     TIME,
  reason         VARCHAR(255),
  created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (store_id, exception_date)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- ARTISTS
-- Profile of a beauty artist. Linked to a user and a salon.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS artists (
  id           UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  salon_id     UUID         REFERENCES salons(id) ON DELETE SET NULL,
  bio          TEXT,
  bio_ar       TEXT,
  instagram    VARCHAR(255),
  rating       NUMERIC(3,2) NOT NULL DEFAULT 0.00,
  review_count INTEGER      NOT NULL DEFAULT 0,
  is_verified  BOOLEAN      NOT NULL DEFAULT FALSE,
  created_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
  updated_at   TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- ARTIST STORES
-- Which stores an artist works at. Many-to-many.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS artist_stores (
  id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  artist_id  UUID NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
  store_id   UUID NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (artist_id, store_id)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- ARTIST STORE BUFFERS
-- Travel buffer per artist per store-pair.
-- Overrides the store-level default when an artist has a specific schedule.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS artist_store_buffers (
  id                UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
  artist_id         UUID    NOT NULL REFERENCES artists(id) ON DELETE CASCADE,
  from_store_id     UUID    NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
  to_store_id       UUID    NOT NULL REFERENCES stores(id) ON DELETE CASCADE,
  weekday_buffer_min INTEGER NOT NULL DEFAULT 150,
  weekend_buffer_min INTEGER NOT NULL DEFAULT 90,
  created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  UNIQUE (artist_id, from_store_id, to_store_id)
);

-- ─────────────────────────────────────────────────────────────────────────────
-- SERVICE CATEGORIES
-- Predefined categories with business rules attached.
-- e.g. full_makeup has earliest_start_time = 11:00
-- e.g. kreatin has has_processing_gap = true
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS service_categories (
  id                  UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  name                VARCHAR(100) NOT NULL UNIQUE,
  name_ar             VARCHAR(100),
  -- if true: artist is free during processing period (kreatin, color)
  has_processing_gap  BOOLEAN      NOT NULL DEFAULT FALSE,
  -- if set: service cannot start before this time (e.g. 11:00 for full makeup)
  earliest_start_time TIME,
  created_at          TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- SERVICES
-- Services offered by a salon. is_custom=true means added freely by salon.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS services (
  id                    UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  salon_id              UUID          NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  category_id           UUID          REFERENCES service_categories(id),
  name                  VARCHAR(200)  NOT NULL,
  name_ar               VARCHAR(200),
  description           TEXT,
  duration_min          INTEGER       NOT NULL CHECK (duration_min >= 15 AND duration_min <= 480),
  -- active_duration_min: artist working time before processing gap starts
  -- NULL means no processing gap — artist is blocked for full duration
  active_duration_min   INTEGER,
  price                 NUMERIC(10,2) NOT NULL CHECK (price >= 0),
  deposit_amount        NUMERIC(10,2) NOT NULL DEFAULT 0.00,
  deposit_deadline_hours INTEGER      NOT NULL DEFAULT 48,
  is_active             BOOLEAN       NOT NULL DEFAULT TRUE,
  is_custom             BOOLEAN       NOT NULL DEFAULT FALSE,
  template_id           UUID          REFERENCES services(id),
  created_at            TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
  updated_at            TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- BOOKINGS
-- Core entity. GIST exclusion constraint prevents double booking at DB level.
-- This is the final atomic guard — no application-level check can replace it.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS bookings (
  id                  UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  salon_id            UUID          NOT NULL REFERENCES salons(id),
  store_id            UUID          NOT NULL REFERENCES stores(id),
  artist_id           UUID          NOT NULL REFERENCES artists(id),
  customer_id         UUID          NOT NULL REFERENCES users(id),
  service_id          UUID          NOT NULL REFERENCES services(id),
  -- start_time and end_time replace separate date + time columns
  -- stored as TIMESTAMPTZ — always UTC, displayed in Asia/Beirut
  start_time          TIMESTAMPTZ   NOT NULL,
  end_time            TIMESTAMPTZ   NOT NULL,
  -- held_until: slot is reserved during checkout for 10 minutes
  held_until          TIMESTAMPTZ,
  status              VARCHAR(30)   NOT NULL DEFAULT 'pending'
                      CHECK (status IN (
                        'pending',         -- submitted, awaiting artist approval
                        'approved',        -- artist approved, awaiting deposit
                        'held',            -- slot held during checkout
                        'deposit_pending', -- deposit deadline set, not yet paid
                        'deposit_paid',    -- deposit paid, awaiting artist confirmation
                        'confirmed',       -- deposit verified, booking confirmed
                        'completed',       -- service delivered
                        'cancelled',       -- cancelled by artist or customer
                        'expired',         -- deposit deadline passed, slot released
                        'no_show',         -- customer did not arrive
                        'refund_due',      -- refund approved, not yet sent
                        'refunded'         -- refund sent
                      )),
  -- financial fields — all NUMERIC(10,2), never FLOAT
  original_price      NUMERIC(10,2) NOT NULL,
  discount_amount     NUMERIC(10,2) NOT NULL DEFAULT 0.00,
  final_price         NUMERIC(10,2) NOT NULL,
  deposit_amount      NUMERIC(10,2) NOT NULL DEFAULT 0.00,
  deposit_deadline    TIMESTAMPTZ,
  deposit_paid_at     TIMESTAMPTZ,
  -- channel: where the booking originated
  channel             VARCHAR(30)   NOT NULL DEFAULT 'customer_pwa'
                      CHECK (channel IN (
                        'customer_pwa',
                        'artist_dashboard',
                        'whatsapp_bot',
                        'walk_in',
                        'phone',
                        'instagram'
                      )),
  special_requests    TEXT,
  cancellation_reason TEXT,
  cancelled_at        TIMESTAMPTZ,
  completed_at        TIMESTAMPTZ,
  no_show_at          TIMESTAMPTZ,
  created_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
  updated_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
  deleted_at          TIMESTAMPTZ,

  -- ── GIST EXCLUSION CONSTRAINT ──────────────────────────────────────────
  -- Prevents two active bookings for the same artist overlapping in time.
  -- First write wins. Second write gets PostgreSQL error 23P01.
  -- Excludes cancelled/expired/no_show so they do not block future bookings.
  -- btree_gist extension (above) is required for UUID in a GIST index.
  EXCLUDE USING gist (
    artist_id WITH =,
    tstzrange(start_time, end_time, '[)') WITH &&
  ) WHERE (status NOT IN ('cancelled', 'expired', 'no_show', 'refunded'))
);

-- ─────────────────────────────────────────────────────────────────────────────
-- REVIEWS
-- One review per booking. customer_id and artist_id denormalised for queries.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS reviews (
  id          UUID    PRIMARY KEY DEFAULT gen_random_uuid(),
  booking_id  UUID    NOT NULL UNIQUE REFERENCES bookings(id),
  customer_id UUID    NOT NULL REFERENCES users(id),
  artist_id   UUID    NOT NULL REFERENCES artists(id),
  rating      INTEGER NOT NULL CHECK (rating BETWEEN 1 AND 5),
  comment     TEXT,
  is_visible  BOOLEAN NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- MEDIA
-- Unified media table for artist portfolio photos and booking photos.
-- owner_type + owner_id identifies what this media belongs to.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS media (
  id            UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  owner_type    VARCHAR(20)  NOT NULL CHECK (owner_type IN ('artist', 'salon', 'booking')),
  owner_id      UUID         NOT NULL,
  url           VARCHAR(500) NOT NULL,
  cloudinary_id VARCHAR(255),
  type          VARCHAR(20)  NOT NULL DEFAULT 'photo' CHECK (type IN ('photo', 'video')),
  display_order INTEGER      NOT NULL DEFAULT 0,
  created_at    TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- NOTIFICATIONS
-- Async queue. Worker reads pending rows and sends WhatsApp/SMS.
-- Transaction commits first. Worker sends after. Never inside a transaction.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS notifications (
  id                UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  booking_id        UUID         REFERENCES bookings(id),
  user_id           UUID         NOT NULL REFERENCES users(id),
  template_name     VARCHAR(100) NOT NULL,
  channel           VARCHAR(20)  NOT NULL DEFAULT 'whatsapp'
                    CHECK (channel IN ('whatsapp', 'sms', 'email')),
  status            VARCHAR(20)  NOT NULL DEFAULT 'pending'
                    CHECK (status IN ('pending', 'sent', 'failed', 'dead')),
  payload           JSONB,
  attempts          INTEGER      NOT NULL DEFAULT 0,
  last_attempted_at TIMESTAMPTZ,
  sent_at           TIMESTAMPTZ,
  error_message     TEXT,
  created_at        TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- PASSWORD RESETS
-- One-use tokens for forgot-password flow.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS password_resets (
  id         UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token      VARCHAR(255) NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ  NOT NULL,
  used_at    TIMESTAMPTZ,
  created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- REFRESH TOKENS
-- Stores hashed refresh tokens so they can be revoked.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS refresh_tokens (
  id         UUID         PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id    UUID         NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  token_hash VARCHAR(255) NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ  NOT NULL,
  revoked_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

-- ─────────────────────────────────────────────────────────────────────────────
-- AUDIT EVENTS
-- Append-only. Never update or delete rows here.
-- 7-year retention. Dispute resolution. Every status change recorded.
-- ─────────────────────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS audit_events (
  id          UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
  salon_id    UUID        REFERENCES salons(id),
  actor_id    UUID        REFERENCES users(id),
  actor_role  VARCHAR(20),
  entity_type VARCHAR(50) NOT NULL,  -- 'booking', 'user', 'service', etc.
  entity_id   UUID        NOT NULL,
  action      VARCHAR(50) NOT NULL,  -- 'created', 'status_changed', 'deposit_paid', etc.
  old_values  JSONB,
  new_values  JSONB,
  ip_address  INET,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
