# P2 WS3' order-svc Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend `services/order` for P2 online payment. The work covers:
- online payment methods at checkout, with a commission snapshot;
- the `AWAITING_PAYMENT` state and a payment timeout;
- a `payment.events` consumer;
- setting `REFUND_PENDING` when a paid order is cancelled;
- auto-complete after delivery, and `OrderCompleted`;
- the `OrderInternal.GetCheckout` gRPC server;
- the P1.5 leftovers assigned to WS3'.

COD and every P1 idempotency and sweeper behaviour stay unchanged.

**Architecture:** Hexagonal, as in P1.
- **`domain`:** pure rules. They cover the transition table, `Commission()`, the side effects of `Apply` on `payment_status` and `delivered_at`, and the status-independent payment moves.
- **`app`:** the use cases (checkout, act, sweeper, payment event handler, checkout query) behind port interfaces.
- **adapters:** postgres (sqlc), payment gRPC client plus a bufconn fake, the kafka consumer, and the gRPC server.
- Every state change still goes through `applyTransition`, which writes history and outbox events under the optimistic lock.

**Tech Stack:** Go 1.27, pgx/v5 + sqlc v1.31.1, golang-migrate (`pgxx.Migrate`), franz-go (`pkg/kafkax`), gRPC (`gen/order/v1`, `gen/payment/v1`), `pkg/inbox`, testcontainers (`pkg/testutil`), testify.

**Spec:** `docs/superpowers/specs/2026-09-29-marketplace-p2-ws3-order.md`. Also read:
- overview `2026-09-29-marketplace-p2-overview.md` §4 and §7;
- WS0' `2026-09-29-marketplace-p2-ws0-contracts.md` §3.1, §4, §5, §8 and §12;
- P1 spec `2026-09-28-marketplace-p1-ws3-order.md`;
- `services/order/README.md` (decisions 1–17).

## Global Constraints

