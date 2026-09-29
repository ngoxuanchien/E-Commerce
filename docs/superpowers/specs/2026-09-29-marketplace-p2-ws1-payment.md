# WS1': payment-svc

> Depends on WS0' (`api/openapi/payment.yaml`, `proto/payment/v1` server, `proto/order/v1` client, `api/events`, `pkg/inbox`, `pkg/idemkey`). Owns `services/payment/`. During development it uses an in-process **fake `OrderInternal`** (bufconn) and httptest stubs that act as VNPay and Stripe with real signatures. ADR-0008 records the design.

## 1. Responsibility

payment-svc handles:
- payment attempts for a checkout (one capturing payment per checkout);
- the provider port and its three adapters (VNPay, Stripe, FAKE);
- verifying callbacks (IPN, webhook, return URL);
- reconciling attempts stuck in `PENDING`;
- refunds;
- publishing `payment.events` through the outbox.

It holds **no balances**. Money positions live in ledger-svc (ADR-0006). It never changes order state directly; order-svc reacts to its events.

## 2. Data (`payment_db`)

```sql
payments(id uuid pk, checkout_id uuid not null, buyer_id uuid not null,
         method text not null,          -- VNPAY | STRIPE
         provider text not null,        -- vnpay | stripe | fake (adapter actually used)
         status text not null,          -- PENDING | SUCCEEDED | FAILED | EXPIRED | DUPLICATE
         amount bigint not null check (amount > 0),
         captured boolean not null default false,  -- this payment funds the checkout's escrow
         provider_ref text null,        -- vnp_TxnRef / Stripe Checkout Session id / fake ref
         provider_txn_id text null,     -- vnp_TransactionNo / PaymentIntent id
         redirect_url text null, expires_at timestamptz not null,
         failure_reason text null, version int not null default 0, created_at, updated_at)
  unique (checkout_id) where status = 'PENDING'   -- at most one open attempt
  unique (checkout_id) where captured             -- at most one capturing payment
  unique (provider, provider_ref)
payment_allocations(payment_id uuid fk, order_id uuid, seller_user_id uuid,
         amount bigint, commission_amount bigint, primary key (payment_id, order_id))
order_cancellations(order_id uuid pk, checkout_id uuid not null, cancelled_at)
refunds(id uuid pk, payment_id uuid fk, order_id uuid null, amount bigint check (amount > 0),
        status text not null,          -- PENDING | SUCCEEDED | FAILED
        provider_refund_ref text null, attempts int not null default 0,
        next_attempt_at timestamptz not null, last_error text null, created_at, updated_at)
  unique (payment_id, order_id); unique (payment_id) where order_id is null
provider_events(provider text, event_key text, received_at, primary key (provider, event_key))
fake_sessions(payment_id uuid pk, outcome text null, hooks jsonb, refunded bigint default 0)  -- FAKE adapter only
processed_events, idempotency_keys, outbox   -- from pkg/inbox, pkg/idemkey, pkg/outbox
```

Indexes: `payments(status, created_at)` for reconciliation, `payments(checkout_id)`, and `refunds(status, next_attempt_at)`.

## 3. Domain rules (pure functions in `internal/domain`)

**Attempt state.** `Apply(payment, observed) → (payment', effects)`:

| Current | Observed at provider | Result |
|---|---|---|
| `PENDING` | pending | unchanged |
| `PENDING` | failed / expired | `FAILED` / `EXPIRED` (`PaymentFailed` for failed) |
| `PENDING`, `FAILED`, `EXPIRED` | succeeded (amount matches) | **success wins**, see "capture" below |
| `SUCCEEDED`, `DUPLICATE` | anything | unchanged (terminal) |
| any | succeeded, amount **differs** | unchanged; logged as an error; `amount_mismatch` metric (VNPay answers `04`) |

A provider-confirmed success always wins, because the buyer's money has really moved. A terminal success never regresses, so stale or out-of-order callbacks can't undo it.

**Capture.** On success, in one transaction that holds `SELECT … FOR UPDATE` on the checkout's payment rows:
- **No captured payment exists for the checkout:** set `captured = true, status = SUCCEEDED`. Write `PaymentSucceeded` with the payment's allocations.
- **Another payment is already captured:** set `status = DUPLICATE`. Write `PaymentSucceeded` with `allocations = []`, and insert a **full refund** row (`order_id = null`).

