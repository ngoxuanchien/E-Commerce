package event_test

import (
	"bytes"
	"encoding/json"
	"path/filepath"
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
	}
}

func TestEncodedEventsMatchJSONSchemas(t *testing.T) {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	for file, p := range samples() {
		t.Run(file, func(t *testing.T) {
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
	_, err = event.Decode([]byte("not json"))
	assert.Error(t, err)
}
