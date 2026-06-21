-- Migration 008: add a primary category to artists for discovery filtering.
--
-- Discovery cards label each artist with a single primary specialty (Makeup,
-- Hair, Nails, Lashes, Skincare). The column is nullable: existing artists have
-- no category until they set one, and uncategorised artists simply do not appear
-- in category-filtered results (they still appear under the "All" filter).
--
-- The CHECK constraint keeps the stored values aligned with the fixed set the
-- customer PWA filters on, so data and UI can never drift apart.
ALTER TABLE artists
    ADD COLUMN category VARCHAR(20)
    CHECK (category IN ('makeup', 'hair', 'nails', 'lashes', 'skincare'));

-- Partial index: discovery filters on category, skipping NULLs.
CREATE INDEX idx_artists_category ON artists (category) WHERE category IS NOT NULL;
