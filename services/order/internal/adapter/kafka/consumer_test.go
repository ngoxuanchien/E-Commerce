package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	kafkaadapter "github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/kafka"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

// recorder is a stub PaymentEvents: it remembers the dispatched events.
type recorder struct {
	succeeded []struct {
		eventID string
		ev      event.PaymentSucceeded
	}
	refunded []struct {
		eventID string
		ev      event.PaymentRefunded
	}
	err error
}

func (r *recorder) PaymentSucceeded(_ context.Context, eventID string, ev event.PaymentSucceeded) error {
	r.succeeded = append(r.succeeded, struct {
		eventID string
		ev      event.PaymentSucceeded
	}{eventID, ev})
	return r.err
}

func (r *recorder) PaymentRefunded(_ context.Context, eventID string, ev event.PaymentRefunded) error {
	r.refunded = append(r.refunded, struct {
		eventID string
		ev      event.PaymentRefunded
	}{eventID, ev})
	return r.err
}

func (r *recorder) calls() int { return len(r.succeeded) + len(r.refunded) }

func encode(t *testing.T, p event.Payload) (event.Envelope, []byte) {
	t.Helper()
	env, err := event.New(p, time.Now())
	require.NoError(t, err)
	b, err := json.Marshal(env)
	require.NoError(t, err)
	return env, b
}

func handle(t *testing.T, h *kafkaadapter.Handler, value []byte) error {
	t.Helper()
	return h.Handle(context.Background(), kafkax.Message{Topic: event.TopicPaymentEvents, Value: value})
}

func TestHandlerDispatchesPaymentEvents(t *testing.T) {
	rec := &recorder{}
	h := kafkaadapter.NewHandler(rec, discard)
	orderID := uuid.Must(uuid.NewV7())
	ev := event.PaymentSucceeded{
		PaymentID: uuid.NewString(), CheckoutID: uuid.NewString(), BuyerID: uuid.NewString(),
		Provider: event.ProviderVNPay, ProviderRef: "vnp-1", Amount: 130_000,
		Allocations: []event.Allocation{{
			OrderID: orderID.String(), SellerUserID: uuid.NewString(),
			Amount: 130_000, CommissionAmount: 6_500,
		}},
	}
	env, b := encode(t, ev)

	require.NoError(t, handle(t, h, b))
	require.Len(t, rec.succeeded, 1)
	assert.Equal(t, env.EventID, rec.succeeded[0].eventID)
	assert.Equal(t, ev, rec.succeeded[0].ev)
	assert.Zero(t, len(rec.refunded))
}

func TestHandlerDispatchesPaymentRefunded(t *testing.T) {
	rec := &recorder{}
	h := kafkaadapter.NewHandler(rec, discard)
	orderID := uuid.NewString()
	ev := event.PaymentRefunded{
		RefundID: uuid.NewString(), PaymentID: uuid.NewString(), CheckoutID: uuid.NewString(),
		OrderID: &orderID, Amount: 130_000, Provider: event.ProviderVNPay,
	}
	env, b := encode(t, ev)

	require.NoError(t, handle(t, h, b))
	require.Len(t, rec.refunded, 1)
	assert.Equal(t, env.EventID, rec.refunded[0].eventID)
	assert.Equal(t, ev, rec.refunded[0].ev)
}

func TestHandlerIgnoresOtherEventTypes(t *testing.T) {
	rec := &recorder{}
	h := kafkaadapter.NewHandler(rec, discard)
	for name, p := range map[string]event.Payload{
		"PaymentFailed":   event.PaymentFailed{PaymentID: uuid.NewString(), CheckoutID: uuid.NewString(), Provider: event.ProviderVNPay, Reason: "declined"},
		"RefundFailed":    event.RefundFailed{RefundID: uuid.NewString(), PaymentID: uuid.NewString(), CheckoutID: uuid.NewString(), Provider: event.ProviderVNPay, Reason: "gateway down"},
		"PayoutSettled":   event.PayoutSettled{PayoutID: uuid.NewString(), SellerUserID: uuid.NewString(), Amount: 5_000, SettledAt: time.Now()},
		"OrderCreated":    event.OrderCreated{OrderID: uuid.NewString(), CheckoutID: uuid.NewString(), BuyerID: uuid.NewString(), ShopID: uuid.NewString(), SellerUserID: uuid.NewString(), Total: 1, Items: []event.OrderItem{}},
		"OrderCancelled":  event.OrderCancelled{OrderID: uuid.NewString(), Reason: "x", CancelledBy: event.CancelledByBuyer},
		"PayoutRequested": event.PayoutRequested{PayoutID: uuid.NewString(), SellerUserID: uuid.NewString(), Amount: 5_000},
	} {
		t.Run(name, func(t *testing.T) {
			_, b := encode(t, p)
			assert.NoError(t, handle(t, h, b), "an event of another service is none of our business")
		})
	}
	assert.Zero(t, rec.calls())
}

