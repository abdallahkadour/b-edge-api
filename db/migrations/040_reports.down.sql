-- Removes the only route a client has to raise a problem inside the product.
DROP INDEX IF EXISTS idx_reports_by_artist;
DROP INDEX IF EXISTS idx_reports_queue;
DROP INDEX IF EXISTS idx_reports_one_open_per_booking;
DROP TABLE IF EXISTS reports;
