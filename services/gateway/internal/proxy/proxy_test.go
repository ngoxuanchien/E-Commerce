package proxy_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/proxy"
)

// TestMain silences the default slog logger (proxy.New routes
// httputil.ReverseProxy's ErrorLog through it) so intentionally triggered
// upstream failures don't spew warnings into test output.
func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func TestParseTarget(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cases := []struct {
			name   string
			raw    string
			scheme string
			host   string
		}{
			{"http with port", "http://catalog:8080", "http", "catalog:8080"},
			{"https no port", "https://order.internal", "https", "order.internal"},
			{"http loopback", "http://127.0.0.1:9000", "http", "127.0.0.1:9000"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				u, err := proxy.ParseTarget(c.raw)
				require.NoError(t, err)
				require.NotNil(t, u)
				assert.Equal(t, c.scheme, u.Scheme)
				assert.Equal(t, c.host, u.Host)
				assert.Equal(t, "", u.Path)
				assert.Equal(t, "", u.RawQuery)
				assert.Equal(t, "", u.Fragment)
				assert.Nil(t, u.User)
			})
		}
	})

	t.Run("invalid", func(t *testing.T) {
		cases := []struct {
			name string
			raw  string
		}{
			{"empty", ""},
			{"unparsable", "://bad"},
			{"missing scheme", "catalog:8080"},
			{"non-http scheme", "ftp://catalog:8080"},
			{"missing host", "http://"},
			{"has path", "http://catalog:8080/api"},
			{"has query", "http://catalog:8080?x=1"},
			{"has fragment", "http://catalog:8080#frag"},
			{"has userinfo", "http://user:pass@catalog:8080"},
		}
		for _, c := range cases {
			t.Run(c.name, func(t *testing.T) {
				u, err := proxy.ParseTarget(c.raw)
				assert.Error(t, err)
				assert.Nil(t, u)
			})
		}
	})
}

// newProxyServer builds a reverse proxy in front of upstream and returns a
// test server for it, along with the target/transport used, so individual
// tests can tweak the timeout.
func newProxyServer(t *testing.T, upstreamURL string, timeout time.Duration) *httptest.Server {
	t.Helper()
	target, err := proxy.ParseTarget(upstreamURL)
	require.NoError(t, err)
	transport := proxy.NewTransport(timeout)
	p := proxy.New(target, transport)
	server := httptest.NewServer(p)
	t.Cleanup(server.Close)
	return server
}

func TestProxyForwardsPathAndQueryUnchanged(t *testing.T) {
	var gotRequestURI string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestURI = r.RequestURI
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	proxyServer := newProxyServer(t, upstream.URL, 5*time.Second)

	const rawTarget = "/api/products/a%2Fb?q=%C3%A1o+d%C3%A0i&limit=5"
	req, err := http.NewRequest(http.MethodGet, proxyServer.URL+rawTarget, nil)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, rawTarget, gotRequestURI)
}

func TestProxyPassesUpstreamResponsesThrough(t *testing.T) {
	const body = `{"error":{"code":"CONFLICT","message":"already exists"}}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream-Marker", "present")
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(body))
	}))
	defer upstream.Close()

	proxyServer := newProxyServer(t, upstream.URL, 5*time.Second)

	resp, err := http.Get(proxyServer.URL + "/api/orders")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	got, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusConflict, resp.StatusCode)
	assert.Equal(t, "present", resp.Header.Get("X-Upstream-Marker"))
	assert.Equal(t, body, string(got))
}

func TestProxyUpstreamDownIs503(t *testing.T) {
	// Close the upstream immediately so its address refuses connections.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	upstream.Close()

	proxyServer := newProxyServer(t, upstream.URL, 5*time.Second)

	resp, err := http.Get(proxyServer.URL + "/api/orders")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assertErrorCode(t, resp, "UPSTREAM_UNAVAILABLE")
}

// roundTripFunc adapts a function to http.RoundTripper.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestProxyUpstreamDownDoesNotLogQueryString(t *testing.T) {
	var logs bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// A transport that fails with a *url.Error, whose text embeds the full
	// upstream URL including its query string.
	cause := errors.New("connection refused")
	transport := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: "Get", URL: r.URL.String(), Err: cause}
	})
	target, err := proxy.ParseTarget("http://upstream.internal:8080")
	require.NoError(t, err)
	p := proxy.New(target, transport)

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/orders?token=secret", nil))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.NotContains(t, logs.String(), "secret")
	assert.Contains(t, logs.String(), "connection refused")
	assert.Contains(t, logs.String(), "upstream.internal:8080")
}

func TestProxyUpstreamTimeoutIs503(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Hang until the client gives up, so the request's context is done
		// and the handler returns, letting the test server shut down
		// cleanly instead of blocking Close().
		<-r.Context().Done()
	}))
	defer upstream.Close()

	proxyServer := newProxyServer(t, upstream.URL, 100*time.Millisecond)

	start := time.Now()
	resp, err := http.Get(proxyServer.URL + "/api/orders")
	elapsed := time.Since(start)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assertErrorCode(t, resp, "UPSTREAM_UNAVAILABLE")
	assert.Less(t, elapsed, 2*time.Second)
}

func TestProxyMapsBodyLimitTo413(t *testing.T) {
	const limit = 1024
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	target, err := proxy.ParseTarget(upstream.URL)
	require.NoError(t, err)
	transport := proxy.NewTransport(5 * time.Second)
	p := proxy.New(target, transport)

	// The gateway wraps every request body in http.MaxBytesReader upstream
	// of the proxy; reproduce that here rather than relying on the proxy
	// itself to enforce a limit.
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		p.ServeHTTP(w, r)
	})
	proxyServer := httptest.NewServer(handler)
	defer proxyServer.Close()

	body := bytes.Repeat([]byte("a"), 2*limit)
	req, err := http.NewRequest(http.MethodPost, proxyServer.URL+"/api/seller/uploads", io.NopCloser(bytes.NewReader(body)))
	require.NoError(t, err)
	// Force chunked transfer encoding: no Content-Length header, so the
	// gateway (and upstream) can't reject the request before reading it.
	req.ContentLength = -1

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	assertErrorCode(t, resp, "PAYLOAD_TOO_LARGE")
}

// assertErrorCode reads resp's body as the gateway's standard error
// envelope and asserts its code matches want.
func assertErrorCode(t *testing.T, resp *http.Response, want string) {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	assert.Equal(t, want, body.Error.Code)
}
