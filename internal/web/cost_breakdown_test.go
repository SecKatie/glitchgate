// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"codeberg.org/kglitchy/llm-proxy/internal/pricing"
	"codeberg.org/kglitchy/llm-proxy/internal/store"
)

func makeCalc(model string, entry pricing.Entry) *pricing.Calculator {
	return pricing.NewCalculator(map[string]pricing.Entry{model: entry})
}

func makeLog(model string, input, output, cacheWrite, cacheRead int64) *store.RequestLogDetail {
	return &store.RequestLogDetail{
		RequestLogSummary: store.RequestLogSummary{
			ModelUpstream:            model,
			InputTokens:              input,
			OutputTokens:             output,
			CacheCreationInputTokens: cacheWrite,
			CacheReadInputTokens:     cacheRead,
			Timestamp:                time.Now(),
		},
	}
}

func TestComputeCostBreakdown(t *testing.T) {
	entry := pricing.Entry{
		InputPerMillion:      3.00,
		OutputPerMillion:     15.00,
		CacheWritePerMillion: 3.75,
		CacheReadPerMillion:  0.30,
	}

	t.Run("unknown model sets PricingKnown=false and nil costs", func(t *testing.T) {
		calc := makeCalc("known-model", entry)
		log := makeLog("unknown-model", 1000, 500, 0, 0)

		cb := computeCostBreakdown(log, calc)

		require.False(t, cb.PricingKnown)
		require.Nil(t, cb.InputCostUSD)
		require.Nil(t, cb.OutputCostUSD)
		require.Nil(t, cb.CacheWriteCostUSD)
		require.Nil(t, cb.CacheReadCostUSD)
		require.Nil(t, cb.TotalCostUSD)
		require.Equal(t, int64(1000), cb.InputTokens)
		require.Equal(t, int64(500), cb.OutputTokens)
	})

	t.Run("known model populates all cost fields", func(t *testing.T) {
		calc := makeCalc("claude-test", entry)
		// 1M input @$3/M = $3.00; 500K output @$15/M = $7.50; total = $10.50
		log := makeLog("claude-test", 1_000_000, 500_000, 0, 0)

		cb := computeCostBreakdown(log, calc)

		require.True(t, cb.PricingKnown)
		require.NotNil(t, cb.InputCostUSD)
		require.InDelta(t, 3.00, *cb.InputCostUSD, 1e-9)
		require.InDelta(t, 7.50, *cb.OutputCostUSD, 1e-9)
		require.InDelta(t, 0.0, *cb.CacheWriteCostUSD, 1e-9)
		require.InDelta(t, 0.0, *cb.CacheReadCostUSD, 1e-9)
		require.InDelta(t, 10.50, *cb.TotalCostUSD, 1e-9)
	})

	t.Run("cache write tokens contribute to cost", func(t *testing.T) {
		calc := makeCalc("claude-test", entry)
		// 1M cache write @$3.75/M = $3.75
		log := makeLog("claude-test", 0, 0, 1_000_000, 0)

		cb := computeCostBreakdown(log, calc)

		require.True(t, cb.PricingKnown)
		require.InDelta(t, 3.75, *cb.CacheWriteCostUSD, 1e-9)
		require.InDelta(t, 3.75, *cb.TotalCostUSD, 1e-9)
	})

	t.Run("cache read tokens contribute to cost", func(t *testing.T) {
		calc := makeCalc("claude-test", entry)
		// 1M cache read @$0.30/M = $0.30
		log := makeLog("claude-test", 0, 0, 0, 1_000_000)

		cb := computeCostBreakdown(log, calc)

		require.True(t, cb.PricingKnown)
		require.InDelta(t, 0.30, *cb.CacheReadCostUSD, 1e-9)
		require.InDelta(t, 0.30, *cb.TotalCostUSD, 1e-9)
	})

	t.Run("all token categories combined", func(t *testing.T) {
		calc := makeCalc("claude-test", entry)
		// 1M input $3.00 + 500K output $7.50 + 200K cache write $0.75 + 100K cache read $0.03 = $11.28
		log := makeLog("claude-test", 1_000_000, 500_000, 200_000, 100_000)

		cb := computeCostBreakdown(log, calc)

		require.True(t, cb.PricingKnown)
		require.InDelta(t, 11.28, *cb.TotalCostUSD, 1e-9)
		require.Equal(t, int64(1_000_000), cb.InputTokens)
		require.Equal(t, int64(500_000), cb.OutputTokens)
		require.Equal(t, int64(200_000), cb.CacheWriteTokens)
		require.Equal(t, int64(100_000), cb.CacheReadTokens)
	})

	t.Run("zero tokens produces zero cost", func(t *testing.T) {
		calc := makeCalc("claude-test", entry)
		log := makeLog("claude-test", 0, 0, 0, 0)

		cb := computeCostBreakdown(log, calc)

		require.True(t, cb.PricingKnown)
		require.InDelta(t, 0.0, *cb.TotalCostUSD, 1e-9)
	})
}
