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

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/kafkax"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/adapter/catalog/catalogfake"
	"github.com/ngoxuanchien/e-commerce/services/order/internal/bootstrap"
	"github.com/ngoxuanchien/e-commerce/services/order/migrations"
)

const (
	orderSpec       = "../../../../api/openapi/order.yaml"
	eventsDir       = "../../../../api/events"
	pendingTimeout  = 2 * time.Second
	sweeperInterval = 500 * time.Millisecond
)

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
	t.Run("checkout is all-or-nothing on shortage", h.testCheckoutOutOfStock)
	t.Run("checkout 503 frees the idempotency key", h.testCheckoutCatalogDownThenRetry)
	t.Run("checkout with an empty cart", h.testCheckoutEmptyCart)
	// Task 15 adds the lifecycle, sweeper, pagination and readiness scenarios here.
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
	fake     *catalogfake.Fake
	events   *eventLog
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
	producer, err := kafkax.NewProducer(brokers)
	require.NoError(t, err)
	t.Cleanup(producer.Close)

	fake := catalogfake.New()
	a := bootstrap.New(bootstrap.Config{
		ShippingFeeFlat: 30_000, SweeperInterval: sweeperInterval, PendingTimeout: pendingTimeout,
		Catalog:       catalog.Config{Timeout: 300 * time.Millisecond, Retries: 2, Backoff: 10 * time.Millisecond},
		RelayInterval: 50 * time.Millisecond,
	}, bootstrap.Deps{Pool: pool, Redis: rdb, CatalogConn: fake.Dial(t), Producer: producer})

	wctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { defer close(done); a.RunWorkers(wctx) }()
	t.Cleanup(func() { cancel(); <-done })

	srv := httptest.NewServer(authctx.Middleware(a.API))
	t.Cleanup(srv.Close)

	return &harness{
		app: a, pool: pool, fake: fake, events: newEventLog(t, brokers), baseURL: srv.URL,
		contract: &http.Client{Transport: testutil.ContractTransport(t, orderSpec, nil)},
		raw:      &http.Client{},
	}
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
	b := map[string]any{"shipping_address": address, "payment_method": "COD"}
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
	ID           string                    `json:"id"`
	CheckoutID   string                    `json:"checkout_id"`
	Status       string                    `json:"status"`
	BuyerID      string                    `json:"buyer_id"`
	Total        int64                     `json:"total"`
	CancelReason *string                   `json:"cancel_reason"`
	Shop         struct{ ID, Name string } `json:"shop"`
	History      []struct {
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
