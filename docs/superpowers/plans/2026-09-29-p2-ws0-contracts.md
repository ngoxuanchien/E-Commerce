# P2 WS0' Contracts and Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix every P2 interface and the shared scaffolding, so that WS1'–WS5' can build payment, ledger, order, gateway and web in parallel without ever editing a shared file.

**Architecture:** WS0' adds:
- the contracts: OpenAPI (`payment.yaml`, `ledger.yaml`, and changes to `order.yaml` and `common.yaml`), gRPC (`order/v1`, `payment/v1`) and event schemas;
- the shared Go libraries (`pkg/event` additions, `pkg/inbox`, `pkg/idemkey`);
- two booting skeletons (`services/payment`, `services/ledger`);
- the shared infrastructure: compose with a `db-init` job, Kafka topics, env and secrets placeholders, the Makefile and CI.

Nothing in `services/{identity,catalog,order,gateway}` changes. `web/` gets only regenerated types and the minimum label entries that keep `tsc` green (the spec gap noted in Task 8).

**Tech Stack:**
- Go 1.27.1 workspace (`go.work`): pgx v5, franz-go, grpc, testcontainers.
- buf v1.73.0 (`make gen-proto`) and redocly 2.54.3 (`make lint-openapi`).
- JSON Schema 2020-12 for events (`santhosh-tekuri/jsonschema/v6`).
- Docker Compose; openapi-typescript for web (`npm run gen:api`).

**Spec:** `docs/superpowers/specs/2026-09-29-marketplace-p2-ws0-contracts.md` (read it first) and `docs/superpowers/specs/2026-09-29-marketplace-p2-overview.md`.

## Global Constraints

- **Money:** VND `int64` with no decimals, never a float. In JSON it is `common.yaml#/components/schemas/Money` (integer ≥ 0).
- **IDs:** UUIDv7 strings with `format: uuid`. Timestamps are RFC 3339 (`common.yaml#/components/schemas/Timestamp`).
- **Ports:** payment HTTP `:8084`, gRPC `:9084`; ledger HTTP `:8085`, gRPC `:9085`. Neither publishes a host port.
- **Topics:** `payment.events` (key `checkout_id`) and `payout.events` (key `seller_user_id`), 3 partitions, RF 1. The existing topic is `order.events`.
- **Event envelope:** unchanged (`event_id, event_type, version=1, occurred_at, data`). New fields on existing events are **optional**, and `version` stays 1.
- **Provider values:** `vnpay | stripe | fake`. **Payment methods:** `COD | VNPAY | STRIPE`.
- **New error code:** `INSUFFICIENT_FUNDS` (409), with `details {available: Money}`.
- **New `Order` fields are optional** in `order.yaml` (user decision 2026-09-29). WS3' adds them to `required` in its own PR (Task 8 notes the exception). The fields are `payment_status`, `payment_deadline`, `commission_rate_bps`, `commission_amount`, `delivered_at` and `shop.slug`.
- **All repo files in English.** Web UI copy stays Vietnamese.
- **Shared-volume rule:** never `docker compose down -v`. Fresh-volume runs use `COMPOSE_PROJECT_NAME=p2check`. The e2e module needs `GOWORK=off`.
- **Pinned tool versions:** use the ones in the Makefile (`BUF_VERSION`, `SQLC_VERSION`, `GOLANGCI_LINT_VERSION`, `REDOCLY_VERSION`). Never bump them.

## Review Focus

1. **A P1 payload without the new optional fields still validates** against the updated `order-created`, `order-cancelled` and `order-status-changed` schemas. order-svc (unchanged) publishes exactly those, and its itest validates them. Task 1 tests the old payloads explicitly.
2. **An `idemkey` row orphaned by a crash is reclaimed only for the same request hash, and only after `staleBefore`.** A zombie run that lost its claim can neither complete nor release the new owner's claim. The P1.5 order-svc bugs must not be reintroduced: no adopting someone else's response, and no re-inserting a claim on a read. Task 3 tests every one of these.
3. **`MarkProcessed` scopes duplicates per consumer.** The same `event_id` for two different consumers is "first time" for both. Task 2 tests it.
4. **An existing P1 `pgdata` volume** (which already has identity/catalog/order DBs) gains `payment_db` and `ledger_db` through `db-init` without errors, and re-running `db-init` is a no-op. Task 7 runs it twice on the real `marketplace` volume.
5. **Unchanged P1 services still pass their contract tests** after the `order.yaml` and `common.yaml` edits. order-svc's itest validates every response against `order.yaml`, and web lint runs `gen:api:check` plus `tsc`. Task 5 runs `go test ./...` in `services/order` and `npm run lint && npm test` in `web`.

---

## File Structure

```
api/events/envelope.schema.json                 modify: event_type enum + description
api/events/order-{created,cancelled,status-changed}.schema.json   modify: optional P2 fields / AWAITING_PAYMENT
api/events/{order-completed,payment-succeeded,payment-failed,payment-refunded,refund-failed,
            payout-requested,payout-settled,payout-failed}.schema.json   create
pkg/event/event.go                               modify: topics, types, constants, P1 optional fields
pkg/event/payment.go                             create: order-completed + payment + payout structs
pkg/event/event_test.go                          modify: schema tests for every event + P1 payloads
pkg/inbox/{inbox.go,schema.sql,inbox_test.go}    create
pkg/idemkey/{idemkey.go,schema.sql,idemkey_test.go}  create
proto/order/v1/order.proto, proto/payment/v1/payment.proto        create
gen/order/v1/*.pb.go, gen/payment/v1/*.pb.go     generated (make gen-proto)
gen/contract_test.go                             create
api/openapi/common.yaml, order.yaml              modify
api/openapi/payment.yaml, ledger.yaml            create
api/.redocly.lint-ignore.yaml                    modify only if redocly asks
pkg/testutil/openapi_test.go                     modify: every spec loads
web/scripts/gen-api.mjs                          modify: SPECS += payment, ledger
web/src/api/gen/*.ts                             regenerated
web/src/features/orders/order-status.ts, web/src/api/errors.ts   modify: one entry each (tsc)
services/payment/…, services/ledger/…           create (skeletons)
go.work, go.work.sum, Makefile, .github/workflows/ci.yml, .gitignore   modify
deploy/docker-compose.yml, deploy/postgres/init.sql                    modify
deploy/secrets/payment.env.example              create
docs/superpowers/specs/2026-09-29-marketplace-p2-{ws0-contracts,overview,ws3-order,ws5-web}.md   modify: gap notes (Task 8)
```

---

### Task 1: Event catalogue — schemas and `pkg/event`

**Files:**
- Modify: `api/events/envelope.schema.json`, `api/events/order-created.schema.json`, `api/events/order-cancelled.schema.json`, `api/events/order-status-changed.schema.json`
- Create: `api/events/order-completed.schema.json`, `payment-succeeded.schema.json`, `payment-failed.schema.json`, `payment-refunded.schema.json`, `refund-failed.schema.json`, `payout-requested.schema.json`, `payout-settled.schema.json`, `payout-failed.schema.json`
- Modify: `pkg/event/event.go`; Create: `pkg/event/payment.go`
- Test: `pkg/event/event_test.go`

**Interfaces:**
- Produces (used by WS1'–WS3'):
  - Constants: `event.TopicPaymentEvents = "payment.events"`, `event.TopicPayoutEvents = "payout.events"`.
  - Types: `TypeOrderCompleted`, `TypePaymentSucceeded`, `TypePaymentFailed`, `TypePaymentRefunded`, `TypeRefundFailed`, `TypePayoutRequested`, `TypePayoutSettled`, `TypePayoutFailed`.
  - Structs: `OrderCompleted`, `PaymentSucceeded` (with `Allocations []Allocation`), `PaymentFailed`, `PaymentRefunded` (`OrderID *string`), `RefundFailed`, `PayoutRequested`, `PayoutSettled` (`SettledAt time.Time`), `PayoutFailed`.
  - Constants: `CompletedByBuyer/System`, `ProviderVNPay/Stripe/Fake`, `PaymentMethodCOD/VNPay/Stripe`.
  - New optional fields: `OrderCreated.PaymentMethod`, `CommissionRateBps`, `CommissionAmount`; `OrderCancelled.CheckoutID`, `PaymentMethod`.

- [x] **Step 1: Read the existing schemas and test** (`api/events/*.json`, `pkg/event/event_test.go`) to match their style: `allOf` envelope, `additionalProperties: false` on `data`, `format: uuid`.

- [x] **Step 2: Write the failing tests.** Extend the `cases` map in `TestEncodedEventsMatchJSONSchemas`. Every new struct gets one fully populated case. A duplicate `PaymentSucceeded` gets `Allocations: []event.Allocation{}`, and `PaymentRefunded` gets `OrderID: nil`. Add two P2-shaped P1 payloads, and add a new test proving P1 payloads still validate:

```go
// in the cases map (key "file#variant" → payload); u() returns a fresh UUIDv7 string, as the existing test does.
"order-created.schema.json#p2": event.OrderCreated{OrderID: u(), CheckoutID: u(), BuyerID: u(), ShopID: u(), SellerUserID: u(),
    Total: 130_000, Items: []event.OrderItem{{SkuID: u(), Quantity: 1, UnitPrice: 100_000}},
    PaymentMethod: event.PaymentMethodVNPay, CommissionRateBps: 500, CommissionAmount: 5_000},
"order-cancelled.schema.json#p2": event.OrderCancelled{OrderID: u(), Reason: "PAYMENT_TIMEOUT", CancelledBy: event.CancelledBySystem,
    CheckoutID: u(), PaymentMethod: event.PaymentMethodStripe},
"order-status-changed.schema.json#p2": event.OrderStatusChanged{OrderID: u(), From: "AWAITING_PAYMENT", To: "PENDING_CONFIRMATION", ActorRole: "system"},
"order-completed.schema.json": event.OrderCompleted{OrderID: u(), CheckoutID: u(), SellerUserID: u(), PaymentMethod: event.PaymentMethodVNPay,
    Total: 130_000, CommissionAmount: 5_000, CompletedBy: event.CompletedBySystem},
"payment-succeeded.schema.json": event.PaymentSucceeded{PaymentID: u(), CheckoutID: u(), BuyerID: u(), Provider: event.ProviderFake,
    ProviderRef: "fake_123", Amount: 260_000, Allocations: []event.Allocation{{OrderID: u(), SellerUserID: u(), Amount: 130_000, CommissionAmount: 5_000}}},
"payment-succeeded.schema.json#duplicate": event.PaymentSucceeded{PaymentID: u(), CheckoutID: u(), BuyerID: u(), Provider: event.ProviderStripe,
    ProviderRef: "pi_1", Amount: 260_000, Allocations: []event.Allocation{}},
"payment-failed.schema.json": event.PaymentFailed{PaymentID: u(), CheckoutID: u(), Provider: event.ProviderVNPay, Reason: "24"},
"payment-refunded.schema.json": event.PaymentRefunded{RefundID: u(), PaymentID: u(), CheckoutID: u(), OrderID: ptr(u()), Amount: 130_000, Provider: event.ProviderFake},
"payment-refunded.schema.json#duplicate": event.PaymentRefunded{RefundID: u(), PaymentID: u(), CheckoutID: u(), OrderID: nil, Amount: 260_000, Provider: event.ProviderFake},
"refund-failed.schema.json": event.RefundFailed{RefundID: u(), PaymentID: u(), CheckoutID: u(), OrderID: ptr(u()), Amount: 1, Provider: event.ProviderStripe, Reason: "card_declined"},
"payout-requested.schema.json": event.PayoutRequested{PayoutID: u(), SellerUserID: u(), Amount: 50_000},
"payout-settled.schema.json": event.PayoutSettled{PayoutID: u(), SellerUserID: u(), Amount: 50_000, SettledAt: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)},
"payout-failed.schema.json": event.PayoutFailed{PayoutID: u(), SellerUserID: u(), Amount: 50_000, Reason: "simulated"},
```

