-- order-svc schema (WS3 spec §2). created_at/updated_at are written by the
-- application clock (UTC) so API responses match what is stored.
CREATE TABLE orders (
    id               uuid        PRIMARY KEY,
    checkout_id      uuid        NOT NULL,
    buyer_id         uuid        NOT NULL,
    shop_id          uuid        NOT NULL,
    shop_name        text        NOT NULL,
    seller_user_id   uuid        NOT NULL,
    status           text        NOT NULL CHECK (status IN ('PENDING', 'PENDING_CONFIRMATION', 'CONFIRMED',
                                                            'SHIPPING', 'DELIVERED', 'COMPLETED', 'CANCELLED')),
    subtotal         bigint      NOT NULL CHECK (subtotal >= 0),
    shipping_fee     bigint      NOT NULL CHECK (shipping_fee >= 0),
    total            bigint      NOT NULL CHECK (total = subtotal + shipping_fee),
    shipping_address jsonb       NOT NULL,
    payment_method   text        NOT NULL DEFAULT 'COD' CHECK (payment_method = 'COD'),
    cancel_reason    text        NULL,
    version          int         NOT NULL DEFAULT 0,
    created_at       timestamptz NOT NULL,
    updated_at       timestamptz NOT NULL
);

CREATE INDEX orders_buyer_idx  ON orders (buyer_id, created_at DESC, id DESC);
CREATE INDEX orders_seller_idx ON orders (seller_user_id, status, created_at DESC, id DESC);
CREATE INDEX orders_status_idx ON orders (status, created_at); -- sweeper

CREATE TABLE order_items (
    id           uuid   PRIMARY KEY,
    order_id     uuid   NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    sku_id       uuid   NOT NULL,
    product_id   uuid   NOT NULL,
    product_name text   NOT NULL,
    variant_name text   NOT NULL,
    image_url    text   NULL,
    unit_price   bigint NOT NULL CHECK (unit_price >= 0),
    quantity     int    NOT NULL CHECK (quantity > 0),
    line_total   bigint NOT NULL CHECK (line_total = unit_price * quantity)
);

CREATE INDEX order_items_order_idx ON order_items (order_id);

CREATE TABLE order_status_history (
    id          uuid        PRIMARY KEY,
    order_id    uuid        NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    from_status text        NULL,
    to_status   text        NOT NULL,
    actor_id    uuid        NULL,
    actor_role  text        NOT NULL CHECK (actor_role IN ('buyer', 'seller', 'system')),
    note        text        NULL,
    created_at  timestamptz NOT NULL
);

CREATE INDEX order_status_history_order_idx ON order_status_history (order_id, created_at, id);

CREATE TABLE idempotency_keys (
    user_id      uuid        NOT NULL,
    key          text        NOT NULL,
    request_hash bytea       NOT NULL,
    response     jsonb       NULL,
    status_code  int         NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (user_id, key)
);

CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at);
