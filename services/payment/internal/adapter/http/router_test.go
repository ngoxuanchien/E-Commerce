package httpadapter_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/pgxx"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/fake"
	httpadapter "github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/http"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/order"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/order/orderfake"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/payment/internal/domain"
	"github.com/ngoxuanchien/e-commerce/services/payment/migrations"
)

const (
	spec   = "../../../../../api/openapi/payment.yaml"
	secret = "fake-webhook-secret"
)

var ctx = context.Background()

func init() {
	// The contract transport cannot decode text/html bodies on its own; the hosted page is opaque HTML.
	openapi3filter.RegisterBodyDecoder("text/html", openapi3filter.FileBodyDecoder)
}

func newID() string { return uuid.Must(uuid.NewV7()).String() }

type harness struct {
	t      *testing.T
	pool   *pgxpool.Pool
	store  *postgres.Store
	orders *orderfake.Fake
	fake   *fake.Provider
	srv    *httptest.Server
	buyer  string
	strict *http.Client // every exchange is validated against payment.yaml
	raw    *http.Client // for requests the contract itself forbids
}

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func newHarness(t *testing.T) *harness {
	t.Helper()
	dsn := testutil.Postgres(t)
	require.NoError(t, pgxx.Migrate(dsn, migrations.FS))
	pool, err := pgxx.NewPool(ctx, dsn)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	h := &harness{t: t, pool: pool, store: postgres.NewStore(pool), orders: orderfake.New(), buyer: newID()}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	h.srv = httptest.NewUnstartedServer(nil)
	h.fake = fake.New(pool, fake.Config{
		PublicBaseURL: "http://gw.test", WebhookURL: "http://" + h.srv.Listener.Addr().String() + "/api/payments/fake/webhook",
		WebhookSecret: secret, WebhookDelay: 10 * time.Millisecond, Logger: log,
	})
	t.Cleanup(h.fake.Wait)

	obs := app.NewObserver(h.store, time.Now, log)
	client := order.New(h.orders.Dial(t), order.Config{Timeout: time.Second, Retries: 0, Backoff: time.Millisecond})
	creator := app.NewCreator(h.store, pool, client, app.NewProviders(h.fake, h.fake), obs,
		app.CreateConfig{PublicBaseURL: "http://gw.test", StripeMinAmount: 12_000}, time.Now)
	cb := httpadapter.NewFakeCallbacks(httpadapter.FakeDeps{
		Provider: h.fake, Store: h.store, Observer: obs, WebhookSecret: secret, Logger: log,
	})
	h.srv.Config.Handler = authctx.Middleware(httpadapter.NewRouter(creator, cb))
	h.srv.Start()
	t.Cleanup(h.srv.Close)

	h.strict = &http.Client{Transport: testutil.ContractTransport(t, spec, nil), CheckRedirect: noRedirect}
	h.raw = &http.Client{CheckRedirect: noRedirect}
	return h
}

func (h *harness) checkout(method string, totals ...int64) string {
	co := orderfake.NewCheckout(h.buyer, method, time.Now().Add(time.Hour), totals...)
	h.orders.Put(co)
	return co.GetCheckoutId()
}

type reply struct {
	Status int
	Header http.Header
	Body   []byte
}

func (r reply) json(t *testing.T) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, json.Unmarshal(r.Body, &m), string(r.Body))
	return m
}

func (h *harness) do(c *http.Client, method, path string, headers map[string]string, body []byte) reply {
	h.t.Helper()
	req, err := http.NewRequest(method, h.srv.URL+path, bytes.NewReader(body))
	require.NoError(h.t, err)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	require.NoError(h.t, err)
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	require.NoError(h.t, err)
	return reply{Status: resp.StatusCode, Header: resp.Header, Body: b}
}

func (h *harness) as(user, key string) map[string]string {
	m := map[string]string{authctx.HeaderUserID: user, "Content-Type": "application/json"}
	if key != "" {
		m["Idempotency-Key"] = key
	}
	return m
}

func (h *harness) create(key, checkoutID string) reply {
	h.t.Helper()
	return h.do(h.strict, "POST", "/api/payments", h.as(h.buyer, key), []byte(`{"checkout_id":"`+checkoutID+`"}`))
}

func (h *harness) mustCreate(method string, total int64) map[string]any {
	h.t.Helper()
	r := h.create(newID(), h.checkout(method, total))
	require.Equal(h.t, 201, r.Status, string(r.Body))
	return r.json(h.t)
}

