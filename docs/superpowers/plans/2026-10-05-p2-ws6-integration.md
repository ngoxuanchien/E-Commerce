# P2 WS6' Integration and E2E Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prove the P2 success criteria (online payment, escrow, wallet, refunds, payouts, ledger invariant) on the real Docker stack with the FAKE provider, extend the demo seed, and document P2.

**Architecture:** Everything lives in the `e2e/` Go module (not in `go.work`, always `GOWORK=off`), `e2e/playwright/`, `docs/`, `README.md`, the Makefile `seed`/`e2e` targets and the CI `e2e` job. The Go API tests drive the gateway with the typed `e2e/client`, validate every call against `api/openapi/*.yaml`, and check `ledger /server trial-balance` after every test that uses the shared world. The e2e stack runs with fast timers passed as compose env vars.

**Tech Stack:** Go 1.27.1, testify, `pkg/testutil.ContractTransport`, Docker Compose, Playwright 1.63 (TypeScript), Jaeger HTTP API.

**Spec:** `docs/superpowers/specs/2026-09-29-marketplace-p2-ws6-integration.md` (+ `2026-09-29-marketplace-p2-overview.md`).

## Global Constraints

- Contracts in `api/` and `gen/` are frozen. If one is wrong, stop and report it; do not edit.
- Edit only: `e2e/**`, `docs/**` (not `docs/superpowers/` except this plan), `README.md`, Makefile targets `seed`/`e2e` (+ one `E2E_TIMERS` variable), the CI `e2e` job. Bugs in services are reported to their workstream (recorded in `e2e/README.md` "P2 findings"), not fixed here.
- Every Go command in `e2e/` runs with `GOWORK=off`. Lint from the worktree: `cd e2e && GOWORK=off ../../E-Commerce/bin/golangci-lint run ./...` (`bin/` lives in the main checkout).
- Compose runs in this worktree use `COMPOSE_PROJECT_NAME=p2ws6`. Never `docker compose down -v`. Two stacks cannot run at once (host ports 3000/8080/9000 and the pinned subnet); take any other stack down (without `-v`) first.
- Fast e2e timers (spec §2): `PAYMENT_TIMEOUT=40s SWEEPER_INTERVAL=2s AUTO_COMPLETE_AFTER=5s RECONCILE_INTERVAL=2s RECONCILE_MIN_AGE=3s FAKE_WEBHOOK_DELAY=300ms PAYOUT_SETTLE_INTERVAL=1s PAYOUT_SETTLE_DELAY=2s`.
- Tests poll with deadlines derived from these timers (`assert.Eventually`/`require.Eventually`/`assert.Never`), never a fixed sleep as the success condition.
- Money is `int64` VND. Commission = `floor(subtotal × 500 / 10000)`; seller credit on completion = `total − commission_amount`. COD never touches the ledger.
- Repo text in English; UI strings stay Vietnamese as the web app renders them.
- The user's GateGuard hook blocks the first Bash call and the first Write of each new file until you state (1) the request in one sentence and (2) what the command/file does. State them and retry.
- Login/register is rate-limited to 10/min per IP: create as few users as possible (reuse the shared `world`).
- API tests run sequentially (no `t.Parallel`): wallet baselines and the trial balance assume nothing else is moving money.

## Review Focus

1. **Async effects racing the next test.** A test that ends before its refund/credit lands corrupts the next test's wallet baseline. Each test waits for its own final money effect (payment status, `payment_status`, wallet figure) before returning (Tasks 4–10).
2. **Redirect following.** FAKE submit/return answer 302 to `/checkout/result`, a web route the gateway doesn't serve. The client must never follow these redirects (Task 2 test).
3. **Trial balance check failing silently.** If `docker compose exec` cannot reach the stack (wrong project name), the check must fail loudly, not pass or skip (Task 3 test with a bogus project name).
4. **Payout minimum vs funds.** `1 ₫` hits the 50,000 ₫ minimum (400) before the funds check; the over-balance case uses 50,000 ₫ on an empty wallet → 409 `INSUFFICIENT_FUNDS` (Task 9).
5. **Seed re-run.** Running `make seed` twice must not create a second set of online orders or a second payout (Task 11, "run twice" step).

---

## File map

