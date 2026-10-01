# payment-svc

Payment attempts for a checkout, behind a provider port with three adapters (FAKE, VNPay sandbox,
Stripe test mode) (WS1', spec `docs/superpowers/specs/2026-09-29-marketplace-p2-ws1-payment.md`,
ADR `docs/adr/0008-payment-provider-port-and-webhooks.md`). It verifies callbacks (IPN, webhook,
return URL), reconciles attempts stuck in `PENDING`, runs refunds, and publishes `PaymentSucceeded`
/ `PaymentFailed` / `PaymentRefunded` / `RefundFailed` to Kafka topic `payment.events` through a
transactional outbox. It holds no balances (ledger-svc does) and never changes order state; order-svc
reacts to its events.

## Layout and ports

| Package | Responsibility |
|---|---|
| `internal/domain` | Pure rules: `Apply` state table, capture-vs-duplicate, refund-due rule |
| `internal/app` | Use cases (`Creator`, callback handling, `CancelCheckoutPayments`, `RefundWorker`, `Reconciler`, `OrderCancelled` consumer logic) and the ports: `Provider`, `Store`/`Tx`, `OrderInternal` |
| `internal/adapter/fake` | FAKE provider: hosted page, signed delayed webhook, hooks |
| `internal/adapter/vnpay` | Pay-URL builder, secure hash, IPN / return, `querydr`, refund |
| `internal/adapter/stripe` | `stripe-go` Checkout Sessions, webhook, query, cancel, refund |
| `internal/adapter/postgres` | `app.Store` on PostgreSQL (sqlc: `queries/` to `sqlcdb/`), outbox, inbox, idempotency keys |
| `internal/adapter/order` | `OrderInternal` gRPC client (3 s per attempt, 2 retries on `UNAVAILABLE`); `orderfake` is the in-process fake |
| `internal/adapter/grpc` | `PaymentInternal` server (called by order-svc) |
| `internal/adapter/kafka` | `order.events` consumer (group `payment-svc`) |
| `internal/adapter/http` | REST handlers for `api/openapi/payment.yaml` and the provider callbacks |
| `internal/bootstrap` | Wiring shared by `cmd/server` and the end-to-end tests |

Ports: HTTP `:8084` (`/healthz`, `/readyz` checking Postgres and Kafka), gRPC `:9084`.

## Configuration

