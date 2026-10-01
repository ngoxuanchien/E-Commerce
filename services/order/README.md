# order-svc

Cart, checkout and the order lifecycle of the marketplace (WS3 + WS3', specs
`docs/superpowers/specs/2026-09-28-marketplace-p1-ws3-order.md` and
`docs/superpowers/specs/2026-09-29-marketplace-p2-ws3-order.md`). Checkout
splits a cart into one order per shop, is all-or-nothing and idempotent, and
publishes `OrderCreated` / `OrderStatusChanged` / `OrderCancelled` /
`OrderCompleted` events to Kafka topic `order.events` through a transactional
outbox. Payment is COD (WS3) or online — VNPay or Stripe — (WS3'): an online
checkout parks its orders in `AWAITING_PAYMENT` with a payment deadline and the
commission computed once, then payment-svc's `payment.events` settle them. The
service never touches stock itself: it reserves and releases it through
catalog-svc's `CatalogInternal` gRPC API, and it never touches money itself:
it asks payment-svc through `PaymentInternal`.

## Layout

| Package | Responsibility |
|---|---|
| `internal/domain` | Pure rules: order state machine (`Transition`), split-by-shop and totals (`BuildOrders`), cart view (`BuildCart`), payment-status rules (`LateCapture`, `MarkRefunded`) |
| `internal/app` | Use cases: `CartService`, `CheckoutService`, `CheckoutQuery`, `OrderService`, `PaymentEventHandler`, `Sweeper`; port interfaces (`Catalog`, `Payments`, `CartStore`, `Store`, `Tx`) |
| `internal/adapter/postgres` | `app.Store` on PostgreSQL (sqlc: `queries/` → `sqlcdb/`), optimistic locking, outbox writes, idempotency keys, payment inbox |
| `internal/adapter/redis` | Cart store: hash `cart:{user_id}` → `sku_id` → quantity, max 50 lines, no TTL |
| `internal/adapter/catalog` | gRPC client: 3 s per attempt, 2 retries on `UNAVAILABLE`/`DEADLINE_EXCEEDED`, error mapping |
| `internal/adapter/catalog/catalogfake` | In-process fake `CatalogInternal` server (bufconn) for tests |
| `internal/adapter/payment` | `PaymentInternal` client: one `CancelCheckoutPayments` call per sweeper tick, 3 s timeout, no retry within a tick, lazy dial |
| `internal/adapter/payment/paymentfake` | In-process fake `PaymentInternal` server (bufconn) with scriptable outcomes and latency |
| `internal/adapter/kafka` | `payment.events` consumer: group `order-svc`, hand-off to `PaymentEventHandler`, kafkax backoff on error |
| `internal/adapter/grpc` | `OrderInternal` server (`GetCheckout`), as payment-svc calls it |
| `internal/adapter/http` | REST handlers for `api/openapi/order.yaml`: validation, DTOs, error mapping |
| `internal/bootstrap` | Wiring shared by `cmd/server` and the end-to-end tests |
| `internal/itest` | End-to-end suite over HTTP with Postgres, Redis, Kafka, the catalog fake and the payment fake |

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `CATALOG_GRPC_ADDR` | — (required) | catalog-svc gRPC address |
| `PAYMENT_GRPC_ADDR` | — (required) | payment-svc gRPC address (dialed lazily; not a readiness dependency) |
| `SHIPPING_FEE_FLAT` | `30000` | Flat shipping fee per order (VND) |
| `SWEEPER_INTERVAL` | `60s` | How often the sweeper runs |
| `PENDING_TIMEOUT` | `2m` | Age after which a `PENDING` order is cancelled, and the overall budget of one checkout |
| `ONLINE_PAYMENT_METHODS` | `VNPAY,STRIPE` | Enabled online methods; only `VNPAY` / `STRIPE`, empty = COD only |
| `COMMISSION_RATE_BPS` | `500` | Platform commission in basis points (0–10000); `commission_amount = floor(subtotal × bps / 10000)` |
| `PAYMENT_TIMEOUT` | `30m` | How long an online checkout waits for its capture before the sweeper cancels it |
| `AUTO_COMPLETE_AFTER` | `72h` | How long a `DELIVERED` order stays open before the system completes it; must be > 0 (the config rejects `0`) |
| `DATABASE_URL`, `REDIS_URL`, `KAFKA_BROKERS` | — | Dependencies (checked by `/readyz`; empty `KAFKA_BROKERS` means no `payment.events` consumer) |
| `HTTP_ADDR`, `GRPC_ADDR`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `LOG_LEVEL`, `SERVICE_NAME` | WS0 | Common service settings |

## Checkout and sweeper

1. Reject a disabled online method (400) **before** claiming the idempotency key, so the check never consumes it.
2. Claim `(buyer, Idempotency-Key)` with a hash of the normalized request (or reclaim it from a dead run, decision 17).
3. Load the selected cart lines, fetch current SKU data, build one order per shop — online orders get `payment_status = UNPAID`, the commission and `payment_deadline` = Tx C clock + `PAYMENT_TIMEOUT`, shared by every order of the checkout.
4. **Tx A** inserts the orders (so a crash from here on is recoverable).
5. Reserve stock for every order concurrently.
6. On any failure: release every order, **Tx B** cancels them all (`OUT_OF_STOCK` / `RESERVE_FAILED`) with events.
7. On success: **Tx C** moves every order to `PENDING_CONFIRMATION` (COD) or `AWAITING_PAYMENT` (online), writes `OrderCreated` + `OrderStatusChanged`, and stores the response.
8. Purchased lines leave the Redis cart (best effort).

The sweeper runs four steps, each in batches of 50 with `FOR UPDATE SKIP LOCKED`:

- **stale `PENDING`** — cancelled after `PENDING_TIMEOUT` (`CHECKOUT_TIMEOUT`), stock released after commit.
- **payment timeout** — pages distinct expired checkouts by `(deadline, checkout_id)` *without* locks, asks payment-svc `CancelCheckoutPayments` first, and only then locks the checkout's `AWAITING_PAYMENT` orders and cancels them (`PAYMENT_TIMEOUT`). A `PAID` checkout is left alone and, still `AWAITING_PAYMENT` more than 10 min past its deadline, logged at warn on every tick. Up to 10 checkouts are asked at once, and the first call that times out ends the step for the tick (decision 34).
- **auto-complete** — completes `DELIVERED` orders older than `AUTO_COMPLETE_AFTER` with `OrderCompleted`.
- **idempotency keys** — purged after 24 h.

A `payment.events` consumer (group `order-svc`) applies `PaymentSucceeded` and
`PaymentRefunded`: a capture moves `UNPAID → PAID`, a refund moves to
`REFUNDED`, and a capture that lands on a cancelled-unpaid order records a late
capture (`REFUND_PENDING`, ADR-0007). None of these change an order's status.
`/readyz` never calls payment-svc.

## Design decisions

Gaps in the WS3 spec, resolved without any contract change:

1. **Unknown SKUs in the cart** (catalog no longer returns them) cannot be rendered: `CartShop.shop` is required and they have no shop. They are omitted from `GET /api/cart` and pruned from Redis (best effort). Catalog never deletes SKUs (absent SKUs become `active=false`), so this only affects bogus data; inactive SKUs are still shown with `available=false`.
2. **Every status change writes `OrderStatusChanged`**, including checkout's `PENDING → PENDING_CONFIRMATION` (after `OrderCreated`) and system cancellations; every cancellation also writes `OrderCancelled`. The initial insert (→ `PENDING`) writes only a history row.
3. **Checkout compensation (Tx B) writes events** (`OrderStatusChanged` + `OrderCancelled` per order), so if the synchronous `ReleaseStock` fails the catalog consumer still releases the stock.
4. **Idempotency storage:** the response is stored for 201 and for deterministic rejections (empty selection / `sku_ids` not in cart → 400; unknown/unavailable/own-shop lines → 409 `CONFLICT`; shortages → 409 `OUT_OF_STOCK`; catalog `NOT_FOUND`/`INVALID_ARGUMENT` → 409 `CONFLICT`). Transient failures (503, 500, checkout expired) delete the key so the client may retry with the same key. The request hash is SHA-256 of the canonical JSON of the normalized request (sorted, de-duplicated `sku_ids`), not of the raw bytes. Replays are JSON-equivalent (the `jsonb` column normalizes whitespace and key order), so a replay compares equal to the stored response even though the bytes may differ.
5. **Reservations run concurrently** for all orders of a checkout; shortages from every shop are merged into one `OUT_OF_STOCK` response.
6. **Checkout outliving `PENDING_TIMEOUT`** (sweeper cancelled the orders first): Tx C's optimistic lock fails → reservations are released and the client gets 409 `CONFLICT` ("checkout took too long"); key released.
7. **Cart `PUT` errors:** unknown SKU → 404; inactive SKU → 409; own shop → 409; 51st distinct line → 409 `CONFLICT`. `DELETE` with a non-UUID `sku_id` returns the unchanged cart (200) because the contract documents no 400/404 for it.
8. **Malformed IDs:** order path IDs that are not canonical UUIDs → 404; `X-User-Id` that is not a UUID → 401. Upper-case UUIDs are canonicalized to lower case.
9. **Sweeper order of operations:** claim + cancel + outbox commit first, then `ReleaseStock` outside the row locks (best effort, logged, at most 10 concurrent calls), so a slow catalog neither holds locks nor delays cancellation beyond one interval. The same rule applies to the payment-timeout and auto-complete steps.
10. **Checkout contexts.** The forward path — the key claim, the cart read, `GetSkus`, Tx A, the reservations and Tx C — runs on `context.WithoutCancel` from the request context, bounded by one `PENDING_TIMEOUT` deadline so a checkout can never outlive the sweeper's cancellation rule. The compensation path (Tx B, the release calls) ignores that deadline so it still runs after the forward deadline fired, but never forever: it has its own 30 s budget. Both contexts are documented here because the deadline, not just the cancellation, is part of the contract the sweeper relies on.
11. **Ordering:** cart shops by name then ID, items by product name, variant name, SKU ID; checkout orders by shop ID, items by SKU ID; lists newest first by `(created_at, id)`.
12. **Lists** return every status (including transient `PENDING`) unless `?status=` filters.
13. **Readiness** checks Postgres, Redis and Kafka. Catalog is not a readiness dependency: cart/checkout answer 503 while it is down, and payment-svc is not one either — the payment client dials lazily.
14. **Timestamps** of orders and history come from the app clock (UTC, µs) so API responses equal what is stored; the outbox keeps its DB default.
15. **Retries** (3 s per attempt, 2 retries, backoff 100 ms × attempt) apply to all three catalog RPCs — all are idempotent.
16. **Seller notes:** the optional `reason` on confirm/ship/deliver is stored as the history `note`.
17. **Idempotency rows orphaned by a crash** are reclaimed. `created_at` is the claim.
    - **When:** a retry of the *same* request takes over a key that has had no response for `PENDING_TIMEOUT + SWEEPER_INTERVAL` plus a 1 min margin. The margin covers the cart read and `GetSkus` before Tx A. By then the sweeper has cancelled whatever the dead run left `PENDING`.
    - **Before that:** the key answers 409 "in progress".
    - **Different request:** it never takes a key over (422).
    - **Ownership:** completing or releasing a key requires the caller's own claim.
    - **A run whose key was reclaimed:**
      - It cannot commit. Its Tx C rolls back, and if its reservation failed, Tx B commits the cancellations without the key.
      - It releases and cancels its own orders (`CHECKOUT_TIMEOUT` after a lost Tx C) and answers 409.
      - It never adopts or touches the retry's response or claim. The ack-lost recovery reads the key and trusts a stored response only under its own claim.

P2 spec gaps, resolved by WS3':

18. **Online `payment_status = UNPAID` is set at Tx A**, while the order is still `PENDING`; the deadline is only known at Tx C. A `PENDING` online order the sweeper cancels therefore stays `UNPAID` — cancelling does not move the payment status, so the CHECK on `payment_status` still holds.
19. **`payment_deadline` comes from the Tx C clock**, not from the key claim: one deadline shared by every order of a checkout, computed as `now + PAYMENT_TIMEOUT` inside the transaction that writes `AWAITING_PAYMENT`.
20. **A buyer cancel in `AWAITING_PAYMENT` does not call payment-svc** — there is no payment to cancel yet, only a checkout awaiting one. `OrderCancelled` now carries `payment_method`, so consumers can tell COD from online; a capture that arrives later yields `REFUND_PENDING` (ADR-0007) rather than being lost. Stock is released by the catalog consumer, exactly as for a P1 buyer cancel.
21. **`ONLINE_PAYMENT_METHODS` is checked before the idempotency key is claimed.** A disabled method answers 400 `VALIDATION_ERROR` and does not consume the key, so a client that enables the method can retry with the same key.
22. **The payment-timeout step pages without locks.** It pages distinct expired checkouts by `(deadline, checkout_id)`, asks payment-svc `CancelCheckoutPayments` (3 s, no retry within a tick) before locking anything, then locks the checkout's `AWAITING_PAYMENT` rows with `FOR UPDATE SKIP LOCKED`. A lost optimistic race (an order moved concurrently) skips that checkout until the next tick rather than failing the sweep. A checkout payment-svc reports as `PAID` but that is still `AWAITING_PAYMENT` more than 10 min past its deadline is logged at warn level on every tick, so a stuck capture is visible without being cancelled.
23. **The sweeper batch size is 50 for every step** (spec §5); P1 used 100 for the stale-`PENDING` step.
24. **`shop_slug` is snapshotted at checkout** (`BuildOrders` copies the cart's shop slug into every order). P1 rows hold `''`, which is valid for the now-required `Order.shop.slug` — a buyer who opens an old order gets an empty slug rather than a 500.
25. **COD completions emit `OrderCompleted`** (spec §3), so every completion — buyer or system, COD or online — writes exactly one. `OrderCancelled` carries `checkout_id` and `payment_method`; the P1 tests whose payload and dto shop assertions were extended are `internal/app/orders_test.go` (`OrderCancelled`), `internal/app/sweeper_test.go`, the lifecycle itest and `internal/adapter/http/dto_test.go`.
26. **Consumer: an unknown or malformed allocation `order_id` is skipped per order**, not per event — one bad allocation does not discard the others. A lost optimistic-lock race is retried once in the handler (`paymentEventAttempts = 2`) against fresh state, because that state may still accept the event (a capture that lost to a cancel becomes a late capture rather than an error); a second loss returns the error to the consumer, which redelivers with kafkax backoff. The inbox row (`MarkProcessed(consumer="order-svc")`) commits in the same transaction as the effect, so an event is applied at most once.
27. **The overall checkout deadline is `PENDING_TIMEOUT`**, so a checkout can never outlive the sweeper rule that cancels stale `PENDING` orders. Compensation has its own 30 s budget and runs even after the forward deadline fired. A checkout that exceeds the deadline answers 503 (`UPSTREAM_UNAVAILABLE`) with its orders cancelled and its key freed. When the sweeper wins the race with Tx B, the response is the real cause, never `CONFLICT`: `OUT_OF_STOCK` with its shortages (stored and replayed), or 503 for `RESERVE_FAILED` (overview §7). A checkout whose Tx C lost to the sweeper still answers 409 `CONFLICT` (decision 6).
28. **`GetCheckout` answers `NOT_FOUND` for a malformed ID**, because the contract names only `NOT_FOUND` for that RPC — it does not invent a `VALIDATION_ERROR` the proto does not declare.

Deviations recorded while implementing Tasks 1–10:

29. **`ONLINE_PAYMENT_METHODS` set but blank means COD only.** `caarlos0/env` substitutes `envDefault` for an explicitly empty value, so parsing cannot tell `""` from unset; `Config.onlineMethods` reads the environment directly (`os.LookupEnv`) for that case. Unset still means the `VNPAY,STRIPE` default.
30. **`Deps.KafkaBrokers` empty means no `payment.events` consumer.** Bootstrap skips the consumer and its goroutine instead of dialling a broker list of zero addresses; the rest of the service (HTTP, gRPC, sweeper) starts normally.
31. **The payment-timeout step treats every `PaymentInternal` error as "retry next tick"**, including `UNAVAILABLE` for an attempt that is still payable: the sweeper logs at warn and leaves the orders alone rather than cancelling a payment it could not check. The checkout is re-evaluated on the next tick.
32. Merged into decision 27 (the checkout deadline and its answers).
33. **A buyer cancel in `AWAITING_PAYMENT` cancels every `AWAITING_PAYMENT` order of the checkout in one transaction**, so a two-shop checkout cannot end up half cancelled.

Final-review fixes:

34. **The payment-timeout step is bounded per tick, and every sweeper step runs every tick.** `CancelCheckoutPayments` is called for up to 10 checkouts at once (3 s each). The first call that times out (`DEADLINE_EXCEEDED` or the client deadline, `app.ErrPaymentTimeout`) stops the step for the rest of the tick, so a black-holed payment-svc costs about one call timeout per tick instead of one per expired checkout. A plain `UNAVAILABLE` answer skips only that checkout, because payment-svc also returns it for a checkout that is still payable. `RunOnce` runs the stale-`PENDING`, payment-timeout, auto-complete and idempotency-purge steps even when an earlier one fails, joins their errors and sums the counts.
35. **`delivered_at` is backfilled for pre-P2 orders** (migration `000007_backfill_delivered_at`): `DELIVERED` and `COMPLETED` orders with a NULL `delivered_at` take the time of their latest `DELIVERED` history row, so orders delivered before the upgrade still auto-complete. Its down migration is a no-op.
36. **Rows of one checkout are locked in ascending order ID everywhere** (buyer cancel fan-out, payment-event handler, sweeper), so a capture and a buyer cancel cannot deadlock. The handler also skips, with a warning, an allocation whose order belongs to another checkout than the event's.

## Testing

```bash
go test -race -count=1 ./...   # everything; needs Docker (testcontainers)
go test -short ./...           # unit tests only, no containers
```

Neither catalog-svc nor payment-svc is ever needed:
`internal/adapter/catalog/catalogfake` serves `CatalogInternal` over bufconn
with real in-memory stock and idempotent reservations, and can inject shortages
(`SetStock`, `SetAvailable`), error codes (`FailNext`, `FailReserveForSkus`)
and latency (`SetDelay`); `internal/adapter/payment/paymentfake` serves
`PaymentInternal` over bufconn and can script per-checkout outcomes and
latency. The end-to-end suite validates every request/response against
`api/openapi/order.yaml` and every Kafka event against
`api/events/*.schema.json`.

### Contract and web types

`api/openapi/order.yaml` is the source of truth; `web/src/api/gen/*.ts` is
generated from it by `cd web && npm run gen:api` (`npm run gen:api:check`,
which runs inside `npm run lint`, fails when the committed types are stale).

> **Contract flip (WS3', 2026-10-01).** `Order.required` now includes
> `payment_status, payment_deadline, commission_rate_bps, commission_amount,
> delivered_at`, and `OrderShop.required` is `[id, name, slug]` (WS0' §12). The
> same commit carries the regenerated `web/src/api/gen/order.ts` and, with the
> user's approval, a defaults-only edit of the WS5'-owned mocks
> (`web/src/mocks/db.ts`, `web/src/mocks/seed.ts`: COD payment fields and the
> shop slug) so `npm run lint`'s `tsc -b` keeps passing.

### Compose smoke test

`COMPOSE_PROJECT_NAME` is honoured by the Makefile's `COMPOSE` variable
(`docker compose -f deploy/docker-compose.yml …`), so the isolated-project
commands work as written:

```bash
COMPOSE_PROJECT_NAME=p2ws3 make up-app      # stack healthy, order-svc healthy
COMPOSE_PROJECT_NAME=p2ws3 docker compose -f deploy/docker-compose.yml ps
COMPOSE_PROJECT_NAME=p2ws3 make down         # never `down -v`
```

Verified on 2026-10-01: `make up-app` brought the whole `app` profile up with
`p2ws3-order-1` healthy; `order_db` is at schema version **6** (migrations 5
`p2_payment` and 6 `processed_events` applied) with the P2 columns, CHECKs and
partial indexes present; with `docker compose -p p2ws3 stop payment` followed
by `restart order`, order-svc came back **healthy** — `/readyz` does not depend
on payment-svc. The stack was torn down with `COMPOSE_PROJECT_NAME=p2ws3 make
down` (no `-v`); no `p2ws3-*` container remained.
