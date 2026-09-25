-- 052_artist_services.down.sql
--
-- Drops the table. Nothing else references it, so the only loss is the
-- overrides themselves - every artist returns to offering every service of
-- her salon at the salon's price, which is exactly the behaviour before 052.
DROP TABLE IF EXISTS artist_services;
