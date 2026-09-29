# WS3': order-svc changes for P2

> Depends on WS0' (`order.yaml` changes, `proto/order/v1` server, `proto/payment/v1` client, `api/events`, `pkg/inbox`) and on the P1.5 order fixes (overview §7). Owns `services/order/`. During development it uses an in-process **fake `PaymentInternal`** (bufconn) with scriptable outcomes. This spec amends P1 `2026-09-28-marketplace-p1-ws3-order.md`. Anything it doesn't mention stays as P1 specifies and as `services/order/README.md` records.

## 1. Responsibility (additions)

See WS0' §12 for the contract items this workstream completes.

order-svc gains:
- online payment methods at checkout, with a commission snapshot;
- the `AWAITING_PAYMENT` state and the payment deadline;
- reactions to `payment.events`;
- auto-completion after delivery;
- the `OrderCompleted` event;
- the `OrderInternal.GetCheckout` gRPC server;
- `payment_status` on every order.

It still never touches money or stock directly. Money is ledger-svc's (ADR-0006), and stock is catalog's (ADR-0004).

## 2. Data (migration on `order_db`)

```sql
alter table orders
  add column payment_status text not null default 'NONE',   -- NONE | UNPAID | PAID | REFUND_PENDING | REFUNDED
  add column payment_deadline timestamptz null,
  add column commission_rate_bps int not null default 0,
  add column commission_amount bigint not null default 0,
  add column delivered_at timestamptz null;
create index orders_awaiting_payment_idx on orders (payment_deadline) where status = 'AWAITING_PAYMENT';
create index orders_delivered_idx       on orders (delivered_at)     where status = 'DELIVERED';
create index orders_checkout_idx        on orders (checkout_id);
-- processed_events from pkg/inbox (consumer = 'order-svc')
```

Existing P1 rows get `payment_status = 'NONE'` and zero commission, which is correct for COD. `payment_method` already exists (P1, default `'COD'`).

## 3. Checkout and the state machine

**Checkout** (P1 WS3 §4). Only these steps change:
- **Step 2.5:** `payment_method` must be `COD` or a method in `ONLINE_PAYMENT_METHODS`; otherwise 400. It is part of the normalized request hash (it already is in P1, as a body field).
- **Step 4:** for an online method, `commission_rate_bps = COMMISSION_RATE_BPS` and `commission_amount = floor(subtotal × rate / 10000)`. For COD both are 0.
- **Step 8 (Tx C):**
  - COD: as P1 → `PENDING_CONFIRMATION`, `payment_status = NONE`.
  - Online: → `AWAITING_PAYMENT`, `payment_status = UNPAID`, with `payment_deadline = now + PAYMENT_TIMEOUT`, the same value for every order of the checkout.
  - In both cases the transaction writes `OrderCreated` (with the new fields) and `OrderStatusChanged`.
- The 201 response shape is the P1 one, with the new `Order` fields. Starting the payment is the client's next call, to payment-svc (overview §3).

**Transitions** (additions to P1 WS3 §5; `Transition()` stays a pure function):

| From | To | Actor | Trigger | Side effects |
|---|---|---|---|---|
| `PENDING` | `AWAITING_PAYMENT` | system | Tx C, online method | — |
| `AWAITING_PAYMENT` | `PENDING_CONFIRMATION` | system | `PaymentSucceeded` whose allocations include the order | `payment_status = PAID` |
| `AWAITING_PAYMENT` | `CANCELLED` | buyer | `POST /api/orders/{id}/cancel` | **every** `AWAITING_PAYMENT` order of the checkout is cancelled in the same tx, with the same reason; `OrderCancelled` per order |
| `AWAITING_PAYMENT` | `CANCELLED` | system | sweeper, deadline passed (§5) | `cancel_reason = PAYMENT_TIMEOUT` |
| `PENDING_CONFIRMATION`, `CONFIRMED` | `CANCELLED` | as in P1 | as in P1 | if `payment_status = PAID` → `REFUND_PENDING` |
| `SHIPPING` | `DELIVERED` | seller | as in P1 | `delivered_at = now` |
| `DELIVERED` | `COMPLETED` | buyer | as in P1 | `OrderCompleted {completed_by: buyer}` |
| `DELIVERED` | `COMPLETED` | system | sweeper, `delivered_at + AUTO_COMPLETE_AFTER ≤ now` | `OrderCompleted {completed_by: system}` |

- The seller can't act on an `AWAITING_PAYMENT` order. Seller confirm and cancel answer 409 `INVALID_STATE_TRANSITION`. The seller's order list shows the order, so the seller knows it is coming.
- `OrderCompleted` is written for **every** completion, COD included. ledger-svc ignores COD. It carries `checkout_id, seller_user_id, payment_method, total, commission_amount, completed_by`.
- `OrderCancelled` now also carries `checkout_id` and `payment_method`.
- **Refund status is independent of the order status.** `payment_status` moves only as follows: `UNPAID → PAID` (the payment event), `PAID → REFUND_PENDING` (cancellation), and `REFUND_PENDING → REFUNDED` (the refund event). These updates never change `status`.

## 4. Consumer of `payment.events` (group `order-svc`)

Each event is handled in one transaction: `inbox.MarkProcessed(consumer="order-svc")`, then the effect, with the existing optimistic lock and one retry.
- **`PaymentSucceeded` with allocations:** for each allocated `order_id`:
  - `AWAITING_PAYMENT` → the transition above, with history (`actor_role = system`, `note = payment_id`) and `OrderStatusChanged`.
  - Already `CANCELLED` (a late success or a race) → keep `CANCELLED` and set `payment_status = REFUND_PENDING`. payment-svc issues the refund itself (ADR-0007), and no order event is needed.
  - Any other status → no-op; log at info level.
