# e2e — integration tests and demo data

Everything here runs against the real Docker Compose stack (`make up-app`):
the gateway on `:8080`, the web app on `:3000`, Jaeger on `:16686`, MinIO on `:9000`.

| Path | What it is |
|---|---|
| `client/` | Small typed REST client for the gateway (cookie jar, bearer token, 429 back-off) |
| `cmd/seed/` | `make seed`: demo sellers, shops, products with images, buyers and orders, through public APIs only |
| `cmd/genimages/` | Regenerates the synthetic images in `fixtures/images/` |
| `api/` | Go API smoke tests; every request/response is validated against `api/openapi/*.yaml` |
| `playwright/` | Browser tests driving the real web app |

## Running

Prerequisites: a running stack (`make up-app`), Go 1.27.1, Node 24.

```bash
make seed      # idempotent; prints the demo accounts
make e2e       # Go tests (unit + API smoke) then Playwright
```

One test at a time:

```bash
cd e2e && GOWORK=off go test -count=1 -v -run TestCancelReturnsStock ./api/
cd e2e/playwright && npx playwright test tests/oversell.spec.ts
npx playwright show-trace test-results/<test>/trace.zip   # after a failure
```

`e2e` is its own Go module and is deliberately not part of `go.work` (owned
by the foundation workstream), so every Go command here runs with
`GOWORK=off`.

| Variable | Default | Used by |
|---|---|---|
| `GATEWAY_URL` | `http://localhost:8080` | seed, API tests, Playwright setup |
| `WEB_URL` | `http://localhost:3000` | Playwright, rate-limit proxy test |
| `JAEGER_URL` | `http://localhost:16686` | trace test |

## Why runs are slow right after `make seed`

The gateway allows 10 register/login calls per minute per client IP (burst
10, then one every 6 s). Seed and the tests all come from one IP, so clients
honor `Retry-After` and wait instead of failing. The gateway rejects before
proxying, so resending is always safe. A full `make e2e` takes a few minutes
mostly for this reason.

## Isolation

Every test creates its own users, shops and products, named with a random
8-letter run id, and clears its buyers' carts first. Tests never depend on
seed data or on earlier runs, so they can run against a stack that has
already been used.

## Findings

Integration found three bugs on the real stack, each first pinned by a failing
test and then fixed. The tests now run as ordinary regression tests:

| Id | Bug | Fixed in | Regression test |
|---|---|---|---|
| F1 | All browser traffic shared one gateway rate-limit bucket (`TRUSTED_PROXIES` empty) | `deploy/`: the gateway trusts only the web container's fixed address | `api/ratelimit_test.go` |
| F2 | Public operations did not document the 401 for an invalid token | `api/openapi/`: 401 added, web types regenerated | `api/auth_test.go` (contract-checked) |
| F3 | A lost checkout response replaced the checkout form with an empty state | `web/src/api/hooks/orders.ts`: no cart refetch after retryable errors | `playwright/tests/idempotency.spec.ts`, `web` unit test |

## P1 success criteria → tests

| # | Criterion | Automated by |
|---|---|---|
| 1 | Register/login, browse, search with/without diacritics, details, cart from several shops | `api/flow_test.go`, `playwright/tests/happy-path.spec.ts`, `playwright/tests/auth.spec.ts` |
| 2 | Checkout splits the cart into one COD order per shop | `api/flow_test.go`, `happy-path.spec.ts` |
| 3 | Open a shop → seller role → products with SKUs and images | `happy-path.spec.ts` (UI upload, two variants), `api/flow_test.go` (presigned upload) |
| 4 | Confirm → ship → deliver → complete; cancelling returns stock | `api/flow_test.go`, `happy-path.spec.ts`, `api/cancel_test.go`, `cancel-restock.spec.ts` |
| 5 | Stock never oversells under concurrent orders | `api/oversell_test.go`, `oversell.spec.ts`, `api/idempotency_test.go`, `idempotency.spec.ts` |
| 6 | Automated tests pass in every service; a checkout is traceable gateway → order → catalog | CI `go` matrix and `web` job; `api/trace_test.go` |

## P2 timers (e2e mode; defaults in brackets)

When `make e2e` runs, it restarts order, payment and ledger services with fast
timers to keep tests quick. A later `make up-app` restores the defaults.

| Timer | e2e value | Default | Purpose |
|---|---|---|---|
| `PAYMENT_TIMEOUT` | 40 s | 30 m | Payment attempt expiry |
| `SWEEPER_INTERVAL` | 2 s | 60 s | Order state cleanup period |
| `AUTO_COMPLETE_AFTER` | 5 s | 72 h | Escrow release after delivery |
| `RECONCILE_INTERVAL` | 2 s | 60 s | Payment reconciliation period |
| `RECONCILE_MIN_AGE` | 3 s | 2 m | Age before a PENDING payment is queried |
| `FAKE_WEBHOOK_DELAY` | 300 ms | 500 ms | Delay before FAKE webhook is sent |
| `PAYOUT_SETTLE_INTERVAL` | 1 s | 30 s | Payout settlement checker period |
| `PAYOUT_SETTLE_DELAY` | 2 s | 1 m | Delay before a payout is settled |
| Refund worker | every 10 s | every 10 s | Retry failed refunds (not configurable) |