The existing loop takes the schema file from the map key. Strip a `#suffix` before loading: `file, _, _ := strings.Cut(name, "#")`. Add `func ptr[T any](v T) *T { return &v }` if it is missing.

```go
// P1 payloads (exact JSON order-svc publishes today) must stay valid.
func TestP1PayloadsStillValidate(t *testing.T) {
	c := jsonschema.NewCompiler()
	types := map[string]string{
		"order-created.schema.json": "OrderCreated", "order-cancelled.schema.json": "OrderCancelled",
		"order-status-changed.schema.json": "OrderStatusChanged",
	}
	for file, data := range map[string]string{
		"order-created.schema.json":        `{"order_id":"01890000-0000-7000-8000-000000000001","checkout_id":"01890000-0000-7000-8000-000000000002","buyer_id":"01890000-0000-7000-8000-000000000003","shop_id":"01890000-0000-7000-8000-000000000004","seller_user_id":"01890000-0000-7000-8000-000000000005","total":130000,"items":[{"sku_id":"01890000-0000-7000-8000-000000000006","quantity":1,"unit_price":100000}]}`,
		"order-cancelled.schema.json":      `{"order_id":"01890000-0000-7000-8000-000000000001","reason":"OUT_OF_STOCK","cancelled_by":"system"}`,
		"order-status-changed.schema.json": `{"order_id":"01890000-0000-7000-8000-000000000001","from":"PENDING","to":"PENDING_CONFIRMATION","actor_role":"system"}`,
	} {
		env := `{"event_id":"01890000-0000-7000-8000-0000000000aa","event_type":"` + types[file] + `","version":1,"occurred_at":"2026-09-29T10:00:00Z","data":` + data + `}`
		inst, err := jsonschema.UnmarshalJSON(strings.NewReader(env))
		require.NoError(t, err)
		sch, err := c.Compile(filepath.Join("..", "..", "api", "events", file)) // use the same path construction as the existing schema test
		require.NoError(t, err)
		assert.NoError(t, sch.Validate(inst), file)
	}
}
```

Also add a decode round-trip for the pointer field:

```go
func TestPaymentRefundedNullOrderIDRoundTrips(t *testing.T) {
	b, err := event.Encode(event.PaymentRefunded{RefundID: "r", PaymentID: "p", CheckoutID: "c", Amount: 1, Provider: event.ProviderFake}, time.Now())
	require.NoError(t, err)
	assert.Contains(t, string(b), `"order_id":null`)
	env, err := event.Decode(b)
	require.NoError(t, err)
	got, err := event.DecodeData[event.PaymentRefunded](env)
	require.NoError(t, err)
	assert.Nil(t, got.OrderID)
}
```

- [x] **Step 3: Run the tests and confirm they fail** (build errors on the missing types).

Run: `cd pkg && go test ./event/`
Expected: FAIL (`undefined: event.OrderCompleted`, …).

- [x] **Step 4: Implement `pkg/event`.** In `event.go`:
  - Change the package doc to "order, payment and payout events".
  - Add the topics next to `TopicOrderEvents`.
  - Add the new `Type…` constants to the const block (`TypeOrderCompleted Type = "OrderCompleted"`, and likewise for the other seven).
  - Add the constants:

```go
// TopicPaymentEvents carries payment-svc events; the key is the checkout ID.
const TopicPaymentEvents = "payment.events"

// TopicPayoutEvents carries ledger-svc payout events; the key is the seller user ID.
const TopicPayoutEvents = "payout.events"

// Values of OrderCompleted.CompletedBy.
const (
	CompletedByBuyer  = "buyer"
	CompletedBySystem = "system"
)

// Payment providers (payment.events "provider").
const (
	ProviderVNPay  = "vnpay"
	ProviderStripe = "stripe"
	ProviderFake   = "fake"
)

// Checkout payment methods (order events "payment_method").
const (
	PaymentMethodCOD    = "COD"
	PaymentMethodVNPay  = "VNPAY"
	PaymentMethodStripe = "STRIPE"
)
```

Add the optional fields to the P1 structs. `omitempty` keeps P1 payloads byte-identical:

```go
type OrderCreated struct {
	// … P1 fields unchanged …
	PaymentMethod     string `json:"payment_method,omitempty"`      // P2; absent in P1 payloads
	CommissionRateBps int32  `json:"commission_rate_bps,omitempty"` // P2; 0 for COD
	CommissionAmount  int64  `json:"commission_amount,omitempty"`   // P2; 0 for COD
}

type OrderCancelled struct {
	// … P1 fields unchanged …
	CheckoutID    string `json:"checkout_id,omitempty"`    // P2
	PaymentMethod string `json:"payment_method,omitempty"` // P2
}
```

Create `pkg/event/payment.go`:

```go
package event

import "time"

// OrderCompleted is published by order-svc when an order reaches COMPLETED
// (by the buyer or automatically after AUTO_COMPLETE_AFTER).
type OrderCompleted struct {
	OrderID          string `json:"order_id"`
	CheckoutID       string `json:"checkout_id"`
	SellerUserID     string `json:"seller_user_id"`
	PaymentMethod    string `json:"payment_method"`
	Total            int64  `json:"total"`
	CommissionAmount int64  `json:"commission_amount"`
	CompletedBy      string `json:"completed_by"` // CompletedByBuyer | CompletedBySystem
}

func (OrderCompleted) EventType() Type { return TypeOrderCompleted }

// Allocation is one order's share of a captured payment.
type Allocation struct {
	OrderID          string `json:"order_id"`
	SellerUserID     string `json:"seller_user_id"`
	Amount           int64  `json:"amount"`
	CommissionAmount int64  `json:"commission_amount"`
}

// PaymentSucceeded: a provider confirmed a charge. Allocations is empty
// (never null) for a DUPLICATE payment, which is refunded automatically.
type PaymentSucceeded struct {
	PaymentID   string       `json:"payment_id"`
	CheckoutID  string       `json:"checkout_id"`
	BuyerID     string       `json:"buyer_id"`
	Provider    string       `json:"provider"`
	ProviderRef string       `json:"provider_ref"`
	Amount      int64        `json:"amount"`
	Allocations []Allocation `json:"allocations"`
}

func (PaymentSucceeded) EventType() Type { return TypePaymentSucceeded }

type PaymentFailed struct {
	PaymentID  string `json:"payment_id"`
	CheckoutID string `json:"checkout_id"`
	Provider   string `json:"provider"`
	Reason     string `json:"reason"`
}

func (PaymentFailed) EventType() Type { return TypePaymentFailed }

// PaymentRefunded: OrderID is nil for the refund of a duplicate payment.
type PaymentRefunded struct {
	RefundID   string  `json:"refund_id"`
	PaymentID  string  `json:"payment_id"`
	CheckoutID string  `json:"checkout_id"`
	OrderID    *string `json:"order_id"`
	Amount     int64   `json:"amount"`
	Provider   string  `json:"provider"`
}

func (PaymentRefunded) EventType() Type { return TypePaymentRefunded }

type RefundFailed struct {
	RefundID   string  `json:"refund_id"`
	PaymentID  string  `json:"payment_id"`
	CheckoutID string  `json:"checkout_id"`
	OrderID    *string `json:"order_id"`
	Amount     int64   `json:"amount"`
	Provider   string  `json:"provider"`
	Reason     string  `json:"reason"`
}

func (RefundFailed) EventType() Type { return TypeRefundFailed }

type PayoutRequested struct {
	PayoutID     string `json:"payout_id"`
	SellerUserID string `json:"seller_user_id"`
	Amount       int64  `json:"amount"`
}

func (PayoutRequested) EventType() Type { return TypePayoutRequested }

type PayoutSettled struct {
	PayoutID     string    `json:"payout_id"`
	SellerUserID string    `json:"seller_user_id"`
	Amount       int64     `json:"amount"`
	SettledAt    time.Time `json:"settled_at"`
}

func (PayoutSettled) EventType() Type { return TypePayoutSettled }

type PayoutFailed struct {
	PayoutID     string `json:"payout_id"`
	SellerUserID string `json:"seller_user_id"`
	Amount       int64  `json:"amount"`
	Reason       string `json:"reason"`
}

func (PayoutFailed) EventType() Type { return TypePayoutFailed }
```

- [x] **Step 5: Write the schemas.**

In `envelope.schema.json`:
- set `event_type.enum` to all 11 types: `OrderCreated, OrderStatusChanged, OrderCancelled, OrderCompleted, PaymentSucceeded, PaymentFailed, PaymentRefunded, RefundFailed, PayoutRequested, PayoutSettled, PayoutFailed`;
- change `description` to "Envelope of every message on Kafka topics order.events (key order_id), payment.events (key checkout_id) and payout.events (key seller_user_id)."

In the P1 files:
- add the new properties, but **not** to `required`;
- in `order-status-changed`, add `AWAITING_PAYMENT` to the `from`/`to` status enums (or to the shared status list if the file has one).

Each new file follows the P1 pattern exactly. Here is `payment-succeeded.schema.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "title": "PaymentSucceeded",
  "allOf": [{ "$ref": "envelope.schema.json" }],
  "properties": {
    "event_type": { "const": "PaymentSucceeded" },
    "data": {
      "type": "object",
      "additionalProperties": false,
      "required": ["payment_id", "checkout_id", "buyer_id", "provider", "provider_ref", "amount", "allocations"],
      "properties": {
        "payment_id": { "type": "string", "format": "uuid" },
        "checkout_id": { "type": "string", "format": "uuid" },
        "buyer_id": { "type": "string", "format": "uuid" },
        "provider": { "enum": ["vnpay", "stripe", "fake"] },
        "provider_ref": { "type": "string", "minLength": 1 },
        "amount": { "type": "integer", "minimum": 0 },
        "allocations": {
          "type": "array",
          "description": "Per-order shares; empty for a DUPLICATE payment (refunded automatically).",
          "items": {
            "type": "object",
            "additionalProperties": false,
            "required": ["order_id", "seller_user_id", "amount", "commission_amount"],
            "properties": {
              "order_id": { "type": "string", "format": "uuid" },
              "seller_user_id": { "type": "string", "format": "uuid" },
              "amount": { "type": "integer", "minimum": 0 },
              "commission_amount": { "type": "integer", "minimum": 0 }
            }
          }
        }
      }
    }
  }
}
```

