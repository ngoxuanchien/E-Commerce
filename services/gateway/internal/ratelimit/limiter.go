// Package ratelimit provides per-IP token bucket rate limiting for the
// gateway: a keyed limiter (limiter.go), client IP resolution that honors
// X-Forwarded-For only from trusted proxies (clientip.go), and the HTTP
// middleware that ties them together (middleware.go).
package ratelimit

import (
	"context"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// IdleTTL is the default duration a per-key bucket may sit unused before
// Sweep evicts it.
const IdleTTL = 10 * time.Minute

// Option configures a Limiter.
type Option func(*Limiter)

// WithClock overrides the Limiter's time source. Intended for deterministic
// tests; production callers should leave this unset (it defaults to
// time.Now).
func WithClock(now func() time.Time) Option {
	return func(l *Limiter) { l.now = now }
}

// bucket is one key's token bucket, plus the last time it was touched so
// Sweep can evict it once idle.
type bucket struct {
	limiter    *rate.Limiter
	lastAccess time.Time
}

// Limiter is a per-key token bucket rate limiter. Each distinct key
// (typically a client IP) gets its own bucket, created lazily on first use
// with the limiter's configured rate and burst, and evicted by Sweep once
// idle for longer than idle.
type Limiter struct {
	limit rate.Limit
	burst int
	idle  time.Duration
	now   func() time.Time

	mu      sync.Mutex
	buckets map[string]*bucket
}

// New creates a Limiter whose per-key buckets allow limit events per second
// with the given burst. idle is how long an unused bucket survives before
// Sweep evicts it.
func New(limit rate.Limit, burst int, idle time.Duration, opts ...Option) *Limiter {
	l := &Limiter{
		limit:   limit,
		burst:   burst,
		idle:    idle,
		now:     time.Now,
		buckets: make(map[string]*bucket),
	}
	for _, opt := range opts {
		opt(l)
	}
	return l
}

// NewLoginLimiter returns a Limiter for the auth login/register routes: one
// request every 6 seconds (10/min), burst 10.
func NewLoginLimiter(opts ...Option) *Limiter {
	return New(rate.Every(6*time.Second), 10, IdleTTL, opts...)
}

// NewGeneralLimiter returns a Limiter for all other routes: 20 requests per
// second, burst 40.
func NewGeneralLimiter(opts ...Option) *Limiter {
	return New(rate.Limit(20), 40, IdleTTL, opts...)
}

// Allow reports whether a request for key is allowed right now. When it is
// not, it also returns how long the caller should wait before retrying. A
// denied call consumes no token: the reservation used to check availability
// is canceled, so it never delays the bucket's refill.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{limiter: rate.NewLimiter(l.limit, l.burst)}
		l.buckets[key] = b
	}
	b.lastAccess = now

	res := b.limiter.ReserveN(now, 1)
	if !res.OK() {
		// The request can never succeed (e.g. burst 0); nothing was reserved.
		return false, 0
	}
	if delay := res.DelayFrom(now); delay > 0 {
		res.CancelAt(now)
		return false, delay
	}
	return true, 0
}

// Sweep removes buckets that have been idle for at least the Limiter's idle
// duration. It is safe to call concurrently with Allow.
func (l *Limiter) Sweep() {
	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	for key, b := range l.buckets {
		if now.Sub(b.lastAccess) >= l.idle {
			delete(l.buckets, key)
		}
	}
}

// Len returns the number of buckets currently tracked.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.buckets)
}

// Run calls Sweep every d until ctx is done. It is meant to be started in
// its own goroutine and returns once ctx is canceled.
func (l *Limiter) Run(ctx context.Context, d time.Duration) {
	ticker := time.NewTicker(d)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.Sweep()
		}
	}
}