| File | Responsibility |
|---|---|
| `Makefile` (`E2E_TIMERS`, `e2e`) | `make e2e` re-ups the stack with fast timers, then Go tests + Playwright |
| `.github/workflows/ci.yml` (`e2e` job) | Job-level fast timers; payment/ledger logs as separate artifacts |
| `e2e/client/types.go` | + `Payment`, `Wallet`, `WalletEntry`, `Payout`, `FakeEvent`; `Order` P2 fields |
| `e2e/client/payments.go` (new) | Payment, FAKE page/webhook, wallet, payout endpoints; `SignFake` |
| `e2e/client/client.go` | Extract `apiError(status, body)` so non-JSON callers reuse it |
| `e2e/api/harness_test.go` | Routes payment/ledger paths to their contracts; P2 actors in `world`; trial-balance cleanup |
| `e2e/api/ledger_test.go` (new) | `trialBalance`, `assertTrialBalance` |
| `e2e/api/timers_test.go` (new) | Timer values from env (defaults = e2e values) |
| `e2e/api/online_helpers_test.go` (new) | `checkoutOnline`, `payFake`, `waitOrder`, `waitPayment`, `walletOf`, `waitWallet`, `deliver`, `credit` |
| `e2e/api/online_test.go` (new) | Scenarios 1 (VNPAY + auto-complete) and 2 (STRIPE) |
| `e2e/api/payment_failure_test.go` (new) | Scenarios 3 (fail → retry) and 4 (timeout → stock back) |
| `e2e/api/refund_test.go` (new) | Scenarios 5 (refund on cancel) and 11 (COD regression) |
| `e2e/api/callbacks_test.go` (new) | Scenarios 6 (duplicate, stale) and 7 (dropped webhook) |
| `e2e/api/doublecharge_test.go` (new) | Scenarios 8 (double charge) and 9 (late success) |
| `e2e/api/payout_test.go` (new) | Scenario 10 |
| `e2e/api/trace_test.go` | + payment trace and webhook → Kafka consumers trace |
| `e2e/playwright/support/api.ts` | `ApiUser` P2 helpers |
| `e2e/playwright/tests/payment.spec.ts` (new) | Buyer online success; failure → pay again |
| `e2e/playwright/tests/wallet.spec.ts` (new) | Seller wallet → payout → SETTLED |
| `e2e/cmd/seed/data.go`, `e2e/cmd/seed/payments.go` (new), `seed.go` | Online orders, payout, wallet summary |
| `docs/architecture.md`, `README.md`, `docs/payments-sandbox.md` (new), `docs/adr/0006-0008` | P2 documentation |
| `e2e/README.md` | P2 timers, criteria → tests table, decisions, findings |

---

### Task 1: Fast timers in `make e2e` and CI

**Files:**
- Modify: `Makefile` (lines 95–100, `e2e` target)
- Modify: `.github/workflows/ci.yml` (`e2e` job, lines 102–144)

- [x] **Step 1: Makefile.** Add above `seed:`:

```make
# Fast timers for the e2e stack (P2 WS6' §2); `make up-app` alone keeps the defaults.
E2E_TIMERS := PAYMENT_TIMEOUT=40s SWEEPER_INTERVAL=2s AUTO_COMPLETE_AFTER=5s \
	RECONCILE_INTERVAL=2s RECONCILE_MIN_AGE=3s FAKE_WEBHOOK_DELAY=300ms \
	PAYOUT_SETTLE_INTERVAL=1s PAYOUT_SETTLE_DELAY=2s
```

Make the `e2e` recipe start with `$(E2E_TIMERS) $(MAKE) up-app` (compose recreates only the services whose env changed: order, payment, ledger), then keep the two existing lines. Update its `##` help text: "(Re)start the stack with fast timers, then API tests + Playwright (make up-app seed first)".

- [x] **Step 2: CI.** In the `e2e` job add a job-level `env:` block with the eight timer variables (same values), so `make keys up-app` already starts fast and `make e2e`'s `up-app` is a no-op. Bump `timeout-minutes` to 60. In "collect service logs" also write `docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.web.yml --profile app logs --no-color payment > e2e-payment-logs.txt` and the same for `ledger` (`e2e-ledger-logs.txt`), and add both files to the artifact `path`.

- [x] **Step 3: Verify.** `make -n e2e` prints the env-prefixed `make up-app`. `AUTO_COMPLETE_AFTER=5s docker compose -f deploy/docker-compose.yml --profile app config | grep AUTO_COMPLETE_AFTER` shows `5s`. CI YAML parses: `python3 -c 'import yaml;yaml.safe_load(open(".github/workflows/ci.yml"))'`.

- [x] **Step 4: Commit** `ci(e2e): run the e2e stack with fast P2 timers`.

---

### Task 2: Client — P2 types and endpoints

**Files:**
- Modify: `e2e/client/types.go`, `e2e/client/client.go`
- Create: `e2e/client/payments.go`
- Test: `e2e/client/client_test.go` (existing httptest style)

**Interfaces (Produces):**