| Variable | Default | Meaning |
|---|---|---|
| `DATABASE_URL`, `KAFKA_BROKERS` | required | Dependencies (checked by `/readyz`) |
| `ORDER_GRPC_ADDR` | required | order-svc gRPC address |
| `PUBLIC_BASE_URL` | required | Public origin used to build provider return URLs |
| `PROVIDER_VNPAY` | `fake` | `fake` or `vnpay`: the adapter behind method `VNPAY` |
| `PROVIDER_STRIPE` | `fake` | `fake` or `stripe`: the adapter behind method `STRIPE` |
| `FAKE_WEBHOOK_URL` | required if any provider is `fake` | Where the FAKE provider posts its signed webhook (the gateway URL) |
| `FAKE_WEBHOOK_SECRET` | required if any provider is `fake` | HMAC secret that signs and verifies FAKE webhooks |
| `FAKE_WEBHOOK_DELAY` | `500ms` | Delay before the FAKE webhook is sent after the buyer submits |
| `RECONCILE_INTERVAL` | `60s` | Reconciler period (must be > 0) |
| `RECONCILE_MIN_AGE` | `2m` | Age before a `PENDING` attempt is polled (must be < 30 min, order-svc's `PAYMENT_TIMEOUT` floor) |
| `REFUND_MAX_ATTEMPTS` | `8` | Failed refund attempts before `FAILED` + `RefundFailed` (must be > 0) |
| `VNPAY_TMN_CODE`, `VNPAY_HASH_SECRET`, `VNPAY_PAY_URL`, `VNPAY_API_URL` | required if `PROVIDER_VNPAY=vnpay` | Sandbox merchant code, hash secret, pay page URL, merchant API URL |
| `STRIPE_SECRET_KEY`, `STRIPE_WEBHOOK_SECRET` | required if `PROVIDER_STRIPE=stripe` | Test-mode secret key (`sk_test_...`) and the webhook signing secret (`whsec_...`) |
| `STRIPE_MIN_AMOUNT` | `10000` | Smallest checkout (VND) sent to Stripe; smaller answers 409 `BELOW_PROVIDER_MINIMUM` (must be >= 0) |
| `HTTP_ADDR`, `GRPC_ADDR`, `OTEL_EXPORTER_OTLP_ENDPOINT`, `LOG_LEVEL`, `SERVICE_NAME` | WS0 | Common service settings |

Secrets belong in `deploy/secrets/payment.env` (gitignored; template `payment.env.example`).

## Flows

- **Create** (`POST /api/payments`): claim the idempotency key, read the checkout from order-svc, validate it (404, or 409 `COD_CHECKOUT` / `NOT_AWAITING_PAYMENT` / `DEADLINE_PASSED` / `ALREADY_PAID` / `BELOW_PROVIDER_MINIMUM`; 503 when order-svc is down), retire any `PENDING` attempt (`Cancel`, `Query`, apply; a success answers 409 `ALREADY_PAID`, otherwise `EXPIRED`), insert the payment and its allocations in one transaction, call `Provider.CreateSession` outside it, store `redirect_url` and `provider_ref`, complete the key with 201.
- **Callbacks** (IPN, webhook): verify first, `Query` for authority (not for FAKE or the VNPay IPN, whose verified payload is authoritative), then one transaction that inserts the `provider_events` key (a conflict means duplicate and does nothing more), applies the observation and writes the capture / refund-due / outbox rows. Success wins over a later failure or expiry; a second success on the same checkout becomes `DUPLICATE` and is refunded in full.
- **Return URLs** (`fake/return`, `vnpay/return`, `stripe/return`): verify, trigger a `Query`, then always 302 to `/checkout/result`.
- **Reconcile** (every `RECONCILE_INTERVAL`, `FOR UPDATE SKIP LOCKED`): `PENDING` attempts older than `RECONCILE_MIN_AGE` are queried and applied; attempts past `expires_at` + 5 min are cancelled, queried, then `EXPIRED` if the provider still reports no success. It also purges idempotency keys older than 24 h and `provider_events` older than 30 days. Every attempt is terminal within `expires_at` + 5 min + one interval.
- **Refunds** (worker every 10 s): a refund becomes due when a captured allocation has no matching live order (cancelled order, `DUPLICATE` payment). The worker claims one due `PENDING` refund per transaction, calls `Provider.Refund` or `QueryRefund`, and writes `PaymentRefunded` or `RefundFailed`.
- **Cancel** (`PaymentInternal.CancelCheckoutPayments`, from order-svc's sweeper): `Cancel`, `Query` and apply for every `PENDING` attempt of the checkout. Any captured attempt answers `PAID`; all attempts terminal answers `NOT_PAID`; an unreachable provider answers `UNAVAILABLE`.
- **OrderCancelled** (Kafka `order.events`): inbox dedupe, lock the checkout's payments, record the cancellation, then the refund-due rule.

## Decisions beyond the spec

Gaps and refinements found while building, resolved without any contract change.

1. **FAKE hooks.** Besides the hidden form fields (`hook_webhook` = `duplicate|drop|stale_after`, `hook_refund` = `fail`, `hook_cancel` = `unsupported`), the hooks can be armed through `?hook_*` query parameters on `GET /api/payments/fake/checkout/{id}`; the e2e suite appends them to `redirect_url`. Armed hooks are stored with the outcome still unset, and the form's hooks are merged over them on submit. This is what makes `hook_cancel=unsupported` reachable before paying (late-success and double-charge scenarios). An invalid value is a validation error.
2. **FAKE refund is idempotent per `RefundID`** (table `fake_refunds`, migration 000003), so a lost commit after a successful provider call never refunds twice.
3. **Creation is serialised per checkout.** Retiring pending attempts through storing the session runs under a session-level `pg_try_advisory_lock(hashtextextended(checkout_id, 0))` held on a pinned connection from a dedicated pool (`LockPoolMaxConns = 8`, separate from the store pool so concurrent creates cannot exhaust it). A create that cannot take the lock answers 409 `CONFLICT` ("a payment for this checkout is being created") and releases its idempotency key; a unique violation on `payments_one_open` maps to the same 409. A double click therefore gets a retryable 409 rather than waiting.
4. **Return URLs always 302 to `/checkout/result`**, never 404 or 400, because the contract documents only 302.
5. **VNPay dates.** `CreatedAt` is truncated to the second (Postgres rounds microseconds; VNPay dates have second precision) and used both for `vnp_CreateDate` in the pay URL and `vnp_TransactionDate` in `querydr` and refund, so they always match. It travels in `app.SessionInput.CreatedAt` (an internal port, not a shared contract).
6. **VNPay refund RC 94** (a refund was already requested for this transaction) maps to `Pending` with the refund reference set, and `QueryRefund` resolves it through `querydr` (`vnp_TransactionType` 02/03 with status 00/05/06 is Succeeded; other statuses or no refund visible is Failed, retried by the worker).
7. **VNPay `querydr`.** Transaction status 05 (VNPay processing), 06 (refund sent to the bank) and 07 (suspected fraud, which also logs a warning) all map to `Pending`, never to a failure, so the reconciler keeps asking and no false `PaymentFailed` is emitted. RC 91 (not found) is `Pending` before `expires_at`, `Expired` after.
8. **VNPay amounts and text.** `vnp_Amount` is dong x 100; a response amount that is not a multiple of 100 is rejected (treated as unavailable). `vnp_OrderInfo` is sanitised to `[A-Za-z0-9 ]` plus `:-_.` (no diacritics, nothing PHP urlencode and Go `QueryEscape` encode differently), spaces collapsed, capped at 255, with a fallback text when empty.
9. **Stripe `async_payment_failed`** maps to `FAILED` through the PaymentIntent status: for a `complete` session that is `unpaid`, a PaymentIntent in `requires_payment_method` or `canceled` is `Failed(stripe_async_failed)`; `processing` or anything else stays `Pending`. A later success still wins.
10. **Stripe webhook** uses `IgnoreAPIVersionMismatch`, so an endpoint left on the dashboard's default API version still parses; the signature is still verified before anything is read.
11. **Stripe expiry.** `expires_at` has a floor of now + 31 min (Stripe's minimum is 30). A shorter payment deadline is enforced through `Cancel` (`sessions.expire`) from the sweeper path. HTTP 409, 429 and 5xx from Stripe are `ErrProviderUnavailable` (retry later).
12. **One transaction per refund.** The worker claims one due refund (`FOR UPDATE SKIP LOCKED`) per transaction, loops up to the batch size, and bounds each provider call with a 15 s timeout.
13. **Pending refund polls back off by age:** next poll = now + `clamp(age/2, 10 s, 10 min)`. After 24 h still `PENDING`, each poll logs at error level. A pending refund never becomes `FAILED` on its own, because money may be moving.
14. **Accepted refunds** (provider reference set): `ErrProviderUnavailable` does not count as an attempt (age backoff applies instead), and a provider-reported `Failed` is immediate (`FAILED` + `RefundFailed`). Without a reference, failures and unavailability count toward `REFUND_MAX_ATTEMPTS`.
15. **Refund hints** (Stripe `refund.updated` / `charge.refunded`): the hint path adopts the signed provider reference when the locked row has none, then queries it, and returns unavailability errors so the webhook answers 500 and Stripe retries. An unknown reference answers 200.
16. **OrderCancelled takes the checkout lock first** (`LockCheckoutPayments`) before recording the cancellation and reading captured allocations. At READ COMMITTED a capture and a cancel would otherwise miss each other and create no refund.
17. **Deliberate deviation from spec §6** ("COD events are recorded but create no refund"): COD and P1-era events create no `order_cancellations` row at all. There is no behavioural difference for refunds, since no allocation matches either way.
18. **Idempotent replays are JSON-equivalent, not byte-identical.** The stored response is a `jsonb` column, which normalises whitespace and key order.
19. **Handlers re-read a payment that an apply left `Unchanged`** instead of trusting the outcome's copy, and the VNPay IPN checks the amount (code 04) before applying, per the spec's code order.
20. **Startup validation** rejects `RECONCILE_INTERVAL <= 0` and `RECONCILE_MIN_AGE >= 30m`.

## Known limitations

- **VNPay refund RC 00 is final.** The refund API has no per-request status to poll, so a refund answered RC 00 is `Succeeded` without a follow-up poll (the same optimism applies in `QueryRefund` for statuses 05 and 06). A refund that VNPay accepts and rejects later needs a manual check in the merchant portal.
- **VNPay RC 00 with transaction status 05 or 06** on a refund is also final (see above).
- **No 24 h upper clamp on Stripe `expires_at`.** Stripe rejects a session longer than 24 h, so a `payment_deadline` beyond that would fail the create (503). Order-svc's `PAYMENT_TIMEOUT` is 30 min by default, so this does not occur in practice.
- **Switching `PROVIDER_*` with PENDING payments or refunds in flight** leaves them waiting until the old provider is configured again (accepted refunds keep polling with the age backoff and never fail).
- **FAKE is not a security boundary.** Its webhook is signed with a shared dev secret and its hooks are open; never expose a FAKE-enabled instance to real users.

## Local development (FAKE)

Default is `PROVIDER_VNPAY=fake` and `PROVIDER_STRIPE=fake`. Nothing external is needed: `make up` starts the whole stack, and choosing VNPAY or STRIPE at checkout opens the FAKE hosted page (`GET /api/payments/fake/checkout/{id}`) where you pick success, failure or expiry. Submitting records the outcome, redirects through `fake/return` to `/checkout/result`, and, after `FAKE_WEBHOOK_DELAY`, posts a signed webhook to `FAKE_WEBHOOK_URL`.

Hooks reproduce the failure modes. Arm them with query parameters (`.../fake/checkout/{id}?hook_webhook=drop`) or the hidden form fields:

| Hook | Value | Effect |
|---|---|---|
| `hook_webhook` | `duplicate` | The webhook is sent twice |
| | `drop` | No webhook is sent (the reconciler must catch it) |
| | `stale_after` | After the real event, a second event with a `pending` outcome is sent |
| `hook_refund` | `fail` | `Refund` reports Failed |
| `hook_cancel` | `unsupported` | `Cancel` answers false, like VNPay (late success or double charge) |

## Real sandboxes (manual runbook, success criterion 7)

Neither sandbox runs in CI. Put the credentials in `deploy/secrets/payment.env` (copy `payment.env.example`), set the provider variables, and restart payment.

**Stripe**

1. Set `STRIPE_SECRET_KEY=sk_test_...` and `PROVIDER_STRIPE=stripe`.
2. Run `make stripe-listen` (forwards to `localhost:8080/api/payments/stripe/webhook`), copy the printed `whsec_...` into `STRIPE_WEBHOOK_SECRET`, and restart payment.
3. Check out with method STRIPE and pay with test card `4242 4242 4242 4242` (any future expiry, any CVC). Expect `PaymentSucceeded` and the order moving on.

**VNPay**

1. Register a sandbox merchant at https://sandbox.vnpayment.vn/devreg/ and note the `TmnCode` and hash secret. Set `VNPAY_TMN_CODE`, `VNPAY_HASH_SECRET`, `VNPAY_PAY_URL`, `VNPAY_API_URL` and `PROVIDER_VNPAY=vnpay`.
2. Without a public URL the IPN cannot reach you, but the flow still completes: the verified return URL triggers `querydr`, and the reconciler covers a closed tab.
3. To test the real IPN, run a tunnel (`cloudflared tunnel --url http://localhost:8080`) and register `<tunnel>/api/payments/vnpay/ipn` as the IPN URL in the merchant portal.
4. Sandbox test cards are listed in VNPay's sandbox documentation (https://sandbox.vnpayment.vn/apis/). They are not copied here.

**Switching.** `PROVIDER_VNPAY` and `PROVIDER_STRIPE` are independent, each `fake` or its real adapter; a mix is fine.

## Opt-in smoke test

```bash
cd services/payment
PAYMENT_SANDBOX=1 \
STRIPE_SECRET_KEY=sk_test_... \
VNPAY_TMN_CODE=... VNPAY_HASH_SECRET=... \
VNPAY_PAY_URL=https://sandbox.vnpayment.vn/paymentv2/vpcpay.html \
VNPAY_API_URL=https://sandbox.vnpayment.vn/merchant_webapi/api/transaction \
go test -tags sandbox -run Sandbox -v ./internal/adapter/...
```

Files: `internal/adapter/stripe/sandbox_test.go` and `internal/adapter/vnpay/sandbox_test.go`, both behind `//go:build sandbox`. Each test skips unless `PAYMENT_SANDBOX=1` and its credentials are set.

- **Stripe** proves the secret key and request shape are accepted: `CreateSession` (20,000 VND, expiry now + 31 min) returns a `https://checkout.stripe.com/` URL, `Query` is Pending, `Cancel` returns true, `Query` is Expired.
- **VNPay** proves the pay-URL hash and the `querydr` hash orders: the pay URL answers 200 or 302 without response code 70 (invalid signature), and `querydr` on the fresh TxnRef returns a response whose hash verifies and maps to Pending.

CI never runs it: the build tag excludes it, and no credentials exist in CI.

## VNPay doc version

Read on 2026-09-29:

- Pay / IPN / secure hash: https://sandbox.vnpayment.vn/apis/docs/thanh-toan-pay/pay.html
- `querydr` and refund: https://sandbox.vnpayment.vn/apis/docs/truy-van-hoan-tien/querydr&refund.html
- API version `2.1.0`, HMAC-SHA512.

Pinned pipe-hash field orders (merchant API; the pay URL and IPN use the sorted `vnp_*` query hash instead):

| Message | Field order |
|---|---|
| querydr request | `vnp_RequestId\|vnp_Version\|vnp_Command\|vnp_TmnCode\|vnp_TxnRef\|vnp_TransactionDate\|vnp_CreateDate\|vnp_IpAddr\|vnp_OrderInfo` |
| querydr response | `vnp_ResponseId\|vnp_Command\|vnp_ResponseCode\|vnp_Message\|vnp_TmnCode\|vnp_TxnRef\|vnp_Amount\|vnp_BankCode\|vnp_PayDate\|vnp_TransactionNo\|vnp_TransactionType\|vnp_TransactionStatus\|vnp_OrderInfo\|vnp_PromotionCode\|vnp_PromotionAmount` |
| refund request | `vnp_RequestId\|vnp_Version\|vnp_Command\|vnp_TmnCode\|vnp_TransactionType\|vnp_TxnRef\|vnp_Amount\|vnp_TransactionNo\|vnp_TransactionDate\|vnp_CreateBy\|vnp_CreateDate\|vnp_IpAddr\|vnp_OrderInfo` |
| refund response | `vnp_ResponseId\|vnp_Command\|vnp_ResponseCode\|vnp_Message\|vnp_TmnCode\|vnp_TxnRef\|vnp_Amount\|vnp_BankCode\|vnp_PayDate\|vnp_TransactionNo\|vnp_TransactionType\|vnp_TransactionStatus\|vnp_OrderInfo` |

If VNPay changes a field order, signatures fail and the smoke test above catches it.

## Testing

```bash
go test -race -count=1 ./...                                 # everything; needs Docker (testcontainers)
go test -short ./...                                         # unit tests only, no containers
go test -tags sandbox -run Sandbox -v ./internal/adapter/... # real sandboxes, opt-in (see above)
```

- **Unit:** the `Apply` table, capture vs duplicate, the refund-due rule in both arrival orders, VNPay hashing (a known vector computed with `openssl`, since the docs publish no usable one) and a URL builder to verifier round trip, `vnp_Amount` x 100, Stripe expiry floor, FAKE signer and verifier.
- **Integration** (Postgres and Kafka containers, `orderfake`, httptest VNPay and Stripe stubs that sign like the real ones): create to FAKE success to `PaymentSucceeded`, idempotent replays, duplicate IPN / webhook, tampered signatures, out-of-order Stripe events, `DUPLICATE` refunds, `OrderCancelled` in both arrival orders, dropped webhooks recovered by the reconciler, refund exhaustion, `CancelCheckoutPayments` outcomes.
- **Contract:** every REST exchange is validated against `api/openapi/payment.yaml` through `pkg/testutil.ContractTransport`, and events against `api/events/*.schema.json`.
- **Sandbox:** the opt-in smoke test above; never part of CI.
