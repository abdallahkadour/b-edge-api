-- 036_targeted_indexes.down.sql
--
-- Safe and lossless: indexes hold no data of their own. Dropping them makes
-- the waitlist sweep and product deactivation slower, nothing more.

DROP INDEX IF EXISTS idx_waitlist_lookup;
DROP INDEX IF EXISTS idx_order_items_product;
