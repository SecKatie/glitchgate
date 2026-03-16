// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/store"
)

func TestBuildSubsidyAnalysis(t *testing.T) {
	calc := pricing.NewCalculator(map[string]pricing.Entry{
		"claude-max/claude-sonnet-4-6": {
			InputPerMillion:      3,
			CacheWritePerMillion: 3.75,
			CacheReadPerMillion:  0.30,
			OutputPerMillion:     15,
		},
		"chatgpt-pro/gpt-5": {
			InputPerMillion:  2.5,
			OutputPerMillion: 10,
		},
	})

	t.Run("returns nil when no subscriptions", func(t *testing.T) {
		groups := []store.CostPricingGroup{
			{ProviderName: "claude-max", ModelUpstream: "claude-sonnet-4-6", InputTokens: 1000, OutputTokens: 500, Requests: 1},
		}
		result := buildSubsidyAnalysis(groups, nil, calc, nil, nil, 30)
		require.Nil(t, result)
	})

	t.Run("returns nil when no calculator", func(t *testing.T) {
		groups := []store.CostPricingGroup{
			{ProviderName: "claude-max", ModelUpstream: "claude-sonnet-4-6", InputTokens: 1000, OutputTokens: 500, Requests: 1},
		}
		subs := map[string]float64{"claude-max": 100}
		result := buildSubsidyAnalysis(groups, nil, nil, nil, subs, 30)
		require.Nil(t, result)
	})

	t.Run("returns nil when no matching provider usage", func(t *testing.T) {
		groups := []store.CostPricingGroup{
			{ProviderName: "other-provider", ModelUpstream: "some-model", InputTokens: 1000, OutputTokens: 500, Requests: 1},
		}
		subs := map[string]float64{"claude-max": 100}
		result := buildSubsidyAnalysis(groups, nil, calc, nil, subs, 30)
		require.Nil(t, result)
	})

	t.Run("single provider hero metric", func(t *testing.T) {
		groups := []store.CostPricingGroup{
			{
				ProviderName: "claude-max", ModelUpstream: "claude-sonnet-4-6",
				InputTokens: 1_000_000, OutputTokens: 500_000,
				CacheCreationTokens: 100_000, CacheReadTokens: 200_000,
				Requests: 10,
			},
		}
		subs := map[string]float64{"claude-max": 100}

		result := buildSubsidyAnalysis(groups, nil, calc, nil, subs, 30)
		require.NotNil(t, result)

		// API costs: input=3.00, cache_write=0.375, cache_read=0.06, output=7.50
		// Total API cost = 10.935
		expectedAPICost := 3.0 + 0.375 + 0.06 + 7.5
		require.InDelta(t, expectedAPICost, result.TrueCostUSD, 1e-6)
		require.Equal(t, 100.0, result.SubscriptionCostUSD)
		require.InDelta(t, expectedAPICost-100.0, result.ProviderSubsidyUSD, 1e-6)
		require.NotNil(t, result.SubsidyPct)
	})

	t.Run("per-category rate allocation", func(t *testing.T) {
		groups := []store.CostPricingGroup{
			{
				ProviderName: "claude-max", ModelUpstream: "claude-sonnet-4-6",
				InputTokens: 1_000_000, OutputTokens: 500_000,
				Requests: 5,
			},
		}
		subs := map[string]float64{"claude-max": 100}

		result := buildSubsidyAnalysis(groups, nil, calc, nil, subs, 30)
		require.NotNil(t, result)

		// API costs: input=$3.00, output=$7.50, total=$10.50
		// Subscription allocation: input=100*(3/10.5)=28.571, output=100*(7.5/10.5)=71.429
		require.Len(t, result.Categories, 2) // only non-zero categories
		require.Equal(t, "Input", result.Categories[0].Category)
		require.Equal(t, "Output", result.Categories[1].Category)

		// Input: API rate = 3.00 $/MTok, Effective = 28.571/1M * 1M = 28.571 $/MTok
		require.InDelta(t, 3.0, result.Categories[0].APIRatePerMTok, 1e-3)
		require.InDelta(t, 28.571, result.Categories[0].EffRatePerMTok, 0.01)
		require.NotNil(t, result.Categories[0].SavingsPct)

		// Output: API rate = 15.00 $/MTok
		require.InDelta(t, 15.0, result.Categories[1].APIRatePerMTok, 1e-3)
	})

	t.Run("multiple providers aggregated", func(t *testing.T) {
		groups := []store.CostPricingGroup{
			{ProviderName: "claude-max", ModelUpstream: "claude-sonnet-4-6", InputTokens: 500_000, OutputTokens: 200_000, Requests: 3},
			{ProviderName: "chatgpt-pro", ModelUpstream: "gpt-5", InputTokens: 300_000, OutputTokens: 100_000, Requests: 2},
		}
		subs := map[string]float64{"claude-max": 100, "chatgpt-pro": 20}

		result := buildSubsidyAnalysis(groups, nil, calc, nil, subs, 30)
		require.NotNil(t, result)

		// claude-max API: input=1.50, output=3.00 -> 4.50
		// chatgpt-pro API: input=0.75, output=1.00 -> 1.75
		// Total API: 6.25, Total sub: 120
		require.InDelta(t, 6.25, result.TrueCostUSD, 1e-6)
		require.Equal(t, 120.0, result.SubscriptionCostUSD)
		require.InDelta(t, 6.25-120.0, result.ProviderSubsidyUSD, 1e-6)
	})

	t.Run("cumulative timeseries", func(t *testing.T) {
		groups := []store.CostPricingGroup{
			{ProviderName: "claude-max", ModelUpstream: "claude-sonnet-4-6", InputTokens: 1_000_000, OutputTokens: 500_000, Requests: 5},
		}
		tsGroups := []store.CostTimeseriesPricingGroup{
			{Date: "2026-03-11", ProviderName: "claude-max", ModelUpstream: "claude-sonnet-4-6", InputTokens: 300_000, OutputTokens: 100_000, Requests: 2},
			{Date: "2026-03-12", ProviderName: "claude-max", ModelUpstream: "claude-sonnet-4-6", InputTokens: 400_000, OutputTokens: 200_000, Requests: 2},
			{Date: "2026-03-13", ProviderName: "claude-max", ModelUpstream: "claude-sonnet-4-6", InputTokens: 300_000, OutputTokens: 200_000, Requests: 1},
		}
		subs := map[string]float64{"claude-max": 100}

		result := buildSubsidyAnalysis(groups, tsGroups, calc, nil, subs, 3)
		require.NotNil(t, result)
		require.Len(t, result.CumulativeData, 3)

		// Daily sub allocation = 100/3 = 33.33
		dailySub := 100.0 / 3.0
		require.InDelta(t, dailySub, result.CumulativeData[0].DailySubAllocation, 1e-2)

		// Day 1 API: input=0.90, output=1.50 -> 2.40
		require.InDelta(t, 2.40, result.CumulativeData[0].DailyAPICost, 1e-2)
		require.InDelta(t, 2.40, result.CumulativeData[0].CumulativeAPICost, 1e-2)
		require.InDelta(t, 2.40-dailySub, result.CumulativeData[0].CumulativeSubsidy, 1e-2)

		// Cumulative should grow each day
		require.Greater(t, result.CumulativeData[1].CumulativeAPICost, result.CumulativeData[0].CumulativeAPICost)
		require.Greater(t, result.CumulativeData[2].CumulativeAPICost, result.CumulativeData[1].CumulativeAPICost)
	})

	t.Run("provider name mapping via aliases", func(t *testing.T) {
		groups := []store.CostPricingGroup{
			{ProviderName: "anthropic:api.anthropic.com", ModelUpstream: "claude-sonnet-4-6", InputTokens: 1_000_000, OutputTokens: 500_000, Requests: 5},
		}
		providerNames := map[string]string{
			"anthropic:api.anthropic.com": "claude-max",
			"claude-max":                  "claude-max",
		}
		subs := map[string]float64{"claude-max": 100}

		result := buildSubsidyAnalysis(groups, nil, calc, providerNames, subs, 30)
		require.NotNil(t, result)
		require.InDelta(t, 10.50, result.TrueCostUSD, 1e-2)
	})
}

func TestDaysInRange(t *testing.T) {
	tests := []struct {
		name     string
		from, to string
		expected int
	}{
		{"same day", "2026-03-14", "2026-03-14", 1},
		{"two days", "2026-03-13", "2026-03-14", 2},
		{"month range", "2026-03-01", "2026-03-31", 31},
		{"invalid from", "bad", "2026-03-14", 30},
		{"invalid to", "2026-03-14", "bad", 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, daysInRange(tt.from, tt.to))
		})
	}
}
