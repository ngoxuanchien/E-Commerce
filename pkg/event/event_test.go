package event_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
)

const (
	orderID = "0192a1b2-0000-7000-8000-000000000001"
	userID  = "0192a1b2-0000-7000-8000-000000000002"
	skuID   = "0192a1b2-0000-7000-8000-000000000003"
)

var at = time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)

func samples() map[string]event.Payload {
	return map[string]event.Payload{
		"order-created.schema.json": event.OrderCreated{
			OrderID: orderID, CheckoutID: orderID, BuyerID: userID, ShopID: orderID, SellerUserID: userID,
			Total: 130000, Items: []event.OrderItem{{SkuID: skuID, Quantity: 2, UnitPrice: 50000}},
		},
		"order-status-changed.schema.json": event.OrderStatusChanged{
			OrderID: orderID, From: "PENDING_CONFIRMATION", To: "CONFIRMED", ActorID: userID, ActorRole: "seller",
		},
		"order-cancelled.schema.json": event.OrderCancelled{
			OrderID: orderID, Reason: "OUT_OF_STOCK", CancelledBy: event.CancelledBySystem,
		},
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
		"payment-failed.schema.json":             event.PaymentFailed{PaymentID: u(), CheckoutID: u(), Provider: event.ProviderVNPay, Reason: "24"},
		"payment-refunded.schema.json":           event.PaymentRefunded{RefundID: u(), PaymentID: u(), CheckoutID: u(), OrderID: ptr(u()), Amount: 130_000, Provider: event.ProviderFake},
		"payment-refunded.schema.json#duplicate": event.PaymentRefunded{RefundID: u(), PaymentID: u(), CheckoutID: u(), OrderID: nil, Amount: 260_000, Provider: event.ProviderFake},
		"refund-failed.schema.json":              event.RefundFailed{RefundID: u(), PaymentID: u(), CheckoutID: u(), OrderID: ptr(u()), Amount: 1, Provider: event.ProviderStripe, Reason: "card_declined"},
		"payout-requested.schema.json":           event.PayoutRequested{PayoutID: u(), SellerUserID: u(), Amount: 50_000},
		"payout-settled.schema.json":             event.PayoutSettled{PayoutID: u(), SellerUserID: u(), Amount: 50_000, SettledAt: time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)},
		"payout-failed.schema.json":              event.PayoutFailed{PayoutID: u(), SellerUserID: u(), Amount: 50_000, Reason: "simulated"},
	}
}

// u returns a fresh UUIDv7 string.
func u() string { return uuid.Must(uuid.NewV7()).String() }

func ptr[T any](v T) *T { return &v }

func TestEncodedEventsMatchJSONSchemas(t *testing.T) {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	for name, p := range samples() {
		file, _, _ := strings.Cut(name, "#")
		t.Run(name, func(t *testing.T) {
			sch, err := c.Compile(filepath.Join("..", "..", "api", "events", file))
			require.NoError(t, err)

			b, err := event.Encode(p, at)
			require.NoError(t, err)
			inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
			require.NoError(t, err)
			assert.NoError(t, sch.Validate(inst))
		})
	}
}

func TestSystemStatusChangeOmitsActorID(t *testing.T) {
	b, err := event.Encode(event.OrderStatusChanged{OrderID: orderID, From: "PENDING", To: "CANCELLED", ActorRole: "system"}, at)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "actor_id")
}

func TestNewSetsEnvelopeFields(t *testing.T) {
	env, err := event.New(event.OrderCancelled{OrderID: orderID, Reason: "r", CancelledBy: "buyer"}, at.In(time.FixedZone("ICT", 7*3600)))
	require.NoError(t, err)

	id, err := uuid.Parse(env.EventID)
	require.NoError(t, err)
	assert.Equal(t, uuid.Version(7), id.Version())
	assert.Equal(t, event.TypeOrderCancelled, env.EventType)
	assert.Equal(t, 1, env.Version)
	assert.Equal(t, time.UTC, env.OccurredAt.Location())
	assert.True(t, env.OccurredAt.Equal(at))
}

func TestRoundTrip(t *testing.T) {
	for _, p := range samples() {
		b, err := event.Encode(p, at)
		require.NoError(t, err)
		env, err := event.Decode(b)
		require.NoError(t, err)
		assert.Equal(t, p.EventType(), env.EventType)

		switch want := p.(type) {
		case event.OrderCreated:
			got, err := event.DecodeData[event.OrderCreated](env)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		case event.OrderStatusChanged:
			got, err := event.DecodeData[event.OrderStatusChanged](env)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		case event.OrderCancelled:
			got, err := event.DecodeData[event.OrderCancelled](env)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		}
	}
}

func TestDecodeDataTypeMismatch(t *testing.T) {
	b, _ := event.Encode(event.OrderCancelled{OrderID: orderID, Reason: "r", CancelledBy: "buyer"}, at)
	env, err := event.Decode(b)
	require.NoError(t, err)
	_, err = event.DecodeData[event.OrderCreated](env)
	assert.ErrorIs(t, err, event.ErrTypeMismatch)
}

func TestDecodeRejectsBadEnvelopes(t *testing.T) {
	valid := map[string]any{"event_id": orderID, "event_type": "OrderCancelled", "version": 1,
		"occurred_at": "2026-09-28T10:00:00Z", "data": map[string]any{}}
	mutate := func(k string, v any) []byte {
		m := map[string]any{}
		for key, val := range valid {
			m[key] = val
		}
		if v == nil {
			delete(m, k)
		} else {
			m[k] = v
		}
		b, _ := json.Marshal(m)
		return b
	}

	_, err := event.Decode(mutate("version", 2))
	assert.ErrorIs(t, err, event.ErrUnsupportedVersion)
	_, err = event.Decode(mutate("event_id", nil))
	assert.ErrorIs(t, err, event.ErrInvalidEnvelope)
	_, err = event.Decode(mutate("event_type", nil))
	assert.ErrorIs(t, err, event.ErrInvalidEnvelope)
	_, err = event.Decode(mutate("data", nil))
	assert.ErrorIs(t, err, event.ErrInvalidEnvelope)
	_, err = event.Decode(mutate("occurred_at", nil))
	assert.ErrorIs(t, err, event.ErrInvalidEnvelope)
	_, err = event.Decode([]byte("not json"))
	assert.Error(t, err)
}

// P1 payloads (exact JSON order-svc publishes today) must stay valid.
func TestP1PayloadsStillValidate(t *testing.T) {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
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
		sch, err := c.Compile(filepath.Join("..", "..", "api", "events", file))
		require.NoError(t, err)
		assert.NoError(t, sch.Validate(inst), file)
	}
}

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

func TestNilAllocationsEncodeAsEmptyArray(t *testing.T) {
	p := event.PaymentSucceeded{PaymentID: u(), CheckoutID: u(), BuyerID: u(), Provider: event.ProviderStripe,
		ProviderRef: "pi_1", Amount: 260_000}
	b, err := event.Encode(p, at)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"allocations":[]`)

	c := jsonschema.NewCompiler()
	c.AssertFormat()
	sch, err := c.Compile(filepath.Join("..", "..", "api", "events", "payment-succeeded.schema.json"))
	require.NoError(t, err)
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	require.NoError(t, err)
	assert.NoError(t, sch.Validate(inst))
}
