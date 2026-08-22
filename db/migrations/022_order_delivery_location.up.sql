-- Adds a pin-dropped delivery location to orders. Lebanese addresses don't
-- reliably geocode from text (informal, landmark-based addressing is the
-- norm, not a street-number system), so the customer marks a point on a
-- map at checkout instead of typing an address. delivery_notes (free text)
-- stays for the extras a pin can't express ("3rd floor, blue door").
ALTER TABLE orders
  ADD COLUMN delivery_lat DOUBLE PRECISION
    CHECK (delivery_lat IS NULL OR (delivery_lat BETWEEN -90 AND 90)),
  ADD COLUMN delivery_lng DOUBLE PRECISION
    CHECK (delivery_lng IS NULL OR (delivery_lng BETWEEN -180 AND 180));
