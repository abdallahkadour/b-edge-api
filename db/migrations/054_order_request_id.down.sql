-- Reverts 054. Loses only the request ids, which exist to recognise a retry
-- in the seconds after an order is placed.
ALTER TABLE orders DROP CONSTRAINT IF EXISTS orders_request_id_key;
ALTER TABLE orders DROP COLUMN IF EXISTS request_id;