func (h *harness) status(paymentID string) string {
	h.t.Helper()
	p, err := h.store.GetPayment(ctx, paymentID)
	require.NoError(h.t, err)
	return string(p.Status)
}

func (h *harness) outbox(checkoutID string) int {
	h.t.Helper()
	var n int
	require.NoError(h.t, h.pool.QueryRow(ctx, `SELECT count(*) FROM outbox WHERE msg_key = $1`, checkoutID).Scan(&n))
	return n
}

func (h *harness) webhook(body []byte, sig string) reply {
	h.t.Helper()
	return h.do(h.strict, "POST", "/api/payments/fake/webhook", map[string]string{"Content-Type": "application/json", "X-Fake-Signature": sig}, body)
}

// rawWebhook posts through the raw client: the body may itself violate the contract.
func (h *harness) rawWebhook(body string) reply {
	h.t.Helper()
	return h.do(h.raw, "POST", "/api/payments/fake/webhook",
		map[string]string{"Content-Type": "application/json", "X-Fake-Signature": fake.Sign(secret, []byte(body), time.Now())}, []byte(body))
}

func (h *harness) signedWebhook(eventID, paymentID, outcome string) reply {
	body, _ := json.Marshal(fake.Event{EventID: eventID, PaymentID: paymentID, Outcome: outcome})
	return h.webhook(body, fake.Sign(secret, body, time.Now()))
}

func TestCreateAndGetPayment(t *testing.T) {
	h := newHarness(t)
	checkout := h.checkout("VNPAY", 100_000, 250_000)

	r := h.create("k1", checkout)
	require.Equal(t, 201, r.Status, string(r.Body))
	p := r.json(t)
	assert.Equal(t, checkout, p["checkout_id"])
	assert.Equal(t, "VNPAY", p["method"])
	assert.Equal(t, "fake", p["provider"])
	assert.Equal(t, "PENDING", p["status"])
	assert.EqualValues(t, 350_000, p["amount"])
	assert.Equal(t, "http://gw.test/api/payments/fake/checkout/"+p["id"].(string), p["redirect_url"])
	assert.Nil(t, p["failure_reason"])

	again := h.create("k1", checkout)
	assert.Equal(t, 201, again.Status)
	assert.JSONEq(t, string(r.Body), string(again.Body), "a replay returns the same body")

	got := h.do(h.strict, "GET", "/api/payments/"+p["id"].(string), h.as(h.buyer, ""), nil)
	require.Equal(t, 200, got.Status, string(got.Body))
	assert.JSONEq(t, string(r.Body), string(got.Body))
}

func TestRedirectURLIsNullOnceTerminal(t *testing.T) {
	h := newHarness(t)
	p := h.mustCreate("VNPAY", 100_000)
	id := p["id"].(string)
	require.Equal(t, 200, h.signedWebhook(newID(), id, "fail").Status)

	got := h.do(h.strict, "GET", "/api/payments/"+id, h.as(h.buyer, ""), nil).json(t)
	assert.Equal(t, "FAILED", got["status"])
	assert.Nil(t, got["redirect_url"])
	assert.Equal(t, "fake_declined", got["failure_reason"])
}

func TestCreateRejections(t *testing.T) {
	h := newHarness(t)
	strangers := orderfake.NewCheckout(newID(), "VNPAY", time.Now().Add(time.Hour), 100_000)
	h.orders.Put(strangers)
	cod := orderfake.NewCheckout(h.buyer, "COD", time.Now().Add(time.Hour), 100_000)
	late := orderfake.NewCheckout(h.buyer, "VNPAY", time.Now().Add(-time.Minute), 100_000)
	paid := orderfake.NewCheckout(h.buyer, "VNPAY", time.Now().Add(time.Hour), 100_000)
	h.orders.Put(cod)
	h.orders.Put(late)
	h.orders.Put(paid)
	notAwaiting := h.checkout("VNPAY", 100_000)
	h.orders.SetOrderStatus(notAwaiting, "PAID")
	small := h.checkout("STRIPE", 5_000)

	// Pay one checkout so ALREADY_PAID applies.
	first := h.create(newID(), paid.GetCheckoutId()).json(t)["id"].(string)
	require.Equal(t, 200, h.signedWebhook(newID(), first, "succeed").Status)

	cases := []struct {
		name, checkout, code, reason string
		status                       int
	}{
		{"unknown checkout", newID(), "NOT_FOUND", "", 404},
		{"another buyer's checkout", strangers.GetCheckoutId(), "NOT_FOUND", "", 404},
		{"COD", cod.GetCheckoutId(), "INVALID_STATE_TRANSITION", "COD_CHECKOUT", 409},
		{"not awaiting payment", notAwaiting, "INVALID_STATE_TRANSITION", "NOT_AWAITING_PAYMENT", 409},
		{"deadline passed", late.GetCheckoutId(), "INVALID_STATE_TRANSITION", "DEADLINE_PASSED", 409},
		{"already paid", paid.GetCheckoutId(), "INVALID_STATE_TRANSITION", "ALREADY_PAID", 409},
		{"below the Stripe minimum", small, "INVALID_STATE_TRANSITION", "BELOW_PROVIDER_MINIMUM", 409},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := h.create("key-"+c.name, c.checkout)
			require.Equal(t, c.status, r.Status, string(r.Body))
			e := r.json(t)["error"].(map[string]any)
			assert.Equal(t, c.code, e["code"])
			if c.reason != "" {
				assert.Equal(t, c.reason, e["details"].(map[string]any)["reason"])
			}
			replay := h.create("key-"+c.name, c.checkout)
			assert.Equal(t, r.Status, replay.Status)
			assert.JSONEq(t, string(r.Body), string(replay.Body))
		})
	}
}

