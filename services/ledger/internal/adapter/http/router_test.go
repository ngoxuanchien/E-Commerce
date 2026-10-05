package httpadapter_test

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

	"github.com/ngoxuanchien/e-commerce/pkg/authctx"
	"github.com/ngoxuanchien/e-commerce/pkg/event"
	"github.com/ngoxuanchien/e-commerce/pkg/testutil"
	httpadapter "github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/http"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/adapter/postgres"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/app"
	"github.com/ngoxuanchien/e-commerce/services/ledger/internal/testsupport"
)

const spec = "../../../../../api/openapi/ledger.yaml"

type api struct {
	t        *testing.T
	url      string
	contract *http.Client // validates every request and response against ledger.yaml
	plain    *http.Client // for requests the contract itself forbids
	events   *app.EventHandler
}

func newAPI(t *testing.T) *api {
	t.Helper()
	db := testsupport.DB(t)
	store := postgres.NewStore(db)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }
	router := httpadapter.NewRouter(httpadapter.Services{
		Wallet:  app.NewWalletService(store),
		Payouts: app.NewPayoutService(store, app.PayoutConfig{MinAmount: 50000, Now: now, Logger: logger}),
	})
	srv := httptest.NewServer(authctx.Middleware(router))
	t.Cleanup(srv.Close)
	return &api{t: t, url: srv.URL, plain: srv.Client(),
		contract: &http.Client{Transport: testutil.ContractTransport(t, spec, srv.Client().Transport)},
		events:   app.NewEventHandler(store, logger, now)}
}

// fund releases amount (commission 0) into seller's wallet through events.
func (a *api) fund(seller uuid.UUID, amount int64) {
	a.t.Helper()
	order, checkout, payment := uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, p := range []event.Payload{
		event.PaymentSucceeded{PaymentID: payment, CheckoutID: checkout, BuyerID: uuid.NewString(), Provider: "fake",
			ProviderRef: "r", Amount: amount, Allocations: []event.Allocation{{OrderID: order, SellerUserID: seller.String(), Amount: amount}}},
		event.OrderCompleted{OrderID: order, CheckoutID: checkout, SellerUserID: seller.String(), PaymentMethod: "VNPAY",
			Total: amount, CompletedBy: "buyer"},
	} {
		env, err := event.New(p, time.Now())
		require.NoError(a.t, err)
		require.NoError(a.t, a.events.Handle(context.Background(), env))
	}
}

type call struct {
	method, path, body string
	user               string // X-User-Id; "" = anonymous
	roles              string
	headers            map[string]string
}

func (a *api) do(c *http.Client, in call) (int, map[string]any) {
	a.t.Helper()
	var body io.Reader
	if in.body != "" {
		body = strings.NewReader(in.body)
	}
	req, err := http.NewRequest(in.method, a.url+in.path, body)
	require.NoError(a.t, err)
	if in.body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if in.user != "" {
		req.Header.Set(authctx.HeaderUserID, in.user)
		req.Header.Set(authctx.HeaderUserRoles, in.roles)
	}
	for k, v := range in.headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	require.NoError(a.t, err)
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	raw, _ := io.ReadAll(resp.Body)
	if len(raw) > 0 {
		require.NoError(a.t, json.Unmarshal(raw, &out), string(raw))
	}
	return resp.StatusCode, out
}

func seller(id uuid.UUID) call { return call{user: id.String(), roles: "buyer,seller"} }

func (c call) at(method, path string) call { c.method, c.path = method, path; return c }

func (c call) with(body string, headers map[string]string) call {
	c.body, c.headers = body, headers
	return c
}

func errCode(body map[string]any) string {
	e, _ := body["error"].(map[string]any)
	s, _ := e["code"].(string)
	return s
}

func TestNewSellerSeesZerosAndEmptyPages(t *testing.T) {
	a := newAPI(t)
	s := seller(uuid.New())
	status, w := a.do(a.contract, s.at("GET", "/api/seller/wallet"))
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, map[string]any{"available": 0.0, "pending_payout": 0.0, "in_escrow": 0.0, "currency": "VND"}, w)

	for _, path := range []string{"/api/seller/wallet/entries", "/api/seller/payouts"} {
		status, page := a.do(a.contract, s.at("GET", path))
		require.Equal(t, http.StatusOK, status, path)
		assert.Equal(t, []any{}, page["items"], path)
		assert.Nil(t, page["next_cursor"], path)
	}
}