## P2 success criteria → tests

| # | Criterion | Automated by |
|---|---|---|
| 1 | Checkout with online payment (VNPay, Stripe, FAKE); escrow captures and auto-completes | `api/online_test.go::TestOnlineCheckoutEscrowAndAutoComplete`, `playwright/tests/payment.spec.ts` (online checkout, success and failure) |
| 2 | Failed payment can be retried; payment timeout cancels and restocks | `api/payment_failure_test.go::TestFailedPaymentCanBeRetried`, `api/payment_failure_test.go::TestPaymentTimeoutCancelsAndRestocks` |
| 3 | Seller cancel after payment refunds that order; duplicate payments are auto-refunded | `api/refund_test.go::TestSellerCancelAfterPaymentRefundsThatOrder`, `api/doublecharge_test.go::TestDoubleChargeIsRefunded` |
| 4 | Payment method routing (VNPay vs Stripe) uses the correct adapter | `api/online_test.go::TestStripeMethodUsesItsAdapter` |
| 5 | Payout settles and cannot overdraw | `api/payout_test.go::TestPayoutSettlesAndCannotOverdraw`, `playwright/tests/wallet.spec.ts` (payout settles to SETTLED) |
| 6 | Duplicate and stale webhooks apply once; dropped webhooks recovered by reconciler; late success and double-charge handled; trial balance always zero | `api/callbacks_test.go::TestDuplicateAndStaleWebhooksApplyOnce`, `api/callbacks_test.go::TestDroppedWebhookIsRecovered`, `api/doublecharge_test.go::TestLateSuccessAfterDeadlineRefundsBothOrders`, `api/main_test.go::TestMain` (suite-end trial balance check), `api/ledger_test.go::TestTrialBalanceIsZero`, `api/harness_test.go::getWorld` (per-test `assertTrialBalance`) |
| 6a | COD orders never touch the ledger (regression) | `api/refund_test.go::TestCODCheckoutNeverTouchesTheLedger` |
| 6b | Payment traces cross gateway → payment → order and ledger via Kafka | `api/trace_test.go::TestPaymentTraceCrossesGatewayPaymentOrder`, `api/trace_test.go::TestFakeWebhookTraceReachesConsumers` |
| 7 | Real VNPay sandbox / Stripe test mode (manual): checkout paid, refund on cancel | `docs/payments-sandbox.md` (manual runbook) |

## P2 test notes

- **TestDoubleChargeIsRefunded:** Asserts the DUPLICATE status in the second payment response and verifies escrow reversal through the trial balance; the refund itself is not observable through the public API.

## P2 decisions

These refinements and design choices emerged during development and are documented
here for future maintainers and changes.

1. **FAKE webhook trace.** The FAKE provider posts its webhook from a detached background
   context, so its trace ID cannot be known from outside. The webhook trace test arms
   `hook_webhook=drop` and posts a correctly signed FAKE webhook itself before the
   reconciler is eligible (`RECONCILE_MIN_AGE`), with its own `traceparent`.

2. **Payout over balance.** The test requests a payout of 50,000 ₫ (the minimum) on an
   empty wallet to verify a 409 `INSUFFICIENT_FUNDS` response. Amounts below the minimum
   answer 400 first.

3. **Refund visibility.** The trial balance has no per-order view. A refund's ledger effect
   is checked through the seller wallet's `in_escrow` field (drops back by the order total)
   and the zero trial balance.

4. **e2e stack recreation.** `make e2e` runs `$(E2E_TIMERS) $(MAKE) up-app` first
   (Makefile:96-98), which sets environment variables for fast timers, so the compose stack
   recreates order, payment and ledger services. A later `make up-app` (without those vars)
   restores the default timers.

5. **Sequential test execution.** API tests are sequential because none calls `t.Parallel()`:
   the 10/min login rate limiter is shared per IP, and wallet baselines assume no other money
   is moving. Only one `go test` process may use the stack at a time.

6. **Client HTTP timeout.** The test client's HTTP timeout is 4 minutes, covering the
   `RetryRateLimited` 3-minute 429 backoff budget. The old 30 s cap made rate-limited
   logins fail.

7. **FAKE HTML validation bypass.** `GET /api/payments/fake/checkout/{id}` returns `text/html`,
   which `pkg/testutil.ContractTransport` (kin-openapi v0.149) cannot validate. The bypass
   is in `api/harness_test.go`; every other call is contract-checked. The POST to the same
   path (form submit) is validated.

8. **Late success and double charge.** Payment-svc reports a still-payable attempt as
   `UNAVAILABLE` to the order sweeper, so with `hook_cancel=unsupported` the orders are
   cancelled only after `expires_at`. The test bounds the wait by `expires_at`.

## P2 findings

| Id | Finding | Owner | Status |
|---|---|---|---|
| F-P2-1 | `pkg/testutil.ContractTransport` cannot validate `text/html` responses (kin-openapi v0.149) | WS0' (`pkg/`) | Bypassed in e2e (`api/harness_test.go`) |

No service bugs were found by the API test scenarios.
