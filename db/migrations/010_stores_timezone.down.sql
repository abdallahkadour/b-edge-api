-- 010_stores_timezone.down.sql
--
-- Reverting drops the per-store zone. Any store outside Asia/Beirut will
-- silently fall back to whatever the Go layer hardcodes, so this should only
-- be run while every store is still in Lebanon.

ALTER TABLE stores DROP COLUMN IF EXISTS timezone;
