// Package itest runs payment-svc end to end — the real bootstrap over real Postgres and Kafka
// (testcontainers), order-svc faked over bufconn, the FAKE provider posting its signed webhooks to
// the test's own HTTP server. Every REST call goes through the payment.yaml ContractTransport and
// every event read back from payment.events is validated against api/events.
package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/timestamppb"

	orderv1 "github.com/ngoxuanchien/e-commerce/gen/order/v1"
	paymentv1 "github.com/ngoxuanchien/e-commerce/gen/payment/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/grpcx"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	orderadapter "github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/order"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/order/orderfake"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/bootstrap"
	"github.com/ngoxuanchien/e-commerce/services/payment/migrations"
)

const (
	paymentSpec = "../../../../api/openapi/payment.yaml"
	eventsDir   = "../../../../api/events"
	fakeSecret  = "itest-fake-secret"
	hookDelay   = 100 * time.Millisecond
)

var ctx = context.Background()

func newID() string { return uuid.Must(uuid.NewV7()).String() }

// TestPaymentFlow runs every scenario against one set of containers.
func TestPaymentFlow(t *testing.T) {
	h := newHarness(t)
	t.Run("create, pay on the FAKE page, PaymentSucceeded on Kafka", h.testPaySucceeds)
	t.Run("duplicate webhook delivery gives one PaymentSucceeded", h.testDuplicateWebhook)
	t.Run("stale webhook after success keeps SUCCEEDED", h.testStaleAfter)
	t.Run("late payment of an expired attempt is DUPLICATE and refunded", h.testLateDuplicateRefund)
	t.Run("OrderCancelled refunds the captured order", h.testOrderCancelledRefund)
	t.Run("a PENDING attempt without a FAKE session is retired, not an outage", h.testMissingSessionRetry)
	t.Run("CancelCheckoutPayments over gRPC", h.testCancelCheckoutPayments)
	t.Run("readiness covers postgres and kafka", h.testReadiness)
	t.Run("every published event matches its schema", h.testEventsValid)
}

type harness struct {
	app      *bootstrap.App
	pool     *pgxpool.Pool
	orders   *orderfake.Fake
	producer *kafkax.Producer
	events   *eventLog
	grpc     paymentv1.PaymentInternalClient
	baseURL  string
	contract *http.Client // validates against payment.yaml; never follows redirects
	raw      *http.Client // for the FAKE HTML page: the contract validator cannot decode text/html
}

