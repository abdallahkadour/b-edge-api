-- 054: an order the customer's phone sends twice is one order.
--
-- WHY
--
-- On a flaky connection a checkout can reach the server, place the order and
-- then lose the reply. The customer sees an error, taps "Place order" again,
-- and every repeat created another order and took the stock again. Bookings
-- never had this problem - the overlap constraint refuses a second booking
-- of the same artist at the same time - but nothing made a second order
-- impossible. Found checking an external review, 2026-09-26.
--
-- The cart now sends request_id: a random UUID it makes when she places the
-- order and reuses if she retries. The server stores it here, unique, and a
-- repeat returns the original order instead of creating another
-- (product.Service.PlaceOrder). Optional: a client that sends none behaves
-- exactly as before, and NULLs never collide.

ALTER TABLE orders ADD COLUMN request_id UUID;
ALTER TABLE orders ADD CONSTRAINT orders_request_id_key UNIQUE (request_id);
