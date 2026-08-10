-- 017_products_and_orders.up.sql
--
-- Product Store (PRD §13). A genuinely separate business from booking
-- appointments - selling physical products (Rania's real launch catalogue:
-- glitter, lip liner, beauty blender, liquid glue, blush, gel eyeliner,
-- sulfate-free shampoo/mask, two grades of scissors, a nail pusher) rather
-- than time. Salon-scoped, matching the exact ownership model already
-- used by the services catalog (internal/artist), not artist-scoped.
--
-- No online payment gateway exists in this market, matching the exact
-- reason booking deposits are confirmed manually - so 'placed' →
-- 'confirmed' is a manual artist action ("payment received via Wish/OMT"),
-- mirroring ConfirmDepositReceived's shape for a full amount instead of a
-- deposit.
--
-- Two deliberately different soft-delete choices in the same migration,
-- both intentional, not inconsistent:
--   - products uses is_active only (no deleted_at), matching services/
--     stores/artists exactly - a catalog item being taken down is a normal
--     "hide it" operation, not a record that needs deleted_at's audit
--     semantics.
--   - orders DOES get deleted_at, matching bookings/users/salons instead
--     an order is a financial/transactional record with the same audit
--     requirement as a booking, not a catalog entry. This is the correct
--     precedent to follow here, not products'.
--
-- order_items snapshots product_name/unit_price at order time rather than
-- joining live to products - a customer's receipt must never silently
-- change because of an unrelated later catalog edit (price change, rename,
-- or the product being deleted). Standard e-commerce practice, and a
-- deliberate departure from how bookings joins live to services (safe
-- there because a service's name essentially never changes after a
-- booking references it - a materially weaker invariant than an order).

CREATE TABLE IF NOT EXISTS products (
  id          UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  salon_id    UUID          NOT NULL REFERENCES salons(id) ON DELETE CASCADE,
  name        VARCHAR(200)  NOT NULL,
  description TEXT,
  category    VARCHAR(20)   CHECK (category IN ('makeup', 'hair', 'nails', 'lashes', 'skincare')),
  price       NUMERIC(10,2) NOT NULL CHECK (price >= 0),
  image_url   VARCHAR(500),
  is_active   BOOLEAN       NOT NULL DEFAULT TRUE,
  created_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
  updated_at  TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_products_salon ON products (salon_id) WHERE is_active = TRUE;

CREATE TABLE IF NOT EXISTS orders (
  id                  UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  salon_id            UUID          NOT NULL REFERENCES salons(id),
  customer_id         UUID          NOT NULL REFERENCES users(id),
  status              VARCHAR(20)   NOT NULL DEFAULT 'placed'
                        CHECK (status IN ('placed', 'confirmed', 'shipped', 'delivered', 'cancelled', 'returned')),
  total_amount        NUMERIC(10,2) NOT NULL CHECK (total_amount >= 0),
  payment_reference   VARCHAR(255),
  delivery_notes      TEXT,
  cancellation_reason TEXT,
  confirmed_at        TIMESTAMPTZ,
  shipped_at          TIMESTAMPTZ,
  delivered_at        TIMESTAMPTZ,
  cancelled_at        TIMESTAMPTZ,
  created_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
  updated_at          TIMESTAMPTZ   NOT NULL DEFAULT NOW(),
  deleted_at          TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_orders_salon_status ON orders (salon_id, status, created_at DESC) WHERE deleted_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_orders_customer ON orders (customer_id, created_at DESC) WHERE deleted_at IS NULL;

CREATE TABLE IF NOT EXISTS order_items (
  id           UUID          PRIMARY KEY DEFAULT gen_random_uuid(),
  order_id     UUID          NOT NULL REFERENCES orders(id) ON DELETE CASCADE,
  product_id   UUID          NOT NULL REFERENCES products(id),
  product_name VARCHAR(200)  NOT NULL,
  unit_price   NUMERIC(10,2) NOT NULL CHECK (unit_price >= 0),
  quantity     INTEGER       NOT NULL CHECK (quantity > 0),
  subtotal     NUMERIC(10,2) NOT NULL CHECK (subtotal >= 0),
  created_at   TIMESTAMPTZ   NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_order_items_order ON order_items (order_id, created_at ASC);

COMMENT ON TABLE products IS 'An artist''s (salon''s) sellable product catalog - separate business from bookings. PRD §13.1.';
COMMENT ON TABLE orders IS 'Product orders. State machine: placed → confirmed → shipped → delivered, with cancelled/returned side branches. PRD §13.2. deleted_at present, matching bookings audit-record precedent, not products catalog-item precedent.';
COMMENT ON COLUMN order_items.product_name IS 'Snapshot at order time - deliberately not a live join to products.name. See migration header.';
COMMENT ON COLUMN order_items.unit_price IS 'Snapshot at order time - deliberately not a live join to products.price. See migration header.';
