# WS3 — order-svc

> Depends on: WS0 (`api/openapi/order.yaml`, `proto/catalog/v1` client, `api/events`, `pkg/`). Owns: `services/order/`. During development it uses an in-process **fake Catalog gRPC server** (`bufconn`) with configurable SKUs, stock and failures.

## 1. Responsibility

Shopping cart, checkout (splitting into one order per shop), the order lifecycle state machine, and publishing order events via the transactional outbox. Never touches stock directly — always through Catalog gRPC.

## 2. Data

**Redis — cart:** hash `cart:{user_id}` → field `sku_id`, value `quantity`. Max 50 lines; quantity 1–99. No TTL.

**Postgres (`order_db`):**
```sql
orders(id uuid pk, checkout_id uuid not null, buyer_id uuid not null,
       shop_id uuid not null, shop_name text not null, seller_user_id uuid not null,
       status text not null, subtotal bigint, shipping_fee bigint, total bigint,
       shipping_address jsonb not null, payment_method text not null default 'COD',
       cancel_reason text null, version int not null default 0, created_at, updated_at)
order_items(id uuid pk, order_id uuid fk, sku_id uuid, product_id uuid, product_name text,
            variant_name text, image_url text, unit_price bigint, quantity int, line_total bigint)
order_status_history(id uuid pk, order_id uuid fk, from_status text null, to_status text,
                     actor_id uuid null, actor_role text, note text null, created_at)
idempotency_keys(user_id uuid, key text, request_hash bytea, response jsonb null,
                 status_code int null, created_at, primary key (user_id, key))
outbox(...)   -- schema provided by pkg/outbox
```
Indexes: `(buyer_id, created_at desc, id)`, `(seller_user_id, status, created_at desc, id)`, `(status, created_at)` for the sweeper.

## 3. Cart

