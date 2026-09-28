# WS2 — catalog-svc

> Depends on: WS0 (`api/openapi/catalog.yaml`, `proto/catalog/v1`, `proto/identity/v1` client, `api/events`, `pkg/`). Owns: `services/catalog/`. During development it uses an in-process **fake Identity gRPC server** (`bufconn`).

## 1. Responsibility

Shops, categories, products + variants (SKUs), images, search, and **inventory** (the single source of truth for stock), including reserving/releasing stock for orders.

## 2. Data (`catalog_db`)

```sql
shops(id uuid pk, owner_user_id uuid unique not null,   -- P1: at most one shop per user
      name text not null, slug text unique not null, description text, created_at, updated_at)
categories(id uuid pk, parent_id uuid null fk, name text, slug text unique, position int)
products(id uuid pk, shop_id uuid fk, category_id uuid fk, name text not null, description text,
         status text check (status in ('draft','active','archived')), min_price bigint,
         search_vector tsvector generated always as (...) stored, created_at, updated_at)
product_images(id uuid pk, product_id uuid fk, object_key text, position int)
skus(id uuid pk, product_id uuid fk, name text not null, price bigint check (price > 0),
     stock int not null check (stock >= 0), active bool default true, created_at, updated_at)
stock_reservations(order_id uuid, sku_id uuid, quantity int,
                   status text check (status in ('reserved','released')),
                   created_at, updated_at, primary key (order_id, sku_id))
processed_events(event_id uuid pk, processed_at)
```
Indexes: GIN on `search_vector`; `(status, created_at desc, id)`, `(status, min_price, id)`, `(shop_id)`, `(category_id)`.
Vietnamese search: `unaccent` extension + an `IMMUTABLE` wrapper `vn_unaccent(text)`; `search_vector = setweight(to_tsvector('simple', vn_unaccent(name)),'A') || setweight(to_tsvector('simple', vn_unaccent(coalesce(description,''))),'B')`. About 10 top-level categories with children are seeded by migration.

## 3. Behavior

**Open shop (`POST /api/shops`):** `name` 3–80 chars, `slug` matches `^[a-z0-9-]{3,50}$` and is not reserved (`me`, `new`, `admin`, `seller`) so `/api/shops/me` never collides with `/api/shops/{slug}`. In one transaction: insert shop → call gRPC `GrantRole(user, "seller")` (3 s timeout) → commit. gRPC failure → rollback, 503 `UPSTREAM_UNAVAILABLE`. User already has a shop / slug taken → 409.

**Products (seller):** every operation is scoped to the shop with `owner_user_id = X-User-Id` (another owner's product → 404, not revealing existence).
- `ProductInput` requires 1–50 SKUs and at most 9 images; `name` 5–200 chars; `price` 1,000–1,000,000,000; `stock` 0–100,000.
- SKU PATCH semantics: SKU with `id` → update; without → create; existing SKU absent from input → `active=false` (never deleted, old orders reference it).
- Editing `stock` sets on-hand stock directly (does not touch reservations).
- `min_price` = lowest price among active SKUs, maintained in the same transaction.
- `DELETE` → `status=archived`.
- `image_keys` must have prefix `shops/{shop_id}/` (prevents attaching another shop's images).

**Image upload:** `POST /api/seller/uploads {content_type}` accepts only `image/jpeg|png|webp`; returns a MinIO presigned POST (policy: max 5 MB, content-type, key `shops/{shop_id}/{uuid}.{ext}`, expires in 10 min). Public URL = `PUBLIC_ASSET_BASE_URL/{object_key}`.

**Browse & search (public):** only `active` products with ≥1 active SKU.
- `q` → `websearch_to_tsquery('simple', vn_unaccent(q))`; default sort `relevance` when `q` is present, otherwise `newest`.
- Filters: `category` (slug, includes child categories), `shop` (slug), `min_price`/`max_price` against `min_price`.
- Keyset pagination: cursor = base64 JSON `{sort_value, id}`.
- `GET /api/products/{id}`: non-active product → 404 (except for its shop owner).

**gRPC `GetSkus`:** returns `SkuInfo` (WS0 §5); max 100 ids per call.

**gRPC `ReserveStock(order_id, items)`** — one transaction:
1. If `stock_reservations` already exist for `order_id` → return OK (idempotent).
2. Merge duplicate items, sort by `sku_id` (deadlock avoidance).
3. For each item: `UPDATE skus SET stock = stock - $q WHERE id = $id AND active AND stock >= $q` (product must be active too). 0 rows → record a shortage.
4. Any shortage → rollback, `FAILED_PRECONDITION` + `InsufficientStock`. Otherwise insert `reserved` rows and commit.

**gRPC `ReleaseStock(order_id)`** — one transaction: `SELECT ... WHERE order_id=$1 AND status='reserved' FOR UPDATE`; add quantities back to stock; mark `released`. No rows → OK.

**Kafka consumer** (`order.events`, group `catalog-svc`): handles only `OrderCancelled` → same logic as Release, inserting into `processed_events` in the same transaction (already present → skip). Other event types are ignored. Failures retry with backoff; offsets are committed only after successful processing.

## 4. Code layout

```
internal/domain             Shop, Product, Sku, Category, validation rules, min_price
internal/app                ShopService, ProductService, SearchService, InventoryService (Reserve/Release)
internal/adapter/postgres   sqlc repositories
internal/adapter/http       public + seller handlers
internal/adapter/grpc       CatalogInternal server; identity client
internal/adapter/storage    MinIO presigning
internal/adapter/kafka      OrderCancelled consumer
```
Service-specific env: `IDENTITY_GRPC_ADDR`, `MINIO_ENDPOINT`, `MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_BUCKET=product-images`, `PUBLIC_ASSET_BASE_URL`.

## 5. Testing

- Unit: `ProductInput` validation, `min_price` computation, cursor encode/decode.
- Integration (Postgres, MinIO, Kafka containers):
  - Open shop → fake identity receives `GrantRole`; fake returns error → no shop is created.
  - Product CRUD; different owner → 404; searching "ao thun" finds "Áo thun"; price/category filters; pagination has no duplicates or gaps.
  - **Concurrency:** SKU with stock 10, 50 goroutines each `ReserveStock` 1 unit with distinct order_ids → exactly 10 succeed, stock = 0, never negative.
  - Reserve the same order_id twice → stock decremented once; Release twice → incremented once.
  - Publish `OrderCancelled` twice → stock returned exactly once.
  - Real presigned POST upload to MinIO succeeds; a file > 5 MB is rejected.
- Contract: requests/responses validated against `api/openapi/catalog.yaml`.

## 6. Acceptance criteria

- [ ] All endpoints in `catalog.yaml` and the three RPCs behave as described.
- [ ] Concurrency & idempotency tests pass reliably (`-count=5`).
- [ ] `go test ./...` + lint green; `/readyz` checks Postgres, MinIO and Kafka.
