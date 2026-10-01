// Package itest runs order-svc end to end over HTTP — real Postgres, Redis
// and Kafka (testcontainers) plus the catalog fake — validating every request
// and response against api/openapi/order.yaml and every published event
// against api/events.
package itest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	orderv1 "github.com/ngoxuanchien/e-commerce/gen/order/v1"
	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/payment/paymentfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/bootstrap"
	"github.com/ngoxuanchien/e-commerce/services/order/migrations"
)

const (
	orderSpec       = "../../../../api/openapi/order.yaml"
	eventsDir       = "../../../../api/events"
	pendingTimeout  = 2 * time.Second
	sweeperInterval = 500 * time.Millisecond
	paymentTimeout  = time.Hour
	autoComplete    = time.Hour
	commissionRate  = 500
)

// baseConfig is the wiring every scenario shares: VNPAY is the only online
// method, the commission is 5 % and a checkout waits an hour for its payment.
func baseConfig() bootstrap.Config {
	return bootstrap.Config{
		ShippingFeeFlat: 30_000, SweeperInterval: sweeperInterval, PendingTimeout: pendingTimeout,
		Catalog:           catalog.Config{Timeout: 300 * time.Millisecond, Retries: 2, Backoff: 10 * time.Millisecond},
		RelayInterval:     50 * time.Millisecond,
		OnlineMethods:     []string{"VNPAY"},
		CommissionRateBps: commissionRate,
		PaymentTimeout:    paymentTimeout,
		AutoCompleteAfter: autoComplete,
	}
}

var ctx = context.Background()

var address = map[string]string{
	"full_name": "Nguyễn Văn A", "phone": "0900000000", "line1": "1 Lê Lợi",
	"ward": "Bến Nghé", "district": "Quận 1", "province": "TP.HCM",
}

func newID() string { return uuid.Must(uuid.NewV7()).String() }

// TestOrderAPI runs every scenario against one set of containers.
func TestOrderAPI(t *testing.T) {
	h := newHarness(t)
	t.Run("cart CRUD", h.testCartCRUD)
	t.Run("cart rejects own-shop, unknown and inactive SKUs", h.testCartRejects)
	t.Run("cart flags a SKU that became unavailable", h.testCartFlagsUnavailable)
	t.Run("cart answers 503 while catalog is down", h.testCartCatalogDown)
	t.Run("checkout from two shops", h.testCheckoutTwoShops)
	t.Run("checkout with VNPAY awaits payment", h.testCheckoutOnline)
	t.Run("checkout rejects a method that is not enabled", h.testCheckoutMethodNotEnabled)
	t.Run("checkout is all-or-nothing on shortage", h.testCheckoutOutOfStock)
	t.Run("checkout 503 frees the idempotency key", h.testCheckoutCatalogDownThenRetry)
	t.Run("checkout with an empty cart", h.testCheckoutEmptyCart)
	t.Run("lifecycle to COMPLETED", h.testLifecycle)
	t.Run("buyer cannot cancel a confirmed order", h.testBuyerCannotCancelConfirmed)
	t.Run("buyer cancels an unpaid checkout", h.testBuyerCancelsAwaitingCheckout)
	t.Run("seller cannot act on an unpaid order", h.testSellerCannotActOnAwaitingPayment)
	t.Run("strangers get 404 and non-sellers 403", h.testStrangers)
	t.Run("concurrent confirm and cancel: exactly one wins", h.testConcurrentConfirmCancel)
	t.Run("upper-case ids are canonicalized", h.testUppercaseIDs)
	t.Run("order lists paginate", h.testPagination)
	t.Run("sweeper cancels a crashed checkout in time", h.testSweeperCancelsCrashedCheckout)
	t.Run("readiness covers postgres, redis and kafka", h.testReadiness)
	t.Run("sweeper cancels a checkout whose payment never arrives", h.testPaymentTimeout)
	t.Run("a paid checkout survives the sweeper", h.testPaymentTimeoutPaidUntouched)
	t.Run("an unavailable payment-svc is retried, then cancels", h.testPaymentTimeoutUnavailableThenNotPaid)
	t.Run("sweeper completes a delivered order on time", h.testAutoComplete)
	t.Run("a captured payment confirms its orders", h.testPaymentSucceeded)
	t.Run("a late capture is refunded, not lost", h.testLateSuccessThenRefund)
	t.Run("a seller cancelling a paid order asks for a refund", h.testPaidOrderCancelledBySeller)
	t.Run("payment-svc reads a checkout over OrderInternal", h.testGetCheckout)
	t.Run("every published event matches its schema", h.testEventsValid)
}

