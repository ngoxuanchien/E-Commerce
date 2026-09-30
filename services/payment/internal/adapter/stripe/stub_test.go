package stripe_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ngoxuanchien/e-commerce/services/payment/internal/adapter/stripe"
)

var bg = context.Background()

const (
	testSecretKey     = "sk_test_51QxYzAbCdEfGhIjKstub"
	testWebhookSecret = "whsec_test_5f2b8c1d9e7a4b3c"
	testSessionID     = "cs_test_a1B2c3D4e5F6g7H8i9J0"
	testIntentID      = "pi_3QxYzAbCdEfGhIjK0aBcDeFg"
	testRefundRef     = "re_3QxYzAbCdEfGhIjK0pQrStUv"
)

// Stripe API operations the stub recognises.
const (
	opCreateSession   = "create_session"
	opRetrieveSession = "retrieve_session"
	opExpireSession   = "expire_session"
	opCreateRefund    = "create_refund"
	opRetrieveRefund  = "retrieve_refund"
)

// reply is what the stub answers for one operation: a recorded fixture (testdata/<fixture>.json),
// an HTTP status (0 → 200) and top-level fields to override in the fixture.
type reply struct {
	fixture  string
	status   int
	override map[string]any
}

// request is one call the stub received.
type request struct {
	op     string
	method string
	path   string
	form   url.Values
	header http.Header
}

// apiStub stands in for api.stripe.com: it serves recorded fixtures per operation and records every
// request. Every answer carries Stripe-Should-Retry: false, so stripe-go does not retry (and sleep)
// on the error answers the tests provoke.
type apiStub struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	replies  map[string]reply
	requests []request
}

func newAPIStub(t *testing.T) *apiStub {
	t.Helper()
	s := &apiStub{t: t, replies: map[string]reply{}}
	s.srv = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.srv.Close)
	return s
}

func (s *apiStub) set(op string, r reply) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.replies[op] = r
}

func (s *apiStub) calls(op string) []request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []request
	for _, r := range s.requests {
		if op == "" || r.op == op {
			out = append(out, r)
		}
	}
	return out
}

func classify(method, path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	switch {
	case method == http.MethodPost && path == "/v1/checkout/sessions":
		return opCreateSession
	case method == http.MethodGet && len(parts) == 4 && parts[1] == "checkout" && parts[2] == "sessions":
		return opRetrieveSession
	case method == http.MethodPost && len(parts) == 5 && parts[1] == "checkout" && parts[4] == "expire":
		return opExpireSession
	case method == http.MethodPost && path == "/v1/refunds":
		return opCreateRefund
	case method == http.MethodGet && len(parts) == 3 && parts[1] == "refunds":
		return opRetrieveRefund
	}
	return ""
}

func loadFixture(t *testing.T, name string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("fixture %s: %v", name, err)
	}
	return m
}

func (s *apiStub) serve(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.t.Errorf("stub: parse form: %v", err)
	}
	op := classify(r.Method, r.URL.Path)
	s.mu.Lock()
	s.requests = append(s.requests, request{op: op, method: r.Method, path: r.URL.Path, form: r.Form, header: r.Header.Clone()})
	rep, ok := s.replies[op]
	s.mu.Unlock()
	w.Header().Set("Stripe-Should-Retry", "false")
	w.Header().Set("Content-Type", "application/json")
	if op == "" || !ok {
		s.t.Errorf("stub: unexpected %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(loadFixture(s.t, "error_resource_missing"))
		return
	}
	body := loadFixture(s.t, rep.fixture)
	for k, v := range rep.override {
		body[k] = v
	}
	// Retrieve with expand[]=payment_intent inlines the recorded PaymentIntent, as Stripe does.
	if pi, isID := body["payment_intent"].(string); isID && op == opRetrieveSession && r.Form.Get("expand[0]") == "payment_intent" {
		intent := loadFixture(s.t, "payment_intent")
		intent["id"] = pi
		if amt, ok := body["amount_total"]; ok {
			intent["amount"], intent["amount_received"] = amt, amt
		}
		body["payment_intent"] = intent
	}
	if rep.status != 0 {
		w.WriteHeader(rep.status)
	}
	_ = json.NewEncoder(w).Encode(body)
}

func testConfig(api string, now time.Time) stripe.Config {
	return stripe.Config{
		SecretKey: testSecretKey, WebhookSecret: testWebhookSecret, PublicBaseURL: "https://gw.test/",
		APIBaseURL: api, Now: func() time.Time { return now },
	}
}

// signature builds a real Stripe-Signature header: t=<unix>,v1=hex(HMAC-SHA256(secret, t + "." + body)).
func signature(secret string, body []byte, at time.Time) string {
	ts := strconv.FormatInt(at.Unix(), 10)
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "." + string(body)))
	return "t=" + ts + ",v1=" + hex.EncodeToString(m.Sum(nil))
}

// sessionFor overrides a session fixture so it belongs to the given payment.
func sessionFor(sessionID, paymentID, checkoutID string, amount int64) map[string]any {
	return map[string]any{
		"id": sessionID, "client_reference_id": paymentID, "amount_total": amount, "amount_subtotal": amount,
		"metadata": map[string]any{"payment_id": paymentID, "checkout_id": checkoutID},
	}
}

// event loads a recorded webhook event and points it at a session and payment.
func event(t *testing.T, fixture, eventID, sessionID, paymentID string) []byte {
	t.Helper()
	ev := loadFixture(t, fixture)
	ev["id"] = eventID
	obj := ev["data"].(map[string]any)["object"].(map[string]any)
	if sessionID != "" {
		obj["id"] = sessionID
	}
	if md, ok := obj["metadata"].(map[string]any); ok {
		if paymentID == "" {
			delete(obj, "metadata")
		} else {
			md["payment_id"] = paymentID
			obj["client_reference_id"] = paymentID
		}
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
