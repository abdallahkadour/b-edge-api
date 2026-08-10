-- 013_bookings_review_token.down.sql
DROP INDEX IF EXISTS bookings_review_token_unique;
ALTER TABLE bookings DROP COLUMN IF EXISTS review_token;
