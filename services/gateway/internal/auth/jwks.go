// Package auth verifies access tokens issued by identity and enforces the
// coarse route auth levels the gateway applies before proxying a request.
// JWKS (this file) fetches and caches identity's RSA signing keys, so
// verifying a token's signature never blocks on a network round trip in the
// common case: keys are refreshed periodically and, at most once every
// MissRefreshInterval, on an unrecognized key id.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// RefreshInterval is the default interval at which Run periodically reloads
// identity's signing keys once the initial load has succeeded.
const RefreshInterval = 5 * time.Minute

// MissRefreshInterval is the minimum time between two refreshes triggered
// by an unrecognized key id. It keeps a flood of forged or stale kids from
// hammering identity's JWKS endpoint.
const MissRefreshInterval = 30 * time.Second

// maxJWKSBodyBytes caps how much of a JWKS response body Refresh reads,
// guarding against a misbehaving or malicious upstream.
const maxJWKSBodyBytes = 1 << 20 // 1 MB

// fetchTimeout bounds how long a single JWKS fetch may take. It backs both
// the default HTTP client's Timeout and a context.WithTimeout applied
// around every fetch, so a hanging JWKS endpoint cannot stall Run forever
// or, on the miss path, hold Key's refresh mutex indefinitely — even if a
// caller-supplied client (via WithHTTPClient) has no timeout of its own.
const fetchTimeout = 5 * time.Second

// defaultInitialBackoff is Run's default starting delay between failed
// startup load attempts, before it doubles on each subsequent failure.
const defaultInitialBackoff = 500 * time.Millisecond

// maxBackoff caps Run's exponential backoff between startup load attempts.
const maxBackoff = 30 * time.Second

// ErrUnknownKey is returned by Key when kid is not present in the cached
// key set, even after a refresh was attempted (or skipped because the last
// miss-triggered refresh happened too recently).
var ErrUnknownKey = errors.New("auth: unknown key id")

// jwk is one entry of a JWKS document's "keys" array.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// jwksDocument is the top-level shape of a JWKS response.
type jwksDocument struct {
	Keys []jwk `json:"keys"`
}

// Option configures a JWKS.
type Option func(*JWKS)

// WithHTTPClient overrides the HTTP client used to fetch the JWKS document.
// Intended mainly for tests; production callers may leave this unset (it
// defaults to a client with a fetchTimeout Timeout). Every fetch is also
// wrapped in a context.WithTimeout(fetchTimeout), so a client passed here
// stays bounded even if it sets no Timeout of its own.
func WithHTTPClient(c *http.Client) Option {
	return func(j *JWKS) { j.httpClient = c }
}

// WithJWKSClock overrides the JWKS's time source, used to enforce
// MissRefreshInterval. Intended for deterministic tests; production callers
// should leave this unset (it defaults to time.Now).
func WithJWKSClock(now func() time.Time) Option {
	return func(j *JWKS) { j.now = now }
}

// WithRefreshInterval overrides the periodic refresh interval Run uses once
// the initial load has succeeded. It defaults to RefreshInterval.
func WithRefreshInterval(d time.Duration) Option {
	return func(j *JWKS) { j.refreshInterval = d }
}

// WithInitialBackoff overrides the starting delay Run waits before retrying
// a failed startup load; it doubles on each subsequent failure up to a 30s
// cap. It defaults to 500ms.
func WithInitialBackoff(d time.Duration) Option {
	return func(j *JWKS) { j.initialBackoff = d }
}

// withFetchTimeout overrides the per-fetch timeout Refresh applies via
// context.WithTimeout. Unexported: production callers get a fixed
// fetchTimeout; only tests need to shrink it to exercise the timeout path
// without waiting out the real default.
func withFetchTimeout(d time.Duration) Option {
	return func(j *JWKS) { j.fetchTimeout = d }
}

// JWKS caches identity's RSA signing keys, fetched from a JWKS endpoint. A
// failed Refresh never discards a previously loaded key set. The zero value
// is not usable; construct one with NewJWKS.
type JWKS struct {
	url        string
	httpClient *http.Client
	now        func() time.Time

	refreshInterval time.Duration
	initialBackoff  time.Duration
	fetchTimeout    time.Duration

	ready atomic.Bool

	mu   sync.Mutex
	keys map[string]*rsa.PublicKey

	// missMu serializes miss-triggered refreshes and guards lastMiss, kept
	// separate from mu so a refresh (which itself locks mu) can run while
	// mu is not held by the miss path.
	missMu   sync.Mutex
	lastMiss time.Time
}

// NewJWKS creates a JWKS that fetches its key set from url. No network call
// is made until Refresh, Key or Run is called.
func NewJWKS(url string, opts ...Option) *JWKS {
	j := &JWKS{
		url:             url,
		httpClient:      &http.Client{Timeout: fetchTimeout},
		now:             time.Now,
		refreshInterval: RefreshInterval,
		initialBackoff:  defaultInitialBackoff,
		fetchTimeout:    fetchTimeout,
		keys:            make(map[string]*rsa.PublicKey),
	}
	for _, opt := range opts {
		opt(j)
	}
	return j
}

