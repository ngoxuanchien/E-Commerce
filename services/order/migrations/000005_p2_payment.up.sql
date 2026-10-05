-- P2 (WS3' spec §2): online payment, commission snapshot, delivery time, shop slug.
ALTER TABLE orders DROP CONSTRAINT orders_status_check;
ALTER TABLE orders ADD CONSTRAINT orders_status_check CHECK (status IN (
    'PENDING', 'AWAITING_PAYMENT', 'PENDING_CONFIRMATION', 'CONFIRMED',
    'SHIPPING', 'DELIVERED', 'COMPLETED', 'CANCELLED'));
ALTER TABLE orders DROP CONSTRAINT orders_payment_method_check;
ALTER TABLE orders ADD CONSTRAINT orders_payment_method_check CHECK (payment_method IN ('COD', 'VNPAY', 'STRIPE'));

ALTER TABLE orders
    ADD COLUMN shop_slug           text        NOT NULL DEFAULT '',
    ADD COLUMN payment_status      text        NOT NULL DEFAULT 'NONE'
        CHECK (payment_status IN ('NONE', 'UNPAID', 'PAID', 'REFUND_PENDING', 'REFUNDED')),
    ADD COLUMN payment_deadline    timestamptz NULL,
    ADD COLUMN commission_rate_bps int         NOT NULL DEFAULT 0 CHECK (commission_rate_bps BETWEEN 0 AND 10000),
    ADD COLUMN commission_amount   bigint      NOT NULL DEFAULT 0 CHECK (commission_amount >= 0),
    ADD COLUMN delivered_at        timestamptz NULL;

CREATE INDEX orders_awaiting_payment_idx ON orders (payment_deadline) WHERE status = 'AWAITING_PAYMENT';
CREATE INDEX orders_delivered_idx        ON orders (delivered_at)     WHERE status = 'DELIVERED';
CREATE INDEX orders_checkout_idx         ON orders (checkout_id);
