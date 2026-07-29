-- Add avatar_url to artists for profile photo support.
-- Stores a Cloudinary (or other CDN) URL; NULL means no avatar uploaded yet.
ALTER TABLE artists
    ADD COLUMN avatar_url TEXT;
