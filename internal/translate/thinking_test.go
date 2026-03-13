// SPDX-License-Identifier: AGPL-3.0-or-later

package translate

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEffortToBudgetTokens(t *testing.T) {
	tests := []struct {
		name      string
		effort    string
		maxTokens int
		want      int
	}{
		{"low", "low", 16384, 1024},
		{"medium", "medium", 16384, 5000},
		{"high", "high", 16384, 10000},
		{"unknown defaults to medium", "unknown", 16384, 5000},
		{"empty defaults to medium", "", 16384, 5000},
		{"high capped by maxTokens", "high", 5000, 4999},
		{"medium capped by maxTokens", "medium", 3000, 2999},
		{"low capped by maxTokens", "low", 500, 499},
		{"maxTokens=1 floors to 1", "low", 1, 1},
		{"maxTokens=0 no capping", "high", 0, 10000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := effortToBudgetTokens(tt.effort, tt.maxTokens)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestBudgetTokensToEffort(t *testing.T) {
	tests := []struct {
		name   string
		budget int
		want   string
	}{
		{"very low budget", 100, "low"},
		{"low boundary", 2000, "low"},
		{"medium lower", 2001, "medium"},
		{"medium boundary", 7000, "medium"},
		{"high lower", 7001, "high"},
		{"high budget", 10000, "high"},
		{"very high budget", 50000, "high"},
		{"zero", 0, "low"},
		{"negative", -1, "low"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := budgetTokensToEffort(tt.budget)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestEffortBudgetRoundTrip(t *testing.T) {
	// Verify that effort -> budget -> effort round-trips for standard values.
	for _, effort := range []string{"low", "medium", "high"} {
		budget := effortToBudgetTokens(effort, 16384)
		got := budgetTokensToEffort(budget)
		require.Equal(t, effort, got, "round trip failed for effort=%q budget=%d", effort, budget)
	}
}
