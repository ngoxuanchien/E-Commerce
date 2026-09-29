# order-svc

Cart, checkout and the order lifecycle of the marketplace (WS3, spec
`docs/superpowers/specs/2026-09-28-marketplace-p1-ws3-order.md`). Checkout
splits a cart into one order per shop, is all-or-nothing and idempotent, and
publishes `OrderCreated` / `OrderStatusChanged` / `OrderCancelled` events to
Kafka topic `order.events` through a transactional outbox. The service never
touches stock itself: it reserves and releases it through catalog-svc's
`CatalogInternal` gRPC API.

## Layout

| Package | Responsibility |
|---|---|
| `internal/domain` | Pure rules: order state machine (`Transition`), split-by-shop and totals (`BuildOrders`), cart view (`BuildCart`) |
| `internal/app` | Use cases: `CartService`, `CheckoutService`, `OrderService`, `Sweeper`; port interfaces (`Catalog`, `CartStore`, `Store`, `Tx`) |
| `internal/adapter/postgres` | `app.Store` on PostgreSQL (sqlc: `queries/` → `sqlcdb/`), optimistic locking, outbox writes, idempotency keys |
| `internal/adapter/redis` | Cart store: hash `cart:{user_id}` → `sku_id` → quantity, max 50 lines, no TTL |
| `internal/adapter/catalog` | gRPC client: 3 s per attempt, 2 retries on `UNAVAILABLE`/`DEADLINE_EXCEEDED`, error mapping |
| `internal/adapter/catalog/catalogfake` | In-process fake `CatalogInternal` server (bufconn) for tests |
| `internal/adapter/http` | REST handlers for `api/openapi/order.yaml`: validation, DTOs, error mapping |
| `internal/bootstrap` | Wiring shared by `cmd/server` and the end-to-end tests |
| `internal/itest` | End-to-end suite over HTTP with Postgres, Redis, Kafka and the catalog fake |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `CATALOG_GRPC_ADDR` | — (required) | catalog-svc gRPC address |
| `SHIPPING_FEE_FLAT` | `30000` | Flat shipping fee per order (VND) |
| `SWEEPER_INTERVAL` | `60s` | How often the sweeper runs |
| `PENDING_TIMEOUT` | `2m` | Age after which a `PENDING` order is cancelled |
| `DATABASE_URL`, `REDIS_URL`, `KAFKA_BROKERS` | — | Dependencies (checked by `/readyz`) |
| `HTTP_ADDR`, `GRPC_ADDR`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `LOG_LEVEL`, `SERVICE_NAME` | WS0 | Common service settings |

## Checkout and sweeper

1. Claim `(buyer, Idempotency-Key)` with a hash of the normalized request.
2. Load the selected cart lines, fetch current SKU data, build one `PENDING` order per shop.
3. **Tx A** inserts the orders (so a crash from here on is recoverable).
4. Reserve stock for every order concurrently.
5. On any failure: release every order, **Tx B** cancels them all (`OUT_OF_STOCK` / `RESERVE_FAILED`) with events.
6. On success: **Tx C** moves every order to `PENDING_CONFIRMATION`, writes `OrderCreated` + `OrderStatusChanged`, and stores the response.
7. Purchased lines leave the Redis cart (best effort).

The sweeper cancels orders still `PENDING` after `PENDING_TIMEOUT`
(`CHECKOUT_TIMEOUT`, `FOR UPDATE SKIP LOCKED`), releases their stock after
commit, and purges idempotency keys older than 24 h.

## Design decisions

Gaps in the WS3 spec, resolved without any contract change:

1. **Unknown SKUs in the cart** (catalog no longer returns them) cannot be rendered: `CartShop.shop` is required and they have no shop. They are omitted from `GET /api/cart` and pruned from Redis (best effort). Catalog never deletes SKUs (absent SKUs become `active=false`), so this only affects bogus data; inactive SKUs are still shown with `available=false`.
2. **Every status change writes `OrderStatusChanged`**, including checkout's `PENDING → PENDING_CONFIRMATION` (after `OrderCreated`) and system cancellations; every cancellation also writes `OrderCancelled`. The initial insert (→ `PENDING`) writes only a history row.
3. **Checkout compensation (Tx B) writes events** (`OrderStatusChanged` + `OrderCancelled` per order), so if the synchronous `ReleaseStock` fails the catalog consumer still releases the stock.
4. **Idempotency storage:** the response is stored for 201 and for deterministic rejections (empty selection / `sku_ids` not in cart → 400; unknown/unavailable/own-shop lines → 409 `CONFLICT`; shortages → 409 `OUT_OF_STOCK`; catalog `NOT_FOUND`/`INVALID_ARGUMENT` → 409 `CONFLICT`). Transient failures (503, 500, checkout expired) delete the key so the client may retry with the same key. The request hash is SHA-256 of the canonical JSON of the normalized request (sorted, de-duplicated `sku_ids`), not of the raw bytes. Replays are JSON-equivalent (the `jsonb` column normalizes whitespace and key order).
5. **Reservations run concurrently** for all orders of a checkout; shortages from every shop are merged into one `OUT_OF_STOCK` response.
6. **Checkout outliving `PENDING_TIMEOUT`** (sweeper cancelled the orders first): Tx C's optimistic lock fails → reservations are released and the client gets 409 `CONFLICT` ("checkout took too long"); key released.
7. **Cart `PUT` errors:** unknown SKU → 404; inactive SKU → 409; own shop → 409; 51st distinct line → 409 `CONFLICT`. `DELETE` with a non-UUID `sku_id` returns the unchanged cart (200) because the contract documents no 400/404 for it.
8. **Malformed IDs:** order path IDs that are not canonical UUIDs → 404; `X-User-Id` that is not a UUID → 401. Upper-case UUIDs are canonicalized to lower case.
9. **Sweeper order of operations:** claim + cancel + outbox commit first, then `ReleaseStock` outside the row locks (best effort, logged), so a slow catalog neither holds locks nor delays cancellation beyond one interval.
10. **Checkout ignores request cancellation** (`context.WithoutCancel`) once started.
11. **Ordering:** cart shops by name then ID, items by product name, variant name, SKU ID; checkout orders by shop ID, items by SKU ID; lists newest first by `(created_at, id)`.
12. **Lists** return every status (including transient `PENDING`) unless `?status=` filters.
13. **Readiness** checks Postgres, Redis and Kafka. Catalog is not a readiness dependency: cart/checkout answer 503 while it is down.
14. **Timestamps** of orders and history come from the app clock (UTC, µs) so API responses equal what is stored; the outbox keeps its DB default.
15. **Retries** (3 s per attempt, 2 retries, backoff 100 ms × attempt) apply to all three catalog RPCs — all are idempotent.
16. **Seller notes:** the optional `reason` on confirm/ship/deliver is stored as the history `note`.
17. **Idempotency rows orphaned by a crash** stay "in progress" (409) until the 24 h purge; the orders themselves are cancelled by the sweeper.

## Testing

```bash
go test -race -count=1 ./...   # everything; needs Docker (testcontainers)
go test -short ./...           # unit tests only, no containers
```

catalog-svc is never needed: `internal/adapter/catalog/catalogfake` serves
`CatalogInternal` over bufconn with real in-memory stock and idempotent
reservations, and can inject shortages (`SetStock`, `SetAvailable`), error
codes (`FailNext`, `FailReserveForSkus`) and latency (`SetDelay`). The
end-to-end suite validates every request/response against
`api/openapi/order.yaml` and every Kafka event against `api/events/*.schema.json`.