```go
// types.go (json tags are the snake_case contract names)
type Payment struct {
	ID            string  `json:"id"`
	CheckoutID    string  `json:"checkout_id"`
	Method        string  `json:"method"`   // VNPAY | STRIPE
	Provider      string  `json:"provider"` // vnpay | stripe | fake
	Status        string  `json:"status"`   // PENDING | SUCCEEDED | FAILED | EXPIRED | DUPLICATE
	Amount        int64   `json:"amount"`
	RedirectURL   *string `json:"redirect_url"`
	ExpiresAt     string  `json:"expires_at"`
	FailureReason *string `json:"failure_reason"`
}
type Wallet struct {
	Available     int64  `json:"available"`
	PendingPayout int64  `json:"pending_payout"`
	InEscrow      int64  `json:"in_escrow"`
	Currency      string `json:"currency"`
}
type WalletEntry struct {
	ID            string  `json:"id"`
	TransactionID string  `json:"transaction_id"`
	Kind          string  `json:"kind"` // RELEASE | PAYOUT_REQUEST | PAYOUT_FAIL
	Amount        int64   `json:"amount"`
	OrderID       *string `json:"order_id"`
	PayoutID      *string `json:"payout_id"`
	CreatedAt     string  `json:"created_at"`
}
type Payout struct {
	ID            string  `json:"id"`
	Amount        int64   `json:"amount"`
	Status        string  `json:"status"` // REQUESTED | SETTLED | FAILED
	FailureReason *string `json:"failure_reason"`
	RequestedAt   string  `json:"requested_at"`
	SettledAt     *string `json:"settled_at"`
}
type FakeEvent struct {
	EventID   string `json:"event_id"`
	PaymentID string `json:"payment_id"`
	Outcome   string `json:"outcome"`
}
// Order gains: PaymentMethod `payment_method`, PaymentStatus `payment_status` string;
// PaymentDeadline *string `payment_deadline`; CommissionRateBps int `commission_rate_bps`;
// CommissionAmount int64 `commission_amount`; DeliveredAt *string `delivered_at`.
// HistoryEntry gains CreatedAt string `json:"created_at"`.

// payments.go
func (c *Client) CreatePayment(ctx context.Context, key, checkoutID string, hdr http.Header) (Payment, error) // POST /api/payments {checkout_id}, Idempotency-Key: key
func (c *Client) Payment(ctx context.Context, id string) (Payment, error)                                    // GET /api/payments/{id}
func (c *Client) FakeArm(ctx context.Context, paymentID string, hooks url.Values) error                      // GET /api/payments/fake/checkout/{id}?<hooks>; HTML 200, body discarded
func (c *Client) FakeSubmit(ctx context.Context, paymentID, outcome string, hooks url.Values, hdr http.Header) (location string, err error) // form POST, expects 302, never follows
func (c *Client) FakeReturn(ctx context.Context, paymentID string) (location string, err error)              // GET /api/payments/fake/return?payment_id=…, expects 302, never follows
func (c *Client) FakeWebhook(ctx context.Context, secret string, ev FakeEvent, hdr http.Header) error        // POST /api/payments/fake/webhook, X-Fake-Signature: SignFake(secret, now, body)
func SignFake(secret string, ts time.Time, body []byte) string                                               // "t=<unix>,v1=<hex HMAC-SHA256(secret, "<unix>." + body)>"
func (c *Client) Wallet(ctx context.Context) (Wallet, error)                                                 // GET /api/seller/wallet
func (c *Client) WalletEntries(ctx context.Context) (Page[WalletEntry], error)                               // GET /api/seller/wallet/entries?limit=100
func (c *Client) CreatePayout(ctx context.Context, key string, amount int64) (Payout, error)                 // POST /api/seller/payouts {amount}, Idempotency-Key: key
func (c *Client) Payouts(ctx context.Context, status string) (Page[Payout], error)                           // GET /api/seller/payouts?limit=100[&status=]
```

Implementation notes: extract the error-envelope decoding at the end of `Do` into `func apiError(status int, data []byte) *APIError` and reuse it. FAKE submit/return copy `*c.HTTP` and set `CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }`; any status other than 302 becomes `apiError`. `FakeSubmit` sends `application/x-www-form-urlencoded` with `outcome` plus every `hooks` value. These callback routes need no bearer token; sending it anyway is harmless (the gateway ignores it on callback routes).

- [x] **Step 1: Write failing tests** in `client_test.go`:
  - `TestSignFakeKnownVector`: `SignFake("dev-fake-secret", time.Unix(1700000000, 0), []byte(`{"event_id":"e1","payment_id":"p1","outcome":"succeed"}`))` == `"t=1700000000,v1=8646cea7fb3478995ffb48101262589e62191e8e6e9a1a65a6288294306b4388"` (computed with `openssl dgst -sha256 -hmac`).
  - `TestFakeSubmitDoesNotFollowRedirect`: an httptest server answers `POST /api/payments/fake/checkout/p1` with 302 `Location: /api/payments/fake/return?payment_id=p1` and fails the test if `/api/payments/fake/return` is ever hit; assert the form had `outcome=fail` and `hook_webhook=drop`, and the returned location.
  - `TestFakeSubmitNon302IsAPIError`: the server answers 404 with `{"error":{"code":"NOT_FOUND","message":"x"}}` → `IsCode(err, "NOT_FOUND")`.
  - `TestCreatePayoutSendsIdempotencyKey`: the server checks `Idempotency-Key: k1` and body `{"amount":50000}`, answers 201 with a payout JSON; the decoded `Status` is `REQUESTED`.
- [x] **Step 2:** `cd e2e && GOWORK=off go test ./client/` → FAIL (undefined).
- [x] **Step 3:** Implement the types and `payments.go`.
- [x] **Step 4:** `GOWORK=off go test ./client/` passes; `GOWORK=off go vet ./...` clean.
- [x] **Step 5: Commit** `feat(e2e): client for payments, FAKE provider, wallet and payouts`.

---

### Task 3: Harness — contracts, world, trial balance, timers, online helpers

**Files:**
- Modify: `e2e/api/harness_test.go`, `e2e/api/main_test.go`
- Create: `e2e/api/ledger_test.go`, `e2e/api/timers_test.go`, `e2e/api/online_helpers_test.go`

**Interfaces (Produces, package `api_test`):**

