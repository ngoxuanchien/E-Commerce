package kafkax_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func runConsumer(t *testing.T, brokers []string, topic string, h kafkax.Handler) (stop func()) {
	t.Helper()
	c, err := kafkax.NewConsumer(kafkax.ConsumerConfig{
		Brokers: brokers, Group: "test-group", Topics: []string{topic},
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond, Logger: discard,
	}, h)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = c.Run(ctx); close(done) }()
	return func() { cancel(); <-done; c.Close() }
}

func TestProduceConsumeRetryAndCommit(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	brokers := testutil.Kafka(t)
	const topic = "kafkax-test"
	testutil.CreateTopic(t, brokers, topic, 1)

	p, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	defer p.Close()
	require.NoError(t, p.Ping(context.Background()))

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	pctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled, Remote: true,
	}))
	require.NoError(t, p.Publish(pctx, kafkax.Message{
		Topic: topic, Key: []byte("order-1"), Value: []byte(`{"n":1}`), Headers: map[string]string{"x-test": "yes"},
	}))

	// First consumer: fail twice, then succeed.
	var calls atomic.Int32
	var mu sync.Mutex
	var got kafkax.Message
	var gotTrace trace.TraceID
	processed := make(chan struct{})
	stop := runConsumer(t, brokers, topic, func(ctx context.Context, m kafkax.Message) error {
		if calls.Add(1) < 3 {
			return errors.New("transient")
		}
		mu.Lock()
		got, gotTrace = m, trace.SpanContextFromContext(ctx).TraceID()
		mu.Unlock()
		close(processed)
		return nil
	})
	select {
	case <-processed:
	case <-time.After(30 * time.Second):
		t.Fatal("message not processed")
	}
	time.Sleep(500 * time.Millisecond) // let the commit land
	stop()

	assert.EqualValues(t, 3, calls.Load())
	mu.Lock()
	assert.Equal(t, "order-1", string(got.Key))
	assert.JSONEq(t, `{"n":1}`, string(got.Value))
	assert.Equal(t, "yes", got.Headers["x-test"])
	assert.Contains(t, got.Headers["traceparent"], tid.String())
	assert.Equal(t, tid, gotTrace)
	mu.Unlock()

	// Second consumer in the same group must only see the new message.
	require.NoError(t, p.Publish(context.Background(), kafkax.Message{Topic: topic, Key: []byte("order-2"), Value: []byte(`{"n":2}`)}))
	keys := make(chan string, 10)
	stop2 := runConsumer(t, brokers, topic, func(_ context.Context, m kafkax.Message) error {
		keys <- string(m.Key)
		return nil
	})
	defer stop2()
	select {
	case k := <-keys:
		assert.Equal(t, "order-2", k)
	case <-time.After(30 * time.Second):
		t.Fatal("second message not consumed")
	}
}

func TestUncommittedMessageIsRedelivered(t *testing.T) {
	brokers := testutil.Kafka(t)
	const topic = "kafkax-redeliver"
	testutil.CreateTopic(t, brokers, topic, 1)

	p, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	defer p.Close()
	require.NoError(t, p.Publish(context.Background(), kafkax.Message{Topic: topic, Key: []byte("k"), Value: []byte(`{}`)}))

	attempted := make(chan struct{}, 100)
	stop := runConsumer(t, brokers, topic, func(context.Context, kafkax.Message) error {
		attempted <- struct{}{}
		return errors.New("always failing")
	})
	<-attempted
	<-attempted // retried, not skipped
	stop()

	redelivered := make(chan string, 1)
	stop2 := runConsumer(t, brokers, topic, func(_ context.Context, m kafkax.Message) error {
		select {
		case redelivered <- string(m.Key):
		default:
		}
		return nil
	})
	defer stop2()
	select {
	case k := <-redelivered:
		assert.Equal(t, "k", k)
	case <-time.After(30 * time.Second):
		t.Fatal("failed message was not redelivered")
	}
}
