-- Allows the polymorphic media table to also hold product photo galleries
-- (owner_type='product', owner_id=products.id) - reusing the exact table
-- that already backs an artist's portfolio, rather than a new one.
-- products.image_url is untouched and stays the primary/first photo shown
-- everywhere it is today; these are the ADDITIONAL photos shown on the
-- customer product-detail gallery.
ALTER TABLE media DROP CONSTRAINT media_owner_type_check;
ALTER TABLE media ADD CONSTRAINT media_owner_type_check
  CHECK (owner_type IN ('artist', 'salon', 'booking', 'product'));
