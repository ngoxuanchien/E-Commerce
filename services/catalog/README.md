# catalog-svc

Shops, categories, products with SKUs and images, Vietnamese accent-insensitive
search, presigned image uploads, and inventory — the single source of truth for
stock. Spec: `docs/superpowers/specs/2026-09-28-marketplace-p1-ws2-catalog.md`.

| Interface | Address | Contract |
|---|---|---|
| REST | `:8082` (via gateway `/api/...`) | `api/openapi/catalog.yaml` |
| gRPC `CatalogInternal` | `:9082` | `proto/catalog/v1/catalog.proto` |
| Kafka consumer | topic `order.events`, group `catalog-svc` | `api/events/order-cancelled.schema.json` |
| Calls | identity-svc `IdentityInternal.GrantRole` | `proto/identity/v1/identity.proto` |

## Layout

```
internal/domain             entities, validation, cursor, stock-item rules (no I/O)
internal/app                ShopService, ProductService, SearchService, InventoryService
internal/adapter/postgres   sqlc queries (queries/ → sqlcdb/) + hand-built search SQL
internal/adapter/httpapi    public + seller REST handlers
internal/adapter/grpcapi    CatalogInternal server, identity client, grpctest fakes
internal/adapter/storage    MinIO presigned POST
internal/adapter/kafka      OrderCancelled consumer
internal/testsupport        Postgres container per test binary, database per test
```

## Configuration

`SERVICE_NAME`, `HTTP_ADDR`, `GRPC_ADDR`, `DATABASE_URL`, `KAFKA_BROKERS`,
`OTEL_EXPORTER_OTLP_ENDPOINT`, `LOG_LEVEL`, `IDENTITY_GRPC_ADDR`, `MINIO_ENDPOINT`,
`MINIO_ACCESS_KEY`, `MINIO_SECRET_KEY`, `MINIO_BUCKET` (default `product-images`),
`MINIO_USE_SSL` (default `false`), `PUBLIC_ASSET_BASE_URL`.
`/readyz` checks `postgres`, `minio` (bucket exists) and `kafka`.

## How stock stays correct

- `ReserveStock` runs in one transaction: `pg_advisory_xact_lock(order_id)` →
  "already reserved?" (idempotency) → items merged and sorted by `sku_id` →
  `UPDATE skus SET stock = stock - q WHERE ... AND stock >= q` per item → any
  shortage rolls everything back and returns `FAILED_PRECONDITION` with
  `InsufficientStock`. `CHECK (stock >= 0)` is the last line of defence.
- `ReleaseStock` and the `OrderCancelled` consumer take the same order lock,
  lock `reserved` rows `FOR UPDATE`, add the quantities back and mark them
  `released`. The consumer also inserts the event id into `processed_events`
  in the same transaction, so redelivery is a no-op.

## Decisions beyond the spec

1. Packages `httpapi`/`grpcapi` avoid shadowing `net/http` and `grpc`.
2. `description` columns are `NOT NULL DEFAULT ''` (the contract always returns a string).
3. Extra caps: shop description ≤ 2,000, product description ≤ 5,000, SKU name 1–100 chars; text is trimmed.
4. `image_keys` must match `shops/{shop_id}/{uuid}.{jpg|png|webp}` exactly, without duplicates; object existence is not checked.
5. Unknown/inactive SKUs in `ReserveStock` are shortages with `available = 0`; malformed ids → `INVALID_ARGUMENT`. `GetSkus` omits malformed ids; > 100 ids → `INVALID_ARGUMENT`.
6. Known limitation: a `ReleaseStock` arriving before the first `ReserveStock` of an order leaves no tombstone (order-svc's sweeper covers this).
7. The cursor also encodes its sort; reusing it with another sort → 400. `sort=relevance` without `q` → `newest`.
8. Public product detail hides inactive SKUs; the owner sees everything.
9. `upload_url` = `PUBLIC_ASSET_BASE_URL` (path-style bucket URL) because `MINIO_ENDPOINT` is internal. Browser CORS on the bucket is a deploy concern.
10. Seller without a shop → 403 on `/api/seller/*`, 404 on `/api/shops/me`. Non-UUID `X-User-Id` → 401.
11. Poison Kafka records are logged and skipped; database errors are retried.
12. **Seller stock edits use compare-and-set on `expected_stock`** (decided 2026-09-29 in P1.5; it replaces the WS2 "PATCH sets stock directly" risk).
    - **The problem:** a PATCH that includes `skus` always writes each SKU's `stock`. A form loaded before an order reserved units would silently re-add those units and oversell.
    - **The rule:** every SKU with `id` in a PATCH must carry `expected_stock`, the stock the client last read (400 otherwise). New SKUs and POST must not carry it (400).
    - **How it is checked:** `applySkus` compares the value with the row locked by `LockProductSkus ... FOR UPDATE`, the same lock `ReserveStock`/`ReleaseStock` take. The check and the write therefore cannot interleave with a reservation.
    - **On a mismatch:** the whole PATCH rolls back (409 `CONFLICT`, `details.skus = [{sku_id, expected_stock, stock}]`) and the client reloads.
    - **Why not a version field:** a per-SKU `version` column would add a migration, and would also reject edits after unrelated changes. What matters is stock, and stock is already the value both parties read.
    - **Why not deltas:** a `stock_delta` field would change what `stock` means in the API.

## Tests

```bash
cd services/catalog
go test -race ./...                                                        # needs Docker
go test -race -count=5 -run 'TestReserve|TestRelease|TestConcurrent' ./internal/adapter/grpcapi/
go test -short ./...                                                       # no containers
```
