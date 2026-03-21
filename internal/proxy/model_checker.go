// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"context"
	"path/filepath"
	"sync"
	"time"
)

// ModelAllowlistStore is the minimal store interface needed by ModelChecker.
type ModelAllowlistStore interface {
	GetKeyAllowedModels(ctx context.Context, keyID string) ([]string, error)
}

type allowlistEntry struct {
	patterns  []string
	fetchedAt time.Time
}

// ModelChecker enforces per-key model allowlists using glob patterns.
// Allowlists are cached in memory with a TTL to avoid per-request DB queries.
type ModelChecker struct {
	store     ModelAllowlistStore
	ttl       time.Duration
	mu        sync.RWMutex
	cache     map[string]*allowlistEntry
	now       func() time.Time
	lastSweep time.Time
}

// NewModelChecker creates a model checker with the given cache TTL.
func NewModelChecker(store ModelAllowlistStore, ttl time.Duration) *ModelChecker {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &ModelChecker{
		store: store,
		ttl:   ttl,
		cache: make(map[string]*allowlistEntry),
		now:   time.Now,
	}
}

// IsModelAllowed checks whether a key is permitted to use a given model.
// Returns true if no allowlist is configured (empty = unrestricted).
func (mc *ModelChecker) IsModelAllowed(ctx context.Context, keyID, model string) (bool, error) {
	patterns, err := mc.getPatterns(ctx, keyID)
	if err != nil {
		return false, err
	}

	// Empty allowlist = unrestricted.
	if len(patterns) == 0 {
		return true, nil
	}

	for _, pattern := range patterns {
		if matched, _ := filepath.Match(pattern, model); matched {
			return true, nil
		}
	}
	return false, nil
}

// InvalidateKey removes a key's cached allowlist (e.g., after admin update).
func (mc *ModelChecker) InvalidateKey(keyID string) {
	mc.mu.Lock()
	delete(mc.cache, keyID)
	mc.mu.Unlock()
}

func (mc *ModelChecker) getPatterns(ctx context.Context, keyID string) ([]string, error) {
	now := mc.now()

	mc.mu.RLock()
	entry, ok := mc.cache[keyID]
	mc.mu.RUnlock()
	if ok && now.Sub(entry.fetchedAt) < mc.ttl {
		return entry.patterns, nil
	}

	// Re-check under write lock to avoid thundering herd on TTL expiry.
	mc.mu.Lock()
	entry, ok = mc.cache[keyID]
	if ok && now.Sub(entry.fetchedAt) < mc.ttl {
		mc.mu.Unlock()
		return entry.patterns, nil
	}
	mc.evictStaleLocked(now)
	mc.mu.Unlock()

	// Cache miss or expired — fetch from DB.
	patterns, err := mc.store.GetKeyAllowedModels(ctx, keyID)
	if err != nil {
		return nil, err
	}

	mc.mu.Lock()
	mc.cache[keyID] = &allowlistEntry{patterns: patterns, fetchedAt: now}
	mc.mu.Unlock()

	return patterns, nil
}

// evictStaleLocked removes cache entries older than 10x TTL.
// Must be called with mc.mu held for writing.
func (mc *ModelChecker) evictStaleLocked(now time.Time) {
	const sweepInterval = 5 // multiples of TTL between sweeps
	if now.Sub(mc.lastSweep) < time.Duration(sweepInterval)*mc.ttl {
		return
	}
	mc.lastSweep = now
	staleThreshold := 10 * mc.ttl
	for k, e := range mc.cache {
		if now.Sub(e.fetchedAt) > staleThreshold {
			delete(mc.cache, k)
		}
	}
}
