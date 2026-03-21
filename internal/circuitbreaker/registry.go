// SPDX-License-Identifier: AGPL-3.0-or-later

package circuitbreaker

import (
	"sync"
	"time"
)

// Registry manages per-provider circuit breakers. Breakers are created lazily
// on first access and share common threshold/cooldown settings.
type Registry struct {
	mu        sync.RWMutex
	breakers  map[string]*Breaker
	threshold int
	cooldown  time.Duration
}

// NewRegistry creates a circuit breaker registry with shared settings.
// Zero/negative threshold and cooldown values are defaulted by NewBreaker.
func NewRegistry(threshold int, cooldown time.Duration) *Registry {
	return &Registry{
		breakers:  make(map[string]*Breaker),
		threshold: threshold,
		cooldown:  cooldown,
	}
}

// Get returns the circuit breaker for the named provider, creating one if
// it does not yet exist.
func (r *Registry) Get(provider string) *Breaker {
	r.mu.RLock()
	b, ok := r.breakers[provider]
	r.mu.RUnlock()
	if ok {
		return b
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock.
	if b, ok = r.breakers[provider]; ok {
		return b
	}
	b = NewBreaker(r.threshold, r.cooldown)
	r.breakers[provider] = b
	return b
}

// AllStats returns a snapshot of every provider's circuit breaker state.
func (r *Registry) AllStats() map[string]Stats {
	r.mu.RLock()
	refs := make(map[string]*Breaker, len(r.breakers))
	for k, v := range r.breakers {
		refs[k] = v
	}
	r.mu.RUnlock()

	out := make(map[string]Stats, len(refs))
	for name, b := range refs {
		out[name] = b.Stats()
	}
	return out
}
