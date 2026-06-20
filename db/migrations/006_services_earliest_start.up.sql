ALTER TABLE services ADD COLUMN IF NOT EXISTS earliest_start_time TIME;

COMMENT ON COLUMN services.earliest_start_time IS 'Earliest time this service can start. E.g. full makeup cannot start before 11:00 AM.';

UPDATE services SET earliest_start_time = '11:00'::TIME
WHERE LOWER(name) LIKE '%bridal%' OR LOWER(name) LIKE '%makeup%';
