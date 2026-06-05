-- disable the transaction
-- ─────────────────────────────────────────────────────────────────────────────
-- B-Edge Indexes
-- All indexes use CONCURRENTLY — never locks the table.
-- Runs outside a transaction (required for CONCURRENTLY).
-- Rules applied:
--   • Every foreign key column has an explicit index
--   • Composite indexes ordered by selectivity (most selective first)
--   • Partial indexes where appropriate (e.g. active records only)
-- ─────────────────────────────────────────────────────────────────────────────

-- users
CREATE INDEX IF NOT EXISTS idx_users_email
  ON users(email) WHERE deleted_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_users_role_status
  ON users(role, status) WHERE deleted_at IS NULL;

-- salons
CREATE INDEX IF NOT EXISTS idx_salons_owner_id
  ON salons(owner_id);

CREATE INDEX IF NOT EXISTS idx_salons_active
  ON salons(is_active) WHERE deleted_at IS NULL;

-- stores
CREATE INDEX IF NOT EXISTS idx_stores_salon_id
  ON stores(salon_id);

CREATE INDEX IF NOT EXISTS idx_stores_city
  ON stores(city) WHERE is_active = TRUE;

-- business_hours
CREATE INDEX IF NOT EXISTS idx_business_hours_store_id
  ON business_hours(store_id);

-- business_hours_exceptions
CREATE INDEX IF NOT EXISTS idx_bh_exceptions_store_date
  ON business_hours_exceptions(store_id, exception_date);

-- artists
CREATE INDEX IF NOT EXISTS idx_artists_user_id
  ON artists(user_id);

CREATE INDEX IF NOT EXISTS idx_artists_salon_id
  ON artists(salon_id);

CREATE INDEX IF NOT EXISTS idx_artists_rating
  ON artists(rating DESC) WHERE is_verified = TRUE;

-- artist_stores
CREATE INDEX IF NOT EXISTS idx_artist_stores_artist_id
  ON artist_stores(artist_id);

CREATE INDEX IF NOT EXISTS idx_artist_stores_store_id
  ON artist_stores(store_id);

-- artist_store_buffers
CREATE INDEX IF NOT EXISTS idx_artist_store_buffers_artist_id
  ON artist_store_buffers(artist_id);

-- services
CREATE INDEX IF NOT EXISTS idx_services_salon_id
  ON services(salon_id) WHERE is_active = TRUE;

CREATE INDEX IF NOT EXISTS idx_services_category_id
  ON services(category_id);

-- bookings — most critical indexes
-- artist + time range: used by slot availability algorithm on every request
CREATE INDEX IF NOT EXISTS idx_bookings_artist_time
  ON bookings(artist_id, start_time, end_time)
  WHERE status NOT IN ('cancelled', 'expired', 'no_show', 'refunded');

-- customer bookings: used by "my bookings" customer PWA screen
CREATE INDEX IF NOT EXISTS idx_bookings_customer_id
  ON bookings(customer_id, created_at DESC) WHERE deleted_at IS NULL;

-- salon bookings: used by artist dashboard
CREATE INDEX IF NOT EXISTS idx_bookings_salon_id
  ON bookings(salon_id, start_time DESC) WHERE deleted_at IS NULL;

-- store bookings
CREATE INDEX IF NOT EXISTS idx_bookings_store_id
  ON bookings(store_id);

-- service FK
CREATE INDEX IF NOT EXISTS idx_bookings_service_id
  ON bookings(service_id);

-- deposit queue: artist sees all bookings awaiting deposit verification
CREATE INDEX IF NOT EXISTS idx_bookings_deposit_queue
  ON bookings(salon_id, deposit_deadline)
  WHERE status = 'deposit_paid';

-- status + salon: used by refund queue and notification worker
CREATE INDEX IF NOT EXISTS idx_bookings_status_salon
  ON bookings(status, salon_id);

-- held slots expiry: background job releases expired holds
CREATE INDEX IF NOT EXISTS idx_bookings_held_until
  ON bookings(held_until)
  WHERE status = 'held';

-- reviews
CREATE INDEX IF NOT EXISTS idx_reviews_artist_id
  ON reviews(artist_id) WHERE is_visible = TRUE;

CREATE INDEX IF NOT EXISTS idx_reviews_customer_id
  ON reviews(customer_id);

-- media
CREATE INDEX IF NOT EXISTS idx_media_owner
  ON media(owner_type, owner_id, display_order);

-- notifications worker: reads pending rows ordered by created_at
CREATE INDEX IF NOT EXISTS idx_notifications_pending
  ON notifications(created_at ASC)
  WHERE status = 'pending';

CREATE INDEX IF NOT EXISTS idx_notifications_user_id
  ON notifications(user_id);

CREATE INDEX IF NOT EXISTS idx_notifications_booking_id
  ON notifications(booking_id);

-- password_resets
CREATE INDEX IF NOT EXISTS idx_password_resets_token
  ON password_resets(token) WHERE used_at IS NULL;

CREATE INDEX IF NOT EXISTS idx_password_resets_user_id
  ON password_resets(user_id);

-- refresh_tokens
CREATE INDEX IF NOT EXISTS idx_refresh_tokens_user_id
  ON refresh_tokens(user_id) WHERE revoked_at IS NULL;

-- audit_events: queried by salon owner for dispute resolution
CREATE INDEX IF NOT EXISTS idx_audit_events_entity
  ON audit_events(entity_type, entity_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_audit_events_salon_id
  ON audit_events(salon_id, created_at DESC);