func TestIdempotencyKeyReuseIs422(t *testing.T) {
	h := newHarness(t)
	a, b := h.checkout("VNPAY", 100_000), h.checkout("VNPAY", 100_000)
	require.Equal(t, 201, h.create("k", a).Status)
	r := h.create("k", b)
	assert.Equal(t, 422, r.Status)
	assert.Equal(t, "IDEMPOTENCY_KEY_REUSED", r.json(t)["error"].(map[string]any)["code"])
}

func TestOrderServiceOutageIs503(t *testing.T) {
	h := newHarness(t)
	checkout := h.checkout("VNPAY", 100_000)
	h.orders.FailNext(14, 1) // UNAVAILABLE
	r := h.create("k", checkout)
	assert.Equal(t, 503, r.Status)
	assert.Equal(t, "UPSTREAM_UNAVAILABLE", r.json(t)["error"].(map[string]any)["code"])
	assert.Equal(t, 201, h.create("k", checkout).Status, "the key was released")
}

func TestGetPaymentIsOwnerOnly(t *testing.T) {
	h := newHarness(t)
	id := h.mustCreate("VNPAY", 100_000)["id"].(string)

	other := h.do(h.strict, "GET", "/api/payments/"+id, h.as(newID(), ""), nil)
	assert.Equal(t, 404, other.Status)
	unknown := h.do(h.strict, "GET", "/api/payments/"+newID(), h.as(h.buyer, ""), nil)
	assert.Equal(t, 404, unknown.Status)
}

func TestRequestsRejectedBeforeReachingTheUseCase(t *testing.T) {
	h := newHarness(t) // the contract forbids these requests, so they go through the raw client
	checkout := h.checkout("VNPAY", 100_000)
	body := `{"checkout_id":"` + checkout + `"}`
	for _, c := range []struct {
		name, method, path, body string
		headers                  map[string]string
		status                   int
		code                     string
	}{
		{"anonymous create", "POST", "/api/payments", body, map[string]string{"Idempotency-Key": "k"}, 401, "UNAUTHENTICATED"},
		{"caller is not a uuid", "GET", "/api/payments/" + newID(), "", map[string]string{authctx.HeaderUserID: "admin"}, 401, "UNAUTHENTICATED"},
		{"missing key", "POST", "/api/payments", body, h.as(h.buyer, ""), 400, "VALIDATION_ERROR"},
		{"key too long", "POST", "/api/payments", body, h.as(h.buyer, strings.Repeat("k", 101)), 400, "VALIDATION_ERROR"},
		{"checkout id not a uuid", "POST", "/api/payments", `{"checkout_id":"x"}`, h.as(h.buyer, "k"), 400, "VALIDATION_ERROR"},
		{"unknown field", "POST", "/api/payments", `{"checkout_id":"` + checkout + `","x":1}`, h.as(h.buyer, "k"), 400, "VALIDATION_ERROR"},
		{"payment id not a uuid", "GET", "/api/payments/nope", "", h.as(h.buyer, ""), 404, "NOT_FOUND"},
	} {
		t.Run(c.name, func(t *testing.T) {
			r := h.do(h.raw, c.method, c.path, c.headers, []byte(c.body))
			assert.Equal(t, c.status, r.Status, string(r.Body))
			assert.Equal(t, c.code, r.json(t)["error"].(map[string]any)["code"])
		})
	}
}