- **`PaymentSucceeded` with `allocations = []`** (a duplicate charge) → no order change.
- **`PaymentRefunded` with `order_id`** → `payment_status = REFUNDED` if it is `REFUND_PENDING` or `PAID`. A refund always follows a cancellation, so `PAID` is only possible when events arrive out of order.
- **`PaymentRefunded` with `order_id = null`** → no-op.
- **`PaymentFailed` and `RefundFailed`** → ignored. The web reads the payment status from payment-svc.
- Malformed events are logged and skipped, as in the catalog consumer. Transient DB errors are retried with backoff and never skipped.

## 5. Sweeper (additions)

The same job and interval (`SWEEPER_INTERVAL`), all with `FOR UPDATE SKIP LOCKED` and in batches of 50:
1. **P1 `PENDING` timeout**: unchanged.
2. **Payment timeout.** For each distinct `checkout_id` with orders in `AWAITING_PAYMENT` and `payment_deadline ≤ now`:
   1. Call `PaymentInternal.CancelCheckoutPayments(checkout_id)` **before** locking anything (3 s timeout, no retry within the tick).
   2. `OUTCOME_NOT_PAID` → one tx cancels every `AWAITING_PAYMENT` order of the checkout (`system`, `PAYMENT_TIMEOUT`) and writes `OrderStatusChanged` and `OrderCancelled`. After commit it calls `ReleaseStock` for each order (best effort, as in P1 README decision 9). The catalog consumer also releases stock from the event.
   3. `OUTCOME_PAID` → skip. The `PaymentSucceeded` event will move the orders. If an order is still `AWAITING_PAYMENT` 10 minutes after its deadline, a warning is logged every tick.
   4. `UNAVAILABLE` or an error → skip and retry on the next tick. An order can therefore outlive its deadline while payment-svc is down, which is deliberate: cancelling without asking could cancel a paid order.
3. **Auto-complete.** `DELIVERED` orders with `delivered_at + AUTO_COMPLETE_AFTER ≤ now` → `COMPLETED` (`system`), with history, `OrderStatusChanged` and `OrderCompleted`.

## 6. gRPC server `OrderInternal` (port 9083)

- **`GetCheckout(checkout_id)`** returns every order of the checkout (all statuses, ordered by `order_id`) with `seller_user_id, status, subtotal, total, commission_amount`, plus `buyer_id`, `payment_method` and `payment_deadline`.
  - An unknown checkout → `NOT_FOUND`.
  - The server doesn't authorize. payment-svc compares `buyer_id` with the caller, and the port isn't reachable from outside the compose network (the P1 trust model).
- The server is registered next to the existing gRPC health service (`pkg/grpcx`).

## 7. Code layout (additions)

```
internal/domain             Transition table additions, Commission(), payment_status transitions
internal/app                PaymentEventHandler, Sweeper (payment timeout + auto-complete), CheckoutQuery
internal/adapter/payment    PaymentInternal client (3 s timeout, error mapping); paymentfake/ (bufconn)
internal/adapter/grpc       OrderInternal server
internal/adapter/kafka      payment.events consumer
```

Service-specific environment (WS0' §8): `PAYMENT_GRPC_ADDR`, `ONLINE_PAYMENT_METHODS`, `COMMISSION_RATE_BPS`, `PAYMENT_TIMEOUT`, `AUTO_COMPLETE_AFTER`. `/readyz` doesn't depend on payment-svc.

## 8. Testing

- **Unit:**
  - the transition table, extended with every new move per actor and illegal seller actions on `AWAITING_PAYMENT`;
  - `Commission()` rounding (for example, subtotal 99,999 at 500 bps → 4,999);
  - the `payment_status` moves.
- **Integration** (Postgres, Redis and Kafka containers, the catalog fake and the payment fake):
  - Online checkout from 2 shops → 2 orders `AWAITING_PAYMENT`, the same deadline, correct commission, `OrderCreated` with the new fields.
  - COD checkout → identical to P1. The existing tests keep passing unchanged.
  - A `PaymentSucceeded` produced to Kafka → both orders `PENDING_CONFIRMATION`/`PAID`. Delivered twice → one transition and one history row.
  - Buyer cancels one `AWAITING_PAYMENT` order → both orders of the checkout `CANCELLED`, 2 × `OrderCancelled`.
  - Sweeper with the fake answering `NOT_PAID` → cancelled with `PAYMENT_TIMEOUT` and stock released. `PAID` → untouched. `UNAVAILABLE` → untouched, then cancelled on a later tick once the fake answers `NOT_PAID`.
  - `PaymentSucceeded` for an already cancelled order → stays `CANCELLED`, `REFUND_PENDING`; then `PaymentRefunded` → `REFUNDED`.
  - A paid order cancelled by the seller at `CONFIRMED` → `REFUND_PENDING` and `OrderCancelled` with `payment_method`.
  - Auto-complete with `AUTO_COMPLETE_AFTER = 1s` → `COMPLETED` by `system`, plus `OrderCompleted`. Buyer completion also emits `OrderCompleted`.
  - `GetCheckout` over bufconn: every field, and `NOT_FOUND`.
- **Contract:** `order.yaml`, the `api/events` schemas and `proto/order/v1`.

## 9. Acceptance criteria

- [ ] COD behaviour and every P1 order test are unchanged.
- [ ] No order stays `AWAITING_PAYMENT` beyond `payment_deadline` + one sweeper interval while payment-svc is reachable.
- [ ] No paid order is ever cancelled by the payment timeout. The integration test with `OUTCOME_PAID` proves this.
- [ ] Every completion emits exactly one `OrderCompleted`, and delivered orders auto-complete after `AUTO_COMPLETE_AFTER`.
- [ ] `go test ./...` and lint are green.
