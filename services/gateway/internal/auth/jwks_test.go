package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ngoxuanchien/e-commerce/services/gateway/internal/auth/authtest"
)

// fakeClock is a deterministic, manually-advanced clock for tests.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock(start time.Time) *fakeClock {
	return &fakeClock{now: start}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestJWKSNotReadyUntilLoaded(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	srv := authtest.NewServer(t, k1)

	j := NewJWKS(srv.URL)
	assert.Error(t, j.Ready(context.Background()))

	require.NoError(t, j.Refresh(context.Background()))
	assert.NoError(t, j.Ready(context.Background()))
}

func TestUnknownKidTriggersRefresh(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	k2 := authtest.NewKey(t, "k2")
	srv := authtest.NewServer(t, k1)

	j := NewJWKS(srv.URL)
	require.NoError(t, j.Refresh(context.Background()))
	require.Equal(t, 1, srv.Hits())

	// Identity rotates in k2.
	srv.SetKeys(k1, k2)

	// The first miss for the newly rotated kid triggers exactly one
	// refresh and finds it.
	pub, err := j.Key(context.Background(), "k2")
	require.NoError(t, err)
	assert.NotNil(t, pub)
	assert.Equal(t, 2, srv.Hits())
}

func TestUnknownKidRefreshIsThrottled(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	k2 := authtest.NewKey(t, "k2")
	srv := authtest.NewServer(t, k1)

	clock := newFakeClock(time.Unix(0, 0))
	j := NewJWKS(srv.URL, WithJWKSClock(clock.Now))
	require.NoError(t, j.Refresh(context.Background()))
	require.Equal(t, 1, srv.Hits())

	// The first forged kid triggers a refresh.
	_, err := j.Key(context.Background(), "forged-0")
	assert.ErrorIs(t, err, ErrUnknownKey)
	assert.Equal(t, 2, srv.Hits())

	// 50 more forged kids, all within the throttle window: no more refreshes.
	for i := 1; i <= 50; i++ {
		_, err := j.Key(context.Background(), "forged-more")
		assert.ErrorIs(t, err, ErrUnknownKey)
	}
	assert.Equal(t, 2, srv.Hits(), "throttled misses must not trigger a refresh")

	// identity rotates in k2, but we're still within the 30s throttle window.
	srv.SetKeys(k1, k2)

	clock.Advance(29 * time.Second)
	_, err = j.Key(context.Background(), "k2")
	assert.ErrorIs(t, err, ErrUnknownKey)
	assert.Equal(t, 2, srv.Hits(), "k2 must still be unknown before the throttle window elapses")

	// Past the throttle window, the miss refreshes and finds k2.
	clock.Advance(2 * time.Second) // total 31s since the first miss
	pub, err := j.Key(context.Background(), "k2")
	require.NoError(t, err)
	assert.NotNil(t, pub)
	assert.Equal(t, 3, srv.Hits())

	// Known keys never trigger a refresh.
	_, err = j.Key(context.Background(), "k1")
	require.NoError(t, err)
	assert.Equal(t, 3, srv.Hits())
}

func TestRefreshFailureKeepsKeys(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	srv := authtest.NewServer(t, k1)

	j := NewJWKS(srv.URL)
	require.NoError(t, j.Refresh(context.Background()))
	require.NoError(t, j.Ready(context.Background()))

	pub1, err := j.Key(context.Background(), "k1")
	require.NoError(t, err)
	require.NotNil(t, pub1)

	cases := []struct {
		name   string
		status int
		body   []byte
	}{
		{"500", http.StatusInternalServerError, []byte("boom")},
		{"empty set", http.StatusOK, []byte(`{"keys":[]}`)},
		{"garbage", http.StatusOK, []byte("not json")},
		{"oversized", http.StatusOK, oversizedBody(t)},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv.Set(tc.status, tc.body)
			err := j.Refresh(context.Background())
			assert.Error(t, err)

			// k1 must still resolve, and readiness must stay healthy.
			pub, err := j.Key(context.Background(), "k1")
			require.NoError(t, err)
			assert.NotNil(t, pub)
			assert.NoError(t, j.Ready(context.Background()))
		})
	}
}

// oversizedBody returns a JWKS-shaped body over the 1 MB Refresh limit.
func oversizedBody(t *testing.T) []byte {
	t.Helper()
	k := authtest.NewKey(t, "k1")
	jwk := k.JWK()
	// Pad the body past the 1 MB cap with a bogus, ignorable extra field.
	padded := map[string]any{
		"keys": []map[string]string{jwk},
		"pad":  make([]byte, 2<<20),
	}
	body, err := json.Marshal(padded)
	require.NoError(t, err)
	require.Greater(t, len(body), 1<<20)
	return body
}

