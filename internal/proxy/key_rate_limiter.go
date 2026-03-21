// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"context"
	"sync"
	"time"

	"github.com/seckatie/glitchgate/internal/ratelimit"
)

// KeyRateLimitStore is the minimal store interface needed by KeyAwareRateLimiter.
type KeyRateLimitStore interface {
	GetKeyRateLimit(ctx context.Context, keyID string) (perMinute, burst int, ok bool, err error)
}

type keyLimiterEntry struct {
	limiter   *ratelimit.Limiter
	fetchedAt time.Time
}

// KeyAwareRateLimiter wraps a global rate limiter and adds per-key overrides.
// Both the global limit and any per-key limit must allow the request (stricter wins).
type KeyAwareRateLimiter struct {
	global    *ratelimit.Limiter
	store     KeyRateLimitStore
	ttl       time.Duration
	mu        sync.RWMutex
	perKey    map[string]*keyLimiterEntry
	now       func() time.Time
	lastSweep time.Time
}

// NewKeyAwareRateLimiter creates a rate limiter that combines global and per-key limits.
func NewKeyAwareRateLimiter(global *ratelimit.Limiter, store KeyRateLimitStore, ttl time.Duration) *KeyAwareRateLimiter {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &KeyAwareRateLimiter{
		global: global,
		store:  store,
		ttl:    ttl,
		perKey: make(map[string]*keyLimiterEntry),
		now:    time.Now,
	}
}

// Allow checks both the global rate limit and any per-key rate limit.
// Both must allow the request. Returns false if either denies it.
func (kl *KeyAwareRateLimiter) Allow(ctx context.Context, keyID string) bool {
	// Check global limit first.
	if kl.global != nil && !kl.global.Allow(keyID) {
		return false
	}

	// Check per-key limit.
	limiter := kl.getKeyLimiter(ctx, keyID)
	if limiter != nil && !limiter.Allow(keyID) {
		return false
	}

	return true
}

func (kl *KeyAwareRateLimiter) getKeyLimiter(ctx context.Context, keyID string) *ratelimit.Limiter {
	now := kl.now()

	kl.mu.RLock()
	entry, ok := kl.perKey[keyID]
	kl.mu.RUnlock()
	if ok && now.Sub(entry.fetchedAt) < kl.ttl {
		return entry.limiter
	}

	// Re-check under write lock to avoid thundering herd on TTL expiry.
	kl.mu.Lock()
	entry, ok = kl.perKey[keyID]
	if ok && now.Sub(entry.fetchedAt) < kl.ttl {
		kl.mu.Unlock()
		return entry.limiter
	}
	kl.evictStaleLocked(now)
	kl.mu.Unlock()

	// Fetch from DB.
	perMinute, burst, found, err := kl.store.GetKeyRateLimit(ctx, keyID)
	if err != nil || !found {
		// Cache the "no limit" result too.
		kl.mu.Lock()
		kl.perKey[keyID] = &keyLimiterEntry{limiter: nil, fetchedAt: now}
		kl.mu.Unlock()
		return nil
	}

	limiter := ratelimit.New(perMinute, burst, 15*time.Minute)

	kl.mu.Lock()
	kl.perKey[keyID] = &keyLimiterEntry{limiter: limiter, fetchedAt: now}
	kl.mu.Unlock()

	return limiter
}

// evictStaleLocked removes cache entries older than 10x TTL.
// Must be called with kl.mu held for writing.
func (kl *KeyAwareRateLimiter) evictStaleLocked(now time.Time) {
	const sweepInterval = 5 // multiples of TTL between sweeps
	if now.Sub(kl.lastSweep) < time.Duration(sweepInterval)*kl.ttl {
		return
	}
	kl.lastSweep = now
	staleThreshold := 10 * kl.ttl
	for k, e := range kl.perKey {
		if now.Sub(e.fetchedAt) > staleThreshold {
			delete(kl.perKey, k)
		}
	}
}
