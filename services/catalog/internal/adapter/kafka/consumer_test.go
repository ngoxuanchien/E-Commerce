package kafka_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/kafka"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/catalog/internal/testsupport"
)

func TestMain(m *testing.M) { os.Exit(testsupport.RunWithPostgres(m)) }

var discard = slog.New(slog.NewTextHandler(io.Discard, nil))

type recorder struct {
	calls [][2]uuid.UUID
	err   error
}

func (r *recorder) ReleaseForEvent(_ context.Context, eventID, orderID uuid.UUID) error {
	r.calls = append(r.calls, [2]uuid.UUID{eventID, orderID})
	return r.err
}

func encode(t *testing.T, p event.Payload) (event.Envelope, []byte) {
	t.Helper()
	env, err := event.New(p, time.Now())
	require.NoError(t, err)
	b, err := json.Marshal(env)
	require.NoError(t, err)
	return env, b
}

func TestHandlerReleasesOnOrderCancelled(t *testing.T) {
	rec := &recorder{}
	order := uuid.Must(uuid.NewV7())
	env, b := encode(t, event.OrderCancelled{OrderID: order.String(), Reason: "changed mind", CancelledBy: event.CancelledByBuyer})

	require.NoError(t, kafka.NewHandler(rec, discard).Handle(context.Background(), kafkax.Message{Value: b}))
	require.Len(t, rec.calls, 1)
	assert.Equal(t, env.EventID, rec.calls[0][0].String())
	assert.Equal(t, order, rec.calls[0][1])
}

func TestHandlerSkipsOtherAndPoisonRecords(t *testing.T) {
	rec := &recorder{}
	h := kafka.NewHandler(rec, discard)
	_, created := encode(t, event.OrderCreated{OrderID: uuid.NewString(), Items: []event.OrderItem{}})
	_, badOrder := encode(t, event.OrderCancelled{OrderID: "not-a-uuid", Reason: "x", CancelledBy: event.CancelledBySystem})
	for name, value := range map[string][]byte{
		"other type":    created,
		"not json":      []byte("not json"),
		"wrong version": []byte(`{"event_id":"` + uuid.NewString() + `","event_type":"OrderCancelled","version":9,"occurred_at":"2026-09-28T10:00:00Z","data":{}}`),
		"bad order id":  badOrder,
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
	_, b := encode(t, event.OrderCancelled{OrderID: uuid.NewString(), Reason: "x", CancelledBy: event.CancelledBySeller})
	assert.Error(t, kafka.NewHandler(rec, discard).Handle(context.Background(), kafkax.Message{Value: b}))
}

func TestOrderCancelledPublishedTwiceReturnsStockOnce(t *testing.T) {
	pool := testsupport.DB(t)
	inv := app.NewInventoryService(postgres.New(pool))
	ctx := context.Background()
	f := testsupport.SeedSku(t, pool, 10)
	orderA, orderB := testsupport.NewID(t), testsupport.NewID(t)
	require.NoError(t, inv.Reserve(ctx, orderA, []domain.StockItem{{SkuID: f.SkuID, Quantity: 3}}))
	require.NoError(t, inv.Reserve(ctx, orderB, []domain.StockItem{{SkuID: f.SkuID, Quantity: 2}}))
	require.Equal(t, int32(5), testsupport.Stock(t, pool, f.SkuID))

	brokers := testutil.Kafka(t)
	// One partition so records are consumed in publish order and the last
	// event proves every earlier one was handled.
	testutil.CreateTopic(t, brokers, event.TopicOrderEvents, 1)

	_, cancelA := encode(t, event.OrderCancelled{OrderID: orderA.String(), Reason: "x", CancelledBy: event.CancelledByBuyer})
	_, created := encode(t, event.OrderCreated{OrderID: uuid.NewString(), Items: []event.OrderItem{}})
	lastEnv, cancelB := encode(t, event.OrderCancelled{OrderID: orderB.String(), Reason: "y", CancelledBy: event.CancelledBySystem})

	producer, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	defer producer.Close()
	msg := func(key uuid.UUID, v []byte) kafkax.Message {
		return kafkax.Message{Topic: event.TopicOrderEvents, Key: []byte(key.String()), Value: v}
	}
	require.NoError(t, producer.Publish(ctx,
		msg(orderA, cancelA), msg(orderA, cancelA), // duplicate delivery
		msg(orderA, []byte("poison")), msg(orderA, created), msg(orderB, cancelB)))

	consumer, err := kafka.NewConsumer(brokers, kafka.NewHandler(inv, discard), discard)
	require.NoError(t, err)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- consumer.Run(runCtx) }()
	t.Cleanup(func() {
		cancel()
		<-done
		consumer.Close()
	})

	require.Eventually(t, func() bool {
		var ok bool
		_ = pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM processed_events WHERE event_id = $1)`, lastEnv.EventID).Scan(&ok)
		return ok
	}, 60*time.Second, 100*time.Millisecond)

	assert.Equal(t, int32(10), testsupport.Stock(t, pool, f.SkuID), "3 + 2 returned, the duplicate is ignored")
	var processed int
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*) FROM processed_events`).Scan(&processed))
	assert.Equal(t, 2, processed)
}
