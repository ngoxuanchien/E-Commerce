package client_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

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
