package outbox_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/outbox"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
)

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

func newPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()
	pool, err := pgxx.NewPool(ctx, testutil.Postgres(t))
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, outbox.SchemaSQL)
	require.NoError(t, err)
	return pool
}

func write(t *testing.T, pool *pgxpool.Pool, ctx context.Context, m outbox.Message, fail bool) {
	t.Helper()
	err := pgxx.WithTx(ctx, pool, func(tx pgx.Tx) error {
		if err := outbox.Write(ctx, tx, m); err != nil {
			return err
		}
		if fail {
			return errors.New("business failure")
		}
		return nil
	})
	if !fail {
		require.NoError(t, err)
	}
}

func consumeAll(t *testing.T, brokers []string, topic string, wait time.Duration) []*kgo.Record {
	t.Helper()
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics(topic),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	require.NoError(t, err)
	defer cl.Close()
	var recs []*kgo.Record
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		cl.PollFetches(ctx).EachRecord(func(r *kgo.Record) { recs = append(recs, r) })
		cancel()
	}
	return recs
}

func TestRolledBackTxPublishesNothingAndCommittedTxPublishesOnce(t *testing.T) {
	otel.SetTextMapPropagator(propagation.TraceContext{})
	ctx := context.Background()
	pool := newPool(t)
	brokers := testutil.Kafka(t)
	const topic = "outbox-test"
	testutil.CreateTopic(t, brokers, topic, 3)

	producer, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	defer producer.Close()
	relay := outbox.NewRelay(pool, producer, outbox.RelayConfig{Logger: discard})

	tid, _ := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	sid, _ := trace.SpanIDFromHex("0102030405060708")
	reqCtx := trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tid, SpanID: sid, TraceFlags: trace.FlagsSampled,
	}))

	write(t, pool, reqCtx, outbox.Message{Topic: topic, Key: "rolled-back", Payload: []byte(`{"n":0}`)}, true)
	write(t, pool, reqCtx, outbox.Message{Topic: topic, Key: "committed", Payload: []byte(`{"n":1}`)}, false)

	n, err := relay.ProcessBatch(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	n, err = relay.ProcessBatch(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n, "already published rows are not sent again")

	recs := consumeAll(t, brokers, topic, 5*time.Second)
	require.Len(t, recs, 1)
	assert.Equal(t, "committed", string(recs[0].Key))
	assert.JSONEq(t, `{"n":1}`, string(recs[0].Value))
	var traceparent string
	for _, h := range recs[0].Headers {
		if h.Key == "traceparent" {
			traceparent = string(h.Value)
		}
	}
	assert.Contains(t, traceparent, tid.String(), "trace context of the writing request is carried")
}

type failingPublisher struct{ calls int }

func (f *failingPublisher) Publish(context.Context, ...kafkax.Message) error {
	f.calls++
	return errors.New("kafka down")
}

type recordingPublisher struct{ msgs []kafkax.Message }

func (r *recordingPublisher) Publish(_ context.Context, msgs ...kafkax.Message) error {
	r.msgs = append(r.msgs, msgs...)
	return nil
}

func TestPublishFailureKeepsRowsForRetry(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	write(t, pool, ctx, outbox.Message{Topic: "t", Key: "k", Payload: []byte(`{}`)}, false)

	failing := &failingPublisher{}
	n, err := outbox.NewRelay(pool, failing, outbox.RelayConfig{Logger: discard}).ProcessBatch(ctx)
	assert.Error(t, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, 1, failing.calls)

	var unpublished int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&unpublished))
	assert.Equal(t, 1, unpublished)

	ok := &recordingPublisher{}
	n, err = outbox.NewRelay(pool, ok, outbox.RelayConfig{Logger: discard}).ProcessBatch(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	require.Len(t, ok.msgs, 1)
	assert.Equal(t, "k", string(ok.msgs[0].Key))
}

func TestBatchSizeAndOrder(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	for _, k := range []string{"a", "b", "c"} {
		write(t, pool, ctx, outbox.Message{Topic: "t", Key: k, Payload: []byte(`{}`)}, false)
	}
	rec := &recordingPublisher{}
	relay := outbox.NewRelay(pool, rec, outbox.RelayConfig{BatchSize: 2, Logger: discard})

	n, err := relay.ProcessBatch(ctx)
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	n, err = relay.ProcessBatch(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	var keys []string
	for _, m := range rec.msgs {
		keys = append(keys, string(m.Key))
	}
	assert.Equal(t, []string{"a", "b", "c"}, keys)
}

func TestRunDrainsUntilCancelled(t *testing.T) {
	ctx := context.Background()
	pool := newPool(t)
	write(t, pool, ctx, outbox.Message{Topic: "t", Key: "k", Payload: []byte(`{}`)}, false)

	rec := &recordingPublisher{}
	relay := outbox.NewRelay(pool, rec, outbox.RelayConfig{Interval: 20 * time.Millisecond, Logger: discard})
	rctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- relay.Run(rctx) }()

	require.Eventually(t, func() bool {
		var n int
		_ = pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE published_at IS NULL`).Scan(&n)
		return n == 0
	}, 5*time.Second, 20*time.Millisecond)
	cancel()
	assert.NoError(t, <-done)
}