**Refund-due rule** (ADR-0007): a refund row for `(payment, order_id)` is created **exactly when the order's allocation is captured AND the order is in `order_cancellations`**. Both paths check the rule in the same transaction as their own write:
- the capture path, for each allocation;
- the `OrderCancelled` consumer, for the order.

Whichever fact arrives second creates the refund. The unique constraint makes the rule idempotent.

## 4. Provider port (`internal/app/provider.go`)

```go
type Provider interface {
    Name() string                                                      // "vnpay" | "stripe" | "fake"
    CreateSession(ctx context.Context, in SessionInput) (Session, error)    // redirect URL + provider_ref
    Query(ctx context.Context, p Payment) (Observed, error)                // authoritative server-to-server status
    Cancel(ctx context.Context, p Payment) (cancelled bool, err error)     // make the session unpayable if the provider can
    Refund(ctx context.Context, in RefundInput) (RefundObserved, error)    // idempotent per RefundInput.RefundID
    QueryRefund(ctx context.Context, r Refund) (RefundObserved, error)
}
type SessionInput struct { PaymentID string; Amount int64; ExpiresAt time.Time; ReturnURL, Description, ClientIP string }
type Observed struct { State ObservedState; Amount int64; ProviderTxnID, Reason string } // Pending|Succeeded|Failed|Expired
```

Each adapter also has an HTTP callback handler that **verifies before parsing** (§5). `PROVIDER_VNPAY` and `PROVIDER_STRIPE` pick the adapter for each method (`vnpay|fake`, `stripe|fake`). With `fake`, `payments.provider = 'fake'`.

## 5. Adapters

### 5.1 VNPay (sandbox, API version 2.1.0)
- **Timestamps are in GMT+7.** `vnp_CreateDate` and `vnp_ExpireDate` use `yyyyMMddHHmmss` in `Asia/Ho_Chi_Minh`.
- **CreateSession** builds `VNPAY_PAY_URL?…` with these parameters:
  - `vnp_Version=2.1.0`, `vnp_Command=pay`, `vnp_TmnCode`, `vnp_CurrCode=VND`;
  - `vnp_Amount = amount × 100`;
  - `vnp_TxnRef` = the payment id without dashes (32 hex characters, unique per attempt);
  - `vnp_OrderInfo` = ASCII text without diacritics (`Thanh toan don hang <checkout_id>`), `vnp_OrderType=other`, `vnp_Locale=vn`;
  - `vnp_ReturnUrl = PUBLIC_BASE_URL + /api/payments/vnpay/return`, `vnp_IpAddr`, `vnp_CreateDate`, `vnp_ExpireDate = expires_at`.
- **Secure hash:**
  1. Take every `vnp_*` parameter except `vnp_SecureHash` and `vnp_SecureHashType`.
  2. Sort them by key and join them as `key=urlencode(value)` with `&`.
  3. Compute `HMAC-SHA512(VNPAY_HASH_SECRET)` of that string, in hex.
  4. Verify with a constant-time comparison against `vnp_SecureHash` (case-insensitive hex).
- **IPN** (`GET /api/payments/vnpay/ipn`). The checks run in this order and the first failure answers:
  1. signature wrong → `97` "Invalid Checksum";
  2. `vnp_TmnCode` differs or the payment isn't found by `vnp_TxnRef` → `01` "Order not found";
  3. `vnp_Amount ≠ amount × 100` → `04` "Invalid amount";
  4. the payment is already terminal and this IPN changes nothing → `02` "Order already confirmed";
  5. otherwise apply the result and answer `00` "Confirm Success";
  6. an unexpected error answers `99`, so VNPay retries.

  Success requires both `vnp_ResponseCode = 00` and `vnp_TransactionStatus = 00`. The inbox key is `sha256(vnp_TxnRef|vnp_TransactionNo|vnp_ResponseCode|vnp_TransactionStatus)`.
