package client_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/e2e/client"
)

func TestDoDecodesErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"code":"OUT_OF_STOCK","message":"not enough stock","details":{"shortages":[]}}}`))
	}))
	defer srv.Close()

	err := client.New(srv.URL, nil).Do(context.Background(), http.MethodGet, "/x", nil, nil, nil)
	require.Error(t, err)
	assert.True(t, client.IsCode(err, "OUT_OF_STOCK"))
	assert.Equal(t, http.StatusConflict, client.StatusOf(err))
	var ae *client.APIError
	require.ErrorAs(t, err, &ae)
	assert.JSONEq(t, `{"shortages":[]}`, string(ae.Details))
}

func TestDoSendsBearerTokenAndHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		_, _ = w.Write([]byte(`{"id":"u1"}`))
	}))
	defer srv.Close()

	c := client.New(srv.URL, nil)
	c.SetToken("tok")
	var out struct {
		ID string `json:"id"`
	}
	require.NoError(t, c.Do(context.Background(), http.MethodPost, "/y", map[string]int{"a": 1}, &out, http.Header{"Idempotency-Key": {"k1"}}))

	assert.Equal(t, "u1", out.ID)
	assert.Equal(t, "Bearer tok", got.Get("Authorization"))
	assert.Equal(t, "application/json", got.Get("Content-Type"))
	assert.Equal(t, "k1", got.Get("Idempotency-Key"))
}

func TestSignFakeKnownVector(t *testing.T) {
	sig := client.SignFake("dev-fake-secret", time.Unix(1700000000, 0), []byte(`{"event_id":"e1","payment_id":"p1","outcome":"succeed"}`))
	assert.Equal(t, "t=1700000000,v1=8646cea7fb3478995ffb48101262589e62191e8e6e9a1a65a6288294306b4388", sig)
}

func TestFakeSubmitDoesNotFollowRedirect(t *testing.T) {
	var followed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/payments/fake/checkout/p1" {
			// Verify form was submitted
			assert.NoError(t, r.ParseForm())
			assert.Equal(t, "fail", r.FormValue("outcome"))
			assert.Equal(t, "drop", r.FormValue("hook_webhook"))
			// Return 302 redirect
			w.Header().Set("Location", "/api/payments/fake/return?payment_id=p1")
			w.WriteHeader(http.StatusFound)
			return
		}
		// If we hit /api/payments/fake/return, mark it (don't use t.Fatalf in handler)
		followed.Store(true)
	}))
	defer srv.Close()

	c := client.New(srv.URL, nil)
	c.SetToken("tok")
	hooks := make(url.Values)
	hooks.Set("hook_webhook", "drop")
	location, err := c.FakeSubmit(context.Background(), "p1", "fail", hooks, nil)
	require.NoError(t, err)
	assert.Equal(t, "/api/payments/fake/return?payment_id=p1", location)
	assert.False(t, followed.Load(), "should not follow redirect to /api/payments/fake/return")
}

func TestFakeSubmitNon302IsAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"NOT_FOUND","message":"x"}}`))
	}))
	defer srv.Close()

	c := client.New(srv.URL, nil)
	c.SetToken("tok")
	hooks := make(url.Values)
	_, err := c.FakeSubmit(context.Background(), "p1", "fail", hooks, nil)
	require.Error(t, err)
	assert.True(t, client.IsCode(err, "NOT_FOUND"))
}

func TestCreatePayoutSendsIdempotencyKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify idempotency key header
		assert.Equal(t, "k1", r.Header.Get("Idempotency-Key"))
		// Verify body
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.JSONEq(t, `{"amount":50000}`, string(body))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":"payout1","amount":50000,"status":"REQUESTED","requested_at":"2026-10-05T10:00:00Z"}`))
	}))
	defer srv.Close()

	c := client.New(srv.URL, nil)
	c.SetToken("tok")
	payout, err := c.CreatePayout(context.Background(), "k1", 50000)
	require.NoError(t, err)
	assert.Equal(t, "REQUESTED", payout.Status)
}

func TestNewTimeoutCoversRateLimitWaits(t *testing.T) {
	c := client.New("http://x", nil)
	assert.Greater(t, c.HTTP.Timeout, 3*time.Minute)
}