type caller struct{ id, roles string }

func newBuyer() caller { return caller{id: newID(), roles: "buyer"} }
func sellerOf(shop catalogfake.Shop) caller {
	return caller{id: shop.SellerUserID, roles: "buyer,seller"}
}

type harness struct {
	app      *bootstrap.App
	pool     *pgxpool.Pool
	rdb      *redis.Client
	fake     *catalogfake.Fake
	pay      *paymentfake.Fake
	producer bootstrap.Producer
	brokers  []string    // bootstrap brokers of the payment.events consumer group
	kafka    *kgo.Client // publishes the payment.events the App consumes
	events   *eventLog
	orderRPC orderv1.OrderInternalClient // OrderInternal, as payment-svc calls it
	baseURL  string
	contract *http.Client // validates against order.yaml
	raw      *http.Client // for deliberately invalid requests
}

func newHarness(t *testing.T) *harness {
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	opts, err := redis.ParseURL(testutil.Redis(t))
	require.NoError(t, err)
	rdb := redis.NewClient(opts)
	t.Cleanup(func() { _ = rdb.Close() })

	brokers := testutil.Kafka(t)
	testutil.CreateTopic(t, brokers, event.TopicOrderEvents, 3)
	testutil.CreateTopic(t, brokers, event.TopicPaymentEvents, 3)
	producer, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	t.Cleanup(producer.Close)
	kafka, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	require.NoError(t, err)
	t.Cleanup(kafka.Close)

	fake := catalogfake.New()
	pay := paymentfake.New()
	a, err := bootstrap.New(baseConfig(), bootstrap.Deps{
		Pool: pool, Redis: rdb, CatalogConn: fake.Dial(t), PaymentConn: pay.Dial(t),
		Producer: producer, KafkaBrokers: brokers,
	})
	require.NoError(t, err)

	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); a.RunWorkers(wctx) }()
	t.Cleanup(func() { cancel(); <-done })

	srv := httptest.NewServer(authctx.Middleware(a.API))
	t.Cleanup(srv.Close)

	return &harness{
		app: a, pool: pool, rdb: rdb, fake: fake, pay: pay, producer: producer,
		brokers: brokers, kafka: kafka, orderRPC: orderInternalRPC(t, a),
		events: newEventLog(t, brokers), baseURL: srv.URL,
		contract: &http.Client{Transport: testutil.ContractTransport(t, orderSpec, nil)},
		raw:      &http.Client{},
	}
}

// orderInternalRPC serves a's OrderInternal on an in-memory listener and
// returns a client to it, so the scenarios talk to the same gRPC server the
// binary serves. It is closed when t ends.
func orderInternalRPC(t *testing.T, a *bootstrap.App) orderv1.OrderInternalClient {
	t.Helper()
	grpcSrv := grpc.NewServer()
	a.RegisterGRPC(grpcSrv)
	lis := bufconn.Listen(1 << 20)
	go func() { _ = grpcSrv.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///order-internal",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = conn.Close()
		grpcSrv.Stop()
	})
	return orderv1.NewOrderInternalClient(conn)
}

// deps wires a second App to the same containers and fakes, including the
// payment.events consumer group.
func (h *harness) deps(t *testing.T) bootstrap.Deps {
	t.Helper()
	return bootstrap.Deps{
		Pool: h.pool, Redis: h.rdb, CatalogConn: h.fake.Dial(t), PaymentConn: h.pay.Dial(t),
		Producer: h.producer, KafkaBrokers: h.brokers,
	}
}