- **Return URL.** After a valid signature, payment-svc calls `Query` (bounded to 5 s), applies the result, then answers 302. With an invalid signature it answers 302 without applying anything. Return parameters are **never** applied directly (ADR-0008).
- **Query** is `POST VNPAY_API_URL` with JSON `vnp_Command=querydr`. The hash is `HMAC-SHA512` over the fields joined with `|` in the documented order: `vnp_RequestId|vnp_Version|vnp_Command|vnp_TmnCode|vnp_TxnRef|vnp_TransactionDate|vnp_CreateDate|vnp_IpAddr|vnp_OrderInfo`. The response hash is verified the same way.
- **Refund** is `vnp_Command=refund` with `vnp_TransactionType = 02` (full) when the refund amount equals the payment amount, and `03` (partial) otherwise. It also sends `vnp_TransactionNo` and `vnp_CreateBy=system`.
- **Cancel** isn't supported by VNPay, so it returns `false`. The session becomes unpayable at `vnp_ExpireDate`.
- Before merging, the implementation must check both pipe orders (query and refund) and the response-hash fields against the current VNPay sandbox documentation, and record the doc version in the README.

### 5.2 Stripe (test mode, `stripe-go`)
- **CreateSession** is a Checkout Session. The request header `Idempotency-Key` is set to the payment id. Parameters:
  - `mode=payment`;
  - one line item `{currency: vnd, unit_amount: amount, quantity: 1, name: "Đơn hàng <checkout_id>"}`. VND is **zero-decimal**, so `unit_amount` is the VND amount itself;
  - `client_reference_id = payment_id` and `metadata {payment_id, checkout_id}`;
  - `success_url` and `cancel_url` = `PUBLIC_BASE_URL/api/payments/stripe/return?session_id={CHECKOUT_SESSION_ID}`;
  - `expires_at = max(expires_at, now + 30 min)`. Stripe's minimum is 30 minutes, so a shorter deadline is enforced by `Cancel` (`sessions.expire`) from the sweeper path, not by Stripe.
- An amount below `STRIPE_MIN_AMOUNT` is rejected before any call, with 409 `INVALID_STATE_TRANSITION` and `details.reason = BELOW_PROVIDER_MINIMUM`.
- **Webhook** (`POST /api/payments/stripe/webhook`):
  1. `webhook.ConstructEvent(rawBody, Stripe-Signature, STRIPE_WEBHOOK_SECRET)` with the default 300 s tolerance. A failure answers 400.
  2. The inbox key is `event.id`.
  3. Handled types: `checkout.session.completed`, `checkout.session.async_payment_succeeded`, `checkout.session.async_payment_failed`, `checkout.session.expired`, `charge.refunded`, `refund.updated`.
  4. **The event is only a hint.** The handler reads `metadata.payment_id`, then `Query` (Session `Retrieve` with `payment_intent` expanded) and applies the **current** state. Delivery order therefore never matters.
  5. An unknown payment or an ignored type answers 200. An internal error answers 500, so Stripe retries.
- **Query** maps session states: `status=complete` and `payment_status=paid` → succeeded; `status=expired` → expired; `status=open` → pending.
- **Cancel** is `sessions.expire`. If the session is already complete, it returns `false`, and the caller runs `Query`.
- **Refund** is `refunds.create {payment_intent, amount}` with `Idempotency-Key = refund_id`. `pending` maps to pending, which the reconcile job resolves through `QueryRefund`.

### 5.3 FAKE (default in dev, CI and e2e)
The FAKE adapter behaves like an asynchronous provider, so the real verification path is always exercised.
- **CreateSession** stores a `fake_sessions` row and returns `PUBLIC_BASE_URL/api/payments/fake/checkout/{payment_id}`.
- **The hosted page** is minimal HTML: amount, "Thanh toán" and "Thất bại" buttons, with labelled buttons and `data-testid`.
  - Submitting records the outcome and redirects to `fake/return`.
  - After `FAKE_WEBHOOK_DELAY` it POSTs a signed event `{event_id, payment_id, outcome}` to `FAKE_WEBHOOK_URL`, which goes through the gateway.
