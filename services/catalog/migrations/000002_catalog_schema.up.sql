-- Catalog schema (WS2 §2).
CREATE EXTENSION IF NOT EXISTS unaccent;

-- unaccent() is STABLE because its dictionary can change; generated columns
-- and indexes need IMMUTABLE, so wrap it with the dictionary pinned.
CREATE FUNCTION vn_unaccent(text) RETURNS text
    LANGUAGE sql IMMUTABLE PARALLEL SAFE STRICT
    AS $$ SELECT public.unaccent('public.unaccent'::regdictionary, $1) $$;

CREATE TABLE shops (
    id            uuid PRIMARY KEY,
    owner_user_id uuid NOT NULL,
    name          text NOT NULL,
    slug          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT shops_owner_user_id_key UNIQUE (owner_user_id), -- P1: one shop per user
    CONSTRAINT shops_slug_key UNIQUE (slug)
);

CREATE TABLE categories (
    id        uuid PRIMARY KEY,
    parent_id uuid REFERENCES categories (id),
    name      text NOT NULL,
    slug      text NOT NULL UNIQUE,
    position  int  NOT NULL DEFAULT 0
);

CREATE TABLE products (
    id            uuid PRIMARY KEY,
    shop_id       uuid NOT NULL REFERENCES shops (id),
    category_id   uuid NOT NULL,
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    status        text NOT NULL CHECK (status IN ('draft', 'active', 'archived')),
    min_price     bigint, -- lowest price among active SKUs; NULL when none is active
    search_vector tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', vn_unaccent(name)), 'A') ||
        setweight(to_tsvector('simple', vn_unaccent(description)), 'B')
    ) STORED,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT products_category_id_fkey FOREIGN KEY (category_id) REFERENCES categories (id)
);
CREATE INDEX products_search_vector_idx ON products USING gin (search_vector);
CREATE INDEX products_status_created_idx ON products (status, created_at DESC, id);
CREATE INDEX products_status_min_price_idx ON products (status, min_price, id);
CREATE INDEX products_shop_id_idx ON products (shop_id);
CREATE INDEX products_category_id_idx ON products (category_id);

CREATE TABLE product_images (
    id         uuid PRIMARY KEY,
    product_id uuid NOT NULL REFERENCES products (id) ON DELETE CASCADE,
    object_key text NOT NULL,
    position   int  NOT NULL
);
CREATE INDEX product_images_product_idx ON product_images (product_id, position);

CREATE TABLE skus (
    id         uuid PRIMARY KEY,
    product_id uuid NOT NULL REFERENCES products (id),
    name       text   NOT NULL,
    price      bigint NOT NULL CHECK (price > 0),
    stock      int    NOT NULL CHECK (stock >= 0), -- last line of defence against overselling
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX skus_product_id_idx ON skus (product_id);

CREATE TABLE stock_reservations (
    order_id   uuid NOT NULL,
    sku_id     uuid NOT NULL REFERENCES skus (id),
    quantity   int  NOT NULL CHECK (quantity > 0),
    status     text NOT NULL CHECK (status IN ('reserved', 'released')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (order_id, sku_id)
);

CREATE TABLE processed_events (
    event_id     uuid PRIMARY KEY,
    processed_at timestamptz NOT NULL DEFAULT now()
);
