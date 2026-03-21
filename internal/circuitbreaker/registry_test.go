// SPDX-License-Identifier: AGPL-3.0-or-later

package circuitbreaker

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestRegistry_GetCreatesOnDemand(t *testing.T) {
	r := NewRegistry(3, 10*time.Second)

	b1 := r.Get("anthropic")
	b2 := r.Get("openai")
	b3 := r.Get("anthropic")

	require.NotNil(t, b1)
	require.NotNil(t, b2)
	require.Same(t, b1, b3, "same provider should return same breaker")
	require.NotSame(t, b1, b2, "different providers should have different breakers")
}

func TestRegistry_AllStats(t *testing.T) {
	r := NewRegistry(2, 10*time.Second)

	r.Get("anthropic").RecordFailure()
	r.Get("openai") // no failures

	stats := r.AllStats()
	require.Len(t, stats, 2)
	require.Equal(t, 1, stats["anthropic"].ConsecutiveFailures)
	require.Equal(t, 0, stats["openai"].ConsecutiveFailures)
}

func TestRegistry_DefaultsClamped(t *testing.T) {
	r := NewRegistry(0, 0)
	b := r.Get("test")
	// Defaults are applied by NewBreaker: threshold=5, cooldown=30s.
	require.Equal(t, 5, b.failureThreshold)
	require.Equal(t, 30*time.Second, b.cooldown)
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	r := NewRegistry(5, time.Second)
	var wg sync.WaitGroup

	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			name := "provider"
			if n%3 == 0 {
				name = "provider-alt"
			}
			b := r.Get(name)
			b.Allow()
			if n%2 == 0 {
				b.RecordSuccess()
			} else {
				b.RecordFailure()
			}
		}(i)
	}
	wg.Wait()
	// No panic = success.
}