func newHarness(t *testing.T) *harness {
	// Runs last (registered first): shows the service log only when the test failed.
	logs := &syncBuffer{}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("payment-svc log:\n%s", logs.String())
		}
	})
	logger := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelWarn}))

	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	lockPool, err := bootstrap.NewLockPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(lockPool.Close)

	brokers := testutil.Kafka(t)
	testutil.CreateTopic(t, brokers, event.TopicOrderEvents, 3)
	testutil.CreateTopic(t, brokers, event.TopicPaymentEvents, 3)
	producer, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	t.Cleanup(producer.Close)

	orders := orderfake.New()

	// The FAKE provider posts its webhooks to this server, which serves the bootstrap's router, so
	// the listener's address must be known before the app is built.
	srv := httptest.NewUnstartedServer(nil)
	baseURL := "http://" + srv.Listener.Addr().String()

	a, err := bootstrap.New(bootstrap.Config{
		Brokers: brokers, PublicBaseURL: baseURL,
		ProviderVNPay: bootstrap.ProviderFake, ProviderStripe: bootstrap.ProviderFake,
		FakeWebhookURL: baseURL + "/api/payments/fake/webhook", FakeWebhookSecret: fakeSecret, FakeWebhookDelay: hookDelay,
		StripeMinAmount:   10_000,
		ReconcileInterval: time.Hour, ReconcileMinAge: 2 * time.Minute,
		RefundMaxAttempts: 3, RefundInterval: 100 * time.Millisecond, RelayInterval: 50 * time.Millisecond,
		Order: orderadapter.Config{Timeout: time.Second, Retries: 1, Backoff: 10 * time.Millisecond},
	}, bootstrap.Deps{Pool: pool, LockPool: lockPool, OrderConn: orders.Dial(t), Producer: producer, Logger: logger})
	require.NoError(t, err)

	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); _ = a.RunWorkers(wctx, cancel) }()
	t.Cleanup(func() { cancel(); <-done })

	srv.Config.Handler = authctx.Middleware(a.API)
	srv.Start()
	t.Cleanup(srv.Close)

	grpcSrv, _ := grpcx.NewServer()
	a.RegisterGRPC(grpcSrv)
	lis, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = grpcSrv.Serve(lis) }()
	t.Cleanup(grpcSrv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return &harness{
		app: a, pool: pool, orders: orders, producer: producer, events: newEventLog(t, brokers),
		grpc: paymentv1.NewPaymentInternalClient(conn), baseURL: baseURL,
		contract: &http.Client{
			Transport:     testutil.ContractTransport(t, paymentSpec, nil),
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		raw: &http.Client{},
	}
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// ---- REST helpers ----

// paymentResp mirrors the Payment fields the scenarios inspect.
type paymentResp struct {
	ID          string  `json:"id"`
	CheckoutID  string  `json:"checkout_id"`
	Method      string  `json:"method"`
	Provider    string  `json:"provider"`
	Status      string  `json:"status"`
	Amount      int64   `json:"amount"`
	RedirectURL *string `json:"redirect_url"`
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(b, &v), string(b))
	return v
}

// send issues a contract-validated request and returns status and body.
func (h *harness) send(t *testing.T, method, path string, body io.Reader, buyerID string, headers ...string) (int, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, h.baseURL+path, body)
	require.NoError(t, err)
	if buyerID != "" {
		req.Header.Set(authctx.HeaderUserID, buyerID)
		req.Header.Set(authctx.HeaderUserRoles, "buyer")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := h.contract.Do(req)
	require.NoError(t, err) // includes contract violations
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, b
}

func (h *harness) createPayment(t *testing.T, buyerID, checkoutID string) paymentResp {
	t.Helper()
	b, err := json.Marshal(map[string]string{"checkout_id": checkoutID})
	require.NoError(t, err)
	status, body := h.send(t, http.MethodPost, "/api/payments", bytes.NewReader(b), buyerID,
		"Content-Type", "application/json", "Idempotency-Key", newID())
	require.Equal(t, http.StatusCreated, status, string(body))
	return decode[paymentResp](t, body)
}

func (h *harness) getPayment(t *testing.T, buyerID, paymentID string) paymentResp {
	t.Helper()
	status, body := h.send(t, http.MethodGet, "/api/payments/"+paymentID, nil, buyerID)
	require.Equal(t, http.StatusOK, status, string(body))
	return decode[paymentResp](t, body)
}

func (h *harness) waitStatus(t *testing.T, buyerID, paymentID, want string) {
	t.Helper()
	require.Eventually(t, func() bool { return h.getPayment(t, buyerID, paymentID).Status == want },
		10*time.Second, 50*time.Millisecond, "payment %s never became %s", paymentID, want)
}

// fakePage opens the FAKE hosted page; the hooks given arm the session (the page route arms them).
func (h *harness) fakePage(t *testing.T, paymentID string, hooks url.Values) {
	t.Helper()
	path := "/api/payments/fake/checkout/" + paymentID
	if len(hooks) > 0 {
		path += "?" + hooks.Encode()
	}
	resp, err := h.raw.Get(h.baseURL + path)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
	require.Contains(t, resp.Header.Get("Content-Type"), "text/html")
	require.Contains(t, string(body), "<form")
}

// fakeSubmit posts the page form; the FAKE adapter then delivers its signed webhook.
func (h *harness) fakeSubmit(t *testing.T, paymentID, outcome string, hooks url.Values) {
	t.Helper()
	form := url.Values{"outcome": {outcome}}
	for k, v := range hooks {
		form[k] = v
	}
	status, body := h.send(t, http.MethodPost, "/api/payments/fake/checkout/"+paymentID,
		strings.NewReader(form.Encode()), "", "Content-Type", "application/x-www-form-urlencoded")
	require.Equal(t, http.StatusFound, status, string(body))
}

// ---- order-svc fake ----

type checkout struct {
	*orderv1.GetCheckoutResponse
}

// newCheckout scripts an AWAITING_PAYMENT checkout of a fresh buyer with one order per total.
func (h *harness) newCheckout(method string, deadline time.Time, totals ...int64) checkout {
	c := orderfake.NewCheckout(newID(), method, deadline, totals...)
	h.orders.Put(c)
	return checkout{c}
}

func (h *harness) setDeadline(c checkout, deadline time.Time) {
	c.PaymentDeadline = timestamppb.New(deadline)
	h.orders.Put(c.GetCheckoutResponse)
}

// ---- event log on payment.events ----

// eventLog consumes payment.events from the start, validating every message; events are indexed by
// message key (the checkout id).
type eventLog struct {
	mu         sync.Mutex
	byCheckout map[string][]event.Envelope
	total      int
	invalid    []string
}

func newEventLog(t *testing.T, brokers []string) *eventLog {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	schemas := map[event.Type]*jsonschema.Schema{}
	for typ, file := range map[event.Type]string{
		event.TypePaymentSucceeded: "payment-succeeded.schema.json",
		event.TypePaymentFailed:    "payment-failed.schema.json",
		event.TypePaymentRefunded:  "payment-refunded.schema.json",
		event.TypeRefundFailed:     "refund-failed.schema.json",
	} {
		sch, err := c.Compile(filepath.Join(eventsDir, file))
		require.NoError(t, err)
		schemas[typ] = sch
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics(event.TopicPaymentEvents),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	require.NoError(t, err)

	l := &eventLog{byCheckout: map[string][]event.Envelope{}}
	pctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			fetches := cl.PollFetches(pctx)
			if pctx.Err() != nil {
				return
			}
			fetches.EachRecord(func(r *kgo.Record) { l.add(schemas, r) })
		}
	}()
	t.Cleanup(func() { cancel(); <-done; cl.Close() })
	return l
}

