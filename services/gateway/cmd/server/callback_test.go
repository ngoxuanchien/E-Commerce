package main

import (
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/proxy"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/routes"
)

// recordingUpstream captures the request body and URL it received.
type recordingUpstream struct {
	mu       sync.Mutex
	body     []byte
	rawQuery string
	headers  http.Header
}

func (r *recordingUpstream) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	body, _ := io.ReadAll(req.Body)
	r.body = body
	r.rawQuery = req.URL.RawQuery
	r.headers = req.Header.Clone()
}

func TestCallbackBodyQueryAndHeadersPassThrough(t *testing.T) {
	body := []byte("{\"a\":\"á\"}\r\n\x00\xff")
	expectedHash := sha256.Sum256(body)

	t.Run("POST with binary body and signature headers", func(t *testing.T) {
		rec := &recordingUpstream{}
		stub := httptest.NewServer(rec)
		t.Cleanup(stub.Close)
		target, err := proxy.ParseTarget(stub.URL)
		require.NoError(t, err)

		h := newHarness(t, withUpstream(routes.Payment, proxy.New(target, proxy.NewTransport(time.Second))))

		resp := h.do("POST", "/api/payments/stripe/webhook",
			withHeader("Content-Type", "application/json"),
			withHeader("Stripe-Signature", "t=1,v1=abc"),
			withHeader("X-Fake-Signature", "t=1,v1=def"),
			withBody(body, false))

		require.Equal(t, http.StatusOK, resp.Code)
		assert.Equal(t, body, rec.body)
		assert.Equal(t, expectedHash, sha256.Sum256(rec.body))
		assert.Equal(t, []string{"t=1,v1=abc"}, rec.headers["Stripe-Signature"])
		assert.Equal(t, []string{"t=1,v1=def"}, rec.headers["X-Fake-Signature"])
	})

	t.Run("GET with query string pass-through", func(t *testing.T) {
		rec := &recordingUpstream{}
		stub := httptest.NewServer(rec)
		t.Cleanup(stub.Close)
		target, err := proxy.ParseTarget(stub.URL)
		require.NoError(t, err)

		h := newHarness(t, withUpstream(routes.Payment, proxy.New(target, proxy.NewTransport(time.Second))))

		resp := h.do("GET", "/api/payments/vnpay/ipn?vnp_B=2&vnp_A=%C3%A1+x")

		require.Equal(t, http.StatusOK, resp.Code)
		assert.Equal(t, "vnp_B=2&vnp_A=%C3%A1+x", rec.rawQuery)
	})
}

func TestCallbackRedirectAndHTMLPassThrough(t *testing.T) {
	t.Run("302 redirect with Location header", func(t *testing.T) {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Location", "http://localhost:3000/orders/o1?paid=1")
			w.WriteHeader(http.StatusFound)
		}))
		t.Cleanup(stub.Close)
		target, err := proxy.ParseTarget(stub.URL)
		require.NoError(t, err)

		h := newHarness(t, withUpstream(routes.Payment, proxy.New(target, proxy.NewTransport(time.Second))))

		resp := h.do("GET", "/api/payments/fake/return")

		require.Equal(t, http.StatusFound, resp.Code)
		assert.Equal(t, "http://localhost:3000/orders/o1?paid=1", resp.Header().Get("Location"))
	})

	t.Run("200 HTML response", func(t *testing.T) {
		htmlBody := "<html><body>Checkout</body></html>"
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(htmlBody))
		}))
		t.Cleanup(stub.Close)
		target, err := proxy.ParseTarget(stub.URL)
		require.NoError(t, err)

		h := newHarness(t, withUpstream(routes.Payment, proxy.New(target, proxy.NewTransport(time.Second))))

		resp := h.do("GET", "/api/payments/fake/checkout/p1")

		require.Equal(t, http.StatusOK, resp.Code)
		assert.Equal(t, "text/html; charset=utf-8", resp.Header().Get("Content-Type"))
		assert.Equal(t, htmlBody, resp.Body.String())
	})
}

func TestCallbackAccessLogOmitsQuery(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stub.Close)
	target, err := proxy.ParseTarget(stub.URL)
	require.NoError(t, err)

	h := newHarness(t, withUpstream(routes.Payment, proxy.New(target, proxy.NewTransport(time.Second))))

	h.do("GET", "/api/payments/vnpay/return?vnp_TxnRef=p1&vnp_SecureHash=deadbeef")

	logs := h.logs.String()
	assert.Contains(t, logs, `"route":"/api/payments/vnpay"`)
	assert.NotContains(t, logs, "vnp_TxnRef")
	assert.NotContains(t, logs, "vnp_SecureHash")
	assert.NotContains(t, logs, "deadbeef")
}

func TestCallbacksShareTheGeneralBucket(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(stub.Close)
	target, err := proxy.ParseTarget(stub.URL)
	require.NoError(t, err)

	h := newHarness(t,
		withProductionLimits(),
		withUpstream(routes.Payment, proxy.New(target, proxy.NewTransport(time.Second))))

	// Make 40 requests to /api/categories (public, general bucket)
	for i := range 40 {
		rec := h.do("GET", "/api/categories")
		require.Equal(t, http.StatusOK, rec.Code, "request %d", i+1)
	}

	// Next request to /api/payments/stripe/webhook should hit the rate limit
	rec := h.do("POST", "/api/payments/stripe/webhook")

	requireError(t, rec, http.StatusTooManyRequests, httpx.CodeRateLimited)
}

func TestCallbackStripsSpoofedIdentityHeaders(t *testing.T) {
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(echoed{
			Upstream: routes.Payment,
			URI:      r.RequestURI,
			Headers:  r.Header,
		})
	}))
	t.Cleanup(stub.Close)
	target, err := proxy.ParseTarget(stub.URL)
	require.NoError(t, err)

	h := newHarness(t, withUpstream(routes.Payment, proxy.New(target, proxy.NewTransport(time.Second))))
	buyer := h.token("u-buyer", "buyer")

	rec := h.do("POST", "/api/payments/stripe/webhook",
		withToken(buyer),
		withHeader("X-User-Id", "admin"),
		withHeader("X-User-Roles", "seller"))

	require.Equal(t, http.StatusOK, rec.Code)
	e := h.proxied(rec)
	assert.Empty(t, identityHeaders(e.Headers), "callback should strip all identity headers even with valid token and spoofed headers")
}
