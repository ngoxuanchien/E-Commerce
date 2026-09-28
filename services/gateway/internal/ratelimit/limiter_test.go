package ratelimit

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/time/rate"
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

func TestNewLoginLimiter_AllowsBurstThenDeniesWithExactWait(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	l := NewLoginLimiter(WithClock(clock.Now))

	for i := 0; i < 10; i++ {
		allowed, _ := l.Allow("1.2.3.4")
		require.Truef(t, allowed, "call %d should be allowed", i+1)
	}

	allowed, wait := l.Allow("1.2.3.4")
	assert.False(t, allowed)
	assert.Equal(t, 6*time.Second, wait)

	clock.Advance(6 * time.Second)
	allowed, _ = l.Allow("1.2.3.4")
	assert.True(t, allowed)
}

func TestNewGeneralLimiter_AllowsBurstThenRefillsAtRate(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	l := NewGeneralLimiter(WithClock(clock.Now))

	allowedCount := 0
	var firstDeniedWait time.Duration
	for i := 0; i < 50; i++ {
		allowed, wait := l.Allow("5.6.7.8")
		if allowed {
			allowedCount++
		} else if firstDeniedWait == 0 {
			firstDeniedWait = wait
		}
	}
	assert.Equal(t, 40, allowedCount)
	assert.Equal(t, 50*time.Millisecond, firstDeniedWait)

	clock.Advance(1 * time.Second)
	allowedAfter := 0
	for i := 0; i < 21; i++ {
		allowed, _ := l.Allow("5.6.7.8")
		if allowed {
			allowedAfter++
		}
	}
	assert.Equal(t, 20, allowedAfter)
}

func TestLimiter_BucketsArePerKey(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	l := New(rate.Every(6*time.Second), 10, IdleTTL, WithClock(clock.Now))

	for i := 0; i < 10; i++ {
		allowed, _ := l.Allow("key-a")
		require.True(t, allowed)
	}
	allowed, _ := l.Allow("key-a")
	assert.False(t, allowed, "key-a should be exhausted")

	// A different key has its own, fresh bucket.
	allowed, _ = l.Allow("key-b")
	assert.True(t, allowed)
}

func TestLimiter_DeniedCallsDoNotDelayRefill(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	l := NewGeneralLimiter(WithClock(clock.Now))

	for i := 0; i < 40; i++ {
		allowed, _ := l.Allow("9.9.9.9")
		require.True(t, allowed)
	}

	for i := 0; i < 100; i++ {
		allowed, _ := l.Allow("9.9.9.9")
		require.False(t, allowed)
	}

	// Refill is unaffected by the 100 denied calls: still exactly one
	// token's worth (50ms) after advancing by that amount.
	clock.Advance(50 * time.Millisecond)
	allowed, _ := l.Allow("9.9.9.9")
	assert.True(t, allowed)
	allowed, _ = l.Allow("9.9.9.9")
	assert.False(t, allowed)
}

func TestLimiter_SweepEvictsOnlyIdleBuckets(t *testing.T) {
	clock := newFakeClock(time.Unix(0, 0))
	l := New(rate.Limit(1), 1, 10*time.Minute, WithClock(clock.Now))

	l.Allow("idle-key")
	clock.Advance(5 * time.Minute)
	l.Allow("fresh-key")
	require.Equal(t, 2, l.Len())

	// idle-key was last touched 9 minutes ago (>= 10m idle threshold once we
	// advance another 4 minutes); fresh-key was touched 4 minutes ago.
	clock.Advance(5 * time.Minute)
	l.Sweep()

	assert.Equal(t, 1, l.Len())
}

func TestLimiter_Run_SweepsUntilContextDone(t *testing.T) {
	l := New(rate.Limit(1), 1, 20*time.Millisecond)
	l.Allow("k")
	require.Equal(t, 1, l.Len())

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		l.Run(ctx, 5*time.Millisecond)
		close(done)
	}()

	require.Eventually(t, func() bool { return l.Len() == 0 }, time.Second, 5*time.Millisecond)

	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancellation")
	}
}