Every other new schema lists exactly the struct fields above in `required`:
- **Amounts:** `{"type":"integer","minimum":0}`.
- **`provider`:** the enum above.
- **`completed_by`:** `{"enum":["buyer","system"]}`.
- **`payment_method`:** `{"enum":["COD","VNPAY","STRIPE"]}`.
- **Nullable order_id** (`payment-refunded`, `refund-failed`): `{"type":["string","null"],"format":"uuid"}`, still listed in `required`.
- **`settled_at`:** `{"type":"string","format":"date-time"}`.
- **`reason`:** `{"type":"string","minLength":1}`.

- [x] **Step 6: Run the tests and confirm they pass.**

Run: `cd pkg && go test ./event/ && cd ../services/order && go test -count=1 ./internal/app/`
Expected: PASS. The order-svc run proves its P1 events still validate against the edited schemas.

- [x] **Step 7: Commit.**

```bash
git add api/events pkg/event
git commit -m "feat(contracts): P2 event catalogue (order completed, payment, payout) with optional P2 fields on order events"
```

---

### Task 2: `pkg/inbox` — idempotent consumer marker

**Files:**
- Create: `pkg/inbox/schema.sql`, `pkg/inbox/inbox.go`, `pkg/inbox/inbox_test.go`

**Interfaces:**
- Produces:
  - `inbox.SchemaSQL string`, which services copy verbatim into a migration.
  - `inbox.Execer` interface `{ Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) }`, satisfied by `pgx.Tx`, `*pgxpool.Pool` and `*pgx.Conn`.
  - `inbox.MarkProcessed(ctx context.Context, db Execer, consumer string, eventID string) (firstTime bool, err error)`.

- [ ] **Step 1: Write the failing test** (a Postgres container through `testutil.Postgres`; skipped with `-short`, like the other container tests):

```go
package inbox_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/inbox"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, testutil.Postgres(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, inbox.SchemaSQL)
	require.NoError(t, err)
	return pool
}

func TestMarkProcessed(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	const id = "01890000-0000-7000-8000-000000000001"

	first, err := inbox.MarkProcessed(ctx, pool, "ledger-svc", id)
	require.NoError(t, err)
	assert.True(t, first)

	again, err := inbox.MarkProcessed(ctx, pool, "ledger-svc", id)
	require.NoError(t, err)
	assert.False(t, again, "a redelivered event is a duplicate")

	other, err := inbox.MarkProcessed(ctx, pool, "order-svc", id)
	require.NoError(t, err)
	assert.True(t, other, "duplicates are scoped per consumer")
}

func TestMarkProcessedRollsBackWithTheEffect(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	const id = "01890000-0000-7000-8000-000000000002"
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	first, err := inbox.MarkProcessed(ctx, tx, "payment-svc", id)
	require.NoError(t, err)
	require.True(t, first)
	require.NoError(t, tx.Rollback(ctx)) // the consumer's effect failed

	first, err = inbox.MarkProcessed(ctx, pool, "payment-svc", id)
	require.NoError(t, err)
	assert.True(t, first, "the redelivery is processed again")
}

func TestSchemaIsIdempotent(t *testing.T) {
	pool := newPool(t)
	_, err := pool.Exec(context.Background(), inbox.SchemaSQL)
	assert.NoError(t, err, "SchemaSQL can be applied twice")
}
```

- [ ] **Step 2: Run the test and confirm it fails.** Run: `cd pkg && go test ./inbox/`. Expected: build failure because package `inbox` has no Go files.

- [ ] **Step 3: Implement.** `schema.sql`:

```sql
-- Idempotent-consumer marker (pkg/inbox). Services copy this into one of
-- their migrations and call MarkProcessed in the same transaction as the
-- event's effect.
CREATE TABLE IF NOT EXISTS processed_events (
    consumer     text        NOT NULL,
    event_id     uuid        NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (consumer, event_id)
);
```

`inbox.go`:

```go
// Package inbox records which events a consumer has already applied, so a
// redelivered Kafka message is recognised and skipped (ADR-0003).
package inbox

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// SchemaSQL creates processed_events. Copy it verbatim into a migration.
//
//go:embed schema.sql
var SchemaSQL string

// Execer is satisfied by pgx.Tx, *pgxpool.Pool and *pgx.Conn.
type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// MarkProcessed records (consumer, eventID). firstTime is false when it was
// already recorded, in which case the caller must skip the event. Call it in
// the same transaction as the event's effect, so both commit or neither does.
func MarkProcessed(ctx context.Context, db Execer, consumer, eventID string) (firstTime bool, err error) {
	tag, err := db.Exec(ctx,
		`INSERT INTO processed_events (consumer, event_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		consumer, eventID)
	if err != nil {
		return false, fmt.Errorf("inbox: mark %s/%s: %w", consumer, eventID, err)
	}
	return tag.RowsAffected() == 1, nil
}
```

- [ ] **Step 4: Run the tests and confirm they pass.** Run: `cd pkg && go test -race ./inbox/ && go vet ./inbox/`. Expected: PASS.

- [ ] **Step 5: Commit.** `git add pkg/inbox && git commit -m "feat(pkg): inbox.MarkProcessed, a per-consumer processed_events marker"`

---

### Task 3: `pkg/idemkey` — Idempotency-Key store with orphan reclaim

**Files:**
- Create: `pkg/idemkey/schema.sql`, `pkg/idemkey/idemkey.go`, `pkg/idemkey/idemkey_test.go`

**Semantics.** These are exactly those of order-svc after P1.5; read `services/order/README.md` decisions 4 and 17, and `services/order/internal/adapter/postgres/idempotency.go`.
- `created_at` **is the claim**.
- `Claim` inserts the row. If a row already exists, it reclaims it when the row has the same hash, no response, and `created_at < staleBefore` (the zero `staleBefore` never reclaims).
- `Complete` and `Release` act only while `created_at = claimedAt`. `Complete` returns `ErrClaimLost` otherwise.
- `Get` is a pure read that never inserts.
- Timestamps are truncated to microseconds before they are used as a claim.

**Interfaces:**
- Produces:
  - `idemkey.SchemaSQL string`.
  - `idemkey.DB` interface: `Exec(ctx, sql, args...) (pgconn.CommandTag, error)` + `QueryRow(ctx, sql, args...) pgx.Row`, satisfied by `pgx.Tx` and `*pgxpool.Pool`.
  - `type Response struct{ Status int; Body []byte }`.
  - `type Record struct{ RequestHash []byte; Response *Response; ClaimedAt time.Time }`.
  - `var ErrClaimLost = errors.New("idemkey: claim lost to a newer request")`.
  - `func Claim(ctx context.Context, db DB, scope, userID, key string, requestHash []byte, at, staleBefore time.Time) (rec Record, created bool, err error)`.
  - `func Get(ctx context.Context, db DB, scope, userID, key string) (rec Record, found bool, err error)`.
  - `func Complete(ctx context.Context, db DB, scope, userID, key string, claimedAt time.Time, resp Response) error`.
  - `func Release(ctx context.Context, db DB, scope, userID, key string, claimedAt time.Time) error`.
  - `func Purge(ctx context.Context, db DB, before time.Time) (int64, error)`.

- [ ] **Step 1: Write the failing tests.** Use the Task 2 `newPool` pattern, applying `idemkey.SchemaSQL`. Package-level: `const scope = "payment.create"`, `var base = time.Now().UTC().Truncate(time.Microsecond)`, `var never time.Time`.

```go
func TestClaimCompleteReplay(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000001"
	rec, created, err := idemkey.Claim(ctx, db, scope, user, "k1", []byte("h"), base, never)
	require.NoError(t, err)
	require.True(t, created)
	assert.Equal(t, base, rec.ClaimedAt)

	_, created, err = idemkey.Claim(ctx, db, scope, user, "k1", []byte("h"), base, never)
	require.NoError(t, err)
	assert.False(t, created, "in progress")

	resp := idemkey.Response{Status: 201, Body: []byte(`{"id":"p1"}`)}
	require.NoError(t, idemkey.Complete(ctx, db, scope, user, "k1", base, resp))
	rec, created, err = idemkey.Claim(ctx, db, scope, user, "k1", []byte("h"), base, never)
	require.NoError(t, err)
	assert.False(t, created)
	require.NotNil(t, rec.Response)
	assert.Equal(t, 201, rec.Response.Status)
	assert.JSONEq(t, `{"id":"p1"}`, string(rec.Response.Body))
}

func TestDifferentHashIsReportedForReuse(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000002"
	_, _, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("a"), base, never)
	require.NoError(t, err)
	rec, created, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("b"), base, base.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, created, "a different request never takes a key over, even when stale")
	assert.Equal(t, []byte("a"), rec.RequestHash, "the caller compares hashes and answers IDEMPOTENCY_KEY_REUSED")
}

func TestKeysAreScopedByScopeAndUser(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	u1, u2 := "01890000-0000-7000-8000-000000000003", "01890000-0000-7000-8000-000000000004"
	for _, c := range []struct{ scope, user string }{{scope, u1}, {scope, u2}, {"ledger.payout", u1}} {
		_, created, err := idemkey.Claim(ctx, db, c.scope, c.user, "same", []byte("h"), base, never)
		require.NoError(t, err)
		assert.True(t, created, "%+v", c)
	}
}

func TestOrphanIsReclaimedOnlyWhenStale(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000005"
	old := base.Add(-10 * time.Minute)
	_, _, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), old, never)
	require.NoError(t, err)

	_, created, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base, old)
	require.NoError(t, err)
	assert.False(t, created, "not older than staleBefore")

	rec, created, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base, base.Add(-5*time.Minute))
	require.NoError(t, err)
	assert.True(t, created, "same request, no response, stale → reclaimed")
	assert.Equal(t, base, rec.ClaimedAt)

	// The zombie that held `old` cannot complete or release the new claim.
	err = idemkey.Complete(ctx, db, scope, user, "k", old, idemkey.Response{Status: 201, Body: []byte(`{}`)})
	assert.ErrorIs(t, err, idemkey.ErrClaimLost)
	require.NoError(t, idemkey.Release(ctx, db, scope, user, "k", old))
	rec, found, err := idemkey.Get(ctx, db, scope, user, "k")
	require.NoError(t, err)
	require.True(t, found, "the zombie's release was a no-op")
	assert.Equal(t, base, rec.ClaimedAt)
	assert.Nil(t, rec.Response, "nor did its completion land")
}

func TestCompletedKeyIsNeverReclaimed(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000006"
	old := base.Add(-time.Hour)
	_, _, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), old, never)
	require.NoError(t, err)
	require.NoError(t, idemkey.Complete(ctx, db, scope, user, "k", old, idemkey.Response{Status: 409, Body: []byte(`{}`)}))
	rec, created, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base, base)
	require.NoError(t, err)
	assert.False(t, created)
	assert.NotNil(t, rec.Response)
}