// Refresh fetches and parses the JWKS document, replacing the cached key
// set on success. On any failure (a non-200 status, a malformed body, a
// body over 1 MB, or a document with zero usable keys) the previously
// cached key set, if any, is left untouched and the error is returned.
func (j *JWKS) Refresh(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, j.fetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.url, nil)
	if err != nil {
		return fmt.Errorf("auth: build JWKS request: %w", err)
	}

	resp, err := j.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("auth: fetch JWKS: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("auth: JWKS endpoint returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBodyBytes+1))
	if err != nil {
		return fmt.Errorf("auth: read JWKS body: %w", err)
	}
	if len(body) > maxJWKSBodyBytes {
		return fmt.Errorf("auth: JWKS body exceeds %d bytes", maxJWKSBodyBytes)
	}

	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("auth: parse JWKS body: %w", err)
	}

	keys, err := parseKeySet(doc)
	if err != nil {
		return err
	}

	j.mu.Lock()
	j.keys = keys
	j.mu.Unlock()

	j.ready.Store(true)
	return nil
}

// parseKeySet converts a parsed JWKS document into a lookup of usable RSA
// public keys, keyed by kid. An entry that is not a signing key we
// recognize is skipped, not an error; the document as a whole is an error
// only when no entry survives.
func parseKeySet(doc jwksDocument) (map[string]*rsa.PublicKey, error) {
	keys := make(map[string]*rsa.PublicKey)
	for _, k := range doc.Keys {
		pub, ok := usableKey(k)
		if !ok {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, errors.New("auth: JWKS document has no usable keys")
	}
	return keys, nil
}

// usableKey validates and decodes one JWKS entry into an RSA public key. It
// accepts only RSA signing keys safe to verify with: a non-empty kid, use
// of "" or "sig", alg of "" or "RS256", a modulus of at least 2048 bits,
// and a public exponent of at least 3.
func usableKey(k jwk) (*rsa.PublicKey, bool) {
	if k.Kty != "RSA" || k.Kid == "" {
		return nil, false
	}
	if k.Use != "" && k.Use != "sig" {
		return nil, false
	}
	if k.Alg != "" && k.Alg != "RS256" {
		return nil, false
	}

	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, false
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, false
	}

	n := new(big.Int).SetBytes(nBytes)
	if n.BitLen() < 2048 {
		return nil, false
	}
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() < 3 {
		return nil, false
	}

	return &rsa.PublicKey{N: n, E: int(e.Int64())}, true
}

// lookup returns the cached public key for kid, if any.
func (j *JWKS) lookup(kid string) (*rsa.PublicKey, bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	pub, ok := j.keys[kid]
	return pub, ok
}

// Key returns the RSA public key for kid. On a cache miss it refreshes at
// most once every MissRefreshInterval: concurrent misses are serialized by
// a mutex, and the cache is checked again after that mutex is acquired, so
// only one goroutine ever performs the refresh for a given throttle window.
func (j *JWKS) Key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if pub, ok := j.lookup(kid); ok {
		return pub, nil
	}

	j.missMu.Lock()
	defer j.missMu.Unlock()

	// Another goroutine may have refreshed while we waited for missMu.
	if pub, ok := j.lookup(kid); ok {
		return pub, nil
	}

	now := j.now()
	if !j.lastMiss.IsZero() && now.Sub(j.lastMiss) < MissRefreshInterval {
		return nil, ErrUnknownKey
	}
	j.lastMiss = now

	// The refresh outlives a client that disconnects mid-request, otherwise
	// it would burn the miss slot without loading anything; Refresh bounds
	// the fetch with fetchTimeout.
	if err := j.Refresh(context.WithoutCancel(ctx)); err != nil {
		slog.WarnContext(ctx, "jwks: refresh on unknown key id failed", "error", err)
		return nil, ErrUnknownKey
	}

	if pub, ok := j.lookup(kid); ok {
		return pub, nil
	}
	return nil, ErrUnknownKey
}

// Ready implements server.Check: it returns nil once a key set has been
// loaded successfully at least once, or an error otherwise.
func (j *JWKS) Ready(_ context.Context) error {
	if j.ready.Load() {
		return nil
	}
	return errors.New("auth: JWKS not loaded yet")
}

// Run loads the initial key set, retrying with exponential backoff
// (starting at the configured initial backoff, doubling on each failure up
// to a 30s cap) until it succeeds or ctx is done. Once loaded, it refreshes
// on a ticker at the configured refresh interval, logging any failure but
// keeping the previously cached keys, until ctx is done.
func (j *JWKS) Run(ctx context.Context, logger *slog.Logger) {
	backoff := j.initialBackoff
	for {
		if err := j.Refresh(ctx); err == nil {
			break
		} else if ctx.Err() != nil {
			return
		} else {
			logger.Error("jwks: initial load failed, retrying", "error", err, "backoff", backoff)
		}

		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}

		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}

	ticker := time.NewTicker(j.refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := j.Refresh(ctx); err != nil {
				logger.Error("jwks: periodic refresh failed", "error", err)
			}
		}
	}
}