func (l *eventLog) add(schemas map[event.Type]*jsonschema.Schema, r *kgo.Record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.total++
	env, err := event.Decode(r.Value)
	if err != nil {
		l.invalid = append(l.invalid, fmt.Sprintf("decode %s: %v", r.Key, err))
		return
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(r.Value))
	sch := schemas[env.EventType]
	switch {
	case err != nil || sch == nil:
		l.invalid = append(l.invalid, fmt.Sprintf("%s %s: no schema or bad JSON: %v", env.EventType, r.Key, err))
	default:
		if err := sch.Validate(inst); err != nil {
			l.invalid = append(l.invalid, fmt.Sprintf("%s %s: %v", env.EventType, r.Key, err))
		}
	}
	l.byCheckout[string(r.Key)] = append(l.byCheckout[string(r.Key)], env)
}

func (l *eventLog) snapshot(checkoutID string) []event.Envelope {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]event.Envelope(nil), l.byCheckout[checkoutID]...)
}

// waitCount waits until the checkout has at least n events and returns them all.
func (l *eventLog) waitCount(t *testing.T, checkoutID string, n int) []event.Envelope {
	t.Helper()
	var got []event.Envelope
	require.Eventually(t, func() bool {
		got = l.snapshot(checkoutID)
		return len(got) >= n
	}, 15*time.Second, 50*time.Millisecond, "checkout %s: want %d events, have %d", checkoutID, n, len(got))
	return got
}

// stays asserts the checkout's event count stays exactly n for d (something that must NOT happen).
func (l *eventLog) stays(t *testing.T, checkoutID string, n int, d time.Duration) {
	t.Helper()
	require.Never(t, func() bool { return len(l.snapshot(checkoutID)) != n }, d, 50*time.Millisecond,
		"checkout %s: event count left %d", checkoutID, n)
}

func data[T event.Payload](t *testing.T, env event.Envelope) T {
	t.Helper()
	v, err := event.DecodeData[T](env)
	require.NoError(t, err)
	return v
}