- `GET /api/cart`: read Redis, enrich via `GetSkus` (current price, stock, availability), group by shop. SKUs missing from catalog are shown `available=false`. `subtotal` counts only available lines.
- `PUT /api/cart/items/{sku_id}`: validate SKU exists and is available via `GetSkus`; reject if `seller_user_id == buyer` (can't buy from your own shop) → 409 `CONFLICT`. Quantity above current stock is allowed in the cart but flagged; checkout enforces stock.
- `DELETE`: remove the line.
- Catalog unavailable → 503 `UPSTREAM_UNAVAILABLE`.

## 4. Checkout (`POST /api/checkout`)

`Idempotency-Key` header is required (1–100 chars). `sku_ids` optional (default: whole cart).

1. **Idempotency:** insert `(user_id, key, sha256(body))`. Existing row with same hash and a stored response → return it verbatim. Same hash still in progress → 409 `CONFLICT`. Different hash → 422 `IDEMPOTENCY_KEY_REUSED`.
2. Load selected cart lines; empty → 400 `VALIDATION_ERROR`.
3. `GetSkus`; any line unavailable, own-shop, or unknown → 409 with details, nothing created.
4. Group by `shop_id` → one order per shop. `subtotal` = Σ `unit_price × quantity` using **current catalog prices**; `shipping_fee` = `SHIPPING_FEE_FLAT` (default 30,000) per order; `total = subtotal + shipping_fee`. Items snapshot product name, variant name, image, price.
5. Tx A: insert all orders with status `PENDING` + items + history.
6. For each order call `ReserveStock(order_id, items)` (timeout 3 s, retry up to 2 times on `UNAVAILABLE`/`DEADLINE_EXCEEDED`).
7. **All-or-nothing:** if any reservation fails → call `ReleaseStock` for every order of this checkout (idempotent), Tx B marks them `CANCELLED` (`cancel_reason = OUT_OF_STOCK` or `RESERVE_FAILED`, actor `system`), respond 409 `OUT_OF_STOCK` with the shortage details (or 503).
8. Success → Tx C: all orders `PENDING → PENDING_CONFIRMATION`, history rows, one `OrderCreated` outbox message per order, store the response in `idempotency_keys`.
9. Remove purchased lines from the Redis cart (best effort; failure is logged, not returned).
10. Respond 201 `{checkout_id, orders}`.

**Sweeper** (every 60 s): orders still `PENDING` older than 2 min (crash between steps 5 and 8) → `ReleaseStock` + `CANCELLED` (`system`, `CHECKOUT_TIMEOUT`) + `OrderCancelled` outbox. Uses `FOR UPDATE SKIP LOCKED` so multiple replicas are safe. Idempotency keys older than 24 h are deleted by the same job.

## 5. Order state machine

| From | To | Actor | Endpoint |
|---|---|---|---|
| `PENDING` | `PENDING_CONFIRMATION` / `CANCELLED` | system | checkout / sweeper |
| `PENDING_CONFIRMATION` | `CONFIRMED` | seller | `POST /api/seller/orders/{id}/confirm` |
| `PENDING_CONFIRMATION` | `CANCELLED` | buyer or seller | `/api/orders/{id}/cancel`, `/api/seller/orders/{id}/cancel` |
| `CONFIRMED` | `SHIPPING` | seller | `.../ship` |
| `CONFIRMED` | `CANCELLED` | seller | `.../cancel` |
| `SHIPPING` | `DELIVERED` | seller | `.../deliver` |
| `DELIVERED` | `COMPLETED` | buyer | `/api/orders/{id}/complete` |

- The state machine is a pure domain function `Transition(order, action, actor) (event, error)`; illegal moves → 409 `INVALID_STATE_TRANSITION`.
- Updates use optimistic locking (`WHERE id=$1 AND version=$2`); a lost race → retry once, then 409.
- Every transition writes a history row + `OrderStatusChanged` outbox message in the same tx; cancellations additionally write `OrderCancelled` (catalog releases stock asynchronously).
- Authorization: buyer endpoints require `buyer_id = X-User-Id`; seller endpoints require `seller_user_id = X-User-Id`; otherwise 404. `GET /api/orders/{id}` allows either party.
- `cancel` requires `reason` (1–200 chars).

## 6. Code layout

```
internal/domain             Order, OrderItem, Status, Transition(), split-by-shop, totals
internal/app                CartService, CheckoutService, OrderService, Sweeper
internal/adapter/redis      cart store
internal/adapter/postgres   sqlc repositories, idempotency store
internal/adapter/catalog    gRPC client wrapper (timeouts, retries, error mapping)
internal/adapter/http       buyer + seller handlers
```
Background workers in the same binary: outbox relay (`pkg/outbox`), sweeper.
Service-specific env: `CATALOG_GRPC_ADDR`, `SHIPPING_FEE_FLAT=30000`, `SWEEPER_INTERVAL=60s`, `PENDING_TIMEOUT=2m`.

## 7. Testing

- Unit: full transition table (every legal and illegal move per actor); split-by-shop and totals.
- Integration (Postgres, Redis, Kafka containers + fake catalog):
  - Cart CRUD; own-shop SKU rejected; unavailable SKU flagged.
  - Checkout with items from 2 shops → 2 orders, 2 reservations, 2 `OrderCreated` events on Kafka, cart cleared.
  - Same `Idempotency-Key` twice → identical response, reservations called once; different body with same key → 422.
  - Fake catalog fails shop B → both orders `CANCELLED`, both released, 409 `OUT_OF_STOCK`.
  - Crash simulation: order left `PENDING` → sweeper cancels + releases.
  - Lifecycle happy path to `COMPLETED`; buyer cancels a `CONFIRMED` order → 409; seller of another shop → 404.
  - Concurrent confirm + cancel on the same order → exactly one wins.
- Contract: requests/responses validated against `api/openapi/order.yaml`.

## 8. Acceptance criteria

- [ ] All endpoints in `order.yaml` behave as described; checkout is all-or-nothing and idempotent.
- [ ] No order stays `PENDING` longer than `PENDING_TIMEOUT` + one sweeper interval.
- [ ] Events on `order.events` match `api/events` schemas.
- [ ] `go test ./...` + lint green; `/readyz` checks Postgres, Redis, Kafka.