- **Signature:** `X-Fake-Signature: t=<unix>,v1=<hex HMAC-SHA256(FAKE_WEBHOOK_SECRET, t + "." + body)>`, with a 300 s tolerance. It is Stripe-shaped on purpose.
- **Test hooks** are optional hidden form fields on the hosted page; e2e sets them:
  - `hook_webhook=duplicate`: deliver twice;
  - `hook_webhook=drop`: never deliver, which forces the return and reconcile path;
  - `hook_webhook=stale_after`: after the real event, deliver an older `pending` event;
  - `hook_refund=fail`: refunds fail;
  - `hook_cancel=unsupported`: `Cancel` returns `false` and the page stays payable after `expires_at`, the way VNPay behaves. This lets e2e reproduce double charges and late successes.

  Hooks are stored in `fake_sessions.hooks`. They only exist when the FAKE adapter is active.
- **Query** returns the recorded outcome. With no outcome it returns pending, or expired once `expires_at` has passed. `Cancel` marks the session expired unless it already has an outcome or `hook_cancel=unsupported` is set. `Refund` succeeds immediately unless `hook_refund=fail`.

## 6. Use cases (`internal/app`)

**CreatePayment** (`POST /api/payments`):
1. `idemkey.Claim(scope="payment", buyer, key, hash)`.
2. `OrderInternal.GetCheckout`. Then check, answering the first failure:
   - unknown checkout, or `buyer_id ≠ X-User-Id` → 404;
   - COD → 409;
   - no order in `AWAITING_PAYMENT`, or `now ≥ payment_deadline` → 409;
   - a captured payment already exists → 409 (`details.reason = ALREADY_PAID`);
   - order-svc unavailable → 503, and the key is released.
3. If a `PENDING` attempt exists:
   - call `Provider.Cancel`, then `Query`, and apply the result;
   - if it succeeded, answer 409 `ALREADY_PAID`;
   - otherwise mark it `EXPIRED`. This is the only case where payment-svc expires an attempt that the provider couldn't cancel (VNPay). A later success on it is handled by "success wins" plus `DUPLICATE`/refund.
4. Tx: insert the payment (`PENDING`, `amount = Σ total` over the `AWAITING_PAYMENT` orders, `expires_at = payment_deadline`) and its allocations.
5. `Provider.CreateSession`, outside the transaction.
   - On error: mark the payment `FAILED` (`PROVIDER_ERROR`), release the key, and answer 503.
   - On success: store `redirect_url` and `provider_ref`.
6. `idemkey.Complete` with 201 `Payment`.

**Callbacks:** verify → `Query` (except FAKE and VNPay IPN, whose verified payload is authoritative) → one transaction: insert the `provider_events` key, where a conflict means duplicate and does nothing more, then `Apply`, the capture/refund-due checks and the outbox writes.

