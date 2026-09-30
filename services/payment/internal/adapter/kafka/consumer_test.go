package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/kafka"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type call struct {
	eventID string
	e       event.OrderCancelled
	at      time.Time
}

type recorder struct {
	mu    sync.Mutex
	calls []call
	err   error
}

func (r *recorder) OrderCancelled(_ context.Context, eventID string, e event.OrderCancelled, at time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call{eventID, e, at})
	return r.err
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

func encode(t *testing.T, p event.Payload) (event.Envelope, []byte) {
	t.Helper()
	env, err := event.New(p, time.Now())
	require.NoError(t, err)
	b, err := json.Marshal(env)
	require.NoError(t, err)
	return env, b
}

func cancelled() event.OrderCancelled {
	return event.OrderCancelled{OrderID: uuid.NewString(), Reason: "x", CancelledBy: event.CancelledBySystem,
		CheckoutID: uuid.NewString(), PaymentMethod: event.PaymentMethodStripe}
}

func TestHandlerForwardsOrderCancelled(t *testing.T) {
	rec := &recorder{}
	data := cancelled()
	env, b := encode(t, data)
	require.NoError(t, kafka.NewHandler(rec, discard).Handle(context.Background(), kafkax.Message{Value: b}))
	require.Len(t, rec.calls, 1)
	assert.Equal(t, env.EventID, rec.calls[0].eventID)
	assert.Equal(t, data, rec.calls[0].e)
	assert.True(t, rec.calls[0].at.Equal(env.OccurredAt))
}

func TestHandlerSkipsOtherAndPoisonRecords(t *testing.T) {
	rec := &recorder{}
	h := kafka.NewHandler(rec, discard)
	_, created := encode(t, event.OrderCreated{OrderID: uuid.NewString(), Items: []event.OrderItem{}})
	_, badOrder := encode(t, event.OrderCancelled{OrderID: "nope", Reason: "x", CancelledBy: event.CancelledBySystem})
	bad := cancelled()
	bad.CheckoutID = "nope"
	_, badCheckout := encode(t, bad)
	for name, value := range map[string][]byte{
		"other type":    created,
		"not json":      []byte("not json"),
		"wrong version": []byte(`{"event_id":"` + uuid.NewString() + `","event_type":"OrderCancelled","version":9,"occurred_at":"2026-09-28T10:00:00Z","data":{}}`),
		"bad order id":  badOrder,
		"bad checkout":  badCheckout,
		"bad event id":  []byte(`{"event_id":"x","event_type":"OrderCancelled","version":1,"occurred_at":"2026-09-28T10:00:00Z","data":{"order_id":"` + uuid.NewString() + `","reason":"x","cancelled_by":"buyer"}}`),
	} {
		t.Run(name, func(t *testing.T) {
			assert.NoError(t, h.Handle(context.Background(), kafkax.Message{Value: value}), "must not block the partition")
		})
	}
	assert.Empty(t, rec.calls)
}

func TestHandlerReturnsStoreErrorsForRetry(t *testing.T) {
	rec := &recorder{err: errors.New("db down")}
	_, b := encode(t, cancelled())
	assert.Error(t, kafka.NewHandler(rec, discard).Handle(context.Background(), kafkax.Message{Value: b}))
}

func TestConsumerDeliversOverKafka(t *testing.T) {
	brokers := testutil.Kafka(t)
	testutil.CreateTopic(t, brokers, event.TopicOrderEvents, 1)
	ctx := context.Background()
	rec := &recorder{}

	_, first := encode(t, cancelled())
	_, created := encode(t, event.OrderCreated{OrderID: uuid.NewString(), Items: []event.OrderItem{}})
	lastEnv, last := encode(t, cancelled())
	producer, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	defer producer.Close()
	msg := func(v []byte) kafkax.Message {
		return kafkax.Message{Topic: event.TopicOrderEvents, Key: []byte("k"), Value: v}
	}
	require.NoError(t, producer.Publish(ctx, msg(first), msg([]byte("poison")), msg(created), msg(last)))

	consumer, err := kafka.NewConsumer(brokers, kafka.NewHandler(rec, discard), discard)
	require.NoError(t, err)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- consumer.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		consumer.Close()
	})

	require.Eventually(t, func() bool { return rec.count() == 2 }, 60*time.Second, 100*time.Millisecond)
	assert.Equal(t, lastEnv.EventID, rec.calls[1].eventID)
}