// publishPayment produces a payment.events envelope keyed by checkoutID; tests
// reuse eventID to simulate redelivery.
func (h *harness) publishPayment(t *testing.T, eventID, checkoutID string, p event.Payload) {
	t.Helper()
	env, err := event.New(p, time.Now())
	require.NoError(t, err)
	env.EventID = eventID
	b, err := json.Marshal(env)
	require.NoError(t, err)
	require.NoError(t, h.kafka.ProduceSync(ctx, &kgo.Record{Topic: event.TopicPaymentEvents, Key: []byte(checkoutID), Value: b}).FirstErr())
}

// scoped starts a second App on the same containers and fakes with the base
// config adjusted by mutate, serving HTTP and running its workers until the
// subtest ends; it shares h.events. The main App keeps running with long
// payment and auto-complete timers, so only the scoped App acts on scoped
// orders — except that the main sweeper may also time out an expired
// checkout, which is harmless: both ask the same payment fake.
//
// The scoped App joins the same payment.events group as the main one; Kafka
// gives each record to one member of the group, and the scenarios that publish
// payment events run on the main App, whose group therefore picks them up.
func (h *harness) scoped(t *testing.T, mutate func(*bootstrap.Config)) *harness {
	t.Helper()
	cfg := baseConfig()
	mutate(&cfg)
	a, err := bootstrap.New(cfg, h.deps(t))
	require.NoError(t, err)

	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); a.RunWorkers(wctx) }()
	t.Cleanup(func() { cancel(); <-done })

	srv := httptest.NewServer(authctx.Middleware(a.API))
	t.Cleanup(srv.Close)

	scoped := *h
	scoped.app, scoped.baseURL = a, srv.URL
	scoped.orderRPC = orderInternalRPC(t, a)
	return &scoped
}

