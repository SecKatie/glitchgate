// SPDX-License-Identifier: AGPL-3.0-or-later

// Package circuitbreaker provides per-provider circuit breaking for upstream
// LLM providers. A breaker tracks consecutive failures and temporarily
// removes unhealthy providers from fallback chains.
package circuitbreaker

import (
	"sync"
	"time"
)

// State represents the current state of a circuit breaker.
type State int

const (
	// StateClosed means the provider is healthy and all requests pass through.
	StateClosed State = iota
	// StateOpen means the provider has tripped and requests should be skipped.
	StateOpen
	// StateHalfOpen means the cooldown has expired and one probe request is allowed.
	StateHalfOpen
)

// String returns a human-readable label for the breaker state.
func (s State) String() string {
	switch s {
	case StateClosed:
		return "closed"
	case StateOpen:
		return "open"
	case StateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// Stats holds a snapshot of circuit breaker state for metrics and display.
type Stats struct {
	State               State
	ConsecutiveFailures int
	TotalTrips          int64
	LastFailure         time.Time
	LastStateChange     time.Time
	CooldownEndsAt      time.Time // zero if closed
}

// Breaker implements a three-state circuit breaker (closed → open → half-open).
//
// Closed: all requests pass. Consecutive failures are tracked.
// Open: requests are rejected. After a cooldown period, transitions to half-open.
// Half-open: one probe request is allowed. Success → closed. Failure → open.
type Breaker struct {
	mu                  sync.Mutex
	state               State
	consecutiveFailures int
	totalTrips          int64
	failureThreshold    int
	cooldown            time.Duration
	lastFailure         time.Time
	lastStateChange     time.Time
	probeInFlight       bool // true when a half-open probe is active
	now                 func() time.Time
}

// NewBreaker creates a circuit breaker that trips after threshold consecutive
// failures and stays open for cooldown before allowing a half-open probe.
func NewBreaker(threshold int, cooldown time.Duration) *Breaker {
	if threshold <= 0 {
		threshold = 5
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &Breaker{
		state:            StateClosed,
		failureThreshold: threshold,
		cooldown:         cooldown,
		lastStateChange:  time.Now(),
		now:              time.Now,
	}
}

// Allow reports whether a request should be sent to this provider.
//
// Closed: always returns true.
// Open: returns false until cooldown expires, then transitions to half-open
//
//	and returns true for exactly one probe request.
//
// Half-open: returns false while a probe is in flight; returns true for the
//
//	first caller after cooldown if no probe is active.
func (b *Breaker) Allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	switch b.state {
	case StateClosed:
		return true
	case StateOpen:
		now := b.now()
		if now.Sub(b.lastStateChange) < b.cooldown {
			return false
		}
		// Cooldown expired — transition to half-open.
		b.state = StateHalfOpen
		b.lastStateChange = now
		b.probeInFlight = true
		return true
	case StateHalfOpen:
		if b.probeInFlight {
			return false
		}
		// Previous probe completed (success or failure already recorded)
		// but state is still half-open — shouldn't happen in normal flow.
		// Allow one more probe.
		b.probeInFlight = true
		return true
	default:
		return false
	}
}

// RecordSuccess signals that a request to this provider succeeded.
// In any state this resets the breaker to closed.
func (b *Breaker) RecordSuccess() {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.consecutiveFailures = 0
	b.probeInFlight = false
	if b.state != StateClosed {
		b.state = StateClosed
		b.lastStateChange = b.now()
	}
}

// RecordFailure signals that a request to this provider failed (5xx or
// network error). In closed state, increments the consecutive failure count
// and trips the breaker if threshold is reached. In half-open state,
// immediately returns to open.
func (b *Breaker) RecordFailure() {
	b.mu.Lock()
	defer b.mu.Unlock()

	now := b.now()
	b.consecutiveFailures++
	b.lastFailure = now
	b.probeInFlight = false

	switch b.state {
	case StateClosed:
		if b.consecutiveFailures >= b.failureThreshold {
			b.state = StateOpen
			b.lastStateChange = now
			b.totalTrips++
		}
	case StateHalfOpen:
		// Probe failed — re-open.
		b.state = StateOpen
		b.lastStateChange = now
		b.totalTrips++
	case StateOpen:
		// Already open, just update timestamps.
	}
}

// CurrentState returns the current breaker state.
func (b *Breaker) CurrentState() State {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

// Stats returns a snapshot of the breaker's state for metrics and display.
func (b *Breaker) Stats() Stats {
	b.mu.Lock()
	defer b.mu.Unlock()

	s := Stats{
		State:               b.state,
		ConsecutiveFailures: b.consecutiveFailures,
		TotalTrips:          b.totalTrips,
		LastFailure:         b.lastFailure,
		LastStateChange:     b.lastStateChange,
	}
	if b.state == StateOpen {
		s.CooldownEndsAt = b.lastStateChange.Add(b.cooldown)
	}
	return s
}
