-- 036_targeted_indexes.up.sql
--
-- Two indexes, chosen from eleven candidates.
--
--
-- WHY NOT ALL ELEVEN
--
-- A 2026-09-06 audit found 11 foreign keys with no index on the leading
-- column. That is a real class of problem - an unindexed FK costs on joins,
-- and costs badly on DELETE/UPDATE of the PARENT, where Postgres scans the
-- whole child table for every row removed.
--
-- It is also a class that invites indexing everything, which is worse. Every
-- index is paid for on every INSERT and UPDATE of the table it sits on, and
-- an index nothing queries is pure cost. Nine of the eleven were left alone
-- deliberately:
--
--   audit_events.actor_id         parent (users) is essentially never deleted
--   invoices.confirmed_by         same
--   client_notes.customer_id      same
--   services.template_id          templates are seed data
--   subscriptions.plan_code       plans are seed data
--   artist_store_buffers.from_store_id  } already covered for READS by the
--   artist_store_buffers.to_store_id    } composite unique index on
--                                       } (artist_id, from_store_id, to_store_id);
--                                       } only the cascade path is unindexed,
--                                       } and stores are rarely deleted
--
-- The two below are different: both are on paths that actually run.
--
--
-- 1. WAITLIST
--
-- waitlist_entries is read by the cascade (NotifyNextWaitlistEntry) and by the
-- background sweep (FindStaleWaitlistGroups), and both filter on the same
-- combination. A composite covering the whole predicate beats three separate
-- single-column indexes: one index scan instead of a bitmap AND, and one
-- index to maintain instead of three.
--
-- Column order is by selectivity - artist first, then store, then service -
-- so a query filtering only artist_id still uses the index, while one
-- filtering only service_id (which nothing does) would not.
--
--
-- 2. ORDER ITEMS -> PRODUCTS
--
-- Deactivating or deleting a product currently scans all of order_items. Small
-- today; it is the table that grows fastest once the shop has traffic, and it
-- is the one place in the schema where a parent delete is a normal operation
-- rather than a rarity.

CREATE INDEX IF NOT EXISTS idx_waitlist_lookup
  ON waitlist_entries (artist_id, store_id, service_id, status);

COMMENT ON INDEX idx_waitlist_lookup IS
  'Covers both the lazy cascade and the background sweep, which filter on the '
  'same four columns. Composite rather than three single-column indexes: one '
  'scan instead of a bitmap AND, one index to maintain instead of three.';

CREATE INDEX IF NOT EXISTS idx_order_items_product
  ON order_items (product_id);

COMMENT ON INDEX idx_order_items_product IS
  'Without this, deactivating a product scans all of order_items - the fastest-'
  'growing table in the shop, and the only place a parent delete is routine.';