// try sends a request and returns status and body; safe off the test goroutine.
func (h *harness) try(client *http.Client, c caller, method, path string, body io.Reader, headers ...string) (int, []byte, error) {
	req, err := http.NewRequest(method, h.baseURL+path, body)
	if err != nil {
		return 0, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.id != "" {
		req.Header.Set(authctx.HeaderUserID, c.id)
		req.Header.Set(authctx.HeaderUserRoles, c.roles)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err // includes contract violations
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	return resp.StatusCode, b, err
}

// do sends a contract-validated request with an optional JSON body.
func (h *harness) do(t *testing.T, c caller, method, path string, body any, headers ...string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		r = bytes.NewReader(b)
	}
	status, b, err := h.try(h.contract, c, method, path, r, headers...)
	require.NoError(t, err)
	return status, b
}

// doRaw sends a request that bypasses contract validation.
func (h *harness) doRaw(t *testing.T, c caller, method, path, body string, headers ...string) (int, []byte) {
	t.Helper()
	var r io.Reader
	if body != "" {
		r = bytes.NewReader([]byte(body))
	}
	status, b, err := h.try(h.raw, c, method, path, r, headers...)
	require.NoError(t, err)
	return status, b
}

func decode[T any](t *testing.T, b []byte) T {
	t.Helper()
	var v T
	require.NoError(t, json.Unmarshal(b, &v), string(b))
	return v
}

func (h *harness) addToCart(t *testing.T, c caller, skuID string, q int) {
	t.Helper()
	status, body := h.do(t, c, http.MethodPut, "/api/cart/items/"+skuID, map[string]int{"quantity": q})
	require.Equal(t, http.StatusOK, status, string(body))
}

func checkoutBody(skuIDs ...string) map[string]any {
	return checkoutBodyWith("COD", skuIDs...)
}

// checkoutBodyWith is a checkout body paying with method (COD, VNPAY, STRIPE).
func checkoutBodyWith(method string, skuIDs ...string) map[string]any {
	b := map[string]any{"shipping_address": address, "payment_method": method}
	if len(skuIDs) > 0 {
		b["sku_ids"] = skuIDs
	}
	return b
}

func (h *harness) checkout(t *testing.T, c caller, key string, body map[string]any) (int, []byte) {
	t.Helper()
	return h.do(t, c, http.MethodPost, "/api/checkout", body, "Idempotency-Key", key)
}

// Response mirrors (only the fields the scenarios inspect).
type cartResp struct {
	Shops []struct {
		Shop  struct{ ID, Name, Slug string } `json:"shop"`
		Items []struct {
			SkuID     string `json:"sku_id"`
			UnitPrice int64  `json:"unit_price"`
			Quantity  int32  `json:"quantity"`
			Stock     int32  `json:"stock"`
			Available bool   `json:"available"`
		} `json:"items"`
	} `json:"shops"`
	Subtotal int64 `json:"subtotal"`
}

type orderResp struct {
	ID                string                          `json:"id"`
	CheckoutID        string                          `json:"checkout_id"`
	Status            string                          `json:"status"`
	BuyerID           string                          `json:"buyer_id"`
	Subtotal          int64                           `json:"subtotal"`
	Total             int64                           `json:"total"`
	CancelReason      *string                         `json:"cancel_reason"`
	PaymentMethod     string                          `json:"payment_method"`
	PaymentStatus     string                          `json:"payment_status"`
	PaymentDeadline   *string                         `json:"payment_deadline"`
	CommissionRateBps int32                           `json:"commission_rate_bps"`
	CommissionAmount  int64                           `json:"commission_amount"`
	DeliveredAt       *string                         `json:"delivered_at"`
	Shop              struct{ ID, Name, Slug string } `json:"shop"`
	History           []struct {
		From      *string `json:"from"`
		To        string  `json:"to"`
		ActorRole string  `json:"actor_role"`
		Note      *string `json:"note"`
		CreatedAt string  `json:"created_at"`
	} `json:"history"`
}

type checkoutResp struct {
	CheckoutID string      `json:"checkout_id"`
	Orders     []orderResp `json:"orders"`
}

type pageResp struct {
	Items      []orderResp `json:"items"`
	NextCursor *string     `json:"next_cursor"`
}

type errResp struct {
	Error struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	} `json:"error"`
}

// eventLog consumes order.events from the start, validating every message.
type eventLog struct {
	mu      sync.Mutex
	byOrder map[string][]event.Envelope
	total   int
	invalid []string
}

func newEventLog(t *testing.T, brokers []string) *eventLog {
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	schemas := map[event.Type]*jsonschema.Schema{}
	for typ, file := range map[event.Type]string{
		event.TypeOrderCreated:       "order-created.schema.json",
		event.TypeOrderStatusChanged: "order-status-changed.schema.json",
		event.TypeOrderCancelled:     "order-cancelled.schema.json",
		event.TypeOrderCompleted:     "order-completed.schema.json",
	} {
		sch, err := c.Compile(filepath.Join(eventsDir, file))
		require.NoError(t, err)
		schemas[typ] = sch
	}
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...), kgo.ConsumeTopics(event.TopicOrderEvents),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	require.NoError(t, err)

	l := &eventLog{byOrder: map[string][]event.Envelope{}}
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
	l.byOrder[string(r.Key)] = append(l.byOrder[string(r.Key)], env)
}

// waitTypes waits until orderID's events have the given types, in order.
func (l *eventLog) waitTypes(t *testing.T, orderID string, want ...event.Type) []event.Envelope {
	t.Helper()
	var got []event.Envelope
	require.Eventually(t, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()
		got = append([]event.Envelope(nil), l.byOrder[orderID]...)
		if len(got) != len(want) {
			return false
		}
		for i := range want {
			if got[i].EventType != want[i] {
				return false
			}
		}
		return true
	}, 15*time.Second, 50*time.Millisecond, "events of %s: want %v", orderID, want)
	return got
}

func (r checkoutResp) mustJSON(t *testing.T) []byte {
	t.Helper()
	b, err := json.Marshal(r)
	require.NoError(t, err)
	return b
}
