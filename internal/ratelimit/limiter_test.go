package ratelimit

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAllow_UnderLimit(t *testing.T) {
	l := New(60, 5, 15*time.Minute)
	for i := 0; i < 5; i++ {
		require.True(t, l.Allow("client-a"), "request %d should be allowed", i+1)
	}
}

func TestAllow_OverLimit(t *testing.T) {
	l := New(60, 2, 15*time.Minute)
	require.True(t, l.Allow("client-b"))
	require.True(t, l.Allow("client-b"))
	require.False(t, l.Allow("client-b"), "third request should be rejected")
}

func TestAllow_Refill(t *testing.T) {
	now := time.Now()
	l := New(60, 1, 15*time.Minute) // 1 token/sec
	l.now = func() time.Time { return now }

	require.True(t, l.Allow("client-c"))
	require.False(t, l.Allow("client-c"))

	// Advance time by 2 seconds — should refill at least 1 token.
	now = now.Add(2 * time.Second)
	require.True(t, l.Allow("client-c"), "should be allowed after refill")
}

func TestAllow_EmptyKey(t *testing.T) {
	l := New(60, 1, 15*time.Minute)
	require.True(t, l.Allow(""), "empty key should be treated as 'unknown' and allowed")
	require.False(t, l.Allow(""), "second request for empty key should be rejected (burst=1)")
}

func TestCleanup_IdleEntries(t *testing.T) {
	now := time.Now()
	ttl := 5 * time.Minute
	l := New(60, 5, ttl)
	l.now = func() time.Time { return now }

	l.Allow("stale-client")

	// Advance past TTL + cleanup interval.
	now = now.Add(ttl + 2*time.Minute)
	l.Allow("fresh-client") // triggers cleanup

	l.mu.Lock()
	_, hasStale := l.entries["stale-client"]
	_, hasFresh := l.entries["fresh-client"]
	l.mu.Unlock()

	require.False(t, hasStale, "stale entry should be evicted")
	require.True(t, hasFresh, "fresh entry should remain")
}

func TestConcurrentAccess(_ *testing.T) {
	l := New(60, 100, 15*time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				l.Allow("concurrent-client")
			}
		}()
	}
	wg.Wait()
	// No panic or race detector failure is the assertion.
}