```go
// harness_test.go
type contractRouter struct{ identity, catalog, order, payment, ledger http.RoundTripper }
// /api/payments, /api/payments/* → payment.yaml; /api/seller/wallet*, /api/seller/payouts* → ledger.yaml.
// Both checked BEFORE the catalog default branch.
// world gains: payer actor (buyer for every P2 test, e2e-<run>-payer@example.test);
//              sellerC actor + shopC client.Shop (payout tests only, so its wallet is isolated).
// getWorld(t) registers t.Cleanup(func() { assertTrialBalance(t) }) on every call.

// ledger_test.go
func trialBalance(ctx context.Context) ([]byte, error)
//   exec: docker compose -f <repo>/deploy/docker-compose.yml exec -T ledger /server trial-balance
//   <repo> = filepath.Join("..", ".."); COMPOSE_PROJECT_NAME is inherited from the environment.
//   Returns combined output; error on non-zero exit.
func assertTrialBalance(t *testing.T) // require.NoError(t, err, "trial balance:\n%s", out)

// timers_test.go — envDuration(key string, def time.Duration) time.Duration; defaults = e2e values
var paymentTimeout, sweeperInterval, autoCompleteAfter, reconcileInterval, reconcileMinAge,
	fakeWebhookDelay, payoutSettleInterval, payoutSettleDelay time.Duration
const refundInterval = 10 * time.Second // payment-svc refund worker period; not configurable in compose

// online_helpers_test.go
func checkoutOnline(t *testing.T, buyer actor, method string, lines map[string]int) client.CheckoutResponse
//   clear cart, set each sku→qty, checkout; requires every order AWAITING_PAYMENT, payment_status UNPAID, non-nil deadline
func startPayment(t *testing.T, buyer actor, checkoutID string) client.Payment // CreatePayment(newKey()); requires PENDING + non-nil RedirectURL
func payFake(t *testing.T, buyer actor, checkoutID, outcome string, hooks url.Values) client.Payment // startPayment + FakeSubmit; returns the started payment
func waitOrder(t *testing.T, a actor, orderID string, within time.Duration, ok func(client.Order) bool, msg string) client.Order
func waitPayment(t *testing.T, buyer actor, paymentID, status string, within time.Duration) client.Payment
func walletOf(t *testing.T, seller actor) client.Wallet
func waitWallet(t *testing.T, seller actor, within time.Duration, ok func(client.Wallet) bool, msg string) client.Wallet
func deliver(t *testing.T, seller actor, orderID string) // confirm, ship, deliver
func credit(o client.Order) int64                       // o.Total - o.CommissionAmount
```

Polling helpers poll every 200 ms and must not call `require` inside the `Eventually` closure (P1 deferred minor): fetch with plain error checks inside, keep the last value, return false on error, and assert on the last value afterwards with a clear message.

In `TestMain` (`main_test.go`), after `m.Run()`, run `trialBalance` once more; on error print its output and exit 1 (global invariant after the suite).

- [x] **Step 1: Failing tests** in `ledger_test.go`: `TestTrialBalanceFailsLoudlyWithoutStack` sets `t.Setenv("COMPOSE_PROJECT_NAME", "no-such-stack-"+runID())` and asserts `trialBalance(ctx)` returns an error; `TestTrialBalanceIsZero` calls `assertTrialBalance(t)`.
- [x] **Step 2:** Bring up the stack once for the rest of the plan and run:

```bash
docker compose ls   # take any other stack down WITHOUT -v first
export COMPOSE_PROJECT_NAME=p2ws6
make keys
PAYMENT_TIMEOUT=40s SWEEPER_INTERVAL=2s AUTO_COMPLETE_AFTER=5s RECONCILE_INTERVAL=2s RECONCILE_MIN_AGE=3s \
  FAKE_WEBHOOK_DELAY=300ms PAYOUT_SETTLE_INTERVAL=1s PAYOUT_SETTLE_DELAY=2s make up-app
cd e2e && GOWORK=off go test -count=1 -run TestTrialBalance ./api/
```

Expected: FAIL (undefined).
- [x] **Step 3:** Implement the five files.
- [x] **Step 4:** The same command passes, and the whole existing suite still passes: `GOWORK=off go test -count=1 ./api/`.
- [x] **Step 5: Commit** `test(e2e): P2 harness — contracts, trial balance after each test, online helpers`.

---

### Task 4: Scenarios 1–2 — online happy path, auto-complete, Stripe routing

**Files:** Create `e2e/api/online_test.go`

`TestOnlineCheckoutEscrowAndAutoComplete` (VNPAY):
1. One product on shopA (price 100,000, buy 2) and one on shopB (`newProduct`). Baseline `baseA := walletOf(sellerA)`, `baseB := walletOf(sellerB)`.
2. `checkoutOnline(payer, "VNPAY", …)` → 2 orders; each has `CommissionRateBps == 500` and `CommissionAmount == Subtotal*500/10000`.
3. `p := payFake(..., "succeed", nil)`; `waitPayment(p.ID, "SUCCEEDED", 5s)` has `Provider == "fake"`, `Method == "VNPAY"`, `Amount == orderA.Total + orderB.Total`.
4. Both orders reach `PENDING_CONFIRMATION` with `PaymentStatus == "PAID"` within 10 s.
5. `waitWallet(sellerA)`: `InEscrow == baseA.InEscrow + orderA.Total`.
6. `deliver(sellerA, orderA)`; `waitOrder` → `COMPLETED` within `autoCompleteAfter + 3*sweeperInterval + 5s`; the last history entry has `To == "COMPLETED"` and `ActorRole == "system"`.
7. `waitWallet(sellerA)`: `Available == baseA.Available + credit(orderA)` and `InEscrow == baseA.InEscrow`.
8. Finish orderB (deliver, buyer `CompleteOrder`) and wait until seller B's `Available == baseB.Available + credit(orderB)`, so nothing is in flight when the test ends.

