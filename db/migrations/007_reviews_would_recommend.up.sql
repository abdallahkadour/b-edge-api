ALTER TABLE reviews ADD COLUMN IF NOT EXISTS would_recommend BOOLEAN;

COMMENT ON COLUMN reviews.would_recommend IS 'Customer indicated whether they would recommend this artist to friends.';