func TestPayoutRequestOverHTTP(t *testing.T) {
	a := newAPI(t)
	id := uuid.New()
	a.fund(id, 200000)
	s := seller(id)
	key := map[string]string{"Idempotency-Key": "k-1"}

	status, created := a.do(a.contract, s.at("POST", "/api/seller/payouts").with(`{"amount":60000}`, key))
	require.Equal(t, http.StatusCreated, status)
	assert.Equal(t, "REQUESTED", created["status"])
	assert.Equal(t, 60000.0, created["amount"])
	assert.Nil(t, created["settled_at"])
	assert.Nil(t, created["failure_reason"])

	status, replay := a.do(a.contract, s.at("POST", "/api/seller/payouts").with(`{"amount":60000}`, key))
	assert.Equal(t, http.StatusCreated, status)
	assert.Equal(t, created, replay)

	status, body := a.do(a.contract, s.at("POST", "/api/seller/payouts").with(`{"amount":70000}`, key))
	assert.Equal(t, http.StatusUnprocessableEntity, status)
	assert.Equal(t, "IDEMPOTENCY_KEY_REUSED", errCode(body))

	status, body = a.do(a.contract, s.at("POST", "/api/seller/payouts").with(`{"amount":10000}`, map[string]string{"Idempotency-Key": "k-2"}))
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "VALIDATION_ERROR", errCode(body))

	status, body = a.do(a.contract, s.at("POST", "/api/seller/payouts").with(`{"amount":150000}`, map[string]string{"Idempotency-Key": "k-3"}))
	assert.Equal(t, http.StatusConflict, status)
	assert.Equal(t, "INSUFFICIENT_FUNDS", errCode(body))
	details := body["error"].(map[string]any)["details"].(map[string]any)
	assert.Equal(t, 140000.0, details["available"])

	status, w := a.do(a.contract, s.at("GET", "/api/seller/wallet"))
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, 140000.0, w["available"])
	assert.Equal(t, 60000.0, w["pending_payout"])

	status, page := a.do(a.contract, s.at("GET", "/api/seller/wallet/entries?limit=1"))
	require.Equal(t, http.StatusOK, status)
	items := page["items"].([]any)
	require.Len(t, items, 1)
	assert.Equal(t, "PAYOUT_REQUEST", items[0].(map[string]any)["kind"])
	assert.Equal(t, -60000.0, items[0].(map[string]any)["amount"])
	require.NotNil(t, page["next_cursor"])
	status, next := a.do(a.contract, s.at("GET", "/api/seller/wallet/entries?limit=1&cursor="+page["next_cursor"].(string)))
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "RELEASE", next["items"].([]any)[0].(map[string]any)["kind"])

	status, payouts := a.do(a.contract, s.at("GET", "/api/seller/payouts?status=REQUESTED"))
	require.Equal(t, http.StatusOK, status)
	assert.Len(t, payouts["items"], 1)
}

func TestRequestValidation(t *testing.T) {
	a := newAPI(t)
	s := seller(uuid.New())
	// The contract forbids these requests, so they go through the plain client.
	status, body := a.do(a.plain, s.at("POST", "/api/seller/payouts").with(`{"amount":60000}`, nil))
	assert.Equal(t, http.StatusBadRequest, status, "missing Idempotency-Key")
	assert.Equal(t, "VALIDATION_ERROR", errCode(body))
	status, _ = a.do(a.plain, s.at("POST", "/api/seller/payouts").with(`{"amount":60000,"x":1}`, map[string]string{"Idempotency-Key": "k"}))
	assert.Equal(t, http.StatusBadRequest, status, "unknown field")
	status, _ = a.do(a.plain, s.at("POST", "/api/seller/payouts").with(`{}`, map[string]string{"Idempotency-Key": "k"}))
	assert.Equal(t, http.StatusBadRequest, status, "missing amount")
	status, _ = a.do(a.plain, s.at("GET", "/api/seller/payouts?status=bogus"))
	assert.Equal(t, http.StatusBadRequest, status)
	status, _ = a.do(a.plain, s.at("GET", "/api/seller/wallet/entries?limit=0"))
	assert.Equal(t, http.StatusBadRequest, status)
	status, _ = a.do(a.plain, s.at("GET", "/api/seller/wallet/entries?limit=101"))
	assert.Equal(t, http.StatusBadRequest, status)

	status, body = a.do(a.contract, s.at("GET", "/api/seller/wallet/entries?cursor=garbage"))
	assert.Equal(t, http.StatusBadRequest, status)
	assert.Equal(t, "VALIDATION_ERROR", errCode(body))
}

func TestAuthorization(t *testing.T) {
	a := newAPI(t)
	status, _ := a.do(a.contract, call{method: "GET", path: "/api/seller/wallet"})
	assert.Equal(t, http.StatusUnauthorized, status)
	status, _ = a.do(a.contract, call{method: "GET", path: "/api/seller/wallet", user: uuid.NewString(), roles: "buyer"})
	assert.Equal(t, http.StatusForbidden, status)
	status, _ = a.do(a.contract, call{method: "GET", path: "/api/seller/wallet", user: "not-a-uuid", roles: "seller"})
	assert.Equal(t, http.StatusUnauthorized, status)
	status, _ = a.do(a.plain, call{method: "GET", path: "/api/other", user: uuid.NewString(), roles: "seller"})
	assert.Equal(t, http.StatusNotFound, status)
}