`TestStripeMethodUsesItsAdapter`: one shopA line, `checkoutOnline(payer, "STRIPE", …)`, `payFake succeed` → `waitPayment SUCCEEDED` with `Method == "STRIPE"` and `Provider == "fake"`; order `PAID`. Deliver + buyer complete; wait for seller A's credit.

- [x] **Step 1:** Write the tests.
- [x] **Step 2:** `cd e2e && GOWORK=off go test -count=1 -v -run 'TestOnline|TestStripe' ./api/` → PASS. These test existing behavior: a failure is a test bug or a finding (see "Findings protocol").
- [x] **Step 3: Commit** `test(e2e): online checkout, escrow, auto-complete and Stripe routing`.

---

### Task 5: Scenarios 3–4 — failure then pay again; payment timeout

**Files:** Create `e2e/api/payment_failure_test.go`

`TestFailedPaymentCanBeRetried`: checkout VNPAY (shopA, 1 line) → `payFake "fail"` → `waitPayment FAILED` (non-nil `FailureReason`). `assert.Never` for `fakeWebhookDelay + 2s` that the order leaves `AWAITING_PAYMENT`/`UNPAID`. A second `payFake "succeed"` (new payment id, different from the first) → `SUCCEEDED`; the order becomes `PENDING_CONFIRMATION`/`PAID`. Then seller A cancels it at `PENDING_CONFIRMATION` (reason "Hết hàng") and the test waits for `PaymentStatus == "REFUNDED"` within `3*refundInterval + 5s`, so no escrow stays behind.

`TestPaymentTimeoutCancelsAndRestocks`: product stock 5, checkout 2 units VNPAY, no payment. `stockOf == 3`. `waitOrder` → `CANCELLED` within `paymentTimeout + 3*sweeperInterval + 10s`, and `CancelReason` equals the payment-timeout reason order-svc writes (read the exact string from `services/order/internal/domain` or app code and assert it exactly). Stock back to 5 within 10 s (`Eventually` on `stockOf`). `CreatePayment` afterwards → 409 `INVALID_STATE_TRANSITION`.

- [x] **Step 1** write; **Step 2** run `-run 'TestFailedPayment|TestPaymentTimeout'` → PASS; **Step 3** commit `test(e2e): failed payment retry and payment timeout`.

---

### Task 6: Scenarios 5 and 11 — refund on cancel; COD regression

**Files:** Create `e2e/api/refund_test.go`

`TestSellerCancelAfterPaymentRefundsThatOrder`: baselines `baseA`, `baseB`. Checkout VNPAY across shopA + shopB, pay succeed, both `PAID`; wait until seller A's `InEscrow == baseA.InEscrow + orderA.Total`. Seller A confirms, then cancels orderA (reason "Hết hàng") → `CANCELLED`; `waitOrder` → `PaymentStatus == "REFUNDED"` within `3*refundInterval + 5s`. Seller A's `InEscrow` returns to `baseA.InEscrow` (the ledger reversed the escrow) and `Available == baseA.Available`. OrderB is still `PENDING_CONFIRMATION`/`PAID` and seller B's `InEscrow == baseB.InEscrow + orderB.Total`. Finish orderB (deliver + buyer complete) and wait for its credit. The trial balance runs in cleanup.

`TestCODCheckoutNeverTouchesTheLedger`: snapshot `walletOf(sellerB)`. COD checkout on shopB: the order has `PaymentMethod "COD"`, `PaymentStatus "NONE"`, `PaymentDeadline == nil`, `CommissionAmount == 0`, status `PENDING_CONFIRMATION`. Deliver + complete → `COMPLETED`. `assert.Never` for `2*sweeperInterval + 2s` that seller B's wallet differs from the snapshot.

- [x] **Step 1** write; **Step 2** run `-run 'TestSellerCancelAfterPayment|TestCOD'` → PASS; **Step 3** commit `test(e2e): refund on cancel and COD regression`.

---

### Task 7: Scenarios 6–7 — duplicate, out-of-order and dropped callbacks

**Files:** Create `e2e/api/callbacks_test.go`

`TestDuplicateAndStaleWebhooksApplyOnce`, subtests over `hook_webhook` ∈ {`duplicate`, `stale_after`}: baseline `walletOf(sellerA)`; checkout VNPAY (shopA, 1 line); `payFake succeed` with that hook → `SUCCEEDED` → order `PAID`. `assert.Never` for `4*fakeWebhookDelay + 2s` that the order leaves `PENDING_CONFIRMATION`/`PAID` or that the payment leaves `SUCCEEDED`. The history has exactly one entry with `To == "PENDING_CONFIRMATION"`. Deliver + buyer complete; `waitWallet` until `Available == base.Available + credit`; then `assert.Never` for 2 s that `Available` exceeds that (credited once).

