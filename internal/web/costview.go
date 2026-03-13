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

	TotalInputTokens int64
	InputTokens      int64
	CacheWriteTokens int64
	CacheReadTokens  int64
	OutputTokens     int64
	ReasoningTokens  int64 // subset of OutputTokens, tracked for visibility

	TotalInputCostUSD *float64
	InputCostUSD      *float64 // nil when PricingKnown=false
	CacheWriteCostUSD *float64
	CacheReadCostUSD  *float64
	ReasoningCostUSD  *float64
	OutputCostUSD     *float64
	TotalCostUSD      *float64 // sum of above; matches stored EstimatedCostUSD

	InputRatePerMillion      float64 // per-million price; 0 when PricingKnown=false
	CacheWriteRatePerMillion float64
	CacheReadRatePerMillion  float64
	OutputRatePerMillion     float64
}

// AggregateCostBreakdown holds per-category costs aggregated across many logs.
// Passed to the cost dashboard token table as .TokenCosts.
type AggregateCostBreakdown struct {
	PricingKnown      bool
	PartialPricing    bool
	HasAnyPricing     bool
	TotalInputCostUSD float64
	InputCostUSD      float64
	CacheWriteCostUSD float64
	CacheReadCostUSD  float64
	OutputCostUSD     float64
}

// computeCostBreakdown computes per-category token costs from a log entry
// and a pricing calculator. If the model is not in the pricing table,
// PricingKnown is false and all cost fields are nil.
func computeCostBreakdown(log *store.RequestLogDetail, calc *pricing.Calculator) *CostBreakdown {
	cb := &CostBreakdown{
		TotalInputTokens: log.InputTokens + log.CacheCreationInputTokens + log.CacheReadInputTokens,
		InputTokens:      log.InputTokens,
		CacheWriteTokens: log.CacheCreationInputTokens,
		CacheReadTokens:  log.CacheReadInputTokens,
		OutputTokens:     log.OutputTokens,
		ReasoningTokens:  log.ReasoningTokens,
	}

	entry, ok := calc.Lookup(log.ProviderName, log.ModelUpstream)
	if !ok {
		return cb
	}

	cb.PricingKnown = true

	inputCost := float64(log.InputTokens) * entry.InputPerMillion / 1_000_000
	cacheWriteCost := float64(log.CacheCreationInputTokens) * entry.CacheWritePerMillion / 1_000_000
	cacheReadCost := float64(log.CacheReadInputTokens) * entry.CacheReadPerMillion / 1_000_000
	reasoningCost := float64(log.ReasoningTokens) * entry.OutputPerMillion / 1_000_000
	outputCost := float64(log.OutputTokens) * entry.OutputPerMillion / 1_000_000
	totalInputCost := inputCost + cacheWriteCost + cacheReadCost
	total := inputCost + cacheWriteCost + cacheReadCost + outputCost

	cb.TotalInputCostUSD = &totalInputCost
	cb.InputCostUSD = &inputCost
	cb.CacheWriteCostUSD = &cacheWriteCost
	cb.CacheReadCostUSD = &cacheReadCost
	cb.ReasoningCostUSD = &reasoningCost
	cb.OutputCostUSD = &outputCost
	cb.TotalCostUSD = &total

	cb.InputRatePerMillion = entry.InputPerMillion
	cb.CacheWriteRatePerMillion = entry.CacheWritePerMillion
	cb.CacheReadRatePerMillion = entry.CacheReadPerMillion
	cb.OutputRatePerMillion = entry.OutputPerMillion

	return cb
}

// computeAggregateCostBreakdown computes per-category costs across many logs by
// grouping token totals by exact provider/model pair before applying pricing.
// If any non-zero usage group lacks pricing, PricingKnown is false.
func computeAggregateCostBreakdown(groups []store.CostPricingGroup, calc *pricing.Calculator) *AggregateCostBreakdown {
	cb := &AggregateCostBreakdown{}
	if calc == nil {
		return cb
	}

	hasUnknownUsage := false
	for _, group := range groups {
		entry, ok := calc.Lookup(group.ProviderName, group.ModelUpstream)
		if !ok {
			if group.InputTokens > 0 || group.OutputTokens > 0 || group.CacheCreationTokens > 0 || group.CacheReadTokens > 0 {
				hasUnknownUsage = true
			}
			continue
		}

		cb.InputCostUSD += float64(group.InputTokens) * entry.InputPerMillion / 1_000_000
		cb.CacheWriteCostUSD += float64(group.CacheCreationTokens) * entry.CacheWritePerMillion / 1_000_000
		cb.CacheReadCostUSD += float64(group.CacheReadTokens) * entry.CacheReadPerMillion / 1_000_000
		cb.OutputCostUSD += float64(group.OutputTokens) * entry.OutputPerMillion / 1_000_000
		cb.HasAnyPricing = true
	}

	cb.TotalInputCostUSD = cb.InputCostUSD + cb.CacheWriteCostUSD + cb.CacheReadCostUSD
	cb.PricingKnown = cb.HasAnyPricing && !hasUnknownUsage
	cb.PartialPricing = cb.HasAnyPricing && hasUnknownUsage
	return cb
}