func TestParseKeySetSkipsUnusableKeys(t *testing.T) {
	good := authtest.NewKey(t, "good")
	goodJWK := good.JWK()

	encUse := authtest.NewKey(t, "enc-use")
	encJWK := encUse.JWK()
	encJWK["use"] = "enc"

	hsAlg := authtest.NewKey(t, "hs-alg")
	hsJWK := hsAlg.JWK()
	hsJWK["alg"] = "HS256"

	missingKid := authtest.NewKey(t, "missing-kid")
	missingKidJWK := missingKid.JWK()
	missingKidJWK["kid"] = ""

	// EC key: a differently-shaped entry with kty=EC, no n/e.
	ecJWK := map[string]string{
		"kty": "EC",
		"kid": "ec-key",
		"use": "sig",
		"crv": "P-256",
		"x":   "abc",
		"y":   "def",
	}

	smallKey, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(t, err)
	smallJWK := map[string]string{
		"kty": "RSA",
		"kid": "small-key",
		"use": "sig",
		"alg": "RS256",
		"n":   base64.RawURLEncoding.EncodeToString(smallKey.PublicKey.N.Bytes()),
		"e":   base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
	}

	badModulusJWK := map[string]string{
		"kty": "RSA",
		"kid": "bad-modulus",
		"use": "sig",
		"alg": "RS256",
		"n":   "not-valid-base64!!!",
		"e":   base64.RawURLEncoding.EncodeToString([]byte{1, 0, 1}),
	}

	doc := jwksDocument{}
	for _, m := range []map[string]string{goodJWK, encJWK, hsJWK, missingKidJWK, ecJWK, smallJWK, badModulusJWK} {
		doc.Keys = append(doc.Keys, jwk{
			Kty: m["kty"],
			Kid: m["kid"],
			Use: m["use"],
			Alg: m["alg"],
			N:   m["n"],
			E:   m["e"],
		})
	}

	keys, err := parseKeySet(doc)
	require.NoError(t, err)
	require.Len(t, keys, 1)
	_, ok := keys["good"]
	assert.True(t, ok)
}

func TestRunRetriesUntilLoadedThenRefreshesPeriodically(t *testing.T) {
	k1 := authtest.NewKey(t, "k1")
	k2 := authtest.NewKey(t, "k2")
	srv := authtest.NewServer(t, k1)
	srv.Set(http.StatusServiceUnavailable, []byte("unavailable"))

	j := NewJWKS(srv.URL,
		WithInitialBackoff(5*time.Millisecond),
		WithRefreshInterval(20*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		j.Run(ctx, discardLogger())
		close(done)
	}()

	// While the JWKS endpoint returns 503, Run must retry at least 3 times
	// and never become ready.
	require.Eventually(t, func() bool { return srv.Hits() >= 3 }, time.Second, time.Millisecond)
	assert.Error(t, j.Ready(context.Background()))

	// Once the endpoint recovers with k1, Run's retry loop picks it up.
	srv.SetKeys(k1)
	require.Eventually(t, func() bool { return j.Ready(context.Background()) == nil }, time.Second, time.Millisecond)

	pub1, err := j.Key(context.Background(), "k1")
	require.NoError(t, err)
	assert.NotNil(t, pub1)

	// Identity rotates in k2; the periodic ticker should pick it up without
	// Key ever needing to treat it as a miss.
	srv.SetKeys(k1, k2)
	require.Eventually(t, func() bool {
		pub, ok := j.lookup("k2")
		return ok && pub != nil
	}, time.Second, time.Millisecond)

	hitsBeforeCancel := srv.Hits()
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}

	// No miss-triggered refresh should have happened for k2 (Hits only grew
	// from Refresh calls in Run's own retry/ticker loop).
	assert.GreaterOrEqual(t, hitsBeforeCancel, 4)
}

// hangingServer starts a JWKS server whose handler blocks until the test
// unblocks it, simulating a hung upstream. The handler is always unblocked
// in t.Cleanup, before the server is closed, so no goroutine or connection
// is leaked past the test.
func hangingServer(t *testing.T) *httptest.Server {
	t.Helper()
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-unblock
	}))
	t.Cleanup(func() {
		close(unblock)
		srv.Close()
	})
	return srv
}

func TestRefreshTimesOutOnHangingServer(t *testing.T) {
	srv := hangingServer(t)

	// A client with no Timeout of its own: only the context bound Refresh
	// applies internally can save us here.
	j := NewJWKS(srv.URL,
		WithHTTPClient(&http.Client{}),
		withFetchTimeout(20*time.Millisecond),
	)

	start := time.Now()
	err := j.Refresh(context.Background())
	elapsed := time.Since(start)

	assert.Error(t, err)
	assert.Less(t, elapsed, time.Second, "Refresh must not block indefinitely on a hanging server")
	assert.Error(t, j.Ready(context.Background()), "Ready must stay false when the only load attempt hung")
}

func TestKeyMissReturnsPromptlyOnHangingServer(t *testing.T) {
	srv := hangingServer(t)

	j := NewJWKS(srv.URL,
		WithHTTPClient(&http.Client{}),
		withFetchTimeout(20*time.Millisecond),
	)

	start := time.Now()
	_, err := j.Key(context.Background(), "unknown")
	elapsed := time.Since(start)

	assert.ErrorIs(t, err, ErrUnknownKey)
	assert.Less(t, elapsed, time.Second, "a miss against a hanging JWKS endpoint must not block indefinitely")
}

func TestNewJWKSDefaultClientHasATimeout(t *testing.T) {
	j := NewJWKS("http://example.invalid")
	assert.Equal(t, fetchTimeout, j.httpClient.Timeout, "the default client must not be able to hang forever")
}
