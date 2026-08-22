-- Adds inventory tracking to products. NULL means "unlimited / not
-- tracked" - every product created before this migration keeps working
-- exactly as it does today, with no stock count forced onto it.
-- Decremented atomically on order placement and restored on
-- cancellation/return - see internal/product/repository.go.
ALTER TABLE products
  ADD COLUMN stock_quantity INTEGER
  CHECK (stock_quantity IS NULL OR stock_quantity >= 0);
