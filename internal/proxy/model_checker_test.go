// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stubAllowlistStore struct {
	data map[string][]string
}

func (s *stubAllowlistStore) GetKeyAllowedModels(_ context.Context, keyID string) ([]string, error) {
	return s.data[keyID], nil
}

func TestModelChecker_EmptyAllowlist_Unrestricted(t *testing.T) {
	mc := NewModelChecker(&stubAllowlistStore{data: map[string][]string{}}, time.Minute)
	allowed, err := mc.IsModelAllowed(context.Background(), "key1", "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.True(t, allowed, "empty allowlist should be unrestricted")
}

func TestModelChecker_ExactMatch(t *testing.T) {
	store := &stubAllowlistStore{data: map[string][]string{
		"key1": {"claude-sonnet-4-20250514", "gpt-4o"},
	}}
	mc := NewModelChecker(store, time.Minute)

	allowed, err := mc.IsModelAllowed(context.Background(), "key1", "claude-sonnet-4-20250514")
	require.NoError(t, err)
	require.True(t, allowed)

	allowed, err = mc.IsModelAllowed(context.Background(), "key1", "gpt-4o")
	require.NoError(t, err)
	require.True(t, allowed)

	allowed, err = mc.IsModelAllowed(context.Background(), "key1", "claude-opus")
	require.NoError(t, err)
	require.False(t, allowed, "model not in allowlist should be denied")
}

func TestModelChecker_GlobPattern(t *testing.T) {
	store := &stubAllowlistStore{data: map[string][]string{
		"key1": {"claude-*", "gpt-4*"},
	}}
	mc := NewModelChecker(store, time.Minute)

	tests := []struct {
		model string
		want  bool
	}{
		{"claude-sonnet-4-20250514", true},
		{"claude-opus-4-20250514", true},
		{"claude-haiku-3.5", true},
		{"gpt-4o", true},
		{"gpt-4o-mini", true},
		{"gpt-3.5-turbo", false},
		{"gemini-2.0-flash", false},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			allowed, err := mc.IsModelAllowed(context.Background(), "key1", tt.model)
			require.NoError(t, err)
			require.Equal(t, tt.want, allowed)
		})
	}
}

func TestModelChecker_CacheTTL(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	store := &stubAllowlistStore{data: map[string][]string{
		"key1": {"model-a"},
	}}
	mc := NewModelChecker(store, 10*time.Second)
	mc.now = func() time.Time { return now }

	// First call populates cache.
	allowed, err := mc.IsModelAllowed(context.Background(), "key1", "model-a")
	require.NoError(t, err)
	require.True(t, allowed)

	// Change store data.
	store.data["key1"] = []string{"model-b"}

	// Still cached — should use old data.
	allowed, err = mc.IsModelAllowed(context.Background(), "key1", "model-a")
	require.NoError(t, err)
	require.True(t, allowed, "should use cached data")

	// Advance past TTL.
	now = now.Add(11 * time.Second)
	allowed, err = mc.IsModelAllowed(context.Background(), "key1", "model-a")
	require.NoError(t, err)
	require.False(t, allowed, "should refetch after TTL and deny model-a")
}

func TestModelChecker_InvalidateKey(t *testing.T) {
	store := &stubAllowlistStore{data: map[string][]string{
		"key1": {"model-a"},
	}}
	mc := NewModelChecker(store, time.Hour)

	// Populate cache.
	_, _ = mc.IsModelAllowed(context.Background(), "key1", "model-a")

	// Change and invalidate.
	store.data["key1"] = []string{"model-b"}
	mc.InvalidateKey("key1")

	// Should refetch immediately.
	allowed, err := mc.IsModelAllowed(context.Background(), "key1", "model-a")
	require.NoError(t, err)
	require.False(t, allowed)

	allowed, err = mc.IsModelAllowed(context.Background(), "key1", "model-b")
	require.NoError(t, err)
	require.True(t, allowed)
}
