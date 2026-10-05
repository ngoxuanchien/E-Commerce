package bootstrap_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/bootstrap"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/testsupport"
)

func TestEventsInWalletOutPayoutEventsOut(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db := testsupport.DB(t)
	brokers := testutil.Kafka(t)
	for _, topic := range []string{event.TopicPaymentEvents, event.TopicOrderEvents, event.TopicPayoutEvents} {
		testutil.CreateTopic(t, brokers, topic, 3)
	}
	producer, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	defer producer.Close()

	svc, err := bootstrap.New(bootstrap.Config{
		Brokers: brokers, PayoutMinAmount: 50000, SettleInterval: 100 * time.Millisecond, SettleDelay: 0,
		PendingInterval: 100 * time.Millisecond, RelayInterval: 50 * time.Millisecond,
	}, bootstrap.Deps{Pool: db, Producer: producer, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { defer close(done); svc.RunWorkers(ctx) }()
	defer func() { cancel(); <-done }()

	seller, order, checkout, payment := uuid.New(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	publish := func(topic, key string, p event.Payload) {
		b, err := event.Encode(p, time.Now())
		require.NoError(t, err)
		require.NoError(t, producer.Publish(ctx, kafkax.Message{Topic: topic, Key: []byte(key), Value: b}))
	}
	// Completion first, on the other topic: it must wait as a pending posting.
	completed := event.OrderCompleted{OrderID: order, CheckoutID: checkout, SellerUserID: seller.String(),
		PaymentMethod: "STRIPE", Total: 200000, CommissionAmount: 10000, CompletedBy: "system"}
	publish(event.TopicOrderEvents, order, completed)
	succeeded := event.PaymentSucceeded{PaymentID: payment, CheckoutID: checkout, BuyerID: uuid.NewString(), Provider: "stripe",
		ProviderRef: "pi_1", Amount: 200000, Allocations: []event.Allocation{{OrderID: order, SellerUserID: seller.String(), Amount: 200000, CommissionAmount: 10000}}}
	publish(event.TopicPaymentEvents, checkout, succeeded)
	publish(event.TopicPaymentEvents, checkout, succeeded) // the same fact again, new event id

	api := authctx.Middleware(svc.API)
	get := func(path string) map[string]any {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set(authctx.HeaderUserID, seller.String())
		req.Header.Set(authctx.HeaderUserRoles, "seller")
		api.ServeHTTP(rec, req)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	require.Eventually(t, func() bool { return get("/api/seller/wallet")["available"] == 190000.0 }, 60*time.Second, 200*time.Millisecond)

	var pending int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM pending_postings`).Scan(&pending))
	assert.Zero(t, pending)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/seller/payouts", strings.NewReader(`{"amount":190000}`))
	req.Header.Set(authctx.HeaderUserID, seller.String())
	req.Header.Set(authctx.HeaderUserRoles, "seller")
	req.Header.Set("Idempotency-Key", "e2e-1")
	api.ServeHTTP(rec, req)
	require.Equal(t, http.StatusCreated, rec.Code, rec.Body.String())

	reader, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics(event.TopicPayoutEvents),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	require.NoError(t, err)
	defer reader.Close()
	seen := map[event.Type]bool{}
	deadline := time.Now().Add(60 * time.Second)
	for (!seen[event.TypePayoutRequested] || !seen[event.TypePayoutSettled]) && time.Now().Before(deadline) {
		pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
		reader.PollFetches(pctx).EachRecord(func(r *kgo.Record) {
			assert.Equal(t, seller.String(), string(r.Key))
			env, err := event.Decode(r.Value)
			require.NoError(t, err)
			seen[env.EventType] = true
		})
		pcancel()
	}
	assert.True(t, seen[event.TypePayoutRequested], "PayoutRequested published")
	assert.True(t, seen[event.TypePayoutSettled], "PayoutSettled published")

	w := get("/api/seller/wallet")
	assert.Equal(t, 0.0, w["available"])
	assert.Equal(t, 0.0, w["pending_payout"])
	// OrderCompleted + 2x PaymentSucceeded consumed: the duplicate was a no-op.
	require.Eventually(t, func() bool {
		var n int
		return db.QueryRow(ctx, `SELECT count(*) FROM processed_events WHERE consumer = 'ledger-svc'`).Scan(&n) == nil && n == 3
	}, 60*time.Second, 200*time.Millisecond)
	var journals int
	require.NoError(t, db.QueryRow(ctx, `SELECT count(*) FROM journal_transactions`).Scan(&journals))
	assert.Equal(t, 5, journals) // PAY, CAPTURE, RELEASE, PAYOUT_REQUEST, PAYOUT_SETTLE
}