func TestFakeCheckoutPage(t *testing.T) {
	h := newHarness(t)
	id := h.mustCreate("VNPAY", 150_000)["id"].(string)

	r := h.do(h.strict, "GET", "/api/payments/fake/checkout/"+id, nil, nil)
	require.Equal(t, 200, r.Status)
	assert.Contains(t, r.Header.Get("Content-Type"), "text/html")
	assert.Contains(t, string(r.Body), "150.000 ₫")

	assert.Equal(t, 404, h.do(h.strict, "GET", "/api/payments/fake/checkout/"+newID(), nil, nil).Status)
}

func TestFakeCheckoutPageArmsHooksFromTheQuery(t *testing.T) {
	h := newHarness(t)
	id := h.mustCreate("VNPAY", 100_000)["id"].(string)

	r := h.do(h.strict, "GET", "/api/payments/fake/checkout/"+id+"?hook_cancel=unsupported", nil, nil)
	require.Equal(t, 200, r.Status)
	assert.Contains(t, string(r.Body), `name="hook_cancel" value="unsupported"`, "the page carries the hook forward")
	p, err := h.store.GetPayment(ctx, id)
	require.NoError(t, err)
	cancelled, err := h.fake.Cancel(ctx, p)
	require.NoError(t, err)
	assert.False(t, cancelled, "the armed hook makes Cancel unsupported")

	bad := h.do(h.raw, "GET", "/api/payments/fake/checkout/"+id+"?hook_webhook=bogus", nil, nil)
	assert.Equal(t, 400, bad.Status)
}

func TestFakeCheckoutPageOfAnAnsweredSessionStillRenders(t *testing.T) {
	h := newHarness(t)
	id := h.mustCreate("VNPAY", 100_000)["id"].(string)
	require.NoError(t, h.fake.Submit(ctx, id, "fail", fake.Hooks{Webhook: "drop"}))

	r := h.do(h.strict, "GET", "/api/payments/fake/checkout/"+id+"?hook_cancel=unsupported", nil, nil)
	require.Equal(t, 200, r.Status)
	assert.NotContains(t, string(r.Body), `value="unsupported"`, "nothing was armed")
}

func form(kv ...string) []byte {
	v := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return []byte(v.Encode())
}

var formHeader = map[string]string{"Content-Type": "application/x-www-form-urlencoded"}

func TestFakeSubmit(t *testing.T) {
	h := newHarness(t)
	id := h.mustCreate("VNPAY", 100_000)["id"].(string)

	bad := h.do(h.raw, "POST", "/api/payments/fake/checkout/"+id, formHeader, form("outcome", "maybe"))
	assert.Equal(t, 400, bad.Status)
	badHook := h.do(h.raw, "POST", "/api/payments/fake/checkout/"+id, formHeader, form("outcome", "succeed", "hook_refund", "boom"))
	assert.Equal(t, 400, badHook.Status)
	unknown := h.do(h.strict, "POST", "/api/payments/fake/checkout/"+newID(), formHeader, form("outcome", "succeed"))
	assert.Equal(t, 404, unknown.Status)

	r := h.do(h.strict, "POST", "/api/payments/fake/checkout/"+id, formHeader, form("outcome", "succeed", "hook_webhook", "drop"))
	require.Equal(t, 302, r.Status)
	assert.Equal(t, "/api/payments/fake/return?payment_id="+id, r.Header.Get("Location"))

	again := h.do(h.strict, "POST", "/api/payments/fake/checkout/"+id, formHeader, form("outcome", "fail"))
	require.Equal(t, 302, again.Status, "an answered session goes straight to the result page")
	assert.Equal(t, "/checkout/result?payment_id="+id, again.Header.Get("Location"))
}

func TestFakeReturnSyncsAndRedirects(t *testing.T) {
	h := newHarness(t)
	id := h.mustCreate("VNPAY", 100_000)["id"].(string)

	pending := h.do(h.strict, "GET", "/api/payments/fake/return?payment_id="+id, nil, nil)
	require.Equal(t, 302, pending.Status)
	assert.Equal(t, "/checkout/result?payment_id="+id, pending.Header.Get("Location"))
	assert.Equal(t, "PENDING", h.status(id), "nothing to apply yet")

	require.NoError(t, h.fake.Submit(ctx, id, "succeed", fake.Hooks{Webhook: "drop"}))
	done := h.do(h.strict, "GET", "/api/payments/fake/return?payment_id="+id, nil, nil)
	require.Equal(t, 302, done.Status)
	assert.Equal(t, "SUCCEEDED", h.status(id), "the return route applied the provider's answer")

	// Never a 404: an unknown payment still lands on the result page.
	ghost := newID()
	unknown := h.do(h.strict, "GET", "/api/payments/fake/return?payment_id="+ghost, nil, nil)
	require.Equal(t, 302, unknown.Status)
	assert.Equal(t, "/checkout/result?payment_id="+ghost, unknown.Header.Get("Location"))

	// The contract forbids an unparseable or missing id, so these go through the raw client.
	for _, q := range []string{"?payment_id=nope", ""} {
		r := h.do(h.raw, "GET", "/api/payments/fake/return"+q, nil, nil)
		require.Equal(t, 302, r.Status, q)
		assert.Equal(t, "/checkout/result", r.Header.Get("Location"), q)
	}
}

