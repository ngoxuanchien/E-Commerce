-- Fails if any non-COD or AWAITING_PAYMENT rows exist, which is intended:
-- rolling back must not silently drop online-payment orders.
DROP INDEX orders_checkout_idx;
DROP INDEX orders_delivered_idx;
DROP INDEX orders_awaiting_payment_idx;

ALTER TABLE orders
    DROP COLUMN delivered_at,
    DROP COLUMN commission_amount,
    DROP COLUMN commission_rate_bps,
    DROP COLUMN payment_deadline,
    DROP COLUMN payment_status,
    DROP COLUMN shop_slug;

ALTER TABLE orders DROP CONSTRAINT orders_payment_method_check;
ALTER TABLE orders ADD CONSTRAINT orders_payment_method_check CHECK (payment_method = 'COD');
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check CHECK (status IN (
    'PENDING', 'PENDING_CONFIRMATION', 'CONFIRMED', 'SHIPPING', 'DELIVERED', 'COMPLETED', 'CANCELLED'));
