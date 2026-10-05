package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"

	"github.com/ngoxuanchien/e-commerce/pkg/httpx"
	"github.com/ngoxuanchien/e-commerce/pkg/logx"
	"github.com/ngoxuanchien/e-commerce/pkg/otelx"
	"github.com/ngoxuanchien/e-commerce/pkg/server"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/auth"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/auth/authtest"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/proxy"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/ratelimit"
	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/routes"
)

const (
	allowedOrigin = "https://shop.example"
	testKID       = "k1"
)

func TestMain(m *testing.M) {
	// Install the tracer provider and W3C propagator the production binary sets up.
	shutdown, err := otelx.Setup(context.Background(), "gateway-test", "")
	if err != nil {
		panic(err)
	}
	// Library code (auth rejections, proxy errors) logs through slog's default logger.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	code := m.Run()
	_ = shutdown(context.Background())
	os.Exit(code)
}

// syncBuffer is a bytes.Buffer safe for concurrent use by the logger.
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

// echoed is the body every echo upstream replies with.
type echoed struct {
	Upstream string      `json:"upstream"`
	URI      string      `json:"uri"`
	Headers  http.Header `json:"headers"`
}

type harness struct {
	t       *testing.T
	handler http.Handler
	hits    map[string]*atomic.Int64
	logs    *syncBuffer
	jwks    *auth.JWKS
	key     *authtest.Key
	stub    *authtest.Server
}

type harnessConfig struct {
	skipJWKS  bool
	gatewayFn []func(*gateway)
}

type harnessOption func(*harnessConfig)

// skipJWKSLoad leaves the JWKS cache empty so /readyz starts out not ready.
func skipJWKSLoad() harnessOption {
	return func(c *harnessConfig) { c.skipJWKS = true }
}

// withGateway lets a test adjust the gateway before the handler is built.
func withGateway(fn func(*gateway)) harnessOption {
	return func(c *harnessConfig) { c.gatewayFn = append(c.gatewayFn, fn) }
}

// withUpstream replaces one upstream with an in-process handler.
func withUpstream(name string, h http.Handler) harnessOption {
	return withGateway(func(g *gateway) { g.upstreams[name] = h })
}

// withProductionLimits swaps the unlimited test limiters for the real ones.
// Their clock is frozen so token refill cannot make the assertions flaky.
func withProductionLimits() harnessOption {
	return withGateway(func(g *gateway) {
		frozen := ratelimit.WithClock(func() time.Time { return frozenNow })
		g.general = ratelimit.NewGeneralLimiter(frozen)
		g.login = ratelimit.NewLoginLimiter(frozen)
	})
}

func newHarness(t *testing.T, opts ...harnessOption) *harness {
	t.Helper()
	var cfg harnessConfig
	for _, o := range opts {
		o(&cfg)
	}

	h := &harness{t: t, hits: map[string]*atomic.Int64{}, logs: &syncBuffer{}}
	upstreams := map[string]http.Handler{}
	for _, name := range []string{routes.Identity, routes.Catalog, routes.Order, routes.Payment, routes.Ledger} {
		counter := &atomic.Int64{}
		h.hits[name] = counter
		srv := httptest.NewServer(echoUpstream(name, counter))
		t.Cleanup(srv.Close)
		target, err := proxy.ParseTarget(srv.URL)
		require.NoError(t, err)
		upstreams[name] = proxy.New(target, proxy.NewTransport(5*time.Second))
	}

	h.key = authtest.NewKey(t, testKID)
	h.stub = authtest.NewServer(t, h.key)
	h.jwks = auth.NewJWKS(h.stub.URL)
	if !cfg.skipJWKS {
		require.NoError(t, h.jwks.Refresh(context.Background()))
	}

	g := &gateway{
		logger:    logx.New(h.logs, "info"),
		checks:    map[string]server.Check{"jwks": h.jwks.Ready},
		routes:    routes.Default(),
		upstreams: upstreams,
		verifier:  auth.NewVerifier(h.jwks),
		origins:   []string{allowedOrigin},
		general:   ratelimit.New(rate.Inf, 1, time.Minute),
		login:     ratelimit.New(rate.Inf, 1, time.Minute),
		clientIP:  ratelimit.ClientIP{}.Resolve,
	}
	for _, fn := range cfg.gatewayFn {
		fn(g)
	}
	handler, err := newHandler(g)
	require.NoError(t, err)
	h.handler = handler
	return h
}

func echoUpstream(name string, hits *atomic.Int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		httpx.WriteJSON(w, http.StatusOK, echoed{Upstream: name, URI: r.RequestURI, Headers: r.Header})
	})
}

func (h *harness) hitCount(name string) int64 { return h.hits[name].Load() }

func (h *harness) totalHits() int64 {
	var n int64
	for _, c := range h.hits {
		n += c.Load()
	}
	return n
}

// token signs a valid access token for sub with roles.
func (h *harness) token(sub string, roles ...string) string {
	h.t.Helper()
	return h.key.Sign(h.t, authtest.ValidClaims(sub, roles...))
}

func withToken(tok string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
}

func withHeader(k, v string) func(*http.Request) {
	return func(r *http.Request) { r.Header.Set(k, v) }
}

// do sends one request through the whole handler.
func (h *harness) do(method, target string, mutators ...func(*http.Request)) *httptest.ResponseRecorder {
	h.t.Helper()
	req := httptest.NewRequest(method, target, nil)
	for _, m := range mutators {
		m(req)
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec
}

// proxied decodes an echo upstream's reply, failing if rec is not one.
func (h *harness) proxied(rec *httptest.ResponseRecorder) echoed {
	h.t.Helper()
	require.Equal(h.t, http.StatusOK, rec.Code, rec.Body.String())
	var e echoed
	require.NoError(h.t, json.Unmarshal(rec.Body.Bytes(), &e), rec.Body.String())
	return e
}

// requireError asserts rec is a standard error response.
func requireError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	require.Equal(t, status, rec.Code, rec.Body.String())
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body), rec.Body.String())
	require.Equal(t, code, body.Error.Code)
}

var frozenNow = time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