func TestGetNeverInserts(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000007"
	for range 2 {
		_, found, err := idemkey.Get(ctx, db, scope, user, "missing")
		require.NoError(t, err)
		assert.False(t, found)
	}
}

func TestReleaseFreesTheKeyAndPurgeDeletesOldRows(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000008"
	_, _, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base, never)
	require.NoError(t, err)
	require.NoError(t, idemkey.Release(ctx, db, scope, user, "k", base))
	_, created, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base, never)
	require.NoError(t, err)
	assert.True(t, created)

	_, _, err = idemkey.Claim(ctx, db, scope, user, "old", []byte("h"), base.Add(-25*time.Hour), never)
	require.NoError(t, err)
	n, err := idemkey.Purge(ctx, db, base.Add(-24*time.Hour))
	require.NoError(t, err)
	assert.GreaterOrEqual(t, n, int64(1))
	_, found, err := idemkey.Get(ctx, db, scope, user, "old")
	require.NoError(t, err)
	assert.False(t, found)
}

func TestClaimTruncatesToMicroseconds(t *testing.T) {
	ctx, db := context.Background(), newPool(t)
	user := "01890000-0000-7000-8000-000000000009"
	rec, _, err := idemkey.Claim(ctx, db, scope, user, "k", []byte("h"), base.Add(123*time.Nanosecond), never)
	require.NoError(t, err)
	require.NoError(t, idemkey.Complete(ctx, db, scope, user, "k", rec.ClaimedAt, idemkey.Response{Status: 201, Body: []byte(`{}`)}),
		"the returned claim matches the stored µs timestamp")
}
```

- [ ] **Step 2: Run the tests and confirm they fail.** Run: `cd pkg && go test ./idemkey/`. Expected: build failure.

- [ ] **Step 3: Implement.** `schema.sql`:

```sql
-- Idempotency-Key store (pkg/idemkey). created_at is the claim: completing
-- or releasing a key requires it (see idemkey.go). Copy into a migration.
CREATE TABLE IF NOT EXISTS idempotency_keys (
    scope        text        NOT NULL,
    user_id      uuid        NOT NULL,
    key          text        NOT NULL,
    request_hash bytea       NOT NULL,
    response     jsonb       NULL,
    status_code  int         NULL,
    created_at   timestamptz NOT NULL,
    PRIMARY KEY (scope, user_id, key)
);

CREATE INDEX IF NOT EXISTS idempotency_keys_created_idx ON idempotency_keys (created_at);
```

`idemkey.go`:

```go
// Package idemkey stores Idempotency-Key claims and their final responses
// (P1 WS3 §4 step 1, with the P1.5 reclaim of orphaned rows).
//
// created_at is the claim. Claim inserts a row, or takes over an orphaned one:
// same request hash, no response, and claimed before staleBefore. Complete and
// Release act only while the caller's claim is still the row's claim, so a
// run whose key was reclaimed can neither overwrite nor free it.
package idemkey

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SchemaSQL creates idempotency_keys. Copy it verbatim into a migration.
//
//go:embed schema.sql
var SchemaSQL string

// ErrClaimLost: a newer request reclaimed the key; roll the caller's transaction back.
var ErrClaimLost = errors.New("idemkey: claim lost to a newer request")

// DB is satisfied by pgx.Tx and *pgxpool.Pool.
type DB interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

// Response is a rendered HTTP response stored for replays.
type Response struct {
	Status int
	Body   []byte
}

// Record is an existing key. Response is nil while its request is in progress.
type Record struct {
	RequestHash []byte
	Response    *Response
	ClaimedAt   time.Time
}

// Claim claims (scope, userID, key) at `at`. created reports whether the caller
// now holds the claim (inserted, or reclaimed an orphan; the zero staleBefore
// never reclaims). Otherwise rec describes the existing row: compare its hash
// (different → key reused), then its Response (nil → in progress).
func Claim(ctx context.Context, db DB, scope, userID, key string, requestHash []byte, at, staleBefore time.Time) (Record, bool, error) {
	at = at.UTC().Truncate(time.Microsecond) // Postgres stores µs; the claim is compared for equality
	claimed := Record{RequestHash: requestHash, ClaimedAt: at}
	tag, err := db.Exec(ctx, `INSERT INTO idempotency_keys (scope, user_id, key, request_hash, created_at)
		VALUES ($1, $2, $3, $4, $5) ON CONFLICT DO NOTHING`, scope, userID, key, requestHash, at)
	if err != nil {
		return Record{}, false, fmt.Errorf("idemkey: insert: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return claimed, true, nil
	}
	tag, err = db.Exec(ctx, `UPDATE idempotency_keys SET created_at = $5
		WHERE scope = $1 AND user_id = $2 AND key = $3 AND request_hash = $4
		  AND response IS NULL AND created_at < $6`, scope, userID, key, requestHash, at, staleBefore)
	if err != nil {
		return Record{}, false, fmt.Errorf("idemkey: reclaim: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return claimed, true, nil
	}
	rec, found, err := Get(ctx, db, scope, userID, key)
	if err != nil {
		return Record{}, false, err
	}
	if !found { // released between our insert and this read: report "in progress" so the client retries
		return Record{RequestHash: requestHash}, false, nil
	}
	return rec, false, nil
}

// Get reads a key without claiming it.
func Get(ctx context.Context, db DB, scope, userID, key string) (Record, bool, error) {
	var rec Record
	var body []byte
	var status *int32
	err := db.QueryRow(ctx, `SELECT request_hash, response, status_code, created_at FROM idempotency_keys
		WHERE scope = $1 AND user_id = $2 AND key = $3`, scope, userID, key).Scan(&rec.RequestHash, &body, &status, &rec.ClaimedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, fmt.Errorf("idemkey: get: %w", err)
	}
	rec.ClaimedAt = rec.ClaimedAt.UTC()
	if status != nil {
		rec.Response = &Response{Status: int(*status), Body: body}
	}
	return rec, true, nil
}

// Complete stores the final response. ErrClaimLost when claimedAt is no
// longer the key's claim.
func Complete(ctx context.Context, db DB, scope, userID, key string, claimedAt time.Time, resp Response) error {
	tag, err := db.Exec(ctx, `UPDATE idempotency_keys SET response = $5, status_code = $6
		WHERE scope = $1 AND user_id = $2 AND key = $3 AND created_at = $4`,
		scope, userID, key, claimedAt, resp.Body, resp.Status)
	if err != nil {
		return fmt.Errorf("idemkey: complete: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrClaimLost
	}
	return nil
}

// Release frees the key after a transient failure, if claimedAt is still its claim.
func Release(ctx context.Context, db DB, scope, userID, key string, claimedAt time.Time) error {
	if _, err := db.Exec(ctx, `DELETE FROM idempotency_keys
		WHERE scope = $1 AND user_id = $2 AND key = $3 AND created_at = $4`, scope, userID, key, claimedAt); err != nil {
		return fmt.Errorf("idemkey: release: %w", err)
	}
	return nil
}

// Purge deletes keys claimed before `before` (callers use a 24 h TTL).
func Purge(ctx context.Context, db DB, before time.Time) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM idempotency_keys WHERE created_at < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("idemkey: purge: %w", err)
	}
	return tag.RowsAffected(), nil
}
```

`response` is `jsonb`. If pgx does not encode `[]byte` as jsonb text, pass `json.RawMessage(resp.Body)` in `Complete` and scan into `json.RawMessage` in `Get`.

- [ ] **Step 4: Run the tests and confirm they pass.** Run: `cd pkg && go test -race ./idemkey/ && go vet ./idemkey/`. Expected: PASS.

- [ ] **Step 5: Commit.** `git add pkg/idemkey && git commit -m "feat(pkg): idemkey Idempotency-Key store with claim-guarded complete/release and orphan reclaim"`

---

### Task 4: gRPC contracts (`order/v1`, `payment/v1`) and generated code

**Files:**
- Create: `proto/order/v1/order.proto`, `proto/payment/v1/payment.proto`
- Generated: `gen/order/v1/order.pb.go`, `order_grpc.pb.go`, `gen/payment/v1/payment.pb.go`, `payment_grpc.pb.go`
- Test: `gen/contract_test.go` (new)
- Modify: `proto/buf.yaml` (comment only)

**Interfaces:**
- Produces:
  - `orderv1.OrderInternalServer/Client` with `GetCheckout(ctx, *orderv1.GetCheckoutRequest) (*orderv1.GetCheckoutResponse, error)`.
  - `paymentv1.PaymentInternalServer/Client` with `CancelCheckoutPayments`.
  - `paymentv1.Outcome_OUTCOME_NOT_PAID` and `Outcome_OUTCOME_PAID`.

- [ ] **Step 1: Write the failing test** `gen/contract_test.go`:

```go
package gen_test

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/ngoxuanchien/e-commerce/gen/order/v1"
	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
)

var (
	_ orderv1.OrderInternalServer     = orderv1.UnimplementedOrderInternalServer{}
	_ paymentv1.PaymentInternalServer = paymentv1.UnimplementedPaymentInternalServer{}
)

func TestP2MessagesRoundTrip(t *testing.T) {
	in := &orderv1.GetCheckoutResponse{
		CheckoutId: "c", BuyerId: "b", PaymentMethod: "VNPAY", PaymentDeadline: timestamppb.Now(),
		Orders: []*orderv1.CheckoutOrder{{OrderId: "o", SellerUserId: "s", Status: "AWAITING_PAYMENT", Subtotal: 100, Total: 130, CommissionAmount: 5}},
	}
	b, err := proto.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out orderv1.GetCheckoutResponse
	if err := proto.Unmarshal(b, &out); err != nil || !proto.Equal(in, &out) {
		t.Fatalf("round trip: %v", err)
	}
	if paymentv1.Outcome_OUTCOME_PAID.Number() != 2 || paymentv1.Outcome_OUTCOME_NOT_PAID.Number() != 1 {
		t.Fatal("Outcome numbers are part of the contract")
	}
}
```

- [ ] **Step 2: Run the test and confirm it fails.** Run: `cd gen && go test ./...`. Expected: FAIL because packages `gen/order/v1` and `gen/payment/v1` are not found.

- [ ] **Step 3: Write the protos** in the P1 header style (`proto/catalog/v1/catalog.proto`). `proto/order/v1/order.proto`:

```proto
syntax = "proto3";

package order.v1;

import "google/protobuf/timestamp.proto";

option go_package = "github.com/ngoxuanchien/e-commerce/gen/order/v1;orderv1";

// OrderInternal is order-svc's service-to-service API (not exposed publicly).
service OrderInternal {
  // GetCheckout returns a checkout's orders for payment-svc. NOT_FOUND if unknown.
  // The server doesn't authorize: payment-svc compares buyer_id with its caller.
  rpc GetCheckout(GetCheckoutRequest) returns (GetCheckoutResponse);
}

