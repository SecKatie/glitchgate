// Package ratelimit provides keyed in-memory token-bucket rate limiting.
package ratelimit

import (
	"sync"
	"time"
)

type entry struct {
	tokens     float64
	lastSeen   time.Time
	lastRefill time.Time
}

// Limiter manages one token bucket per key and evicts idle buckets over time.
type Limiter struct {
	mu            sync.Mutex
	entries       map[string]*entry
	tokensPerSec  float64
	burst         int
	idleTTL       time.Duration
	cleanupEvery  time.Duration
	lastCleanupAt time.Time
	now           func() time.Time
}

// New constructs a keyed in-memory rate limiter.
func New(perMinute, burst int, idleTTL time.Duration) *Limiter {
	if perMinute <= 0 {
		perMinute = 1
	}
	if burst <= 0 {
		burst = 1
	}
	if idleTTL <= 0 {
		idleTTL = 15 * time.Minute
	}
	return &Limiter{
		entries:      make(map[string]*entry),
		tokensPerSec: float64(perMinute) / 60.0,
		burst:        burst,
		idleTTL:      idleTTL,
		cleanupEvery: time.Minute,
		now:          time.Now,
	}
}

// Allow consumes one token from the bucket for key.
func (l *Limiter) Allow(key string) bool {
	if key == "" {
		key = "unknown"
	}

	now := l.now()

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.lastCleanupAt.IsZero() || now.Sub(l.lastCleanupAt) >= l.cleanupEvery {
		l.cleanupLocked(now)
	}

	e, ok := l.entries[key]
	if !ok {
		e = &entry{
			tokens:     float64(l.burst),
			lastRefill: now,
		}
		l.entries[key] = e
	}
	e.lastSeen = now

	elapsed := now.Sub(e.lastRefill).Seconds()
	if elapsed > 0 {
		e.tokens += elapsed * l.tokensPerSec
		if e.tokens > float64(l.burst) {
			e.tokens = float64(l.burst)
		}
		e.lastRefill = now
	}

	if e.tokens < 1 {
		return false
	}
	e.tokens--
	return true
}

func (l *Limiter) cleanupLocked(now time.Time) {
	for key, e := range l.entries {
		if now.Sub(e.lastSeen) > l.idleTTL {
			delete(l.entries, key)
		}
	}
	l.lastCleanupAt = now
}
