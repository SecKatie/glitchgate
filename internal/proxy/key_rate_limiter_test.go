// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/ratelimit"
)

type stubRateLimitStore struct {
	limits map[string]struct{ perMinute, burst int }
}

func (s *stubRateLimitStore) GetKeyRateLimit(_ context.Context, keyID string) (int, int, bool, error) {
	if l, ok := s.limits[keyID]; ok {
		return l.perMinute, l.burst, true, nil
	}
	return 0, 0, false, nil
}

func TestKeyAwareRateLimiter_NoPerKeyLimit(t *testing.T) {
	global := ratelimit.New(120, 30, 15*time.Minute)
	store := &stubRateLimitStore{limits: map[string]struct{ perMinute, burst int }{}}
	kl := NewKeyAwareRateLimiter(global, store, time.Minute)

	// Should always defer to global only.
	for range 30 {
		require.True(t, kl.Allow(context.Background(), "key1"), "should allow up to global burst")
	}
}

func TestKeyAwareRateLimiter_StricterPerKeyLimit(t *testing.T) {
	// Global: 120/min, burst 30. Per-key: 60/min, burst 5.
	global := ratelimit.New(120, 30, 15*time.Minute)
	store := &stubRateLimitStore{limits: map[string]struct{ perMinute, burst int }{
		"key1": {60, 5},
	}}
	kl := NewKeyAwareRateLimiter(global, store, time.Minute)

	// Per-key burst is 5, which is stricter than global's 30.
	for range 5 {
		require.True(t, kl.Allow(context.Background(), "key1"))
	}
	// 6th request should be denied by per-key limiter.
	require.False(t, kl.Allow(context.Background(), "key1"), "per-key limit should deny")
}

func TestKeyAwareRateLimiter_StricterGlobalLimit(t *testing.T) {
	// Global: 60/min, burst 2. Per-key: 120/min, burst 30.
	global := ratelimit.New(60, 2, 15*time.Minute)
	store := &stubRateLimitStore{limits: map[string]struct{ perMinute, burst int }{
		"key1": {120, 30},
	}}
	kl := NewKeyAwareRateLimiter(global, store, time.Minute)

	// Global burst is 2, which is stricter.
	for range 2 {
		require.True(t, kl.Allow(context.Background(), "key1"))
	}
	require.False(t, kl.Allow(context.Background(), "key1"), "global limit should deny")
}
