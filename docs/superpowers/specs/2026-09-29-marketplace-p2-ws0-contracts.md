# WS0': P2 Contracts and Foundation

> Overview: `2026-09-29-marketplace-p2-overview.md`. WS0' merges first. WS1'–WS5' rely only on what this spec defines. WS0' is the **only** P2 workstream that edits the shared files (§8–§10).

## 1. Goal

WS0' fixes every P2 interface so that payment, ledger, order, gateway and web can be built in parallel:
- the REST contracts (new and changed);
- the gRPC contracts;
- the event catalogue and its schemas;
- the shared `pkg/` additions;
- two runnable service skeletons;
- local infrastructure, the Makefile and CI.

WS0' doesn't touch `services/{identity,catalog,order,gateway}/` or `web/`.

## 2. Changes at a glance

| Area | New | Changed |
|---|---|---|
| `api/openapi/` | `payment.yaml`, `ledger.yaml` | `order.yaml` (checkout method, order payment fields, new status), `common.yaml` (error code) |
| `api/events/` | 8 schemas (§5) | `order-created`, `order-cancelled`, `order-status-changed` (additive) |
| `proto/` | `order/v1/order.proto`, `payment/v1/payment.proto` | — |
| `gen/` | generated code for both protos | — |
| `pkg/` | `event` structs and constants for the new types, `inbox`, `idemkey` | — |
| `services/` | `payment/`, `ledger/` skeletons | — |
| `deploy/` | `db-init` job, topics, 2 services, `secrets/payment.env.example` | `docker-compose.yml`, `postgres/init.sql` |
| root | — | `go.work`, `go.work.sum`, `Makefile`, `.github/workflows/ci.yml`, `.gitignore` |

## 3. REST contracts

Everything here extends P1 WS0 §4. The **Auth** column uses `–` for public, `U` for a logged-in user and `S` for the `seller` role. The gateway routes are in the WS4' spec.

### 3.1 `order.yaml` changes
- `POST /api/checkout`: `payment_method` becomes the enum `COD | VNPAY | STRIPE`, still required.
  - A method missing from order-svc's `ONLINE_PAYMENT_METHODS` answers 400 `VALIDATION_ERROR`, with `details.fields.payment_method`.
  - Online orders are returned with `status: AWAITING_PAYMENT`.
- The `Order` status enum gains `AWAITING_PAYMENT`.
- `Order` gains the following fields:
  - `payment_status`: `NONE | UNPAID | PAID | REFUND_PENDING | REFUNDED` (`NONE` for COD).
  - `payment_deadline`: RFC 3339, or null for COD.
  - `commission_rate_bps`: integer, 0 for COD.
  - `commission_amount`: `Money`, 0 for COD.
  - `delivered_at`: RFC 3339 or null.
