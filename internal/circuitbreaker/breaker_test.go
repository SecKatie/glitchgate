// SPDX-License-Identifier: AGPL-3.0-or-later

package circuitbreaker

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNewBreaker_DefaultsClamped(t *testing.T) {
	b := NewBreaker(0, 0)
	require.Equal(t, 5, b.failureThreshold)
	require.Equal(t, 30*time.Second, b.cooldown)
}

func TestBreaker_ClosedAllowsAll(t *testing.T) {
	b := NewBreaker(3, time.Minute)
	for range 100 {
		require.True(t, b.Allow(), "closed breaker should allow")
	}
	require.Equal(t, StateClosed, b.CurrentState())
}

func TestBreaker_TripsAfterThreshold(t *testing.T) {
	b := NewBreaker(3, time.Minute)
	b.RecordFailure()
	require.Equal(t, StateClosed, b.CurrentState(), "1 failure: still closed")
	b.RecordFailure()
	require.Equal(t, StateClosed, b.CurrentState(), "2 failures: still closed")
	b.RecordFailure()
	require.Equal(t, StateOpen, b.CurrentState(), "3 failures: tripped")
	require.False(t, b.Allow(), "open breaker should reject")
}

func TestBreaker_SuccessResetsFailures(t *testing.T) {
	b := NewBreaker(3, time.Minute)
	b.RecordFailure()
	b.RecordFailure()
	b.RecordSuccess()

	s := b.Stats()
	require.Equal(t, StateClosed, s.State)
	require.Equal(t, 0, s.ConsecutiveFailures)

	// Should need 3 more failures to trip.
	b.RecordFailure()
	b.RecordFailure()
	require.Equal(t, StateClosed, b.CurrentState())
	b.RecordFailure()
	require.Equal(t, StateOpen, b.CurrentState())
}

func TestBreaker_OpenToHalfOpenAfterCooldown(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := NewBreaker(1, 30*time.Second)
	b.now = func() time.Time { return now }

	b.RecordFailure()
	require.Equal(t, StateOpen, b.CurrentState())
	require.False(t, b.Allow(), "should reject during cooldown")

	// Advance past cooldown.
	now = now.Add(31 * time.Second)
	require.True(t, b.Allow(), "should allow probe after cooldown")
	require.Equal(t, StateHalfOpen, b.CurrentState())

	// Second concurrent request during probe should be rejected.
	require.False(t, b.Allow(), "should reject while probe in flight")
}

func TestBreaker_HalfOpenSuccessCloses(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := NewBreaker(1, 10*time.Second)
	b.now = func() time.Time { return now }

	b.RecordFailure()
	require.Equal(t, StateOpen, b.CurrentState())

	now = now.Add(11 * time.Second)
	require.True(t, b.Allow()) // probe
	b.RecordSuccess()

	require.Equal(t, StateClosed, b.CurrentState())
	require.True(t, b.Allow(), "should be fully open after successful probe")
}

func TestBreaker_HalfOpenFailureReopens(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := NewBreaker(1, 10*time.Second)
	b.now = func() time.Time { return now }

	b.RecordFailure()
	now = now.Add(11 * time.Second)
	require.True(t, b.Allow()) // probe

	b.RecordFailure()
	require.Equal(t, StateOpen, b.CurrentState(), "failed probe should reopen")
	require.False(t, b.Allow(), "should reject after failed probe")
}

func TestBreaker_Stats(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := NewBreaker(2, 30*time.Second)
	b.now = func() time.Time { return now }

	now = now.Add(time.Second)
	b.RecordFailure()
	now = now.Add(time.Second)
	b.RecordFailure()

	s := b.Stats()
	require.Equal(t, StateOpen, s.State)
	require.Equal(t, 2, s.ConsecutiveFailures)
	require.Equal(t, int64(1), s.TotalTrips)
	require.False(t, s.LastFailure.IsZero())
	require.Equal(t, now.Add(30*time.Second), s.CooldownEndsAt)
}

func TestBreaker_TotalTripsAccumulate(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := NewBreaker(1, 5*time.Second)
	b.now = func() time.Time { return now }

	// Trip 1.
	b.RecordFailure()
	now = now.Add(6 * time.Second)
	b.Allow() // half-open
	b.RecordSuccess()

	// Trip 2.
	now = now.Add(time.Second)
	b.RecordFailure()
	now = now.Add(6 * time.Second)
	b.Allow()         // half-open
	b.RecordFailure() // failed probe → trip 3

	require.Equal(t, int64(3), b.Stats().TotalTrips)
}

func TestBreaker_ConcurrentAccess(t *testing.T) {
	t.Parallel()
	b := NewBreaker(10, time.Second)
	var wg sync.WaitGroup

	for range 100 {
		wg.Add(3)
		go func() { defer wg.Done(); b.Allow() }()
		go func() { defer wg.Done(); b.RecordSuccess() }()
		go func() { defer wg.Done(); b.RecordFailure() }()
	}
	wg.Wait()
	// No panic = success (race detector validates synchronization).
}