`TestDroppedWebhookIsRecovered`, two subtests, each with its own checkout (VNPAY, shopA, 1 line) and `hook_webhook=drop`:
- `reconciler`: `payFake succeed`, never call the return URL → `waitPayment SUCCEEDED` within `reconcileMinAge + 3*reconcileInterval + 2s`.
- `return_url`: `payFake succeed`, then `FakeReturn` → location starts with `/checkout/result?payment_id=` → `waitPayment SUCCEEDED` within `reconcileMinAge` (the return path's `Query` must win before the reconciler is even eligible).

Each subtest ends by seller A cancelling the order and waiting for `REFUNDED`, so no escrow is left in flight.

- [x] **Step 1** write; **Step 2** run `-run 'TestDuplicateAndStale|TestDroppedWebhook'` → PASS; **Step 3** commit `test(e2e): duplicate, stale and dropped FAKE callbacks`.

---

### Task 8: Scenarios 8–9 — double charge and late success

**Files:** Create `e2e/api/doublecharge_test.go`

`TestDoubleChargeIsRefunded`: baseline `walletOf(sellerA)`. Checkout VNPAY (shopA, 1 line). `p1 := startPayment`; `FakeArm(p1.ID, hook_cancel=unsupported)`. `p2 := payFake(..., "succeed", nil)`: creating p2 while p1 is open but uncancellable must succeed (if it answers 409/503, follow the Findings protocol). Order `PAID`. Then `FakeSubmit(p1.ID, "succeed")` → `waitPayment(p1.ID, "DUPLICATE")` within 10 s; `p2` stays `SUCCEEDED`. Seller A's `InEscrow` grew by `orderA.Total` once (`assert.Never` 2 s beyond). The order is still `PAID` with one `PENDING_CONFIRMATION` history entry. There is no public refund field for a DUPLICATE payment; the trial balance in cleanup is the ledger check. Finish the order (deliver + complete + credit).

`TestLateSuccessAfterDeadlineRefundsBothOrders`: baselines for sellers A and B. Checkout VNPAY across shopA + shopB. `p := startPayment`; `FakeArm(p.ID, hook_cancel=unsupported)`. Payment-svc reports a still-payable attempt as UNAVAILABLE to the sweeper, so the orders are cancelled only after the attempt expires: wait until both orders are `CANCELLED` within `time.Until(p.ExpiresAt) + paymentTimeout + 3*sweeperInterval + 10s`. Then `FakeSubmit(p.ID, "succeed")` → `waitPayment SUCCEEDED` → both orders reach `PaymentStatus == "REFUNDED"` within `3*refundInterval + 5s`. Both sellers' `InEscrow` are back to baseline.

If the stack disagrees with the spec here (for example, FAKE rejects a submit after expiry), stop and report the observed behavior to the controller before changing the test.

- [x] **Step 1** write; **Step 2** run `-run 'TestDoubleCharge|TestLateSuccess'` → PASS; **Step 3** commit `test(e2e): double charge and late success refunds`.

---

### Task 9: Scenario 10 — payouts

**Files:** Create `e2e/api/payout_test.go`

Helper `earn(t *testing.T, w *world) int64`: payer buys one new shopC product (price 100,000, qty 1) with VNPAY, pays, seller C delivers, the buyer completes, `waitWallet(sellerC)` until credited; returns `credit(order)` (125,000 = 130,000 − 5,000).

`TestPayoutSettlesAndCannotOverdraw`:
1. `earn`; `a := walletOf(sellerC).Available` (> 0).
2. `CreatePayout(newKey(), a)` → `REQUESTED`; the wallet shows `Available 0`, `PendingPayout a`.
3. Poll `Payouts(ctx, "")` until that payout is `SETTLED` within `payoutSettleDelay + 3*payoutSettleInterval + 5s`; the wallet shows `Available 0`, `PendingPayout 0`; `WalletEntries` has a `PAYOUT_REQUEST` entry with amount `-a` and that `PayoutID`.
4. `CreatePayout(newKey(), 1)` → 400 `VALIDATION_ERROR` (minimum 50,000 ₫ is checked first); `CreatePayout(newKey(), 50_000)` → 409 `INSUFFICIENT_FUNDS`.
5. `earn` again (`Available == 125,000`). Two goroutines call `CreatePayout(newKey(), 70_000)` at once → exactly one 201 and one 409 `INSUFFICIENT_FUNDS`; then wait until the winner is `SETTLED`.

- [ ] **Step 1** write; **Step 2** run `-run TestPayout` → PASS; **Step 3** commit `test(e2e): payout settle, overdraw and concurrent requests`.

---

### Task 10: Traces across payment, order and ledger

**Files:** Modify `e2e/api/trace_test.go`

Generalize `fetchTrace(t, traceID) (services map[string]bool, ops map[string][]string)` (operation names by service) and keep the P1 test's assertions (catalog `ReserveStock`). Keep polling errors out of `require` inside loops.

`TestPaymentTraceCrossesGatewayPaymentOrder`: checkout VNPAY (shopA, 1 line); `CreatePayment` with header `Traceparent: 00-<trace>-<span>-01` → within 30 s the trace has `gateway`, `payment`, and an `order` operation containing `GetCheckout`. Then cancel the checkout as buyer (`CancelOrder`) so it doesn't wait for the timeout.

`TestFakeWebhookTraceReachesConsumers`: the FAKE sender posts its webhook from a detached background context, so its trace id cannot be known from outside (decision, record in `e2e/README.md`). The test plays the provider instead: `payFake succeed` with `hook_webhook=drop` (records the outcome, sends nothing), then immediately `FakeWebhook(ctx, envOr("FAKE_WEBHOOK_SECRET", "dev-fake-secret"), client.FakeEvent{EventID: "e2e-" + newKey(), PaymentID: p.ID, Outcome: "succeed"}, Traceparent header)`, well before `reconcileMinAge`. Within 30 s the trace contains `gateway`, `payment`, `order` and `ledger` (the Kafka consumers continue the outbox `traceparent`). Then wait for the order `PAID`, cancel it as seller, and wait for `REFUNDED`.

If the order/ledger consumer spans are missing, follow the Findings protocol (owner: whoever writes/consumes the outbox headers) rather than changing services.

- [ ] **Step 1** write; **Step 2** run `-run Trace` → PASS; **Step 3** commit `test(e2e): traces through payment, order and ledger`.

---

### Task 11: Seed — online orders, payout, wallet summary

**Files:**
- Modify: `e2e/cmd/seed/data.go`, `e2e/cmd/seed/seed.go` (call the new phases from `run` after `orders`, extend `summary`)
- Create: `e2e/cmd/seed/payments.go`
- Test: `e2e/cmd/seed/data_test.go`

**Data** (`data.go`): `var onlinePlans []onlinePlan` with
`type onlinePlan struct { Buyer, Shop, Product int; Method string; Pay bool; Steps []string; Want, WantPayment string }`:

| Buyer | Shop | Method | Pay | Steps | Want / payment |
|---|---|---|---|---|---|
| 1 | 0 | VNPAY | no | – | AWAITING_PAYMENT / UNPAID |
| 1 | 0 | VNPAY | yes | – | PENDING_CONFIRMATION / PAID |
| 1 | 1 | STRIPE | yes | confirm, ship | SHIPPING / PAID |
| 1 | 0 | VNPAY | yes | confirm, ship, deliver, complete | COMPLETED / PAID |
| 0 | 0 | STRIPE | yes | confirm, ship, deliver, complete | COMPLETED / PAID |
| 0 | 0 | VNPAY | yes | confirm, cancel (seller) | CANCELLED / REFUNDED |

Use products that P1 `orderPlans` don't use. `data_test.go` adds: every plan references an existing buyer, shop and product; `Method ∈ {VNPAY, STRIPE}`; at least one `COMPLETED` plan is on shop 0 (seller 1 gets a balance).

**Phases** (`payments.go`):
- `onlineOrders(ctx, shops, bs)`: skipped (prints `online orders: exist, skipped`) when buyer index 1 already has any order with `PaymentMethod != "COD"`. Otherwise for each plan: clear cart → set line → checkout → if `Pay`, `CreatePayment` + `FakeSubmit(succeed)` and poll the order until `PAID` (deadline 30 s); run the steps (`cancel` is the seller action with reason "Hết hàng"; `complete` is the buyer); poll until `Status == Want` and `PaymentStatus == WantPayment` (deadline 60 s; refunds take up to one refund interval). Print each order.
- `payout(ctx, seller1)`: skipped (prints `payout: exists, skipped`) when `Payouts(ctx, "")` is non-empty. Poll the wallet until `Available > 0` (60 s); request `Available/2` rounded down to a multiple of 1,000 ₫ (at least 50,000); poll until `SETTLED` (deadline 3 min, since the default `PAYOUT_SETTLE_DELAY` is 1 min), printing `payout: waiting to settle…` once.
- `summary` adds a `WALLET` table: per seller `available / in_escrow / pending_payout` from `Wallet` (log in again if needed; reuse the clients from `run`).

- [ ] **Step 1:** Write the `data_test.go` cases → `GOWORK=off go test ./cmd/seed/` FAIL.
- [ ] **Step 2:** Implement data + phases.
- [ ] **Step 3:** Against the stack: `make seed` succeeds; run it **again**: the output shows `online orders: exist, skipped` and `payout: exists, skipped`, and seller 1's wallet figures are identical in both summaries.
- [ ] **Step 4: Commit** `feat(seed): online orders, a settled payout and wallet figures`.

---

### Task 12: Playwright — buyer online payment

**Files:**
- Modify: `e2e/playwright/support/api.ts`: add to `ApiUser` `checkout(method: 'COD'|'VNPAY'|'STRIPE')` (POST `/api/checkout` with `Idempotency-Key`), `startPayment(checkoutId)`, `fakeSubmit(paymentId, outcome)` (form POST via `this.ctx.fetch(..., { form: { outcome }, maxRedirects: 0 })`, expect 302), `order(id)`, `sellerAction(orderId, action)`, `complete(orderId)`, `wallet()`; extend `Order` with `payment_status`.
- Create: `e2e/playwright/tests/payment.spec.ts`

Read `web/README.md` "Deviations and decisions (WS5')" for testids. FAKE page buttons: `data-testid="fake-pay-succeed"` / `"fake-pay-fail"`.

Tests:
1. `online checkout pays on the FAKE page and the orders show as paid`: seller + product via API; buyer `signIn`, add to cart, `openCheckout`, click `payment-method-VNPAY`, place the order → URL matches `/api/payments/fake/checkout/` → click `fake-pay-succeed` → URL contains `/checkout/result` and `payment-result` reaches `data-status="SUCCEEDED"` (expect.poll / toHaveAttribute with timeout) → on `/orders`, `order-payment-status` shows `Đã thanh toán`.
2. `a failed payment can be paid again`: same start, click `fake-pay-fail` → `payment-result` `data-status="FAILED"` → click `pay-again` (text `Thanh toán lại`) → FAKE page → `fake-pay-succeed` → `SUCCEEDED`.

- [ ] **Step 1** write; **Step 2** `cd e2e/playwright && npx tsc --noEmit && npx playwright test tests/payment.spec.ts` → PASS; **Step 3** commit `test(e2e): Playwright online payment and pay-again`.

---

### Task 13: Playwright — seller wallet and payout

**Files:** Create `e2e/playwright/tests/wallet.spec.ts` (uses the Task 12 helpers)

Setup via API: seller + product (price 120,000); buyer `checkout('VNPAY')`, `startPayment`, `fakeSubmit('succeed')`, poll the order until `payment_status === 'PAID'`; seller confirm/ship/deliver; buyer complete; poll `seller.wallet()` until `available > 0`.
UI: seller `signIn` → wallet page (find the route in `web/src`) → `wallet-available` shows the amount → payouts page: fill `payout-amount` with the full available amount, click `payout-submit` → a `payout-row` appears; reload until its `data-status="SETTLED"` (expect.poll, 30 s) → `wallet-available` shows `0 ₫`.

- [ ] **Step 1** write; **Step 2** run the spec → PASS, then the full `npx playwright test` stays green; **Step 3** commit `test(e2e): Playwright seller wallet payout`.

---

### Task 14: Architecture doc and ADR consequences

**Files:** Modify `docs/architecture.md`; `docs/adr/0006-…`, `0007-…`, `0008-…` only where the built system deviates (compare each "Consequences" section with `services/payment/README.md`, `services/ledger/README.md`, `services/order/README.md`).

Add to `docs/architecture.md`:
- services table rows for payment-svc (8084/9084, payment_db) and ledger-svc (8085/9085, ledger_db); topics `payment.events` and `payout.events`;
- Mermaid sequence diagrams: online checkout as built (return URL → `/checkout/result`, reconciler); refund on cancel (`OrderCancelled` → payment refund worker → `PaymentRefunded` → order `REFUNDED` + ledger escrow reversal); escrow release (deliver → buyer or sweeper auto-complete → `OrderCompleted` → ledger RELEASE: seller wallet `total − commission`, platform revenue `commission`); payout (POST → REQUESTED → settle job → SETTLED, `payout.events`);
- a "Money" section: the chart of accounts (exact names from `services/ledger/internal/domain`), balances derived from entries, the trial-balance invariant and how e2e checks it, links to ADR-0006/0007/0008;
- known limitations: the P2 items from the service READMEs (FAKE is not a security boundary, VNPay refund RC 00 treated as final, trial balance is a full scan).

- [ ] **Step 1** write; **Step 2** check every account name, env var and file path named in the doc exists (`grep -r`), and each Mermaid block is a `sequenceDiagram` whose participants are declared; **Step 3** commit `docs: architecture for payments, escrow and the ledger`.

---

### Task 15: README, payments runbook, e2e README

**Files:** Modify `README.md`, `e2e/README.md`; Create `docs/payments-sandbox.md`

- `README.md`: "What it does" gains P2 (online payment with VNPay/Stripe, FAKE by default; escrow; auto-complete; seller wallet; payouts; 5% commission); the architecture diagram adds payment and ledger; "Switching to the real sandboxes" (`deploy/secrets/payment.env` from `.example`, `PROVIDER_VNPAY=vnpay` / `PROVIDER_STRIPE=stripe`, link to the runbook); demo figures from the seed summary (seller 1's wallet); a note that `make e2e` restarts order/payment/ledger with fast timers and `make up-app` restores the defaults; the roadmap marks P2 done.
- `docs/payments-sandbox.md`: Stripe (`make stripe-listen`, the `whsec_` value into `STRIPE_WEBHOOK_SECRET`, test card `4242 4242 4242 4242`, expected events `checkout.session.completed` and `charge.refunded`); VNPay (sandbox registration, `VNPAY_*` variables as named in `deploy/secrets/payment.env.example`, the return URL + `querydr` path without a tunnel, optional `cloudflared tunnel --url http://localhost:8080` for the IPN, NCB test card); a checklist table `| Date | Provider | Checkout paid | Refund on cancel | Notes |` with empty rows for the user to fill in.
- `e2e/README.md`: P2 timers table; P2 success criteria 1–7 → tests table; "P2 decisions" (webhook-trace approach, payout over-balance case uses 50,000 ₫, refund visibility via wallet `in_escrow` + trial balance, `make e2e` re-up, sequential tests); "P2 findings" (whatever Tasks 4–13 reported, with owners).

- [ ] **Step 1** write; **Step 2** check every command and env var named exists (`grep` the Makefile and `deploy/secrets/payment.env.example`); **Step 3** commit `docs: P2 README, payments sandbox runbook and e2e guide`.

---

### Task 16: Full verification on a fresh stack

- [ ] **Step 1:** `docker compose ls`; take the `p2ws6` stack down without `-v`. With `COMPOSE_PROJECT_NAME=p2ws6fresh` (fresh volumes): `make keys up-app seed`, then `make e2e` → all green. Record the wall time in `e2e/README.md`.
- [ ] **Step 2:** `cd e2e && GOWORK=off ../../E-Commerce/bin/golangci-lint run ./...` clean; `cd e2e/playwright && npx tsc --noEmit` clean.
- [ ] **Step 3:** Take the stack down without `-v`. Tick this plan's boxes; commit `docs: mark WS6' plan done`.

## Findings protocol

A scenario that fails on correct test code is a finding. Keep the test, report it to the controller with the observed vs expected behavior and the owning workstream, and add a row to `e2e/README.md` "P2 findings" (id, bug, owner, test). Only with the controller's approval mark it `t.Skip("finding Fn: <owner> — <one line>")`. Never change files under `services/`, `web/`, `api/`, `gen/`, `pkg/`, `deploy/`.