- Edit only `services/order/**`. The exceptions: Task 11 edits the `required` lists in `api/openapi/order.yaml` and commits the regenerated `web/src/api/gen/order.ts` (WS0' §12). Everything else in `api/`, `proto/`, `gen/`, `pkg/`, `deploy/`, `Makefile`, `go.work*` and `.github/` is frozen. If a contract looks wrong, **stop and report to the user**. Don't edit it.
- Money is VND `int64`. `commission_amount = floor(subtotal × COMMISSION_RATE_BPS / 10000)`, computed once at checkout, never recomputed. It is 0 for COD.
- Environment (WS0' §8, defaults):
  - `PAYMENT_GRPC_ADDR` (compose `payment:9084`);
  - `ONLINE_PAYMENT_METHODS=VNPAY,STRIPE`;
  - `COMMISSION_RATE_BPS=500`;
  - `PAYMENT_TIMEOUT=30m`;
  - `AUTO_COMPLETE_AFTER=72h`;
  - `SWEEPER_INTERVAL=60s`.
- `/readyz` must not depend on payment-svc. The payment client dials lazily (`grpcx.Dial`).
- Consumer group `order-svc` on topic `payment.events`. `inbox.MarkProcessed(consumer="order-svc")` runs in the same tx as the effect.
- `PaymentInternal.CancelCheckoutPayments`: 3 s timeout, **no retry within a tick**, called **before** locking any row.
- Sweeper steps use `FOR UPDATE SKIP LOCKED` in batches of 50.
- `payment_status` moves only `UNPAID→PAID`, `PAID→REFUND_PENDING`, `REFUND_PENDING→REFUNDED`, plus the spec's two special cases:
  - a late success on a `CANCELLED`+`UNPAID` order → `REFUND_PENDING`;
  - an out-of-order refund `PAID→REFUNDED`.
- `payment_status` moves never change `status`.
- Every completion (buyer or system, COD or online) writes exactly one `OrderCompleted`.
- `OrderCancelled` now carries `checkout_id` and `payment_method`.
- All repo text (code, comments, README, commit messages) is in English.
- Tooling:
  - lint with `../E-Commerce/bin/golangci-lint run ./...` from `services/order`;
  - sqlc with `../../../E-Commerce/bin/sqlc generate` from `services/order`, or `make gen-sqlc` from the worktree root;
  - compose runs need `COMPOSE_PROJECT_NAME=p2ws3`. **Never `down -v`.**
- **GateGuard hook:** the first Bash command and the first Write of each file in a session is blocked until you state (1) the user request in one sentence and (2) what the command or file does. State them and retry.
- Test commands (from `services/order`): unit tests with `go test -short ./...`, full suite with `go test -race -count=1 ./...`. The full suite needs Docker.

## Review Focus

1. **A payment succeeds while the buyer is cancelling the same checkout.** The final state must be either `PENDING_CONFIRMATION`+`PAID` or `CANCELLED`+`REFUND_PENDING`, never `CANCELLED`+`UNPAID` with money captured. Pinned in Task 8: the "cancelled before success" and "optimistic-lock retry" handler tests.
2. **An online checkout replayed with the same `Idempotency-Key`** returns the byte-equivalent stored 201, with the same `payment_deadline` and status `AWAITING_PAYMENT`, even after the orders moved on. Pinned in Task 4: the "online replay" app test.
3. **A method missing from `ONLINE_PAYMENT_METHODS`** is rejected with 400 before the key is claimed, so the same key then works with COD. Pinned in Task 4: the "method not enabled" itest.
4. **A `PaymentSucceeded` whose allocations include an unknown `order_id`** (or a malformed UUID) doesn't block the partition. The known orders are still processed. Pinned in Task 8: the "unknown allocation" handler test.
5. **P1-era rows** (`payment_status NONE`, `shop_slug ''`, no commission) render valid under the flipped `required` contract, and a P1 COD order still completes with `OrderCompleted{payment_method: COD, commission_amount: 0}`. Pinned in Task 1 (defaults test) and Task 3 (COD completion event).

---

## File Structure

| File | Responsibility |
|---|---|
| `migrations/000005_p2_payment.{up,down}.sql` | New columns, relaxed CHECKs, indexes |
| `migrations/000006_processed_events.{up,down}.sql` | `processed_events` (verbatim `pkg/inbox` schema) |
| `internal/domain/status.go` | `StatusAwaitingPayment`, `PaymentStatus`, new actions |
| `internal/domain/transition.go` | Transition table additions |
| `internal/domain/order.go` | New Order fields, `Apply` side effects, `LateCapture`, `MarkRefunded`, payment-method helpers |
| `internal/domain/commission.go` | `Commission()` |
| `internal/domain/checkout.go` | `BuildOrders`: commission, `payment_status`, shop slug |
| `internal/app/ports.go` | `Payments` port, new Store and Tx methods |
| `internal/app/transition.go` | Event payloads (`OrderCancelled` fields, `OrderCompleted`, `OrderCreated` fields) |
| `internal/app/checkout.go` | Method check, `AWAITING_PAYMENT` in Tx C, overall deadline, Tx B race |
| `internal/app/orders.go` | Checkout-wide buyer cancel of `AWAITING_PAYMENT` |
| `internal/app/sweeper.go` | Payment timeout and auto-complete steps |
| `internal/app/payment_events.go` | `PaymentEventHandler` |
| `internal/app/checkout_query.go` | `CheckoutQuery` for `GetCheckout` |
| `internal/adapter/postgres/*` | New columns, queries, `MarkProcessed`, `SavePaymentStatus` |
| `internal/adapter/payment/client.go` | `PaymentInternal` client |
| `internal/adapter/payment/paymentfake/fake.go` | bufconn fake with sticky per-checkout outcomes |
| `internal/adapter/kafka/consumer.go` | `payment.events` decoding and dispatch |
| `internal/adapter/grpc/server.go` | `OrderInternal` server |
| `internal/adapter/http/{dto,checkout,errors}.go` | New fields, method enum, error mapping |
| `internal/bootstrap/bootstrap.go` | Wiring: config, payment conn, brokers, consumer, gRPC registration |
| `cmd/server/{config,main}.go` | New env vars, payment dial, gRPC registration |
| `internal/itest/*` | P2 scenarios, a scoped-app helper, payment fake, new schemas |
| `README.md` | Config, P2 layout, decisions 18+ |

---

### Task 1: Schema and persistence of the new order fields

**Files:**
- Create: `services/order/migrations/000005_p2_payment.{up,down}.sql`, `000006_processed_events.{up,down}.sql`
- Modify: `services/order/migrations/migrations_test.go`
- Modify: `services/order/internal/domain/order.go`, `internal/domain/status.go`, `internal/domain/transition_test.go`
- Modify: `services/order/internal/adapter/postgres/queries/orders.sql` (regenerate `sqlcdb/`), `convert.go`, `tx.go`
- Modify: `services/order/internal/app/ports.go` (`SavePaymentStatus` on `Tx`)
- Test: `services/order/internal/adapter/postgres/store_test.go`

**Interfaces:**
- Produces (domain):
  ```go
  type PaymentStatus string
  const (
      PaymentNone PaymentStatus = "NONE"; PaymentUnpaid PaymentStatus = "UNPAID"; PaymentPaid PaymentStatus = "PAID"
      PaymentRefundPending PaymentStatus = "REFUND_PENDING"; PaymentRefunded PaymentStatus = "REFUNDED"
  )
  // new Order fields
  ShopSlug string; PaymentStatus PaymentStatus; PaymentDeadline *time.Time
  CommissionRateBps int32; CommissionAmount int64; DeliveredAt *time.Time
  const StatusAwaitingPayment Status = "AWAITING_PAYMENT" // in Statuses right after StatusPending
  ```
- Produces (app.Tx): `SavePaymentStatus(ctx context.Context, o domain.Order, prevVersion int) error`. It writes the mutable columns with a version check and bumps the version. It adds no history row. It returns `ErrConcurrentUpdate` when the version moved on.
- `SaveTransition` also persists `payment_status`, `payment_deadline` and `delivered_at`.

- [x] **Step 1: Write the migrations**

`000005_p2_payment.up.sql`:
```sql
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
```
Before relying on the constraint names `orders_status_check` and `orders_payment_method_check` (the Postgres defaults for the column CHECKs in 000002), verify them on a migrated test DB: `SELECT conname FROM pg_constraint WHERE conrelid='orders'::regclass`. If they differ, use the real names.

`000005_p2_payment.down.sql` reverses it in the opposite order:
- drop the three indexes and the six columns;
- restore the P1 CHECKs (`status` without `AWAITING_PAYMENT`, `payment_method = 'COD'`).

A comment says that down fails if non-COD rows exist, which is intended.

`000006_processed_events.up.sql` holds exactly the bytes of `pkg/inbox/schema.sql`: copy the file. `000006_processed_events.down.sql` is `DROP TABLE IF EXISTS processed_events;`.

- [x] **Step 2: Extend the migration tests**

In `migrations_test.go`:
- add `processed_events` to the table list of `TestMigrationsApplyCleanly`;
- add `TestInboxMigrationMatchesSharedSchema`: read `000006_processed_events.up.sql` and `assert.Equal(t, inbox.SchemaSQL, string(b), "copy pkg/inbox/schema.sql verbatim")`;
- add `TestP2ColumnsDefaultForP1Rows`: insert a row with the P1 column list, as the existing `'LOST'` insert does but with `status 'PENDING_CONFIRMATION'` and a known `id`, and assert the defaults:
  ```go
  var ps, slug string; var bps int32; var amt int64; var dl, da *time.Time
  require.NoError(t, pool.QueryRow(ctx, `SELECT payment_status, shop_slug, commission_rate_bps, commission_amount, payment_deadline, delivered_at FROM orders WHERE id=$1`, id).Scan(&ps, &slug, &bps, &amt, &dl, &da))
  assert.Equal(t, "NONE", ps); assert.Equal(t, "", slug); assert.Zero(t, bps); assert.Zero(t, amt); assert.Nil(t, dl); assert.Nil(t, da)
  ```
  Also assert that `status 'AWAITING_PAYMENT'` with `payment_method 'VNPAY'` inserts, and that `payment_method 'CASH'` violates the check.

Run: `go test ./migrations/ -count=1`. Expected: FAIL before the migration files exist, PASS after.

- [x] **Step 3: Domain fields**

In `status.go`:
- add `StatusAwaitingPayment` to the consts and to `Statuses` right after `StatusPending`;
- add the `PaymentStatus` type and consts.

In `order.go`, add the six fields to `Order` with doc comments.

In `transition_test.go`, update `require.Len(t, domain.Statuses, 8, …)` and the final count to `8*6*3`. Task 2 adds the legal moves; for now every `AWAITING_PAYMENT` combination is illegal.

- [x] **Step 4: Queries**

In `queries/orders.sql`, extend `InsertOrder` with `shop_slug, payment_status, payment_deadline, commission_rate_bps, commission_amount, delivered_at` ($17–$22). Then replace `UpdateOrderStatus` with:
```sql
-- name: UpdateOrder :execrows
UPDATE orders
SET status = @status, cancel_reason = sqlc.narg(cancel_reason), payment_status = @payment_status,
    payment_deadline = sqlc.narg(payment_deadline), delivered_at = sqlc.narg(delivered_at),
    version = version + 1, updated_at = @updated_at
WHERE id = @id AND version = @version;
```
Run `../../../E-Commerce/bin/sqlc generate` from `services/order`.

- [x] **Step 5: Adapter**

- `convert.go/toDomainOrder`: map the new columns (`PaymentStatus: domain.PaymentStatus(r.PaymentStatus)`, `ShopSlug: r.ShopSlug`, `CommissionRateBps: r.CommissionRateBps`, `CommissionAmount: r.CommissionAmount`, and the two times converted with `.UTC()` when non-nil).
- `tx.go/InsertOrder`: pass the new fields.
- A shared helper does the update:
  ```go
  // update writes o's mutable columns if the stored version is still prevVersion.
  func (t *txStore) update(ctx context.Context, o domain.Order, prevVersion int) error {
      id, err := mustParseUUID(o.ID)
      if err != nil { return err }
      n, err := t.q.UpdateOrder(ctx, sqlcdb.UpdateOrderParams{
          Status: string(o.Status), CancelReason: o.CancelReason, PaymentStatus: string(o.PaymentStatus),
          PaymentDeadline: o.PaymentDeadline, DeliveredAt: o.DeliveredAt, UpdatedAt: o.UpdatedAt,
          ID: id, Version: int32(prevVersion),
      })
      if err != nil { return fmt.Errorf("update order: %w", err) }
      if n == 0 { return app.ErrConcurrentUpdate }
      return nil
  }
  func (t *txStore) SavePaymentStatus(ctx context.Context, o domain.Order, prevVersion int) error {
      return t.update(ctx, o, prevVersion)
  }
  ```
- `SaveTransition` calls `t.update` and then inserts the last history entry, as before.
- Add `SavePaymentStatus` to the `app.Tx` interface with the doc from **Interfaces**.

- [x] **Step 6: Store round-trip test**

In `store_test.go`, add `t.Run("P2 fields round-trip", …)`:
- insert an order with `ShopSlug "shop-a"`, `PaymentMethod "VNPAY"`, `PaymentStatus UNPAID`, `CommissionRateBps 500`, `CommissionAmount 1000` and `Status PENDING`;
- `GetOrder` returns those fields;
- set `PaymentDeadline`, apply a hand-built `domain.StatusChange{From: PENDING, To: AWAITING_PAYMENT, Actor: SystemActor()}` with `o.Apply`, then `SaveTransition` → the deadline round-trips at µs precision;
- set `PaymentStatus = PAID` and call `SavePaymentStatus(o, o.Version)` → `GetOrder` shows `PAID`, the version is 2, and the history length is unchanged by `SavePaymentStatus`;
- `SavePaymentStatus` with a stale version → `ErrorIs app.ErrConcurrentUpdate`.

Run: `go test ./internal/adapter/postgres/ ./migrations/ ./internal/domain/ -count=1`. Expected: PASS. Then `go build ./... && go test -short ./...`. Expected: PASS.

- [x] **Step 7: Commit**
```bash
git add services/order
git commit -m "feat(order): P2 schema — payment, commission, delivery and shop slug columns"
```

---

### Task 2: Domain rules — transitions, commission, payment moves

**Files:**
- Modify: `internal/domain/status.go`, `transition.go`, `order.go`, `checkout.go`
- Create: `internal/domain/commission.go`, `internal/domain/commission_test.go`, `internal/domain/order_test.go` (if absent)
- Test: `internal/domain/transition_test.go`, `internal/domain/checkout_test.go`

**Interfaces:**
- Produces:
  ```go
  const (ActionAwaitPayment Action = "await_payment"; ActionMarkPaid Action = "mark_paid") // appended to Actions (8 total)
  const (PaymentVNPay = "VNPAY"; PaymentStripe = "STRIPE")               // next to PaymentCOD
  const CancelPaymentTimeout = "PAYMENT_TIMEOUT"
  func IsOnline(method string) bool                                      // VNPAY | STRIPE
  func Commission(subtotal int64, rateBps int32) int64                   // floor(subtotal*rate/10000)
  func (o *Order) LateCapture(at time.Time) bool   // CANCELLED+UNPAID → REFUND_PENDING
  func (o *Order) MarkRefunded(at time.Time) bool  // REFUND_PENDING|PAID → REFUNDED
  // CheckoutParams gains CommissionRateBps int32
  ```
- The `Apply` side effects:
  - `AWAITING_PAYMENT → PENDING_CONFIRMATION` sets `PaymentStatus = PAID`;
  - `→ CANCELLED` with `PaymentStatus == PAID` sets `REFUND_PENDING`;
  - `→ DELIVERED` sets `DeliveredAt = &at`.

- [x] **Step 1: Failing transition-table test**

Extend `legal` in `TestTransitionTable` with:
```go
{domain.StatusPending, domain.ActionAwaitPayment, domain.RoleSystem}:     domain.StatusAwaitingPayment,
{domain.StatusAwaitingPayment, domain.ActionMarkPaid, domain.RoleSystem}: domain.StatusPendingConfirmation,
{domain.StatusAwaitingPayment, domain.ActionCancel, domain.RoleBuyer}:    domain.StatusCancelled,
{domain.StatusAwaitingPayment, domain.ActionCancel, domain.RoleSystem}:   domain.StatusCancelled,
{domain.StatusDelivered, domain.ActionComplete, domain.RoleSystem}:       domain.StatusCompleted,
```
Set `require.Len(t, domain.Actions, 8, …)` and `assert.Equal(t, 8*8*3, seen)`. Add:
```go
func TestSellerCannotActOnAwaitingPayment(t *testing.T) {
	for _, a := range []domain.Action{domain.ActionConfirm, domain.ActionCancel, domain.ActionShip} {
		_, err := domain.Transition(domain.Order{Status: domain.StatusAwaitingPayment}, a, domain.Actor{ID: "s", Role: domain.RoleSeller})
		assert.ErrorIs(t, err, domain.ErrInvalidTransition, a)
	}
}
```

- [x] **Step 2: Failing commission and payment-move tests**

`commission_test.go`:
```go
func TestCommission(t *testing.T) {
	for _, tc := range []struct {
		subtotal int64
		bps      int32
		want     int64
	}{
		{99_999, 500, 4_999}, {100_000, 500, 5_000}, {240_000, 500, 12_000}, {45_000, 500, 2_250},
		{1, 500, 0}, {0, 500, 0}, {123_456, 0, 0}, {123_456, 10_000, 123_456},
	} {
		assert.Equal(t, tc.want, domain.Commission(tc.subtotal, tc.bps), "%d@%d", tc.subtotal, tc.bps)
	}
}
```
`order_test.go` needs a table-driven test per rule: build `Order{Status: from, PaymentStatus: ps}`, call `Transition` and then `Apply(ch, "", at)`, and assert the fields.
- `AWAITING_PAYMENT`+`UNPAID` marked paid → `PENDING_CONFIRMATION`+`PAID`.
- `PENDING_CONFIRMATION`+`PAID` cancelled by the buyer → `CANCELLED`+`REFUND_PENDING`.
- `CONFIRMED`+`PAID` cancelled by the seller → `REFUND_PENDING`.
- `AWAITING_PAYMENT`+`UNPAID` cancelled by the system → stays `UNPAID`.
- `PENDING_CONFIRMATION`+`NONE` cancelled → stays `NONE`.
- `SHIPPING` delivered → `DeliveredAt == at`.
- `LateCapture`: `true` only for `CANCELLED`+`UNPAID` (then `PaymentStatus REFUND_PENDING` and `UpdatedAt == at`). It's `false`, with the order unchanged, for `CANCELLED`+`REFUND_PENDING`, `CANCELLED`+`NONE` and `PENDING_CONFIRMATION`+`PAID`.
- `MarkRefunded`: `true` for `REFUND_PENDING` and `PAID`; `false` for `UNPAID`, `NONE` and `REFUNDED`.
- Neither of them changes `Status` or `History`.

`checkout_test.go`, `BuildOrders`:
- `PaymentMethod "VNPAY"` with `CommissionRateBps 500` gives each order `CommissionRateBps 500`, `CommissionAmount == Commission(order.Subtotal, 500)`, `PaymentStatus UNPAID` and `ShopSlug` = the SKU's `ShopSlug`;
- COD gives `0`, `0`, `NONE`, and still the slug.

Run: `go test ./internal/domain/ -count=1`. Expected: FAIL (undefined symbols).

- [x] **Step 3: Implement**

`commission.go`:
```go
package domain

// Commission is the platform's share of an online order: floor(subtotal ×
// rateBps / 10000), computed once at checkout (overview §4). Inputs are ≥ 0.
func Commission(subtotal int64, rateBps int32) int64 {
	return subtotal * int64(rateBps) / 10_000
}
```
In `order.go`:
```go
// IsOnline reports whether method is paid through payment-svc.
func IsOnline(method string) bool { return method == PaymentVNPay || method == PaymentStripe }
```
In `Apply`, before the history append:
```go
switch {
case ch.From == StatusAwaitingPayment && ch.To == StatusPendingConfirmation:
	o.PaymentStatus = PaymentPaid
case ch.To == StatusCancelled && o.PaymentStatus == PaymentPaid:
	o.PaymentStatus = PaymentRefundPending
case ch.To == StatusDelivered:
	d := at
	o.DeliveredAt = &d
}
```
`LateCapture` and `MarkRefunded` set `PaymentStatus` and `UpdatedAt` and return `true`, or return `false` without touching `o`.

In `BuildOrders`, when building each order:
```go
o.ShopSlug = first.ShopSlug
o.PaymentStatus = PaymentNone
if IsOnline(p.PaymentMethod) {
	o.PaymentStatus = PaymentUnpaid
	o.CommissionRateBps = p.CommissionRateBps
}
// after the subtotal loop:
if IsOnline(p.PaymentMethod) {
	o.CommissionAmount = Commission(o.Subtotal, p.CommissionRateBps)
}
```
Add the table entries and actions. Update the `ActionSubmit` comment ("COD checkout reserved all stock: PENDING → PENDING_CONFIRMATION").

Run: `go test ./internal/domain/ -count=1`. Expected: PASS. `go test -short ./...` must pass: existing callers of `BuildOrders` compile unchanged, and COD is unaffected.

- [x] **Step 4: Commit** — `feat(order): P2 transition table, commission and payment-status rules`

---

### Task 3: Event payloads and API fields

**Files:**
- Modify: `internal/app/transition.go`, `internal/adapter/http/dto.go`
- Modify: `internal/itest/harness_test.go` (register `order-completed.schema.json`; add the new `orderResp` fields), `internal/itest/lifecycle_test.go`, `internal/itest/cart_checkout_test.go`
- Test: `internal/app/transition_internal_test.go`

**Interfaces:**
- `transitionEvents(o, ch)` returns the following, in this order:
  - `OrderStatusChanged` always;
  - then `OrderCancelled{OrderID, Reason, CancelledBy, CheckoutID: o.CheckoutID, PaymentMethod: o.PaymentMethod}` when `To == CANCELLED`;
  - or `OrderCompleted{OrderID, CheckoutID, SellerUserID, PaymentMethod, Total, CommissionAmount, CompletedBy: string(ch.Actor.Role)}` when `To == COMPLETED`.
- `orderCreated(o)` also sets `PaymentMethod`, `CommissionRateBps` and `CommissionAmount`.
- `orderDTO` gains the following, after `PaymentMethod`:
  ```go
  PaymentStatus     domain.PaymentStatus `json:"payment_status"`
  PaymentDeadline   *time.Time           `json:"payment_deadline"`
  CommissionRateBps int32                `json:"commission_rate_bps"`
  CommissionAmount  int64                `json:"commission_amount"`
  DeliveredAt       *time.Time           `json:"delivered_at"`
  ```
- `orderShopDTO` gains `Slug string \`json:"slug"\``.

- [x] **Step 1: Failing unit tests** (`transition_internal_test.go`)
  - A COD order (`PENDING_CONFIRMATION`→…→`DELIVERED`) completed by the buyer yields `[OrderStatusChanged, OrderCompleted]`, where the `OrderCompleted` equals `{OrderID, CheckoutID, SellerUserID, PaymentMethod: "COD", Total: o.Total, CommissionAmount: 0, CompletedBy: "buyer"}`.
  - A system completion of a VNPAY order yields `CompletedBy "system"` and the order's `CommissionAmount`.
  - A cancel of a VNPAY order yields `OrderCancelled{…, CheckoutID: o.CheckoutID, PaymentMethod: "VNPAY"}`.
  - `orderCreated` of an online order carries `PaymentMethod`, `CommissionRateBps` and `CommissionAmount`.
  - Each payload is encoded with `event.Encode` and validated against its schema in `../../../../api/events/`. Use `santhosh-tekuri/jsonschema/v6` exactly as `internal/itest/harness_test.go/newEventLog` does.

Run: `go test ./internal/app/ -run Transition -count=1`. Expected: FAIL.

- [x] **Step 2: Implement** `transition.go` and `dto.go` as in **Interfaces**. In `toOrderDTO`, map the new fields and `Shop: orderShopDTO{ID: o.ShopID, Name: o.ShopName, Slug: o.ShopSlug}`.

- [x] **Step 3: Update the itests**

COD completion now emits `OrderCompleted` (WS3' §3: "written for every completion, COD included").
- In `newEventLog`, register `event.TypeOrderCompleted: "order-completed.schema.json"`.
- In `testLifecycle`, append `event.TypeOrderCompleted` to `waitTypes`, decode the last envelope and assert `CompletedBy "buyer"`, `PaymentMethod "COD"`, `CommissionAmount 0`.
- Extend `orderResp`. The `Shop` line replaces the existing one:
  ```go
  PaymentMethod     string                          `json:"payment_method"`
  PaymentStatus     string                          `json:"payment_status"`
  PaymentDeadline   *string                         `json:"payment_deadline"`
  CommissionRateBps int32                           `json:"commission_rate_bps"`
  CommissionAmount  int64                           `json:"commission_amount"`
  DeliveredAt       *string                         `json:"delivered_at"`
  Shop              struct{ ID, Name, Slug string } `json:"shop"`
  ```
- In `testCheckoutTwoShops`, assert that each COD order has `PaymentStatus "NONE"`, `PaymentDeadline == nil`, `CommissionAmount 0` and `Shop.Slug` equal to the fake shop's slug.

This is the only P1 test assertion that changes. Task 11 records it in the README.

Run: `go test ./... -count=1`. Expected: PASS (Docker).

- [x] **Step 4: Commit** — `feat(order): OrderCompleted, P2 fields on OrderCancelled/OrderCreated and the Order API`

---

### Task 4: Online checkout — method check, commission, AWAITING_PAYMENT

**Files:**
- Modify: `internal/app/checkout.go`, `internal/app/errors.go`, `internal/adapter/http/checkout.go`, `internal/adapter/http/errors.go`
- Modify: `internal/bootstrap/bootstrap.go`, `cmd/server/config.go`, `cmd/server/config_test.go`, `cmd/server/main.go`
- Test: `internal/app/checkout_test.go`, `internal/itest/cart_checkout_test.go`, `internal/itest/harness_test.go`

**Interfaces:**
- `app.CheckoutConfig` gains:
  - `OnlineMethods []string` (the `ONLINE_PAYMENT_METHODS` values);
  - `CommissionRateBps int32`;
  - `PaymentTimeout time.Duration`.
- `app.ErrPaymentMethodNotEnabled = errors.New("payment method is not enabled")`. HTTP maps it to `httpx.ValidationError(map[string]string{"payment_method": "is not enabled"})`.
- `bootstrap.Config` gains `OnlineMethods []string`, `CommissionRateBps int32`, `PaymentTimeout time.Duration` and `AutoCompleteAfter time.Duration`. Task 7 consumes `AutoCompleteAfter`.
- `cmd/server.Config` gains:
  ```go
  OnlinePaymentMethods []string      `env:"ONLINE_PAYMENT_METHODS" envDefault:"VNPAY,STRIPE" envSeparator:","`
  CommissionRateBps    int32         `env:"COMMISSION_RATE_BPS" envDefault:"500"`
  PaymentTimeout       time.Duration `env:"PAYMENT_TIMEOUT" envDefault:"30m"`
  AutoCompleteAfter    time.Duration `env:"AUTO_COMPLETE_AFTER" envDefault:"72h"`
  ```
  `validate()`:
  - trims each method and drops empty entries (an empty list means COD only);
  - rejects any entry other than `VNPAY` or `STRIPE`;
  - rejects `COMMISSION_RATE_BPS` outside 0..10000;
  - rejects `PAYMENT_TIMEOUT <= 0` and `AUTO_COMPLETE_AFTER <= 0`.

  Normalize in a method `onlineMethods() []string` used by `main`.

- [x] **Step 1: Failing app tests** (`checkout_test.go`; reuse `newEnv`, `cartSet` and the encoder there)
  - "online checkout from two shops":
    - setup: a cart from 2 shops (120 000 × 2 and 45 000 × 1), `PaymentMethod "VNPAY"`, and a service with `OnlineMethods ["VNPAY","STRIPE"]`, `CommissionRateBps 500`, `PaymentTimeout 30m` and the test clock;
    - 2 orders `AWAITING_PAYMENT`/`UNPAID`;
    - `PaymentDeadline` equal on both and `== clock.Now().Add(30m)` (the clock doesn't advance during the call);
    - commissions 12 000 and 2 250;
    - outbox per order: `[OrderCreated, OrderStatusChanged]`, with `OrderStatusChanged{From: "PENDING", To: "AWAITING_PAYMENT"}` and `OrderCreated{PaymentMethod: "VNPAY", CommissionRateBps: 500, CommissionAmount: …}`.
  - "online replay" (Review Focus 2): the same key and body afterwards → `Response.Body` byte-equal to the first, and `fake.Calls(catalogfake.ReserveStock)` unchanged.
  - "method not enabled":
    - a service with `OnlineMethods ["VNPAY"]` and `PaymentMethod "STRIPE"` → `ErrorIs app.ErrPaymentMethodNotEnabled`;
    - `store.IdempotencyKey(buyer, key)` gives `found == false`;
    - no catalog calls.
  - "COD unchanged": COD → `PENDING_CONFIRMATION`, `PaymentStatus NONE`, `PaymentDeadline == nil`, commission 0.

Run: `go test ./internal/app/ -run TestCheckout -count=1`. Expected: FAIL.

- [x] **Step 2: Implement**
  - In `Checkout`, right after `ctx = context.WithoutCancel(ctx)` and **before** `BeginIdempotency`:
    ```go
    if domain.IsOnline(in.PaymentMethod) && !slices.Contains(s.cfg.OnlineMethods, in.PaymentMethod) {
        return Response{}, ErrPaymentMethodNotEnabled
    }
    ```
  - Pass `CommissionRateBps: s.cfg.CommissionRateBps` to `BuildOrders`.
  - In `commit`, inside the per-order loop:
    ```go
    action := domain.ActionSubmit
    if domain.IsOnline(o.PaymentMethod) {
        deadline := now.Add(s.cfg.PaymentTimeout)
        o.PaymentDeadline = &deadline
        action = domain.ActionAwaitPayment
    }
    if err := tx.Publish(ctx, o.ID, orderCreated(*o), now); err != nil { return err }
    if err := applyTransition(ctx, tx, o, action, domain.SystemActor(), "", now); err != nil { return err }
    ```
  - HTTP: change the `PaymentMethod` validate tag to `required,oneof=COD VNPAY STRIPE`. In `toAppError`, add `case errors.Is(err, app.ErrPaymentMethodNotEnabled): return httpx.ValidationError(map[string]string{"payment_method": "is not enabled"})`.
  - bootstrap passes `OnlineMethods`, `CommissionRateBps` and `PaymentTimeout` into `CheckoutConfig`. `main.go` sets the four new `bootstrap.Config` fields from `cfg`.
  - `config_test.go` checks that:
    - the defaults parse to `[VNPAY STRIPE]`, 500, 30m and 72h;
    - `ONLINE_PAYMENT_METHODS="VNPAY, CASH"` → a validate error;
    - `ONLINE_PAYMENT_METHODS=""` → valid, COD only;
    - `COMMISSION_RATE_BPS=10001` → an error.

- [x] **Step 3: itest**
  - Factor the harness config into `func baseConfig() bootstrap.Config` (Task 7 reuses it). Add `OnlineMethods: []string{"VNPAY"}`, `CommissionRateBps: 500`, `PaymentTimeout: time.Hour` and `AutoCompleteAfter: time.Hour`.
  - Add `checkoutBodyWith(method string, skuIDs ...string) map[string]any`, and make `checkoutBody` call it with `"COD"`.
  - Add `testCheckoutOnline` (2 shops, VNPAY). Assert:
    - 201, both orders `AWAITING_PAYMENT`/`UNPAID`;
    - identical non-nil `PaymentDeadline` values;
    - commissions 12 000 and 2 250;
    - `waitTypes(OrderCreated, OrderStatusChanged)`, with the decoded `OrderCreated` commission fields and `OrderStatusChanged.To == "AWAITING_PAYMENT"`.
  - Add `testCheckoutMethodNotEnabled` (Review Focus 3):
    - `STRIPE` with key `"ck-stripe"` → 400, `error.code == "VALIDATION_ERROR"`, `error.details.fields.payment_method` present;
    - then a COD checkout with the **same key** → 201.
  - Register both in `TestOrderAPI` after "checkout from two shops".

Run: `go test ./... -count=1`. Expected: PASS.

- [x] **Step 4: Commit** — `feat(order): online checkout creates AWAITING_PAYMENT orders with a commission snapshot`

---

### Task 5: Buyer cancel of an AWAITING_PAYMENT checkout

**Files:**
- Modify: `internal/app/ports.go`, `internal/app/orders.go`, `internal/adapter/postgres/queries/orders.sql` (regenerate), `internal/adapter/postgres/store.go`
- Test: `internal/app/orders_test.go`, `internal/adapter/postgres/store_test.go`, `internal/itest/lifecycle_test.go`

**Interfaces:**
- `app.Store.ListCheckoutOrders(ctx context.Context, checkoutID string) ([]domain.Order, error)`:
  - every order of the checkout, all statuses, hydrated with items and history, ordered by `id`;
  - an empty slice (not an error) for an unknown or non-UUID checkout.

  Task 9 also uses it.
- `OrderService.Act` with a buyer cancel of an `AWAITING_PAYMENT` order cancels, in one tx, the requested order plus every **other** `AWAITING_PAYMENT` order of its checkout. Each gets the same buyer actor and note, and each writes `OrderStatusChanged` + `OrderCancelled`. The return value is the requested order. On `ErrConcurrentUpdate`, the whole thing (re-read included) is retried once, as P1 does.

- [x] **Step 1: Query and store**
```sql
-- name: ListCheckoutOrders :many
SELECT * FROM orders WHERE checkout_id = @checkout_id ORDER BY id;
```
`Store.ListCheckoutOrders`: if `parseUUID` fails, return `[]domain.Order{}, nil`. Otherwise run the query and `s.hydrate(ctx, rows)`. Store test: 2 orders of one checkout and 1 of another → exactly the 2, sorted by id, with items and history loaded. An unknown ID gives an empty slice; `"x"` gives an empty slice.

- [x] **Step 2: Failing app tests** (`orders_test.go`, with a helper `awaitingCheckout(t, e, n int, deadline time.Duration) []domain.Order` in `env_test.go`, shared with Tasks 7 and 8). The helper:
  - calls `BuildOrders` for `n` shops with `PaymentMethod VNPAY` and `CommissionRateBps 500`;
  - calls `InsertOrder`;
  - sets `PaymentDeadline = clock.Now()+deadline`;
  - applies `Transition(ActionAwaitPayment)` and then `SaveTransition`;
  - reserves stock in the catalog fake for each order: the helper adds a SKU per shop to `e.fake`, so ReleaseStock can be asserted.

  The tests:
  - "buyer cancel of AWAITING_PAYMENT cancels the whole checkout":
    - `awaitingCheckout(n=3)`; order 3 is first cancelled by the system (`ActionCancel`, `SystemActor`, `PAYMENT_TIMEOUT`) directly via the store, so it is already `CANCELLED`;
    - the buyer `Act(cancel, note "changed my mind")` on order 1;
    - orders 1 and 2 are `CANCELLED` with `CancelReason "changed my mind"`, and their last history entry has `ActorRole buyer`;
    - order 3's version is unchanged;
    - outbox of 1 and 2 ends `OrderStatusChanged, OrderCancelled`, and the `OrderCancelled` has `PaymentMethod "VNPAY"` and `CheckoutID`.
  - "seller cannot confirm or cancel AWAITING_PAYMENT": `Act` as the seller with `confirm` and `cancel` → `ErrorIs domain.ErrInvalidTransition`.
  - "buyer cancel of PENDING_CONFIRMATION stays per order": two COD orders of one checkout, both `PENDING_CONFIRMATION`; cancelling one leaves the other `PENDING_CONFIRMATION`.

Run: `go test ./internal/app/ -run TestOrder -count=1`. Expected: FAIL.

- [x] **Step 3: Implement** in `Act`, inside the existing `for range 2` loop, after `canAct`:
```go
var siblings []domain.Order
if in.Action == domain.ActionCancel && in.Actor.Role == domain.RoleBuyer && o.Status == domain.StatusAwaitingPayment {
	all, err := s.store.ListCheckoutOrders(ctx, o.CheckoutID)
	if err != nil {
		return domain.Order{}, err
	}
	for _, x := range all {
		if x.ID != o.ID && x.Status == domain.StatusAwaitingPayment {
			siblings = append(siblings, x)
		}
	}
}
err = s.store.InTx(ctx, func(tx Tx) error {
	now := s.now()
	if err := applyTransition(ctx, tx, &o, in.Action, in.Actor, in.Note, now); err != nil {
		return err
	}
	for i := range siblings {
		if err := applyTransition(ctx, tx, &siblings[i], in.Action, in.Actor, in.Note, now); err != nil {
			return err
		}
	}
	return nil
})
```
Stock is released asynchronously by catalog on `OrderCancelled`, as for P1 buyer cancels.

- [x] **Step 4: itest**
  - `testBuyerCancelsAwaitingCheckout`:
    - an online checkout from 2 shops;
    - `POST /api/orders/{first}/cancel {"reason":"no longer needed"}` → 200 with the first order `CANCELLED`;
    - `GET` the second → `CANCELLED`;
    - `waitTypes` on both ends with `OrderCancelled`, and the decoded payload has `PaymentMethod "VNPAY"` and `CheckoutID == res.CheckoutID`.
  - `testSellerCannotActOnAwaitingPayment`:
    - seller confirm and seller cancel of an `AWAITING_PAYMENT` order → 409 with `error.code == "INVALID_STATE_TRANSITION"`;
    - `GET /api/seller/orders?status=AWAITING_PAYMENT` contains the order.
  - Register both.

Run: `go test ./... -count=1`. Expected: PASS.

- [x] **Step 5: Commit** — `feat(order): buyer cancel of an unpaid checkout cancels all of its orders`

---

### Task 6: PaymentInternal client and fake

**Files:**
- Create: `internal/adapter/payment/client.go`, `client_test.go`, `internal/adapter/payment/paymentfake/fake.go`, `fake_test.go`
- Modify: `internal/app/ports.go`, `internal/app/errors.go`, `internal/bootstrap/bootstrap.go`, `internal/bootstrap/bootstrap_test.go`, `cmd/server/config.go`, `cmd/server/config_test.go`, `cmd/server/main.go`, `internal/itest/harness_test.go`

**Interfaces:**
- `app`:
  ```go
  // PaymentOutcome is PaymentInternal.CancelCheckoutPayments' answer.
  type PaymentOutcome int
  const (
      OutcomeNotPaid PaymentOutcome = iota + 1
      OutcomePaid
  )
  // Payments is payment-svc's internal API (adapter/payment).
  type Payments interface {
      // CancelCheckoutPayments makes sure no attempt of the checkout can still
      // succeed and reports whether one did. ErrPaymentUnavailable when
      // payment-svc can't answer; the caller retries on a later tick.
      CancelCheckoutPayments(ctx context.Context, checkoutID string) (PaymentOutcome, error)
  }
  var ErrPaymentUnavailable = errors.New("payment service unavailable") // in errors.go
  ```
- `payment.New(cc grpc.ClientConnInterface, timeout time.Duration) *Client` (0 → `payment.DefaultTimeout = 3 * time.Second`), `var _ app.Payments = (*Client)(nil)`:
  - one attempt under `context.WithTimeout(ctx, timeout)`;
  - any RPC error → `fmt.Errorf("%w: %w", app.ErrPaymentUnavailable, err)`;
  - `OUTCOME_UNSPECIFIED` or an unknown value → `fmt.Errorf("%w: unexpected outcome %v", app.ErrPaymentUnavailable, o)`.
- `paymentfake`:
  ```go
  type Fake struct { paymentv1.UnimplementedPaymentInternalServer /* mu, def, outcomes, errs, delay, calls */ }
  func New() *Fake                                                  // default answer: OUTCOME_PAID
  func (f *Fake) SetDefault(o paymentv1.Outcome)
  func (f *Fake) SetOutcome(checkoutID string, o paymentv1.Outcome) // sticky; clears an error set for the checkout
  func (f *Fake) SetError(checkoutID string, c codes.Code)          // sticky
  func (f *Fake) SetDelay(d time.Duration)
  func (f *Fake) Calls(checkoutID string) int
  func (f *Fake) Dial(t testing.TB) *grpc.ClientConn               // bufconn, like catalogfake.Dial
  ```
  The default is `PAID`, so orders of an unrelated test never get timed out by the fake.
- `bootstrap.Deps` gains `PaymentConn grpc.ClientConnInterface`, and `bootstrap.Config` gains `PaymentCallTimeout time.Duration` (0 = the client default).
- `cmd/server.Config` gains `PaymentGRPCAddr string \`env:"PAYMENT_GRPC_ADDR,required,notEmpty"\``. `main` dials it with `grpcx.Dial` next to the catalog dial, with a deferred close.

- [x] **Step 1: Failing tests**

`client_test.go` (package `payment_test`, with the fake):
- `SetOutcome(c, OUTCOME_NOT_PAID)` → `OutcomeNotPaid`;
- `OUTCOME_PAID` → `OutcomePaid`;
- `SetError(c, codes.Unavailable)` → `ErrorIs app.ErrPaymentUnavailable`;
- `SetError(c, codes.Internal)` → `ErrPaymentUnavailable`;
- `SetDefault(OUTCOME_UNSPECIFIED)` → `ErrPaymentUnavailable`;
- `SetDelay(300ms)` with a client timeout of 50 ms → `ErrPaymentUnavailable`, `time.Since(start) < 250ms` and `Calls(c) == 1` (no retry).

`fake_test.go`:
- an outcome persists across calls;
- `Calls` counts per checkout;
- `SetOutcome` after `SetError` clears the error.

Run: `go test ./internal/adapter/payment/... -count=1`. Expected: FAIL.

- [x] **Step 2: Implement** the client and the fake. The fake mirrors `catalogfake`: a `bufconn.Listen(1<<20)` server, `grpc.NewServer()`, `paymentv1.RegisterPaymentInternalServer`, `t.Cleanup` stopping the server, and a client via `grpc.NewClient("passthrough:///bufnet", WithContextDialer, insecure)`, exactly as `catalogfake.Dial` does. Honour `ctx` during the delay (`select` on `ctx.Done()`).

- [x] **Step 3: Wiring**
  - bootstrap: `pay := payment.New(d.PaymentConn, cfg.PaymentCallTimeout)`, stored for the sweeper (Task 7);
  - `main` dials and passes `PaymentConn`;
  - the itest harness creates `h.pay = paymentfake.New()` and passes `PaymentConn: h.pay.Dial(t)`;
  - `bootstrap_test.go` passes a `paymentfake` conn if it calls `New`;
  - `config_test.go` sets `PAYMENT_GRPC_ADDR` wherever `CATALOG_GRPC_ADDR` is set, and adds a missing-var error case.

  `/readyz` is unchanged.

Run: `go test ./... -count=1`. Expected: PASS.

- [x] **Step 4: Commit** — `feat(order): PaymentInternal client and bufconn fake`

---

### Task 7: Sweeper — payment timeout and auto-complete

**Files:**
- Modify: `internal/app/sweeper.go`, `internal/app/ports.go`, `internal/adapter/postgres/queries/orders.sql` (regenerate), `internal/adapter/postgres/store.go`, `tx.go`, `internal/bootstrap/bootstrap.go`
- Test: `internal/app/sweeper_test.go`, `internal/adapter/postgres/store_test.go`, `internal/itest/harness_test.go`, create `internal/itest/payment_test.go`

**Interfaces:**
- `app.CheckoutCursor struct { Deadline time.Time; CheckoutID string }`.
- `app.Store.ExpiredCheckouts(ctx context.Context, now time.Time, after *CheckoutCursor, limit int) ([]CheckoutCursor, error)`:
  - distinct checkouts with an `AWAITING_PAYMENT` order whose `payment_deadline <= now`;
  - ordered by `(deadline, checkout_id)`, strictly after `after`;
  - **no row locks**.
- `app.Tx.LockAwaitingPayment(ctx context.Context, checkoutID string, now time.Time) ([]domain.Order, error)`: `FOR UPDATE SKIP LOCKED` over the checkout's `AWAITING_PAYMENT` orders with `payment_deadline <= now`, without items or history, like `ClaimStalePending`.
- `app.Tx.ClaimDueDelivered(ctx context.Context, deliveredBefore time.Time, limit int) ([]domain.Order, error)`: `FOR UPDATE SKIP LOCKED` on `status='DELIVERED' AND delivered_at <= $1`, ordered by `delivered_at, id`, limit.
- `SweeperConfig` gains:
  - `AutoCompleteAfter time.Duration` (0 = auto-complete disabled);
  - `PaidGrace time.Duration` (0 = 10 min).

  The default `BatchSize` becomes 50.
- `NewSweeper(store Store, catalog Catalog, payments Payments, cfg SweeperConfig) *Sweeper`. A nil `payments` disables the payment-timeout step.
- `RunOnce(ctx) (int, error)` runs P1 pending → payment timeout → auto-complete → idempotency purge. It returns the number of orders it cancelled or completed. Existing tests pass `nil` payments and no `AutoCompleteAfter`, so their counts are unchanged.

- [x] **Step 1: Queries and store tests**
```sql
-- name: ExpiredCheckouts :many
SELECT checkout_id, min(payment_deadline)::timestamptz AS deadline
FROM orders
WHERE status = 'AWAITING_PAYMENT' AND payment_deadline <= @now
GROUP BY checkout_id
HAVING sqlc.narg(after_deadline)::timestamptz IS NULL
    OR (min(payment_deadline), checkout_id) > (sqlc.narg(after_deadline)::timestamptz, sqlc.narg(after_checkout_id)::uuid)
ORDER BY deadline, checkout_id
LIMIT @batch_size;

-- name: LockAwaitingPayment :many
SELECT * FROM orders
WHERE checkout_id = @checkout_id AND status = 'AWAITING_PAYMENT' AND payment_deadline <= @now
ORDER BY id
FOR UPDATE SKIP LOCKED;

-- name: ClaimDueDelivered :many
SELECT * FROM orders
WHERE status = 'DELIVERED' AND delivered_at <= @delivered_before
ORDER BY delivered_at, id
LIMIT @batch_size
FOR UPDATE SKIP LOCKED;
```
Store tests:
- `ExpiredCheckouts` pages 3 expired checkouts with limit 2 and the cursor. It ignores a non-expired checkout and a `CANCELLED` one.
- `LockAwaitingPayment` skips a row locked by another open tx: `pool.Begin` → `SELECT id FROM orders WHERE id=$1 FOR UPDATE`, then assert that order is missing from the result.
- `ClaimDueDelivered` respects `delivered_at`.

- [x] **Step 2: Failing app tests** (`sweeper_test.go`, a new `TestSweeperPayments` using `newEnv`, a `paymentfake`, `payment.New(fake.Dial(t), 200*time.Millisecond)`, the clock and `awaitingCheckout` from Task 5)
  - "NOT_PAID cancels every order of the checkout and releases stock":
    - `awaitingCheckout(n=2, 30m)`, `SetOutcome(checkout, NOT_PAID)`, `clock.Advance(31m)`;
    - `RunOnce` returns 2;
    - both are `CANCELLED` with `CancelReason PAYMENT_TIMEOUT` and the last history entry has `ActorRole system`;
    - outbox ends `OrderStatusChanged, OrderCancelled`;
    - `e.fake.ReleasedOrders()` contains both;
    - `pay.Calls(checkout) == 1`.
  - "PAID leaves the orders untouched and warns after the grace":
    - `SetOutcome(PAID)`, advance 31m → `RunOnce` returns 0, both `AWAITING_PAYMENT`, nothing released, no warning;
    - advance 10m more → a warning is logged. The sweeper `Logger` is `slog.New(slog.NewTextHandler(&buf, nil))`; assert `buf` contains `"still awaiting payment"` and the checkout ID.
  - "UNAVAILABLE retries on a later tick": `SetError(checkout, codes.Unavailable)` → untouched; then `SetOutcome(NOT_PAID)` and `RunOnce` again → cancelled.
  - "not yet expired is not asked": deadline in the future → `pay.Calls(checkout) == 0`.
  - "payment is asked before any row is locked":
    - wrap `Payments` in a hook type whose `CancelCheckoutPayments` first runs, on `e.pool`, `SELECT id FROM orders WHERE id = $1 FOR UPDATE NOWAIT` inside a tx that it then rolls back, and records the error;
    - assert the recorded error is nil.
  - "auto-complete":
    - sweeper with `AutoCompleteAfter 72h`;
    - `seedOrder(..., StatusDelivered)` for a COD order (after Task 2, `Apply` sets `DeliveredAt`), and a paid online order walked to `DELIVERED`: `awaitingCheckout(1)`, then `MarkPaid`, confirm, ship and deliver via `Transition` + `Apply` + `SaveTransition`;
    - advance 71h → `RunOnce` completes nothing;
    - advance 2h → both `COMPLETED`, the last history `ActorRole system`, and the outbox ends `OrderStatusChanged, OrderCompleted` with `CompletedBy system`.
  - "auto-complete processes more than one batch": `BatchSize 2`, 5 due orders → all 5 completed in one `RunOnce`.

  The existing `TestSweeper` must call `app.NewSweeper(e.store, e.catalog, nil, …)` and stay green.

Run: `go test ./internal/app/ -run TestSweeper -count=1`. Expected: FAIL.

- [x] **Step 3: Implement** in `sweeper.go`:
```go
// timeoutPayments cancels AWAITING_PAYMENT checkouts past their deadline that
// payment-svc confirms are unpaid (WS3' §5.2). payment-svc is asked before
// any row is locked; PAID and unavailable answers leave the orders alone.
func (s *Sweeper) timeoutPayments(ctx context.Context) (int, error) {
	now := s.cfg.Now()
	var after *CheckoutCursor
	n := 0
	for {
		batch, err := s.store.ExpiredCheckouts(ctx, now, after, s.cfg.BatchSize)
		if err != nil {
			return n, err
		}
		for _, c := range batch {
			cancelled, err := s.timeoutCheckout(ctx, c, now)
			if err != nil {
				return n, err
			}
			n += cancelled
		}
		if len(batch) < s.cfg.BatchSize {
			return n, nil
		}
		after = &batch[len(batch)-1]
	}
}

func (s *Sweeper) timeoutCheckout(ctx context.Context, c CheckoutCursor, now time.Time) (int, error) {
	outcome, err := s.payments.CancelCheckoutPayments(ctx, c.CheckoutID)
	switch {
	case err != nil:
		s.cfg.Logger.WarnContext(ctx, "payment-svc unavailable; payment timeout retried next tick",
			"checkout_id", c.CheckoutID, "error", err)
		return 0, nil
	case outcome == OutcomePaid:
		if now.Sub(c.Deadline) > s.cfg.PaidGrace {
			s.cfg.Logger.WarnContext(ctx, "paid checkout still awaiting payment after its deadline",
				"checkout_id", c.CheckoutID, "deadline", c.Deadline)
		}
		return 0, nil
	}
	var cancelled []domain.Order
	err = s.store.InTx(ctx, func(tx Tx) error {
		orders, err := tx.LockAwaitingPayment(ctx, c.CheckoutID, now)
		if err != nil {
			return err
		}
		for i := range orders {
			if err := applyTransition(ctx, tx, &orders[i], domain.ActionCancel, domain.SystemActor(), domain.CancelPaymentTimeout, now); err != nil {
				return err
			}
		}
		cancelled = orders
		return nil
	})
	if errors.Is(err, ErrConcurrentUpdate) {
		return 0, nil // an order moved concurrently; the next tick re-evaluates the checkout
	}
	if err != nil {
		return 0, err
	}
	s.releaseAll(ctx, cancelled)
	return len(cancelled), nil
}
```
`autoComplete(ctx) (int, error)` mirrors the P1 pending loop:
- `ClaimDueDelivered(now.Add(-AutoCompleteAfter), BatchSize)`;
- `applyTransition(ActionComplete, SystemActor(), "", now)` for each claimed order;
- loop until a batch comes back short.

If `AutoCompleteAfter == 0`, it returns 0. In `RunOnce`, call the steps in order and sum the counts. `releaseAll` for pending cancels keeps its current place.

In bootstrap, `app.NewSweeper(store, cat, pay, app.SweeperConfig{…, AutoCompleteAfter: cfg.AutoCompleteAfter})`.

- [x] **Step 4: Scoped-app itest helper and scenarios**

In `harness_test.go`, keep `pay`, `rdb`, `producer` and `brokers` on the harness, and add:
```go
// scoped starts a second App on the same containers and fakes with the base
// config adjusted by mutate, serving HTTP and running its workers until the
// subtest ends; it shares h.events. The main App keeps running with long
// payment and auto-complete timers, so only the scoped App acts on scoped
// orders — except that the main sweeper may also time out an expired
// checkout, which is harmless: both ask the same payment fake.
func (h *harness) scoped(t *testing.T, mutate func(*bootstrap.Config)) *harness
```
It:
- builds `cfg := baseConfig()` and calls `mutate(&cfg)`;
- calls `bootstrap.New(cfg, h.deps(t))` (a `deps` method builds `Deps` from the shared pool, rdb, fakes and producer);
- starts `RunWorkers` with cancellation in `t.Cleanup` and an `httptest.NewServer(authctx.Middleware(a.API))`;
- returns a shallow copy of `h` with `app` and `baseURL` replaced.

`payment_test.go`:
- `testPaymentTimeout`, with `s := h.scoped(t, func(c *bootstrap.Config){ c.PaymentTimeout = time.Second })`:
  - an online checkout from 2 shops through `s`, then `h.pay.SetOutcome(checkout, NOT_PAID)`;
  - `require.Eventually` (10 s, 100 ms) until both are `CANCELLED` with `CancelReason "PAYMENT_TIMEOUT"`;
  - `h.fake.ReleasedOrders()` contains both, and `waitTypes` for each ends with `OrderCancelled`.
- `testPaymentTimeoutPaidUntouched`: `SetOutcome(PAID)` → `Eventually(Calls(checkout) >= 2)` → both still `AWAITING_PAYMENT`.
- `testPaymentTimeoutUnavailableThenNotPaid`: `SetError(Unavailable)` → `Eventually(Calls >= 2)`, then assert untouched → `SetOutcome(NOT_PAID)` → eventually cancelled.
- `testAutoComplete`, with `s := h.scoped(t, func(c *bootstrap.Config){ c.AutoCompleteAfter = time.Second })`:
  - a COD order via `s`, walked to `DELIVERED` through the seller routes;
  - eventually `COMPLETED`, with the last history `actor_role "system"` and `delivered_at` non-null;
  - `waitTypes` ends with `OrderCompleted`, where `CompletedBy "system"`.

Register them in `TestOrderAPI` before "every published event matches its schema".

Run: `go test ./... -count=1`. Expected: PASS.

- [x] **Step 5: Commit** — `feat(order): sweeper payment timeout and auto-complete`

---

### Task 8: payment.events consumer

**Files:**
- Create: `internal/app/payment_events.go`, `internal/app/payment_events_test.go`, `internal/adapter/kafka/consumer.go`, `internal/adapter/kafka/consumer_test.go`
- Modify: `internal/app/ports.go`, `internal/adapter/postgres/tx.go`, `internal/bootstrap/bootstrap.go` (`New` returns an error), `internal/bootstrap/bootstrap_test.go`, `cmd/server/main.go`, `internal/itest/harness_test.go`, `internal/itest/payment_test.go`

**Interfaces:**
- `app.Tx.MarkProcessed(ctx context.Context, eventID string) (firstTime bool, err error)`. The postgres implementation is `inbox.MarkProcessed(ctx, t.tx, InboxConsumer, eventID)`, with `const InboxConsumer = "order-svc"` in the postgres package.
- `app.PaymentEventHandler`:
  ```go
  func NewPaymentEventHandler(store Store, now func() time.Time, logger *slog.Logger) *PaymentEventHandler
  func (h *PaymentEventHandler) PaymentSucceeded(ctx context.Context, eventID string, ev event.PaymentSucceeded) error
  func (h *PaymentEventHandler) PaymentRefunded(ctx context.Context, eventID string, ev event.PaymentRefunded) error
  ```
  Each call:
  - makes up to 2 attempts;
  - per attempt, reads the orders with `GetOrder` (`ErrNotFound` → log a warning and skip that `order_id`);
  - runs one `InTx`: `MarkProcessed` (if it isn't the first time, return nil), then the effects;
  - retries on `ErrConcurrentUpdate`, and after the second loss returns it (kafkax retries with backoff).

  The effects:
  - **`PaymentSucceeded`, per allocation order:**
    - `AWAITING_PAYMENT` → `applyTransition(ActionMarkPaid, SystemActor(), note = ev.PaymentID)`;
    - `CANCELLED` → `LateCapture(now)`, and if it changed, `SavePaymentStatus`; no event is written;
    - anything else → log at info level.

    An empty `Allocations` makes no order change, but the event is still marked processed.
  - **`PaymentRefunded`:**
    - `OrderID == nil` → no-op, marked processed;
    - otherwise `MarkRefunded(now)`, and if it changed, `SavePaymentStatus`.
- `internal/adapter/kafka` (package `kafka`, imported as `kafkaadapter`):
  ```go
  const GroupID = "order-svc"
  type PaymentEvents interface {
      PaymentSucceeded(ctx context.Context, eventID string, ev event.PaymentSucceeded) error
      PaymentRefunded(ctx context.Context, eventID string, ev event.PaymentRefunded) error
  }
  func NewHandler(p PaymentEvents, logger *slog.Logger) *Handler
  func (h *Handler) Handle(ctx context.Context, m kafkax.Message) error
  func NewConsumer(brokers []string, h *Handler, logger *slog.Logger) (*kafkax.Consumer, error) // topic event.TopicPaymentEvents
  ```
  `Handle` decodes the envelope and dispatches the two types. Other types return nil. Undecodable envelopes, malformed data, or a non-UUID `event_id`, `checkout_id` or refund `order_id` → a warning and return nil. A non-UUID allocation `order_id` is dropped with a warning while the others are kept. Handler errors are returned.
- `bootstrap.Deps` gains `KafkaBrokers []string`. `bootstrap.New(cfg, d) (*App, error)` builds the consumer. `RunWorkers` runs the relay, the sweeper and the consumer, and closes the consumer after its `Run` returns.

- [x] **Step 1: Failing handler tests** (`payment_events_test.go`, real Postgres via `newEnv`, `awaitingCheckout` from Task 5)
  - "success moves every allocated order to PENDING_CONFIRMATION/PAID":
    - 2 orders; the last history entry has `ActorRole system` and `*Note == paymentID`;
    - outbox `OrderStatusChanged{From: "AWAITING_PAYMENT", To: "PENDING_CONFIRMATION"}`.
  - "redelivery is a no-op": the same `eventID` twice → exactly one `PENDING_CONFIRMATION` history entry per order, and the versions are unchanged by the second call.
  - "empty allocations change nothing" (a duplicate charge): the versions are unchanged, and a `processed_events` row exists for the event ID.
  - "cancelled before success → REFUND_PENDING, no events": the buyer cancels via `OrderService.Act` first, then the success arrives → `CANCELLED` + `REFUND_PENDING`, and the outbox row count is unchanged by the handler.
  - "unknown allocation is skipped" (Review Focus 4): `Allocations: [{OrderID: uid()}, {OrderID: real}]` → the real order is `PAID` and the call returns nil.
  - "other statuses are no-ops": a `CONFIRMED` order in the allocations stays `CONFIRMED`.
  - "refund marks REFUNDED":
    - a paid order is cancelled (→ `REFUND_PENDING`), then `PaymentRefunded{OrderID: &id}` → `REFUNDED`, the status stays `CANCELLED`, and the history length is unchanged;
    - an out-of-order `PAID` order → `REFUNDED`;
    - `OrderID nil` → no change.
  - "optimistic-lock retry" (Review Focus 1):
    - a `Store` wrapper (embedding `app.Store`) whose **first** `InTx` call first runs a buyer cancel of the order through a real `app.NewOrderService(e.store, …)`, then delegates;
    - the handler loses the race, re-reads, sees `CANCELLED` and applies `LateCapture`;
    - final state `CANCELLED` + `REFUND_PENDING`; the call returns nil.

Run: `go test ./internal/app/ -run TestPaymentEvent -count=1`. Expected: FAIL.

- [x] **Step 2: Implement** `payment_events.go` and `txStore.MarkProcessed`.

- [x] **Step 3: Kafka adapter tests** (`consumer_test.go`, unit, a stub `PaymentEvents` recording calls), with messages built by `event.Encode`:
  - `PaymentSucceeded` → the stub gets the envelope event ID and the decoded payload;
  - `PaymentRefunded` → dispatched;
  - `PaymentFailed`, `RefundFailed`, `PayoutSettled` → no call, nil;
  - garbage bytes, `version 2`, or a data type mismatch → nil and no call;
  - a malformed allocation `order_id` is dropped, and the valid one is passed through;
  - a non-UUID `checkout_id` → skipped;
  - a stub error is returned.

- [x] **Step 4: Wiring and itest**
  - bootstrap: `New` returns `(*App, error)`. Update `main.go` (`svc, err := bootstrap.New(...)`, passing `KafkaBrokers: cfg.KafkaBrokers`), `bootstrap_test.go` and the itest harness (and `scoped`).
  - harness: `testutil.CreateTopic(t, brokers, event.TopicPaymentEvents, 3)`, plus:
    ```go
    // publishPayment produces a payment.events envelope keyed by checkoutID; tests
    // reuse eventID to simulate redelivery.
    func (h *harness) publishPayment(t *testing.T, eventID, checkoutID string, p event.Payload) {
        t.Helper()
        env, err := event.New(p, time.Now())
        require.NoError(t, err)
        env.EventID = eventID
        b, err := json.Marshal(env)
        require.NoError(t, err)
        require.NoError(t, h.kafka.ProduceSync(ctx, &kgo.Record{Topic: event.TopicPaymentEvents, Key: []byte(checkoutID), Value: b}).FirstErr())
    }
    ```
    `h.kafka` is a `kgo.Client` created in `newHarness` and closed in cleanup.
  - `payment_test.go` scenarios (on the main app, 1 h payment timeout), with a helper `h.paySucceeded(t, eventID string, res checkoutResp)` that builds allocations `{order_id, seller_user_id, amount: total, commission_amount}` from the checkout response. It takes `seller_user_id` from the fake shop whose ID is `order.shop.id`, so the helper also takes a `map[string]catalogfake.Shop` keyed by shop ID:
    - `testPaymentSucceeded`:
      - an online checkout from 2 shops → publish → eventually both `PENDING_CONFIRMATION`/`PAID`;
      - publish the **same event ID** again and wait for a later marker: publish a second, unrelated `PaymentSucceeded` with empty allocations and a fresh ID for the same checkout key, and wait until its `processed_events` row exists (`SELECT 1 FROM processed_events WHERE event_id=$1`);
      - then each order's history has exactly one entry with `to == "PENDING_CONFIRMATION"`.
    - `testLateSuccessThenRefund`: the buyer cancels an online checkout → publish a success → eventually both `CANCELLED`/`REFUND_PENDING` → publish `PaymentRefunded{OrderID: &first}` → the first is `REFUNDED` and the second stays `REFUND_PENDING`.
    - `testPaidOrderCancelledBySeller`: pay → the seller confirms → the seller cancels with `{"reason":"out of stock"}` → `CANCELLED`/`REFUND_PENDING`, and the order's last event is `OrderCancelled` with `PaymentMethod "VNPAY"` and `CheckoutID`.

  Register them.

Run: `go test ./... -count=1`. Expected: PASS.

- [x] **Step 5: Commit** — `feat(order): consume payment.events (paid, late success, refunded)`

---

### Task 9: OrderInternal gRPC server

**Files:**
- Create: `internal/app/checkout_query.go`, `internal/app/checkout_query_test.go`, `internal/adapter/grpc/server.go`, `internal/adapter/grpc/server_test.go`
- Modify: `internal/bootstrap/bootstrap.go`, `cmd/server/main.go`, `internal/itest/harness_test.go`, `internal/itest/payment_test.go`

**Interfaces:**
- `app`:
  ```go
  // CheckoutView is a checkout as payment-svc sees it (OrderInternal.GetCheckout).
  type CheckoutView struct {
      CheckoutID, BuyerID, PaymentMethod string
      PaymentDeadline *time.Time // nil for COD
      Orders []domain.Order      // every status, ordered by order ID
  }
  func NewCheckoutQuery(store Store) *CheckoutQuery
  func (q *CheckoutQuery) GetCheckout(ctx context.Context, checkoutID string) (CheckoutView, error) // ErrNotFound if no orders
  ```
  `BuyerID` and `PaymentMethod` come from the first order. `PaymentDeadline` comes from the first order with a non-nil deadline, which is the same for every order of an online checkout.
- `grpcadapter.NewServer(q *app.CheckoutQuery, logger *slog.Logger) orderv1.OrderInternalServer` (package `grpcadapter`, directory `internal/adapter/grpc`):
  - `app.ErrNotFound`, or a `checkout_id` that isn't a canonical UUID (`uuid.Parse` fails) → `status.Error(codes.NotFound, "checkout not found")`;
  - other errors → `codes.Internal`, logged;
  - the response `CheckoutOrder{OrderId, SellerUserId, Status: string(o.Status), Subtotal, Total, CommissionAmount}`;
  - `PaymentDeadline: timestamppb.New(*d)`, left nil when it's unset.
- `bootstrap.App.RegisterGRPC(s grpc.ServiceRegistrar)` → `orderv1.RegisterOrderInternalServer(s, grpcadapter.NewServer(query, logger))`. `main.go`: `grpcSrv, _ := grpcx.NewServer(); svc.RegisterGRPC(grpcSrv)`.

- [ ] **Step 1: Failing tests**
  - `checkout_query_test.go` (Postgres):
    - `awaitingCheckout(n=2)`, then a buyer cancel of one → both returned, sorted by ID, with the view's `BuyerID`, `PaymentMethod "VNPAY"` and `PaymentDeadline` set;
    - a COD checkout → `PaymentDeadline nil`;
    - an unknown checkout → `ErrNotFound`.
  - `server_test.go`: bufconn (copy the pattern from `catalogfake.Dial`) serving `NewServer(app.NewCheckoutQuery(stubStore))`, where `stubStore` embeds `app.Store` and implements only `ListCheckoutOrders`. It checks:
    - every field for an online checkout;
    - an unset deadline for COD;
    - an unknown checkout → `codes.NotFound`;
    - `"not-a-uuid"` → `codes.NotFound`;
    - a store error → `codes.Internal`.

Run: `go test ./internal/app/ ./internal/adapter/grpc/ -count=1`. Expected: FAIL.

- [ ] **Step 2: Implement** as in **Interfaces**.

- [ ] **Step 3: itest** `testGetCheckout`:
  - `newHarness` starts a bufconn `grpc.NewServer()` with `a.RegisterGRPC(srv)` and dials `h.orderRPC = orderv1.NewOrderInternalClient(conn)`;
  - an online checkout from 2 shops → `GetCheckout` returns `CheckoutId`, `BuyerId == b.id` and `PaymentMethod "VNPAY"`;
  - `PaymentDeadline.AsTime()` equals the REST `payment_deadline` (parsed with RFC 3339);
  - 2 orders sorted by `OrderId`, whose `Subtotal`/`Total`/`CommissionAmount`/`Status`/`SellerUserId` match the REST orders and the fake shops;
  - an unknown ID → `status.Code(err) == codes.NotFound`.

Run: `go test ./... -count=1`. Expected: PASS.

- [ ] **Step 4: Commit** — `feat(order): OrderInternal.GetCheckout gRPC server`

---

### Task 10: P1.5 leftovers — checkout deadline and Tx B race code

**Files:**
- Modify: `internal/app/checkout.go`, `internal/bootstrap/bootstrap.go`
- Test: `internal/app/checkout_compensation_test.go`, `internal/app/checkout_test.go`

**Interfaces:**
- `CheckoutConfig` gains:
  - `Deadline time.Duration`: the overall bound on the forward path, measured from the key claim to Tx C (0 = none). bootstrap sets it to `cfg.PendingTimeout`, so a checkout gives up before the sweeper would cancel its orders.
  - `CleanupTimeout time.Duration`: the bound on compensation (release, Tx B, recovery, key release) (0 = 30 s).
- The forward path runs under `context.WithTimeout(context.WithoutCancel(ctx), Deadline)`: cart read, `GetSkus`, Tx A, reservations and Tx C. Compensation runs under a **fresh** `context.WithTimeout(context.WithoutCancel(ctx), CleanupTimeout)`, so it still works after the deadline fired. A run that hit the deadline behaves like any other transient failure:
  - its orders are cancelled (`RESERVE_FAILED`, or `CHECKOUT_TIMEOUT` if it failed in Tx C);
  - the key is released;
  - the error is returned (503 via `ErrCatalogUnavailable`, or a Tx C failure as today);
  - it never leaves orders `PENDING` for the sweeper.
- Tx B race: when Tx B in `abort` fails with `ErrConcurrentUpdate` (the sweeper cancelled some orders first), `abort`:
  - re-reads the orders with `GetOrder` and cancels those still `PENDING`;
  - stores the response under its claim (when storable);
  - answers the **real cause**: `OUT_OF_STOCK` with its shortages, or the transient error for `RESERVE_FAILED`, never `CONFLICT`.

  It retries once. If the second attempt also loses the race, it returns the cause without storing it, and releases the key, as for a transient failure.

- [ ] **Step 1: Failing tests**
  - `checkout_compensation_test.go`, "sweeper racing Tx B still answers OUT_OF_STOCK":
    - a catalog wrapper whose `ReserveStock` (for the second order of a 2-shop checkout) first delegates, getting the fake's shortage for a SKU set to stock 0, and then, before returning, runs `sweeper.RunOnce` with the clock advanced past `PENDING_TIMEOUT`, so the sweeper cancels both `PENDING` orders before Tx B;
    - the response is the stored OUT_OF_STOCK rendering (extend the test encoder in `checkout_test.go` so its body includes the error's type, e.g. `{"error":"*app.OutOfStockError"}`, if it doesn't already distinguish it);
    - all orders are `CANCELLED`;
    - a replay with the same key returns the same stored response.
  - `checkout_test.go`, "a checkout exceeding its deadline cancels its orders and frees the key":
    - `Deadline 200ms`, `catalog.Config{Timeout: time.Second, Retries: 0}`, `fake.SetDelay(catalogfake.ReserveStock, 500ms)`;
    - `Checkout` returns an error in under 1.5 s;
    - the buyer's orders (via `store.ListOrders`) are all `CANCELLED`;
    - `IdempotencyKey` gives `found == false`;
    - after `SetDelay(ReserveStock, 0)` and restoring the cart lines, the same key → 201.
  - "a deadline during GetSkus creates nothing": `SetDelay(GetSkus, 500ms)` → an error, the buyer has no orders, and the key is freed.

Run: `go test ./internal/app/ -run 'TestCheckout' -count=1`. Expected: FAIL.

- [ ] **Step 2: Implement.**
  - In `Checkout`, derive `base := context.WithoutCancel(ctx)` and `fwd, cancel := s.forwardCtx(base)` (with `defer cancel()`).
  - Pass `base` to `run` along with `fwd`. `run` uses `fwd` for the forward steps.
  - `abort`, `recoverCommit`, the `ErrCheckoutExpired` release in `commit`, and the `DeleteIdempotencyKey` in `Checkout` each use `cctx, done := s.cleanupCtx(base); defer done()`.
  - The claim itself (`BeginIdempotency`) uses `fwd`.

  Keep README decision 10 (request cancellation is ignored); Task 11 updates its text.

Run: `go test -race ./... -count=1`. Expected: PASS, including every P1 checkout test unchanged.

- [ ] **Step 3: Commit** — `fix(order): bound checkout duration and answer OUT_OF_STOCK when the sweeper wins Tx B`

---

### Task 11: Contract flip, generated web types, README, final verification

**Files:**
- Modify: `api/openapi/order.yaml` (**only** the `required` lists of `Order` and `OrderShop`, and the "optional until WS3' marks it required" wording in those property descriptions)
- Modify: `web/src/api/gen/order.ts` (regenerated, **never hand-edited**)
- Modify: `services/order/README.md`

- [ ] **Step 1: Flip required**
  - `Order.required` gains `payment_status, payment_deadline, commission_rate_bps, commission_amount, delivered_at`;
  - `OrderShop.required` becomes `[id, name, slug]`;
  - remove "(P2; optional until WS3' marks it required)" and keep the rest of each description (for example `NONE for COD`, `null for COD`).

Run `make lint-openapi` from the worktree root. Expected: no errors.
Run `cd services/order && go test ./internal/itest/ -count=1`. Expected: PASS, because the contract transport now enforces the new fields on every response.

- [ ] **Step 2: Regenerate web types**

`cd web && npm ci && npm run gen:api && git status --short && npm run lint && npm test`.
- If `gen:api` changes only `web/src/api/gen/order.ts` and `lint` plus `test` pass, `git add web/src/api/gen/order.ts`.
- **If `npm run lint` (its `tsc -b` step) fails** because WS5'-owned code builds `Order` objects without the new required fields (expected in `web/src/mocks/db.ts`), **STOP**. Don't edit `web/` beyond `order.ts`. Report the failing files and errors to the user and ask how to proceed.

- [ ] **Step 3: README**

Update `services/order/README.md`:
- the intro (the P2 scope and spec path);
- the Layout table: `adapter/payment`, `adapter/payment/paymentfake`, `adapter/kafka`, `adapter/grpc`, and `PaymentEventHandler`/`CheckoutQuery` in app;
- the Configuration table: `PAYMENT_GRPC_ADDR`, `ONLINE_PAYMENT_METHODS`, `COMMISSION_RATE_BPS`, `PAYMENT_TIMEOUT`, `AUTO_COMPLETE_AFTER`;
- "Checkout and sweeper": the online Tx C, the payment timeout, auto-complete, and the `payment.events` consumer;
- decision 10 (the deadline and cleanup contexts).

Add **Design decisions 18+** (P2 spec gaps resolved by WS3'):
- 18. Online `payment_status = UNPAID` is set at Tx A, while the order is still `PENDING`. A `PENDING` online order the sweeper cancels stays `UNPAID`.
- 19. `payment_deadline` is taken from the Tx C clock (the same for every order of a checkout), not from the key claim.
- 20. Buyer cancel in `AWAITING_PAYMENT` doesn't call payment-svc. `OrderCancelled` carries `payment_method`, and a success arriving later yields `REFUND_PENDING` (ADR-0007). Stock is released by the catalog consumer, as for P1 buyer cancels.
- 21. `ONLINE_PAYMENT_METHODS` is checked before the idempotency key is claimed. A disabled method answers 400 and doesn't consume the key.
- 22. The payment-timeout step pages distinct expired checkouts by `(deadline, checkout_id)` without locks. It asks payment-svc first, then locks with `SKIP LOCKED`. A lost optimistic race skips the checkout until the next tick. A paid checkout still `AWAITING_PAYMENT` 10 min past its deadline is logged at warn level every tick.
- 23. The sweeper batch size is 50 for every step (P1 used 100; spec §5).
- 24. `shop_slug` is snapshotted at checkout. P1 rows hold `''`, which is valid for the now-required `Order.shop.slug`.
- 25. COD completions emit `OrderCompleted` (spec §3). The P1 lifecycle itest was extended with that event, the only P1 test assertion that changed.
- 26. Consumer: an unknown or malformed allocation `order_id` is skipped per order. A lost optimistic race is retried once in the handler, then by kafkax backoff. The inbox row commits with the effect.
- 27. The overall checkout deadline is `PENDING_TIMEOUT`. Compensation has its own 30 s budget. When the sweeper wins the race with Tx B, the response is the real cause (overview §7).
- 28. `GetCheckout` answers `NOT_FOUND` for a malformed ID, since the contract names only `NOT_FOUND`.

Also add every other deviation recorded during Tasks 1–10.

- [ ] **Step 4: Full verification** (from the worktree root)
```bash
(cd services/order && go build ./... && go vet ./... && ../../../E-Commerce/bin/golangci-lint run ./... && go test -race -count=1 ./...)
make lint-openapi
```
Expected: all green. Then a compose smoke test with an isolated project:
```bash
COMPOSE_PROJECT_NAME=p2ws3 make up-app
docker compose -p p2ws3 -f deploy/docker-compose.yml ps
COMPOSE_PROJECT_NAME=p2ws3 make down   # never down -v
```
If the Makefile doesn't honour `COMPOSE_PROJECT_NAME`, run the equivalent `docker compose -p p2ws3 … --profile app up -d --build` and note it in the README. Expected: order-svc healthy, and migrations 5 and 6 applied. With `docker compose -p p2ws3 stop payment` followed by a restart of order, order is still healthy.

- [ ] **Step 5: Commit** — `docs(order): P2 README; contract: mark P2 Order fields required`