**`CancelCheckoutPayments`** (gRPC, called by order-svc's sweeper):
- For each `PENDING` attempt of the checkout: `Cancel`, then `Query`, then apply the result.
- A provider that can't be reached → `UNAVAILABLE`.
- If any attempt is captured → `OUTCOME_PAID`; otherwise, with every attempt terminal → `OUTCOME_NOT_PAID`.
- An attempt the provider couldn't cancel (VNPay, or FAKE with `hook_cancel=unsupported`) whose `Query` doesn't report success after `expires_at` becomes `EXPIRED`.

**`OrderCancelled` consumer** (group `payment-svc`): `inbox.MarkProcessed`, insert `order_cancellations`, then the refund-due rule. COD events are recorded but create no refund, because no allocation matches.

**Refund worker** (every 10 s, `FOR UPDATE SKIP LOCKED`, `PENDING` rows with `next_attempt_at ≤ now`):
- `Provider.Refund`. Succeeded → `SUCCEEDED` + `PaymentRefunded`. Pending → recheck later with `QueryRefund`.
- A transient error → `attempts++`, with exponential backoff from 10 s up to a 10 min cap.
- After `REFUND_MAX_ATTEMPTS` → `FAILED` + `RefundFailed`.

**Reconciler** (`RECONCILE_INTERVAL`, `FOR UPDATE SKIP LOCKED`):
- `PENDING` attempts older than `RECONCILE_MIN_AGE` → `Query` and apply.
- Attempts past `expires_at` + 5 min that are still pending → `Cancel`, then `Query`, then `EXPIRED` if the provider still doesn't report success.
- It also purges `idempotency_keys` older than 24 h and `provider_events` older than 30 days.

## 7. Local development

- **Default:** `PROVIDER_*=fake`. Nothing external is needed.
- **Stripe:** put `STRIPE_SECRET_KEY=sk_test_…` in `deploy/secrets/payment.env`, run `make stripe-listen`, copy the printed `whsec_…` into `STRIPE_WEBHOOK_SECRET`, restart payment, and pay with test card `4242 4242 4242 4242`.
- **VNPay:** register a sandbox merchant and set `VNPAY_TMN_CODE` and `VNPAY_HASH_SECRET`.
  - Locally the IPN can't reach you, because there is no public URL. The flow still completes, because the verified return URL triggers `querydr`, and the reconciler covers a closed tab.
  - To test the real IPN, run a tunnel (`cloudflared tunnel --url http://localhost:8080`) and register `<tunnel>/api/payments/vnpay/ipn` as the IPN URL in the VNPay merchant portal.
  - Sandbox test cards are listed in VNPay's sandbox docs. The README links to them rather than copying them.

## 8. Code layout

```
internal/domain                Payment, Apply(), capture/duplicate decision, refund-due rule
internal/app                   CreatePayment, HandleCallback, CancelCheckoutPayments, RefundWorker, Reconciler, ports
internal/adapter/vnpay         URL builder, hash, IPN/return handlers, querydr/refund client
internal/adapter/stripe        stripe-go client, webhook handler
internal/adapter/fake          hosted page, signer, delayed webhook sender, hooks
internal/adapter/postgres      sqlc repositories, outbox, inbox, idemkey
internal/adapter/order         OrderInternal client (3 s timeout, 2 retries on UNAVAILABLE)
internal/adapter/grpc          PaymentInternal server
internal/adapter/kafka         order.events consumer
internal/adapter/http          REST handlers (payment.yaml)
```

Service-specific environment variables are listed in WS0' §8.

## 9. Testing

- **Unit:**
  - the whole `Apply` table;
  - capture vs duplicate;
  - the refund-due rule in both arrival orders;
  - VNPay hashing against a known vector from the VNPay docs, plus a round trip of our URL builder through our verifier, and `vnp_Amount ×100`;
  - Stripe `expires_at` clamping; FAKE signer and verifier (tampered body, stale `t`).
- **Integration** (Postgres and Kafka containers, fake `OrderInternal`, httptest VNPay and Stripe stubs; Stripe through `stripe.SetBackend` pointed at the stub):
  - Create → FAKE succeed → `PaymentSucceeded` with allocations on Kafka.
  - A same-key replay returns the same `Payment`; a different body → 422.
  - A duplicate IPN answers `02` and a duplicate webhook answers 200, each with no second event.
  - A tampered signature answers 97/400, and the payment is unchanged.
  - Stripe `expired` delivered after `completed` → still `SUCCEEDED`, because it is re-fetched.
  - Two attempts both succeed → the second is `DUPLICATE`, gets a full refund and `PaymentRefunded` with `order_id` null.
  - `OrderCancelled` before capture, then success → a refund for that order. The reverse order gives the same result.
  - A dropped webhook → the reconciler applies success within one interval.
  - A refund fails `REFUND_MAX_ATTEMPTS` times → `RefundFailed`.
  - `CancelCheckoutPayments` gives `NOT_PAID` / `PAID` / `UNAVAILABLE`.
- **Contract:** REST validated against `payment.yaml` (`pkg/testutil.ContractTransport`), and events against `api/events`.

## 10. Acceptance criteria

- [ ] Every endpoint in `payment.yaml` behaves as described, and callbacks are verified before any state change.
- [ ] No double capture and no double refund under duplicate or out-of-order delivery. This is shown by the integration tests.
- [ ] Every attempt reaches a terminal state within `expires_at` + 5 min + one reconcile interval.
- [ ] The Stripe and VNPay adapters are checked manually against the real sandboxes, following the README steps (the non-CI success criterion 7).
- [ ] `go test ./...` and lint are green. `/readyz` checks Postgres and Kafka.