- `POST /api/orders/{id}/cancel` is also valid in `AWAITING_PAYMENT`. The response is the requested order. The other `AWAITING_PAYMENT` orders of the same checkout are cancelled too (WS3' §3), and the description says so.
- `GET /api/orders?status=` accepts `AWAITING_PAYMENT`.

### 3.2 `payment.yaml` (new; served by payment-svc)
| Method & path | Auth | Body → Response |
|---|---|---|
| `POST /api/payments` | U | header `Idempotency-Key` (1–100); `{checkout_id}` → 201 `Payment` · 404 (unknown checkout, or not the caller's) · 409 `INVALID_STATE_TRANSITION` (COD, no order `AWAITING_PAYMENT`, deadline passed) · 422 `IDEMPOTENCY_KEY_REUSED` · 503 |
| `GET /api/payments/{id}` | U | → `Payment` (the buyer's own; otherwise 404) |
| `GET /api/payments/vnpay/ipn` | – | VNPay query parameters → 200 `{"RspCode","Message"}` (always 200; the result is carried in `RspCode`, see WS1' §5.1) |
| `GET /api/payments/vnpay/return` | – | VNPay query parameters → 302 `Location: /checkout/result?payment_id=…` |
| `POST /api/payments/stripe/webhook` | – | raw Stripe event, header `Stripe-Signature` → 200 · 400 (bad signature) |
| `GET /api/payments/stripe/return` | – | `?session_id=` → 302 `Location: /checkout/result?payment_id=…` |
| `GET /api/payments/fake/checkout/{id}` | – | → 200 `text/html` simulated hosted page |
| `POST /api/payments/fake/checkout/{id}` | – | form `outcome=succeed\|fail` → 302 to `fake/return` |
| `GET /api/payments/fake/return` | – | `?payment_id=` → 302 `Location: /checkout/result?payment_id=…` |
| `POST /api/payments/fake/webhook` | – | FAKE event JSON, header `X-Fake-Signature` → 200 · 400 |

Schemas:
- `Payment {id, checkout_id, method: VNPAY|STRIPE, provider: vnpay|stripe|fake, status: PENDING|SUCCEEDED|FAILED|EXPIRED|DUPLICATE, amount: Money, redirect_url: string|null, expires_at, failure_reason: string|null, created_at, updated_at}`.
  - `redirect_url` is null once the payment is terminal.
  - `DUPLICATE` is a successful payment that isn't the checkout's capturing payment, and it is refunded automatically.
- Provider endpoints carry `x-provider-callback: true`, and their `security` is `[]`.
  - Unlike other public operations (P1 finding F2), they document **no 401**. The gateway ignores an invalid `Authorization` header on these prefixes (WS4' §2 rule 4).

### 3.3 `ledger.yaml` (new; served by ledger-svc)
| Method & path | Auth | Body → Response |
|---|---|---|
| `GET /api/seller/wallet` | S | → `Wallet` |
| `GET /api/seller/wallet/entries` | S | `limit, cursor` → page of `WalletEntry` (newest first) |
| `POST /api/seller/payouts` | S | header `Idempotency-Key`; `{amount}` → 201 `Payout` · 400 (`amount < PAYOUT_MIN_AMOUNT`) · 409 `INSUFFICIENT_FUNDS` (`details {available}`) · 422 |
| `GET /api/seller/payouts` | S | `status, limit, cursor` → page of `Payout` |

Schemas:
- `Wallet {available: Money, pending_payout: Money, in_escrow: Money, currency: "VND"}`
- `WalletEntry {id, transaction_id, kind: RELEASE|PAYOUT_REQUEST|PAYOUT_FAIL, amount, order_id|null, payout_id|null, created_at}`. `amount` is signed from the seller's view: a release is positive and a payout request is negative.
- `Payout {id, amount, status: REQUESTED|SETTLED|FAILED, failure_reason|null, requested_at, settled_at|null}`.

### 3.4 `common.yaml`
Adds the error code `INSUFFICIENT_FUNDS` (409). A `details` schema for it is `{available: Money}`.

## 4. gRPC contracts (`proto/`, generated into `gen/`)

```proto
// proto/order/v1/order.proto
service OrderInternal {
  rpc GetCheckout(GetCheckoutRequest) returns (GetCheckoutResponse); // NOT_FOUND if unknown
}
message GetCheckoutRequest { string checkout_id = 1; }
message GetCheckoutResponse {
  string checkout_id = 1; string buyer_id = 2;
  string payment_method = 3;                        // COD | VNPAY | STRIPE
  google.protobuf.Timestamp payment_deadline = 4;   // unset for COD
  repeated CheckoutOrder orders = 5;                // ordered by order_id
}
message CheckoutOrder {
  string order_id = 1; string seller_user_id = 2; string status = 3;
  int64 subtotal = 4; int64 total = 5; int64 commission_amount = 6;
}

// proto/payment/v1/payment.proto
service PaymentInternal {
  // Makes sure no attempt of the checkout can still succeed, then reports whether one did.
  // Idempotent. UNAVAILABLE when a provider can't be asked; the caller retries later.
  rpc CancelCheckoutPayments(CancelCheckoutPaymentsRequest) returns (CancelCheckoutPaymentsResponse);
}
message CancelCheckoutPaymentsRequest { string checkout_id = 1; }
message CancelCheckoutPaymentsResponse { Outcome outcome = 1; }
enum Outcome { OUTCOME_UNSPECIFIED = 0; OUTCOME_NOT_PAID = 1; OUTCOME_PAID = 2; }
```

ledger-svc exposes only the standard gRPC health service on 9085 in P2. There is no `ledger/v1` proto yet.

## 5. Event catalogue (`api/events/`, `pkg/event`)

The P1 envelope (`event_id, event_type, version, occurred_at, data`) and the `traceparent` Kafka header are unchanged. New fields on existing events are **optional** and the version stays `1`. This is safe because every consumer decodes with lenient `encoding/json`, and catalog skips event types it doesn't handle (`services/catalog/internal/adapter/kafka/consumer.go`). The schemas are updated to allow the new properties.

| Topic (key) | event_type | data | Producer | Consumers (effect) | Consumer idempotency |
|---|---|---|---|---|---|
| `order.events` (order_id) | `OrderCreated` | P1 fields + `payment_method, commission_rate_bps, commission_amount` | order | — | — |
| | `OrderStatusChanged` | P1 fields; `from`/`to` may be `AWAITING_PAYMENT` | order | — | — |
| | `OrderCancelled` | P1 fields + `checkout_id, payment_method` | order | catalog (release stock, P1); payment (record cancellation, refund if captured) | `processed_events` per service |
| | **`OrderCompleted`** | `order_id, checkout_id, seller_user_id, payment_method, total, commission_amount, completed_by: buyer\|system` | order | ledger (release escrow; ignored for COD) | ledger key `release:{order_id}` |
| `payment.events` (checkout_id) | **`PaymentSucceeded`** | `payment_id, checkout_id, buyer_id, provider, provider_ref, amount, allocations: [{order_id, seller_user_id, amount, commission_amount}]` (empty for a duplicate) | payment | order (orders → `PENDING_CONFIRMATION`, `PAID`); ledger (pay + capture) | order: `processed_events`; ledger: `pay:{payment_id}` |
| | **`PaymentFailed`** | `payment_id, checkout_id, provider, reason` | payment | — (observability; P3) | — |
| | **`PaymentRefunded`** | `refund_id, payment_id, checkout_id, order_id\|null, amount, provider` (`order_id` is null for a duplicate refund) | payment | order (`REFUNDED`); ledger (reverse escrow or suspense) | order: `processed_events`; ledger: `refund:{refund_id}` |
| | **`RefundFailed`** | `refund_id, payment_id, checkout_id, order_id\|null, amount, provider, reason` | payment | — (manual follow-up; P3 alerts) | — |
| `payout.events` (seller_user_id) | **`PayoutRequested`** | `payout_id, seller_user_id, amount` | ledger | — (P3) | — |
| | **`PayoutSettled`** | `payout_id, seller_user_id, amount, settled_at` | ledger | — | — |
| | **`PayoutFailed`** | `payout_id, seller_user_id, amount, reason` | ledger | — | — |

**P1-era `OrderCancelled` events.** `checkout_id` and `payment_method` are absent on events produced before WS3' (P1 order-svc). New consumer groups start at the earliest offset, so payment-svc may replay retained P1 cancellations. Consumers treat a missing `payment_method` as COD and skip the event.

New schema files:
- `order-completed.schema.json`
- `payment-succeeded.schema.json`, `payment-failed.schema.json`, `payment-refunded.schema.json`
- `refund-failed.schema.json`
- `payout-requested.schema.json`, `payout-settled.schema.json`, `payout-failed.schema.json`

All amounts are integers ≥ 0, and all IDs are UUIDs. `provider` is `vnpay | stripe | fake`.

Consumer groups: `order-svc` (`payment.events`), `payment-svc` (`order.events`), `ledger-svc` (`order.events`, `payment.events`). The P1 group `catalog-svc` is unchanged.

## 6. Shared libraries (`pkg/`)

| Package | Addition |
|---|---|
| `event` | Structs and `Type…` constants for every event in §5, plus the optional new fields on the P1 structs. The schema tests cover every new file. |
| `inbox` (new) | `SchemaSQL` for `processed_events(consumer text, event_id uuid, processed_at, primary key(consumer, event_id))`, and `MarkProcessed(ctx, tx, consumer, eventID) (firstTime bool, err error)`. This is the P1 catalog pattern made generic. catalog keeps its own table. |
| `idemkey` (new) | The `Idempotency-Key` store used by payment and ledger: `SchemaSQL` for `idempotency_keys(scope, user_id, key, request_hash, response jsonb, status_code, created_at)`, plus `Claim` / `Complete` / `Release` / `Purge`. It has the same semantics as order-svc's checkout keys (P1 WS3 §4 step 1, README decision 4) and includes the P1.5 reclaim of rows orphaned by a crash. order-svc may adopt it later, but isn't required to in P2. |

## 7. Service skeletons

`services/payment/` and `services/ledger/` follow P1 WS0 §8:
- `cmd/server/main.go` with the `healthcheck` subcommand;
- empty `internal/{domain,app,adapter}`;
- a no-op baseline migration `000001_init`;
- `sqlc.yaml`, a distroless `Dockerfile` and `go.mod` with the `pkg`/`gen` `replace` directives.

`/readyz` checks Postgres and Kafka. The ledger skeleton also reserves the `trial-balance` subcommand name; WS2' implements it. After WS0' merges, each directory belongs to its workstream.

## 8. Local infrastructure (`deploy/`)

**Databases.** `postgres/init.sql` adds `payment_db` and `ledger_db`, but init scripts only run on an **empty** volume, and the `pgdata` volume is shared across worktrees and existing clones.
- So WS0' adds a one-shot **`db-init`** service (`postgres:17` image, `depends_on: postgres healthy`).
- It runs, for each of the five databases: `SELECT 'CREATE DATABASE <db>' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '<db>')\gexec`.
- Every Go service `depends_on: db-init: service_completed_successfully`.

**Kafka.** `kafka-init` also creates `payment.events` and `payout.events` (`--if-not-exists`, 3 partitions, RF 1).

**Services** (profile `app`). Neither service publishes a port. payment and order call each other over gRPC but don't `depends_on` each other: clients dial lazily, and neither makes the other a readiness dependency (P1 rule, order README decision 13).

| Service | Environment (defaults; values in `${VAR:-default}` form so e2e can shorten timers) |
|---|---|
| payment | `SERVICE_NAME=payment`, `HTTP_ADDR=:8084`, `GRPC_ADDR=:9084`, `DATABASE_URL=…/payment_db`, `KAFKA_BROKERS`, `ORDER_GRPC_ADDR=order:9083`, `PUBLIC_BASE_URL=http://localhost:3000`, `PROVIDER_VNPAY=${PROVIDER_VNPAY:-fake}`, `PROVIDER_STRIPE=${PROVIDER_STRIPE:-fake}`, `FAKE_WEBHOOK_URL=http://gateway:8080/api/payments/fake/webhook`, `FAKE_WEBHOOK_SECRET=dev-fake-secret`, `FAKE_WEBHOOK_DELAY=${FAKE_WEBHOOK_DELAY:-500ms}`, `RECONCILE_INTERVAL=${RECONCILE_INTERVAL:-60s}`, `RECONCILE_MIN_AGE=${RECONCILE_MIN_AGE:-2m}`, `REFUND_MAX_ATTEMPTS=8`; `env_file: {path: ./secrets/payment.env, required: false}` for `VNPAY_TMN_CODE`, `VNPAY_HASH_SECRET`, `VNPAY_PAY_URL`, `VNPAY_API_URL`, `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET`, `STRIPE_MIN_AMOUNT` |
| ledger | `SERVICE_NAME=ledger`, `HTTP_ADDR=:8085`, `GRPC_ADDR=:9085`, `DATABASE_URL=…/ledger_db`, `KAFKA_BROKERS`, `PAYOUT_MIN_AMOUNT=50000`, `PAYOUT_SETTLE_INTERVAL=${PAYOUT_SETTLE_INTERVAL:-30s}`, `PAYOUT_SETTLE_DELAY=${PAYOUT_SETTLE_DELAY:-1m}`, `PENDING_POSTING_INTERVAL=10s` |
| order (added vars) | `PAYMENT_GRPC_ADDR=payment:9084`, `ONLINE_PAYMENT_METHODS=VNPAY,STRIPE`, `COMMISSION_RATE_BPS=500`, `PAYMENT_TIMEOUT=${PAYMENT_TIMEOUT:-30m}`, `AUTO_COMPLETE_AFTER=${AUTO_COMPLETE_AFTER:-72h}`, `SWEEPER_INTERVAL=${SWEEPER_INTERVAL:-60s}` |
| gateway (added vars) | `PAYMENT_URL=http://payment:8084`, `LEDGER_URL=http://ledger:8085` |

**Secrets.** `deploy/secrets/payment.env.example` lists every provider variable with a comment and the sandbox URLs:
- `VNPAY_PAY_URL=https://sandbox.vnpayment.vn/paymentv2/vpcpay.html`
- `VNPAY_API_URL=https://sandbox.vnpayment.vn/merchant_webapi/api/transaction`

Setting `PROVIDER_VNPAY=vnpay` or `PROVIDER_STRIPE=stripe` in the shell (or in `.env`) switches that method to the real sandbox.

## 9. Workspace, Makefile, CI, gitignore

- **`go.work`:** `use ./services/payment ./services/ledger`, and commit the updated `go.work.sum`.
- **`Makefile`:**
  - `GO_MODULES` adds both services, so `lint`, `test`, `test-short` and `tidy` cover them.
  - New target `stripe-listen`: `stripe listen --forward-to localhost:8080/api/payments/stripe/webhook`. It prints the `whsec_…` value to copy into `deploy/secrets/payment.env`.
  - `seed` and `e2e` stay as they are; WS6' edits them after WS1'–WS5' merge.
- **CI:** the Go matrix adds `services/payment` and `services/ledger`. `buf breaking` runs against `main` as before. The `e2e` job is WS6''s.
- **`.gitignore`:** replace `deploy/secrets/` with `deploy/secrets/*` plus `!deploy/secrets/*.example`. Git can't re-include a file inside an excluded directory, so the example file would otherwise stay ignored.

## 10. Ownership after WS0' merges

WS0' keeps editing rights on every file listed in §2 for the rest of P2. If a workstream needs a change there, WS0' makes it in a small follow-up PR to `main`, and the other workstreams then rebase. This is the rule that keeps parallel branches from ever editing the same file. The environment in §8 is meant to be complete, so that no such PR is needed.

## 11. Acceptance criteria

- [x] `make gen` is clean. `buf lint`, `buf breaking` (against `main`) and `redocly lint` pass for all six OpenAPI files.
- [x] `pkg/event` tests encode every new event and validate it against its schema. P1 payloads without the new fields still validate.
- [x] `pkg/inbox` and `pkg/idemkey` have Postgres container tests. They cover first/duplicate `MarkProcessed`, and Claim → Complete → replay, different hash → reused, and reclaiming an orphaned row.
- [x] On an **existing** P1 volume, `make up-app` creates `payment_db` and `ledger_db`, and all six services report `/readyz` 200.
- [x] On a fresh volume (`COMPOSE_PROJECT_NAME=p2check`), the same holds.
- [x] `kafka-topics --list` shows `order.events`, `payment.events` and `payout.events`.
- [x] `git check-ignore deploy/secrets/payment.env` succeeds, and `deploy/secrets/payment.env.example` is tracked.
- [x] `make lint && make test` are green, and CI is green with the new matrix entries (PR #2, all 12 jobs).

## 12. Gaps filled by WS0' (2026-09-29)

Shared-file changes WS0' made beyond the original scope, and the items it handed to other workstreams.

- **Optional `Order` fields.** The new `Order` fields and `OrderShop.slug` are optional in `order.yaml` (user decision 2026-09-29), because the unchanged order-svc validates every response against it. WS3' adds them to `required` in its implementing PR. This is the only contract edit allowed outside WS0'. Web's lint runs `gen:api:check`, so WS3''s PR that flips `required` also commits the regenerated `web/src/api/gen/order.ts` (`cd web && npm run gen:api`). That is generated output only, an explicit exception like the flip itself. Whichever of WS3'/WS5' merges second re-runs `npm run gen:api` on rebase.
- **`Order.shop.slug`.** P1.5 did not deliver it (overview §7). WS0' adds it to the contract, and WS3' implements it: order-svc must snapshot the shop slug at checkout, which needs a migration.
- **`web/` fallout.** `web/src/api/gen` was regenerated, `gen-api.mjs` includes payment and ledger, and there is one new entry each for `AWAITING_PAYMENT` (status label and tone) and `INSUFFICIENT_FUNDS` (error message), because web lint runs `gen:api:check` and `tsc`. The fallout also touched `web/src/api/types.test.ts` and `web/src/api/errors.test.ts`, because they pin the contract (the OrderStatus union and the ERROR_MESSAGES count). WS5' owns everything else.
- **Gateway readiness.** The gateway `depends_on` payment and ledger (healthy), as it does for the P1 upstreams.
- **Envelope schema.** `event_type` lists every P2 type, and the description covers all three topics.
- **§7 items P1.5 did not cover.** P1.5 (PR #1) fixed the orphaned idempotency rows, `CartShop.shipping_fee`, the seller PATCH stock overwrite and the first CI e2e run. The rest now have owners:
  - the `WithoutCancel` checkout deadline and the Tx B race code: WS3' (inside `services/order`);
  - `Order.shop.slug`: contract here, implementation WS3';
  - the reserve-after-release tombstone: stays a documented limitation (catalog README decision 6; catalog has no P2 workstream);
  - seed resume: WS6'.
- **`createPayment` 409 `details.reason` set.** WS1' §5 named only `ALREADY_PAID` and `BELOW_PROVIDER_MINIMUM`. The contract now pins the full set: `COD_CHECKOUT`, `NOT_AWAITING_PAYMENT`, `DEADLINE_PASSED`, `ALREADY_PAID`, `BELOW_PROVIDER_MINIMUM` (in `payment.yaml` and the `Error.details` description in `common.yaml`). WS5' maps these reasons to messages.
- **FAKE webhook body and signature.** They follow WS1' §5.3: body `{event_id, payment_id, outcome}`, header `X-Fake-Signature: t=<unix>,v1=<hex>`. `outcome` is left an unconstrained string in the contract.
- **`STRIPE_MIN_AMOUNT` example value.** `deploy/secrets/payment.env.example` sets `STRIPE_MIN_AMOUNT=10000` as an example. WS1' gives no default, so WS1' confirms the value.
- **`go.work.sum`.** Workspace-mode builds in WS1'–WS5' may rewrite `go.work.sum`. Conflicts there are resolved by running `go work sync` on rebase and do not need a WS0' PR.
