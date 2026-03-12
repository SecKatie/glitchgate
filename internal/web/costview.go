// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

// CostBreakdown holds per-category token counts and costs for a single request.
// Passed to log_detail.html as .Cost.
type CostBreakdown struct {
	PricingKnown bool // false if model not in pricing table

	InputTokens      int64
	CacheWriteTokens int64
	CacheReadTokens  int64
	OutputTokens     int64

	InputCostUSD      *float64 // nil when PricingKnown=false
	CacheWriteCostUSD *float64
	CacheReadCostUSD  *float64
	OutputCostUSD     *float64
	TotalCostUSD      *float64 // sum of above; matches stored EstimatedCostUSD

	InputRatePerMillion      float64 // per-million price; 0 when PricingKnown=false
	CacheWriteRatePerMillion float64
	CacheReadRatePerMillion  float64
	OutputRatePerMillion     float64
}

// computeCostBreakdown computes per-category token costs from a log entry
// and a pricing calculator. If the model is not in the pricing table,
// PricingKnown is false and all cost fields are nil.
func computeCostBreakdown(log *store.RequestLogDetail, calc *pricing.Calculator) *CostBreakdown {
	cb := &CostBreakdown{
		InputTokens:      log.InputTokens,
		CacheWriteTokens: log.CacheCreationInputTokens,
		CacheReadTokens:  log.CacheReadInputTokens,
		OutputTokens:     log.OutputTokens,
	}

	entry, ok := calc.Lookup(log.ProviderName, log.ModelUpstream)
	if !ok {
		return cb
	}

	cb.PricingKnown = true

	inputCost := float64(log.InputTokens) * entry.InputPerMillion / 1_000_000
	cacheWriteCost := float64(log.CacheCreationInputTokens) * entry.CacheWritePerMillion / 1_000_000
	cacheReadCost := float64(log.CacheReadInputTokens) * entry.CacheReadPerMillion / 1_000_000
	outputCost := float64(log.OutputTokens) * entry.OutputPerMillion / 1_000_000
	total := inputCost + cacheWriteCost + cacheReadCost + outputCost

	cb.InputCostUSD = &inputCost
	cb.CacheWriteCostUSD = &cacheWriteCost
	cb.CacheReadCostUSD = &cacheReadCost
	cb.OutputCostUSD = &outputCost
	cb.TotalCostUSD = &total

	cb.InputRatePerMillion = entry.InputPerMillion
	cb.CacheWriteRatePerMillion = entry.CacheWritePerMillion
	cb.CacheReadRatePerMillion = entry.CacheReadPerMillion
	cb.OutputRatePerMillion = entry.OutputPerMillion

	return cb
}