message GetCheckoutRequest {
  string checkout_id = 1;
}

message GetCheckoutResponse {
  string checkout_id = 1;
  string buyer_id = 2;
  // COD | VNPAY | STRIPE
  string payment_method = 3;
  // Unset for COD.
  google.protobuf.Timestamp payment_deadline = 4;
  // Ordered by order_id.
  repeated CheckoutOrder orders = 5;
}

message CheckoutOrder {
  string order_id = 1;
  string seller_user_id = 2;
  // Order status, e.g. AWAITING_PAYMENT.
  string status = 3;
  // VND.
  int64 subtotal = 4;
  // VND, subtotal + shipping fee.
  int64 total = 5;
  // VND, snapshotted at checkout; 0 for COD.
  int64 commission_amount = 6;
}
```

`proto/payment/v1/payment.proto`:

```proto
syntax = "proto3";

package payment.v1;

option go_package = "github.com/ngoxuanchien/e-commerce/gen/payment/v1;paymentv1";

// PaymentInternal is payment-svc's service-to-service API (not exposed publicly).
service PaymentInternal {
  // CancelCheckoutPayments makes sure no attempt of the checkout can still
  // succeed, then reports whether one did. Idempotent. UNAVAILABLE when a
  // provider can't be asked; the caller retries later.
  rpc CancelCheckoutPayments(CancelCheckoutPaymentsRequest) returns (CancelCheckoutPaymentsResponse);
}

message CancelCheckoutPaymentsRequest {
  string checkout_id = 1;
}

message CancelCheckoutPaymentsResponse {
  Outcome outcome = 1;
}

