-- The seller order list without ?status= orders by (created_at, id) DESC.
-- orders_seller_idx has status in between, so such a query had to sort.
CREATE INDEX IF NOT EXISTS orders_seller_created_idx ON orders (seller_user_id, created_at DESC, id DESC);