// TestHandlerSkipsPoisonRecords: a record that can never be applied must be
// skipped with a warning, not block its partition with endless retries.
func TestHandlerSkipsPoisonRecords(t *testing.T) {
	rec := &recorder{}
	h := kafkaadapter.NewHandler(rec, discard)
	badCheckout := event.PaymentSucceeded{
		PaymentID: uuid.NewString(), CheckoutID: "not-a-uuid", BuyerID: uuid.NewString(),
		Provider: event.ProviderVNPay, Amount: 1,
		Allocations: []event.Allocation{{OrderID: uuid.NewString(), SellerUserID: uuid.NewString(), Amount: 1}},
	}
	refundOrderID := "not-a-uuid"
	badRefund := event.PaymentRefunded{
		RefundID: uuid.NewString(), PaymentID: uuid.NewString(), CheckoutID: uuid.NewString(),
		OrderID: &refundOrderID, Amount: 1, Provider: event.ProviderVNPay,
	}
	// A PaymentFailed payload under a PaymentSucceeded envelope: the type does
	// not match the data.
	_, badData := encode(t, event.PaymentFailed{Reason: "declined"})
	// A data field of the wrong shape for its event type.
	mismatched := `{"event_id":"` + uuid.NewString() + `","event_type":"PaymentSucceeded","version":1,` +
		`"occurred_at":"2026-09-30T10:00:00Z","data":{"payment_id":1}}`

	for name, value := range map[string][]byte{
		"not json":         []byte("not json"),
		"wrong version":    []byte(`{"event_id":"` + uuid.NewString() + `","event_type":"PaymentSucceeded","version":2,"occurred_at":"2026-09-30T10:00:00Z","data":{}}`),
		"bad data type":    []byte(mismatched),
		"wrong payload":    badData,
		"bad checkout id":  mustEncode(t, badCheckout),
		"bad refund order": mustEncode(t, badRefund),
		"empty event id":   []byte(`{"event_id":"","event_type":"PaymentSucceeded","version":1,"occurred_at":"2026-09-30T10:00:00Z","data":{}}`),
		"missing data":     []byte(`{"event_id":"` + uuid.NewString() + `","event_type":"PaymentSucceeded","version":1,"occurred_at":"2026-09-30T10:00:00Z"}`),
	} {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, handle(t, h, value), "must not block the partition")
		})
	}
	assert.Zero(t, rec.calls(), "a record that can never be applied is never dispatched")
}

func mustEncode(t *testing.T, p event.Payload) []byte {
	t.Helper()
	_, b := encode(t, p)
	return b
}

// TestHandlerDropsMalformedAllocations: one bad allocation must not throw away
// the orders that can still be paid.
func TestHandlerDropsMalformedAllocations(t *testing.T) {
	rec := &recorder{}
	h := kafkaadapter.NewHandler(rec, discard)
	good := uuid.NewString()
	ev := event.PaymentSucceeded{
		PaymentID: uuid.NewString(), CheckoutID: uuid.NewString(), BuyerID: uuid.NewString(),
		Provider: event.ProviderVNPay, Amount: 2,
		Allocations: []event.Allocation{
			{OrderID: "not-a-uuid", SellerUserID: uuid.NewString(), Amount: 1},
			{OrderID: good, SellerUserID: uuid.NewString(), Amount: 1},
		},
	}
	_, b := encode(t, ev)

	require.NoError(t, handle(t, h, b))
	require.Len(t, rec.succeeded, 1)
	require.Len(t, rec.succeeded[0].ev.Allocations, 1)
	assert.Equal(t, good, rec.succeeded[0].ev.Allocations[0].OrderID)
}

func TestHandlerReturnsUseCaseErrorsForRetry(t *testing.T) {
	rec := &recorder{err: errors.New("db down")}
	_, b := encode(t, event.PaymentSucceeded{
		PaymentID: uuid.NewString(), CheckoutID: uuid.NewString(), BuyerID: uuid.NewString(),
		Provider: event.ProviderVNPay, Amount: 1,
	})
	assert.Error(t, handle(t, kafkaadapter.NewHandler(rec, discard), b),
		"a transient failure must be retried, not dropped")
}