// Outcome of CancelCheckoutPayments.
enum Outcome {
  OUTCOME_UNSPECIFIED = 0;
  // No attempt succeeded; none can succeed any more.
  OUTCOME_NOT_PAID = 1;
  // An attempt succeeded; the checkout is paid.
  OUTCOME_PAID = 2;
}
```

The `SERVICE_SUFFIX` exception in `proto/buf.yaml` already covers the `…Internal` names. Update its comment to list OrderInternal and PaymentInternal too.

- [ ] **Step 4: Generate the code and run the checks.**

Run: `make lint-proto && make gen-proto && (cd gen && go test ./... && go vet ./...) && bin/buf breaking proto --against ".git#ref=refs/heads/main,subdir=proto"`
Expected:
- lint clean;
- the four new `.pb.go` files appear;
- tests pass;
- no breaking changes.

- [ ] **Step 5: Commit.** `git add proto gen && git commit -m "feat(contracts): OrderInternal.GetCheckout and PaymentInternal.CancelCheckoutPayments gRPC contracts"`

---

### Task 5: REST contracts (`common`, `order`, `payment`, `ledger`) and the web fallout

**Files:**
- Modify: `api/openapi/common.yaml`, `api/openapi/order.yaml`
- Create: `api/openapi/payment.yaml`, `api/openapi/ledger.yaml`
- Modify (only if lint requires): `api/.redocly.lint-ignore.yaml`
- Modify: `web/scripts/gen-api.mjs` (SPECS), regenerate `web/src/api/gen/*.ts`, `web/src/features/orders/order-status.ts`, `web/src/api/errors.ts`
- Test: `pkg/testutil/openapi_test.go` (every spec loads and validates in kin-openapi)

**Interfaces:**
- Produces: the REST contracts WS1', WS2', WS4' and WS5' implement or consume (spec §3). Operation IDs (WS4' and WS5' rely on these names):
  - payment: `createPayment`, `getPayment`, `vnpayIpn`, `vnpayReturn`, `stripeWebhook`, `stripeReturn`, `fakeCheckoutPage`, `fakeCheckoutSubmit`, `fakeReturn`, `fakeWebhook`;
  - ledger: `getSellerWallet`, `listSellerWalletEntries`, `createSellerPayout`, `listSellerPayouts`.

- [ ] **Step 1: Write the failing test** in `pkg/testutil/openapi_test.go`:

```go
func TestAllOpenAPISpecsLoad(t *testing.T) {
	for _, name := range []string{"common", "identity", "catalog", "order", "payment", "ledger"} {
		t.Run(name, func(t *testing.T) {
			doc := testutil.LoadOpenAPI(t, filepath.Join("..", "..", "api", "openapi", name+".yaml"))
			if name != "payment" {
				return
			}
			op := doc.Paths.Find("/api/payments/vnpay/ipn").Get
			require.NotNil(t, op)
			assert.EqualValues(t, true, op.Extensions["x-provider-callback"], "provider callbacks are marked")
			require.NotNil(t, op.Security)
			assert.Empty(t, *op.Security, "provider callbacks are public")
		})
	}
}
```

Adapt `Paths.Find` and `Extensions` access to the kin-openapi version in `pkg/go.mod`. If extensions come back as raw JSON, unmarshal before comparing.

Run: `cd pkg && go test ./testutil/ -run TestAllOpenAPISpecsLoad`. Expected: FAIL (`payment.yaml`: no such file).

- [ ] **Step 2: `common.yaml`.**
  - Add `INSUFFICIENT_FUNDS` to `ErrorCode.enum`, placed after `OUT_OF_STOCK`.
  - Extend the `Error.details` description with the line `INSUFFICIENT_FUNDS: {"available": <Money>}.`
  - Extend the `Conflict` response description to `(CONFLICT, OUT_OF_STOCK, INVALID_STATE_TRANSITION, INSUFFICIENT_FUNDS)`.

- [ ] **Step 3: `order.yaml`** (spec §3.1; new `Order` fields are **optional**):
  - `CheckoutRequest.payment_method`: `enum: [COD, VNPAY, STRIPE]`. Its description says a method missing from `ONLINE_PAYMENT_METHODS` answers 400 VALIDATION_ERROR (`details.fields.payment_method`), and that online orders are created in `AWAITING_PAYMENT`.
  - `Order.payment_method`: same enum.
  - `OrderStatus.enum`: add `AWAITING_PAYMENT` (before `PENDING_CONFIRMATION`). The list filter `status` query parameter must accept it: check that it references `OrderStatus`, and add the value if it has its own enum.
  - `OrderShop`: add optional `slug` (`type: string`, description "Shop slug for links (P2; optional until WS3' marks it required)").
  - Add these `Order` properties, **not** in `required`, each with the description suffix "(P2; optional until WS3' marks it required)":
    - `payment_status: {type: string, enum: [NONE, UNPAID, PAID, REFUND_PENDING, REFUNDED]}`, described "NONE for COD";
    - `payment_deadline: {type: [string, 'null'], format: date-time}`, described "null for COD";
    - `commission_rate_bps: {type: integer, minimum: 0, maximum: 10000}`, described "0 for COD";
    - `commission_amount`: `allOf: [$ref common Money]` plus a description (the `Cart.subtotal` style), "0 for COD";
    - `delivered_at: {type: [string, 'null'], format: date-time}`.
  - `POST /api/orders/{id}/cancel` description: also valid in `AWAITING_PAYMENT`. The response is the requested order, and the other `AWAITING_PAYMENT` orders of the same checkout are cancelled too.

- [ ] **Step 4: Create `payment.yaml`.** Match `order.yaml`'s header style exactly: `openapi` version string, `info`, `servers`, how it `$ref`s `common.yaml` responses and the bearer scheme. Use this content:

```yaml
openapi: 3.1.0
info:
  title: Marketplace Payment API
  version: 2.0.0
  description: Online payments (VNPay, Stripe, FAKE) served by payment-svc. WS0' §3.2.
servers:
  - url: http://localhost:8080
tags:
  - name: payments
  - name: provider-callbacks
paths:
  /api/payments:
    post:
      tags: [payments]
      operationId: createPayment
      summary: Start (or resume) paying a checkout. One open attempt per checkout.
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/IdempotencyKey'
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/CreatePaymentRequest'
      responses:
        '201':
          description: The payment attempt.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Payment'
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '409':
          description: INVALID_STATE_TRANSITION — COD checkout, no order AWAITING_PAYMENT, or the payment deadline passed.
          content:
            application/json:
              schema:
                $ref: 'common.yaml#/components/schemas/Error'
        '422':
          description: IDEMPOTENCY_KEY_REUSED — the key was used with a different request.
          content:
            application/json:
              schema:
                $ref: 'common.yaml#/components/schemas/Error'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/payments/{id}:
    get:
      tags: [payments]
      operationId: getPayment
      summary: The caller's own payment (404 for anyone else's).
      security:
        - bearerAuth: []
      parameters:
        - $ref: '#/components/parameters/PaymentId'
      responses:
        '200':
          description: The payment.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/Payment'
        '401':
          $ref: 'common.yaml#/components/responses/Unauthenticated'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
        '429':
          $ref: 'common.yaml#/components/responses/RateLimited'
        5XX:
          $ref: 'common.yaml#/components/responses/ServerError'
  /api/payments/vnpay/ipn:
    get:
      tags: [provider-callbacks]
      operationId: vnpayIpn
      summary: VNPay IPN. Always 200; the outcome is carried in RspCode (WS1' §5.1).
      x-provider-callback: true
      security: []
      parameters:
        - $ref: '#/components/parameters/VnpayParams'
      responses:
        '200':
          description: Acknowledgement for VNPay.
          content:
            application/json:
              schema:
                $ref: '#/components/schemas/VnpayIpnAck'
  /api/payments/vnpay/return:
    get:
      tags: [provider-callbacks]
      operationId: vnpayReturn
      summary: Browser return from VNPay; redirects to the result page. Never changes state.
      x-provider-callback: true
      security: []
      parameters:
        - $ref: '#/components/parameters/VnpayParams'
      responses:
        '302':
          $ref: '#/components/responses/ResultRedirect'
  /api/payments/stripe/webhook:
    post:
      tags: [provider-callbacks]
      operationId: stripeWebhook
      summary: Stripe webhook; the raw body is verified against the Stripe-Signature header.
      x-provider-callback: true
      security: []
      parameters:
        - name: Stripe-Signature
          in: header
          required: true
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              additionalProperties: true
      responses:
        '200':
          description: Accepted (including events that are ignored).
        '400':
          description: Bad or missing signature.
          content:
            application/json:
              schema:
                $ref: 'common.yaml#/components/schemas/Error'
  /api/payments/stripe/return:
    get:
      tags: [provider-callbacks]
      operationId: stripeReturn
      summary: Browser return from Stripe Checkout; redirects to the result page.
      x-provider-callback: true
      security: []
      parameters:
        - name: session_id
          in: query
          required: true
          schema:
            type: string
            minLength: 1
      responses:
        '302':
          $ref: '#/components/responses/ResultRedirect'
  /api/payments/fake/checkout/{id}:
    get:
      tags: [provider-callbacks]
      operationId: fakeCheckoutPage
      summary: FAKE provider's simulated hosted payment page.
      x-provider-callback: true
      security: []
      parameters:
        - $ref: '#/components/parameters/PaymentId'
      responses:
        '200':
          description: HTML page with "succeed" and "fail" buttons.
          content:
            text/html:
              schema:
                type: string
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
    post:
      tags: [provider-callbacks]
      operationId: fakeCheckoutSubmit
      summary: Submit the simulated page; schedules the signed FAKE webhook and redirects to fake/return.
      x-provider-callback: true
      security: []
      parameters:
        - $ref: '#/components/parameters/PaymentId'
      requestBody:
        required: true
        content:
          application/x-www-form-urlencoded:
            schema:
              type: object
              required: [outcome]
              properties:
                outcome:
                  type: string
                  enum: [succeed, fail]
      responses:
        '302':
          description: Redirect to /api/payments/fake/return?payment_id=….
          headers:
            Location:
              schema:
                type: string
        '400':
          $ref: 'common.yaml#/components/responses/ValidationError'
        '404':
          $ref: 'common.yaml#/components/responses/NotFound'
  /api/payments/fake/return:
    get:
      tags: [provider-callbacks]
      operationId: fakeReturn
      summary: Browser return from the FAKE page; redirects to the result page.
      x-provider-callback: true
      security: []
      parameters:
        - name: payment_id
          in: query
          required: true
          schema:
            $ref: 'common.yaml#/components/schemas/Id'
      responses:
        '302':
          $ref: '#/components/responses/ResultRedirect'
  /api/payments/fake/webhook:
    post:
      tags: [provider-callbacks]
      operationId: fakeWebhook
      summary: FAKE provider's signed asynchronous callback.
      x-provider-callback: true
      security: []
      parameters:
        - name: X-Fake-Signature
          in: header
          required: true
          description: Hex HMAC-SHA256 of the raw body with FAKE_WEBHOOK_SECRET.
          schema:
            type: string
      requestBody:
        required: true
        content:
          application/json:
            schema:
              $ref: '#/components/schemas/FakeWebhookEvent'
      responses:
        '200':
          description: Accepted.
        '400':
          description: Bad signature or body.
          content:
            application/json:
              schema:
                $ref: 'common.yaml#/components/schemas/Error'
components:
  securitySchemes:
    bearerAuth:
      $ref: 'common.yaml#/components/securitySchemes/bearerAuth'
  parameters:
    IdempotencyKey:
      name: Idempotency-Key
      in: header
      required: true
      schema:
        type: string
        minLength: 1
        maxLength: 100
    PaymentId:
      name: id
      in: path
      required: true
      schema:
        $ref: 'common.yaml#/components/schemas/Id'
    VnpayParams:
      name: vnp_params
      in: query
      required: false
      description: VNPay's own vnp_* query parameters, passed through unchanged and verified by vnp_SecureHash.
      style: form
      explode: true
      schema:
        type: object
        additionalProperties:
          type: string
  responses:
    ResultRedirect:
      description: Redirect to the web result page, /checkout/result?payment_id=….
      headers:
        Location:
          schema:
            type: string
  schemas:
    CreatePaymentRequest:
      type: object
      additionalProperties: false
      required: [checkout_id]
      properties:
        checkout_id:
          $ref: 'common.yaml#/components/schemas/Id'
    Payment:
      type: object
      required: [id, checkout_id, method, provider, status, amount, redirect_url, expires_at, failure_reason, created_at, updated_at]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        checkout_id:
          $ref: 'common.yaml#/components/schemas/Id'
        method:
          type: string
          enum: [VNPAY, STRIPE]
        provider:
          type: string
          enum: [vnpay, stripe, fake]
          description: Which adapter serves the method (FAKE in local dev, CI and e2e).
        status:
          type: string
          enum: [PENDING, SUCCEEDED, FAILED, EXPIRED, DUPLICATE]
          description: DUPLICATE is a successful payment that isn't the checkout's capturing payment; it is refunded automatically.
        amount:
          $ref: 'common.yaml#/components/schemas/Money'
        redirect_url:
          type: [string, 'null']
          format: uri
          description: Where to send the buyer; null once the payment is terminal.
        expires_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
        failure_reason:
          type: [string, 'null']
        created_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
        updated_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
    VnpayIpnAck:
      type: object
      required: [RspCode, Message]
      properties:
        RspCode:
          type: string
          description: 00 confirmed, 01 order not found, 02 already confirmed, 04 invalid amount, 97 invalid signature, 99 unknown error.
        Message:
          type: string
    FakeWebhookEvent:
      type: object
      required: [id, type, payment_id, provider_ref, amount]
      properties:
        id:
          type: string
          minLength: 1
          description: Event id; deduplicated by the webhook inbox.
        type:
          type: string
          enum: [payment.succeeded, payment.failed, refund.succeeded, refund.failed]
        payment_id:
          $ref: 'common.yaml#/components/schemas/Id'
        refund_id:
          type: [string, 'null']
        provider_ref:
          type: string
        amount:
          $ref: 'common.yaml#/components/schemas/Money'
        reason:
          type: [string, 'null']
```

Two checks before committing:
- **`VnpayParams`:** if redocly or kin-openapi reject the free-form object query parameter, replace it with explicit optional `string` query parameters `vnp_Amount, vnp_TxnRef, vnp_ResponseCode, vnp_TransactionStatus, vnp_TransactionNo, vnp_SecureHash`. Say in the description that other `vnp_*` parameters pass through.
- **`FakeWebhookEvent`:** this is the FAKE provider's own wire format. Read WS1' spec §5 (`…-p2-ws1-payment.md`). Where WS1' already names these fields differently, WS1' wins, so match it here.

- [ ] **Step 5: Create `ledger.yaml`** (spec §3.3). Use the same header style as payment.yaml. Every operation has `security: [bearerAuth: []]` and `x-required-role: seller`, like `catalog.yaml`'s seller routes.

Operations:
- **`GET /api/seller/wallet`** (`getSellerWallet`): 200 `Wallet`, plus 401 / 403 / 429 / 5XX.
- **`GET /api/seller/wallet/entries`** (`listSellerWalletEntries`):
  - parameters `common.yaml#/components/parameters/Limit` and `Cursor`;
  - 200 `WalletEntryPage`;
  - 400 `ValidationError` for a bad cursor, plus 401 / 403 / 429 / 5XX.
- **`POST /api/seller/payouts`** (`createSellerPayout`):
  - parameters: the `IdempotencyKey` header (same definition as payment.yaml);
  - body `PayoutRequest`;
  - 201 `Payout`;
  - 400 `ValidationError` (described "amount below PAYOUT_MIN_AMOUNT");
  - 409 (described "INSUFFICIENT_FUNDS; details.available is the current available balance", `Error` schema);
  - 422 (`IDEMPOTENCY_KEY_REUSED`), plus 401 / 403 / 429 / 5XX.
- **`GET /api/seller/payouts`** (`listSellerPayouts`):
  - parameters `status` (query, optional, enum `REQUESTED | SETTLED | FAILED`), `Limit` and `Cursor`;
  - 200 `PayoutPage`, plus 400 / 401 / 403 / 429 / 5XX.

Schemas (every property listed is required):

```yaml
    Wallet:
      type: object
      required: [available, pending_payout, in_escrow, currency]
      properties:
        available:
          $ref: 'common.yaml#/components/schemas/Money'
        pending_payout:
          $ref: 'common.yaml#/components/schemas/Money'
        in_escrow:
          $ref: 'common.yaml#/components/schemas/Money'
        currency:
          type: string
          const: VND
    WalletEntry:
      type: object
      required: [id, transaction_id, kind, amount, order_id, payout_id, created_at]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        transaction_id:
          $ref: 'common.yaml#/components/schemas/Id'
        kind:
          type: string
          enum: [RELEASE, PAYOUT_REQUEST, PAYOUT_FAIL]
        amount:
          type: integer
          format: int64
          description: Signed from the seller's view — RELEASE positive, PAYOUT_REQUEST negative, PAYOUT_FAIL positive.
        order_id:
          type: [string, 'null']
          format: uuid
        payout_id:
          type: [string, 'null']
          format: uuid
        created_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
    WalletEntryPage:
      type: object
      required: [items, next_cursor]
      properties:
        items:
          type: array
          items:
            $ref: '#/components/schemas/WalletEntry'
        next_cursor:
          type: [string, 'null']
    PayoutRequest:
      type: object
      additionalProperties: false
      required: [amount]
      properties:
        amount:
          $ref: 'common.yaml#/components/schemas/Money'
    Payout:
      type: object
      required: [id, amount, status, failure_reason, requested_at, settled_at]
      properties:
        id:
          $ref: 'common.yaml#/components/schemas/Id'
        amount:
          $ref: 'common.yaml#/components/schemas/Money'
        status:
          type: string
          enum: [REQUESTED, SETTLED, FAILED]
        failure_reason:
          type: [string, 'null']
        requested_at:
          $ref: 'common.yaml#/components/schemas/Timestamp'
        settled_at:
          type: [string, 'null']
          format: date-time
    PayoutPage:
      type: object
      required: [items, next_cursor]
      properties:
        items:
          type: array
          items:
            $ref: '#/components/schemas/Payout'
        next_cursor:
          type: [string, 'null']
```

- [ ] **Step 6: Lint the specs and run the test.** Run: `make lint-openapi && (cd pkg && go test ./testutil/)`. Expected: redocly "valid" for all six files, and the test passes.
  - If redocly flags `common.yaml` components as unused, add them to `api/.redocly.lint-ignore.yaml`; don't delete them.
  - Fix every other warning at the source.

- [ ] **Step 7: Confirm unchanged services still honour the edited contracts** (Review Focus 5):

Run: `(cd services/order && go test -count=1 ./...) && (cd services/gateway && go test -count=1 ./...)`
Expected: PASS. The new `Order` fields are optional, and the enum values are only additions.

- [ ] **Step 8: The web fallout.**
  - In `web/scripts/gen-api.mjs`, set `SPECS = ['common', 'identity', 'catalog', 'order', 'payment', 'ledger']`.
  - Run `cd web && npm ci && npm run gen:api && npm run lint`.
  - Expected `tsc` errors: `ORDER_STATUS_LABEL` and `ORDER_STATUS_TONE` lack `AWAITING_PAYMENT`, and `ERROR_MESSAGES` lacks `INSUFFICIENT_FUNDS`.
  - Add exactly one entry to each map, in the file's style:
    - `AWAITING_PAYMENT: 'Chờ thanh toán'` in `ORDER_STATUS_LABEL`;
    - the same tone value that `PENDING_CONFIRMATION` uses in `ORDER_STATUS_TONE`;
    - `INSUFFICIENT_FUNDS: 'Số dư không đủ để thực hiện yêu cầu.'` in `ERROR_MESSAGES`.
  - Change nothing else in `web/` (WS5' owns it).

Run: `npm run lint && npm test`. Expected: PASS.

- [ ] **Step 9: Commit.** `git add api pkg/testutil web && git commit -m "feat(contracts): payment and ledger REST APIs; order checkout methods, AWAITING_PAYMENT and optional payment fields; INSUFFICIENT_FUNDS"`

---

### Task 6: Service skeletons (`services/payment`, `services/ledger`) and workspace wiring

**Files (per service `<s>` ∈ {payment, ledger}):**
- Create: `services/<s>/go.mod`, `go.sum`, `Dockerfile`, `sqlc.yaml`, `README.md`
- Create: `services/<s>/cmd/server/{main.go,config.go,handler.go,handler_test.go,config_test.go,ready_test.go}`
- Create: `services/<s>/migrations/{migrations.go,000001_init.up.sql,000001_init.down.sql}`
- Create: `services/<s>/internal/{domain,app,adapter}/doc.go`
- Ledger only: the `trial-balance` subcommand (`main.go`) and `cmd/server/trial_balance_test.go`
- Modify: `go.work`, `go.work.sum`, `Makefile` (`GO_MODULES`), `.github/workflows/ci.yml` (Go matrix)

**Interfaces:**
- Produces:
  - module paths `github.com/ngoxuanchien/e-commerce/services/payment` and `…/services/ledger`;
  - `newHandler(logger *slog.Logger, checks map[string]server.Check, api http.Handler) http.Handler`;
  - `readinessChecks(pool *pgxpool.Pool, producer *kafkax.Producer) map[string]server.Check` (keys `postgres`, `kafka`);
  - `Config{config.Base}`;
  - `migrations.FS`;
  - ledger's `trialBalance(w io.Writer) int`.
- WS1' and WS2' replace the bodies and keep the layout.

- [ ] **Step 1: Read the patterns:** `services/order/cmd/server/{main.go,handler.go,handler_test.go,config.go}`, `services/order/migrations/migrations.go`, `services/identity/migrations/000001_init.*.sql`, `services/identity/Dockerfile` and `services/identity/sqlc.yaml`.

- [ ] **Step 2: Write the failing tests** (payment shown; ledger is the same with `ledger` names). `cmd/server/handler_test.go`:

```go
package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/ngoxuanchien/e-commerce/pkg/server"
)

func TestHealthzAndReadyz(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	up := func(context.Context) error { return nil }
	h := newHandler(logger, map[string]server.Check{"postgres": up, "kafka": up}, nil)
	for path, want := range map[string]int{"/healthz": 200, "/readyz": 200, "/api/anything": 404} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		assert.Equal(t, want, rec.Code, path)
	}

	down := func(context.Context) error { return errors.New("down") }
	rec := httptest.NewRecorder()
	newHandler(logger, map[string]server.Check{"postgres": up, "kafka": down}, nil).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
}
```

`cmd/server/config_test.go`:

```go
package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
)

func TestConfigRequiresServiceName(t *testing.T) {
	t.Setenv("SERVICE_NAME", "")
	_, err := config.Load[Config]()
	assert.Error(t, err)
	t.Setenv("SERVICE_NAME", "payment")
	cfg, err := config.Load[Config]()
	require.NoError(t, err)
	assert.Equal(t, "payment", cfg.ServiceName)
}
```

`cmd/server/ready_test.go` (containers; `testutil` skips with `-short`):

```go
package main

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/payment/migrations"
)

func TestReadinessChecksAgainstRealDependencies(t *testing.T) {
	ctx := context.Background()
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS), "baseline migration applies")
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	defer pool.Close()
	producer, err := kafkax.NewProducer(testutil.Kafka(t))
	require.NoError(t, err)
	defer producer.Close()

	checks := readinessChecks(pool, producer)
	assert.ElementsMatch(t, []string{"postgres", "kafka"}, slices.Collect(maps.Keys(checks)))
	for name, check := range checks {
		assert.NoError(t, check(ctx), name)
	}
}
```

Ledger also gets `cmd/server/trial_balance_test.go`:

```go
package main

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTrialBalanceIsReservedUntilWS2(t *testing.T) {
	var out bytes.Buffer
	assert.Equal(t, 2, trialBalance(&out))
	assert.Contains(t, out.String(), "not implemented")
}
```

- [ ] **Step 3: Create the modules so the tests compile and fail.** In `services/<s>`, run `go mod init github.com/ngoxuanchien/e-commerce/services/<s>`, then add the `replace` block exactly as `services/identity/go.mod` has it. At the repo root, add `./services/ledger` and `./services/payment` to `go.work`'s `use` block (sorted).

Run: `cd services/payment && go test ./...`. Expected: FAIL (undefined `newHandler`, `Config`, …).

- [ ] **Step 4: Implement.**

`cmd/server/config.go`:

```go
package main

import "github.com/ngoxuanchien/e-commerce/pkg/config"

// Config is payment-svc's configuration. WS1' adds the service-specific
// variables listed in WS0' §8 (already set in deploy/docker-compose.yml).
type Config struct {
	config.Base
}
```

`cmd/server/handler.go`: copy `services/order/cmd/server/handler.go` verbatim.

`cmd/server/main.go`:

```go
// Command server runs payment-svc (WS0' skeleton: health, readiness, migrations).
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/ngoxuanchien/e-commerce/pkg/config"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/payment/migrations"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(server.HealthcheckCommand(os.Getenv("HTTP_ADDR")))
	}
	if err := run(); err != nil {
		slog.Error("server exited", "error", err)
		os.Exit(1)
	}
}

// readinessChecks are /readyz's dependencies: Postgres and Kafka.
func readinessChecks(pool *pgxpool.Pool, producer *kafkax.Producer) map[string]server.Check {
	return map[string]server.Check{"postgres": pool.Ping, "kafka": producer.Ping}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load[Config]()
	if err != nil {
		return err
	}
	logger := logx.New(os.Stdout, cfg.LogLevel).With("service", cfg.ServiceName)
	slog.SetDefault(logger)

	shutdownOtel, err := otelx.Setup(ctx, cfg.ServiceName, cfg.OTLPEndpoint)
	if err != nil {
		return err
	}
	defer func() { _ = shutdownOtel(context.Background()) }()

	if err := pgxx.Migrate(cfg.DatabaseURL, migrations.FS); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	pool, err := pgxx.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	producer, err := kafkax.NewProducer(cfg.KafkaBrokers)
	if err != nil {
		return err
	}
	defer producer.Close()

	grpcSrv, _ := grpcx.NewServer() // standard gRPC health service; WS1' registers PaymentInternal
	return server.Run(ctx, server.Config{
		HTTP: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           otelx.HTTPHandler(newHandler(logger, readinessChecks(pool, producer), nil), cfg.ServiceName),
			ReadHeaderTimeout: 10 * time.Second,
		},
		GRPC:            grpcSrv,
		GRPCAddr:        cfg.GRPCAddr,
		ShutdownTimeout: cfg.ShutdownTimeout,
		Logger:          logger,
	})
}
```

Ledger's `main.go` is the same (with `ledger` names and "WS2' registers its services"). It adds a check before `run()`:

```go
	if len(os.Args) > 1 && os.Args[1] == "trial-balance" {
		os.Exit(trialBalance(os.Stdout))
	}
```

It also adds the function (imports `io`):

```go
// trialBalance is reserved for WS2' (ledger spec §6): it will verify that the
// ledger balances and exit 1 if not. Until then it exits 2.
func trialBalance(w io.Writer) int {
	fmt.Fprintln(w, "trial-balance: not implemented yet (WS2')")
	return 2
}
```

Migrations:
- `migrations/migrations.go`: copy order's, with the doc comment's service name changed.
- `000001_init.up.sql` and `.down.sql`: mirror identity's `000001_init` baseline. If identity's is not a no-op, use `-- Baseline; WS1'/WS2' add the schema in 000002+.` followed by `SELECT 1;`.

Other files:
- `internal/{domain,app,adapter}/doc.go`: one package doc line each, e.g. `// Package domain holds payment-svc's entities and rules (WS1').`
- `sqlc.yaml`: copy identity's verbatim. `make gen-sqlc` skips services without query files.
- `Dockerfile`: copy identity's and replace `identity` with `<s>` everywhere (5 occurrences).
- `README.md`: service name, ports (8084/9084 or 8085/9085), "WS0' skeleton — owned by WS1'/WS2' from here", and the spec path.

Run `go mod tidy` in both services and `go work sync` at the root. `go.work.sum` changes get committed.

- [ ] **Step 5: Makefile and CI.**
  - `Makefile`: `GO_MODULES ?= pkg gen services/gateway services/identity services/catalog services/order services/payment services/ledger`.
  - `.github/workflows/ci.yml`: add `services/payment` and `services/ledger` to the `go` job's matrix `module:` list.

- [ ] **Step 6: Run the tests, lint and build.**

Run: `for s in payment ledger; do (cd services/$s && go test -race -count=1 ./... && ../../bin/golangci-lint run ./...) || break; done && make test-short && docker build -q -f services/payment/Dockerfile . && docker build -q -f services/ledger/Dockerfile .`
Expected: PASS, 0 lint issues, both images build.

- [ ] **Step 7: Commit.** `git add services/payment services/ledger go.work go.work.sum Makefile .github/workflows/ci.yml && git commit -m "feat: payment-svc and ledger-svc skeletons (health, readiness, baseline migration) wired into the workspace, Makefile and CI"`

---

### Task 7: Local infrastructure (`deploy/`), secrets placeholder, `.gitignore`, `stripe-listen`

**Files:**
- Modify: `deploy/docker-compose.yml`, `deploy/postgres/init.sql`, `.gitignore`, `Makefile`
- Create: `deploy/secrets/payment.env.example`

**Interfaces:**
- Produces:
  - compose services `db-init`, `payment` and `ledger`, with the full spec §8 environment (WS1'–WS3' read these variable names);
  - Kafka topics `payment.events` and `payout.events`;
  - `make stripe-listen`.

- [ ] **Step 1: Run the observable checks first and see them fail:**

```bash
git check-ignore -q deploy/secrets/payment.env.example && echo IGNORED || echo NOT_IGNORED   # today: IGNORED (must become NOT_IGNORED)
docker compose -f deploy/docker-compose.yml config --services | grep -cE '^(db-init|payment|ledger)$'   # today: 0 (must become 3)
```

- [ ] **Step 2: `.gitignore` and `init.sql`.**
  - `.gitignore`: replace the line `deploy/secrets/` with a comment, then `deploy/secrets/*` and `!deploy/secrets/*.example`. The comment says git can't re-include a file inside an excluded directory.
  - `deploy/postgres/init.sql`: append `CREATE DATABASE payment_db;` and `CREATE DATABASE ledger_db;`, and add a header line noting that existing volumes get new databases from the `db-init` compose job.

- [ ] **Step 3: Create `deploy/secrets/payment.env.example`:**

```bash
# Copy to deploy/secrets/payment.env (gitignored) and fill in to use the real
# sandboxes. Then set PROVIDER_VNPAY=vnpay and/or PROVIDER_STRIPE=stripe in your
# shell or deploy/.env. Without them both methods use the FAKE provider.

# VNPay sandbox (https://sandbox.vnpayment.vn/devreg/): merchant code and hash secret.
VNPAY_TMN_CODE=
VNPAY_HASH_SECRET=
VNPAY_PAY_URL=https://sandbox.vnpayment.vn/paymentv2/vpcpay.html
VNPAY_API_URL=https://sandbox.vnpayment.vn/merchant_webapi/api/transaction

# Stripe test mode (https://dashboard.stripe.com/test/apikeys). The webhook
# secret is printed by `make stripe-listen` (whsec_...).
STRIPE_SECRET_KEY=
STRIPE_WEBHOOK_SECRET=
# Smallest checkout (VND, a zero-decimal currency) payment-svc sends to Stripe.
STRIPE_MIN_AMOUNT=10000
```

Check `STRIPE_MIN_AMOUNT`'s meaning and default against WS1' §5.2 and match it.

- [ ] **Step 4: Compose.** Add `db-init` right after `postgres`:

```yaml
  # init.sql only runs on an empty volume, and pgdata is shared across
  # worktrees and clones: this job creates any missing database on every up.
  db-init:
    image: postgres:17-alpine
    depends_on:
      postgres:
        condition: service_healthy
    environment:
      PGHOST: postgres
      PGUSER: marketplace
      PGPASSWORD: marketplace
      PGDATABASE: postgres
    entrypoint: ["/bin/sh", "-c"]
    command:
      - |
        set -e
        for db in identity_db catalog_db order_db payment_db ledger_db; do
          echo "SELECT 'CREATE DATABASE $$db' WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = '$$db')\gexec" | psql -v ON_ERROR_STOP=1 -q
        done
    restart: "no"
```

- **`kafka-init`:** its `command` becomes a `|` block under `set -e` that creates `order.events`, `payment.events` and `payout.events`, each with `--create --if-not-exists --partitions 3 --replication-factor 1`.
- **Existing Go services:** in identity, catalog and order, replace `postgres: condition: service_healthy` with `db-init: condition: service_completed_successfully`. The gateway has no postgres dependency.

Add after `order`:

```yaml
  payment:
    <<: *go-service
    build:
      context: ..
      dockerfile: services/payment/Dockerfile
    environment:
      <<: *common-env
      SERVICE_NAME: payment
      HTTP_ADDR: ":8084"
      GRPC_ADDR: ":9084"
      DATABASE_URL: postgres://marketplace:marketplace@postgres:5432/payment_db?sslmode=disable
      KAFKA_BROKERS: kafka:9092
      ORDER_GRPC_ADDR: order:9083
      PUBLIC_BASE_URL: http://localhost:3000
      PROVIDER_VNPAY: ${PROVIDER_VNPAY:-fake}
      PROVIDER_STRIPE: ${PROVIDER_STRIPE:-fake}
      FAKE_WEBHOOK_URL: http://gateway:8080/api/payments/fake/webhook
      FAKE_WEBHOOK_SECRET: dev-fake-secret
      FAKE_WEBHOOK_DELAY: ${FAKE_WEBHOOK_DELAY:-500ms}
      RECONCILE_INTERVAL: ${RECONCILE_INTERVAL:-60s}
      RECONCILE_MIN_AGE: ${RECONCILE_MIN_AGE:-2m}
      REFUND_MAX_ATTEMPTS: "8"
    # Provider credentials (VNPAY_*, STRIPE_*): see secrets/payment.env.example.
    env_file:
      - path: ./secrets/payment.env
        required: false
    depends_on:
      db-init:
        condition: service_completed_successfully
      kafka-init:
        condition: service_completed_successfully

  ledger:
    <<: *go-service
    build:
      context: ..
      dockerfile: services/ledger/Dockerfile
    environment:
      <<: *common-env
      SERVICE_NAME: ledger
      HTTP_ADDR: ":8085"
      GRPC_ADDR: ":9085"
      DATABASE_URL: postgres://marketplace:marketplace@postgres:5432/ledger_db?sslmode=disable
      KAFKA_BROKERS: kafka:9092
      PAYOUT_MIN_AMOUNT: "50000"
      PAYOUT_SETTLE_INTERVAL: ${PAYOUT_SETTLE_INTERVAL:-30s}
      PAYOUT_SETTLE_DELAY: ${PAYOUT_SETTLE_DELAY:-1m}
      PENDING_POSTING_INTERVAL: 10s
    depends_on:
      db-init:
        condition: service_completed_successfully
      kafka-init:
        condition: service_completed_successfully
```

- **`order` environment:**
  - add `PAYMENT_GRPC_ADDR: payment:9084`, `ONLINE_PAYMENT_METHODS: VNPAY,STRIPE`, `COMMISSION_RATE_BPS: "500"`, `PAYMENT_TIMEOUT: ${PAYMENT_TIMEOUT:-30m}` and `AUTO_COMPLETE_AFTER: ${AUTO_COMPLETE_AFTER:-72h}`;
  - change `SWEEPER_INTERVAL` to `${SWEEPER_INTERVAL:-60s}`;
  - order and payment must not `depends_on` each other.
- **`gateway`:**
  - environment: add `PAYMENT_URL: http://payment:8084` and `LEDGER_URL: http://ledger:8085`;
  - `depends_on`: add `payment` and `ledger` with `condition: service_healthy`. This is a gap fill recorded in Task 8.

- [ ] **Step 5: Makefile `stripe-listen`.** Add it to `.PHONY` and add:

```make
stripe-listen: ## Forward Stripe test-mode webhooks to the local gateway (prints the whsec_ secret for deploy/secrets/payment.env)
	stripe listen --forward-to localhost:8080/api/payments/stripe/webhook
```

- [ ] **Step 6: Verify on the EXISTING P1 volume** (Review Focus 4). The `marketplace` project owns the shared `pgdata`. The run is additive only: it creates two new databases and doesn't change any P1 service.

```bash
docker compose -f deploy/docker-compose.yml config -q
make keys up-app
docker compose -f deploy/docker-compose.yml exec -T postgres psql -U marketplace -d postgres -Atc "SELECT datname FROM pg_database WHERE datname LIKE '%\_db' ORDER BY 1"
docker compose -f deploy/docker-compose.yml run --rm db-init && echo "db-init rerun OK"
docker compose -f deploy/docker-compose.yml exec -T kafka /opt/kafka/bin/kafka-topics.sh --bootstrap-server localhost:9092 --list
for s in identity catalog order gateway payment ledger; do docker compose -f deploy/docker-compose.yml exec -T $s /server healthcheck && echo "$s ready"; done
```

Expected:
- the databases listed are `catalog_db identity_db ledger_db order_db payment_db`;
- the `db-init` re-run exits 0;
- the topics include all three;
- all six `healthcheck` calls (which probe `/readyz`) exit 0.

- [ ] **Step 7: Verify on a FRESH volume.** Stop the `marketplace` stack first **without `-v`**, because the host ports and the pinned subnet can't be shared:

```bash
docker compose -p marketplace -f deploy/docker-compose.yml -f deploy/docker-compose.web.yml --profile app down
export COMPOSE_PROJECT_NAME=p2check
make up-app
# repeat the Step 6 psql / kafka-topics / healthcheck checks (they pick up COMPOSE_PROJECT_NAME)
docker compose -f deploy/docker-compose.yml -f deploy/docker-compose.web.yml --profile app down   # NO -v
unset COMPOSE_PROJECT_NAME
```

- [ ] **Step 8: Re-run the Step 1 checks.** Expected: `payment.env.example` is NOT_IGNORED, `git check-ignore deploy/secrets/payment.env` still succeeds, and 3 services are listed.

- [ ] **Step 9: Commit.** `git add deploy .gitignore Makefile && git commit -m "feat(deploy): db-init job, payment/ledger services and topics, provider env and secrets placeholder, stripe-listen"`

---

### Task 8: Gap notes in the specs, full verification, CI

**Files:**
- Modify: `docs/superpowers/specs/2026-09-29-marketplace-p2-ws0-contracts.md` (append `## 12. Gaps filled by WS0' (2026-09-29)`; tick §11)
- Modify: `docs/superpowers/specs/2026-09-29-marketplace-p2-overview.md` (§7 status column)
- Modify: `docs/superpowers/specs/2026-09-29-marketplace-p2-ws3-order.md` and `…-ws5-web.md` (one pointer line each)

- [ ] **Step 1: Write WS0' §12**, one bullet per gap:
  1. **Optional `Order` fields.** The new `Order` fields and `OrderShop.slug` are **optional** in `order.yaml` (user decision 2026-09-29), because the unchanged order-svc validates every response against it. WS3' adds them to `required` in its implementing PR. This is the only contract edit allowed outside WS0'.
  2. **`Order.shop.slug`.** P1.5 did not deliver it (overview §7). WS0' adds it to the contract, and WS3' implements it: order-svc must snapshot the shop slug at checkout, which needs a migration.
  3. **`web/` fallout.** `web/src/api/gen` was regenerated, `gen-api.mjs` includes payment and ledger, and there is one new entry each for `AWAITING_PAYMENT` (status label and tone) and `INSUFFICIENT_FUNDS` (error message), because web lint runs `gen:api:check` and `tsc`. WS5' owns everything else.
  4. **Gateway readiness.** The gateway `depends_on` payment and ledger (healthy), as it does for the P1 upstreams.
  5. **Envelope schema.** `event_type` lists every P2 type, and the description covers all three topics.
  6. **§7 items P1.5 did not cover.** P1.5 (PR #1) fixed the orphaned idempotency rows, `CartShop.shipping_fee`, the seller PATCH stock overwrite and the first CI e2e run. The rest now have owners:
     - the `WithoutCancel` checkout deadline and the Tx B race code → WS3' (inside `services/order`);
     - `Order.shop.slug` → contract here, implementation WS3';
     - the reserve-after-release tombstone → stays a documented limitation (catalog README decision 6; catalog has no P2 workstream);
     - seed resume → WS6'.

  Also:
  - add a "Status" column to overview §7;
  - add a pointer line "See WS0' §12 for the contract items this workstream completes" to WS3' §1 and WS5' §1;
  - tick the WS0' §11 acceptance boxes that Tasks 1–7 proved.

- [ ] **Step 2: Full verification.**

```bash
make lint
make gen && git status --porcelain -- gen services     # must print nothing
make test
(cd web && npm run lint && npm test)
(cd e2e && GOWORK=off go vet ./...)
bin/buf breaking proto --against ".git#ref=refs/heads/main,subdir=proto"
```

Expected: all green, and the gen diff is empty.

- [ ] **Step 3: Commit, push and open a draft PR to trigger CI.**

```bash
git add docs && git commit -m "docs(specs): record the shared-file gaps WS0' filled"
git push -u origin p2-ws0-contracts
gh pr create --draft --base main --title "P2 WS0': contracts and foundation" --body "<summary of the 8 tasks + verification results>"
```

Watch `gh pr checks` until every job is green: the Go matrix including payment and ledger, contracts, web, images, and e2e (the P1 e2e still passes with the two extra services booting). Fix any failure in the owning task's files and push again.