func TestFakeWebhookRejectsBadSignaturesWithoutApplyingAnything(t *testing.T) {
	h := newHarness(t)
	p := h.mustCreate("VNPAY", 100_000)
	id, checkout := p["id"].(string), p["checkout_id"].(string)
	body, _ := json.Marshal(fake.Event{EventID: newID(), PaymentID: id, Outcome: "succeed"})

	for name, sig := range map[string]string{
		"forged":  fake.Sign("other-secret", body, time.Now()),
		"stale":   fake.Sign(secret, body, time.Now().Add(-time.Hour)),
		"garbage": "nonsense",
		"empty":   "x=",
	} {
		t.Run(name, func(t *testing.T) {
			r := h.webhook(body, sig)
			assert.Equal(t, 400, r.Status)
			assert.Equal(t, "PENDING", h.status(id))
			assert.Zero(t, h.outbox(checkout))
		})
	}

	tampered := h.webhook(append(body[:len(body)-1:len(body)-1], `,"x":1}`...), fake.Sign(secret, body, time.Now()))
	assert.Equal(t, 400, tampered.Status, "the MAC covers the raw bytes")
	assert.Equal(t, "PENDING", h.status(id))
}

func TestFakeWebhookSucceedsOnceAndIsIdempotent(t *testing.T) {
	h := newHarness(t)
	p := h.mustCreate("VNPAY", 100_000)
	id, checkout := p["id"].(string), p["checkout_id"].(string)
	eventID := newID()

	require.Equal(t, 200, h.signedWebhook(eventID, id, "succeed").Status)
	assert.Equal(t, "SUCCEEDED", h.status(id))
	assert.Equal(t, 1, h.outbox(checkout))

	require.Equal(t, 200, h.signedWebhook(eventID, id, "succeed").Status)
	assert.Equal(t, 1, h.outbox(checkout), "the same event_id again publishes nothing")
	require.Equal(t, 200, h.signedWebhook(newID(), id, "succeed").Status)
	assert.Equal(t, 1, h.outbox(checkout), "a new event for a captured payment publishes nothing either")
}

func TestFakeWebhookEdgeCases(t *testing.T) {
	h := newHarness(t)
	id := h.mustCreate("VNPAY", 100_000)["id"].(string)

	assert.Equal(t, 200, h.signedWebhook(newID(), newID(), "succeed").Status, "an unknown payment is acknowledged")

	signed := h.rawWebhook
	assert.Equal(t, 400, signed(`not json`).Status)
	assert.Equal(t, 400, signed(`{"event_id":"e","payment_id":"`+id+`","outcome":"bogus"}`).Status)
	assert.Equal(t, 400, signed(`{"event_id":"","payment_id":"`+id+`","outcome":"succeed"}`).Status)

	big := `{"event_id":"e","payment_id":"` + id + `","outcome":"succeed","pad":"` + strings.Repeat("a", 70<<10) + `"}`
	assert.Equal(t, 400, signed(big).Status, "bodies over 64 KiB are refused")
	assert.Equal(t, "PENDING", h.status(id))

	assert.Equal(t, 200, h.signedWebhook(newID(), id, "pending").Status)
	assert.Equal(t, "PENDING", h.status(id), "a pending event changes nothing")
}

func TestFakePaymentCompletesThroughTheSignedWebhook(t *testing.T) {
	h := newHarness(t)
	p := h.mustCreate("VNPAY", 100_000)
	id, checkout := p["id"].(string), p["checkout_id"].(string)

	r := h.do(h.strict, "POST", "/api/payments/fake/checkout/"+id, formHeader, form("outcome", "succeed"))
	require.Equal(t, 302, r.Status)
	require.Eventually(t, func() bool { return h.status(id) == string(domain.StatusSucceeded) }, 5*time.Second, 20*time.Millisecond)
	h.fake.Wait()
	assert.Equal(t, 1, h.outbox(checkout))
}
