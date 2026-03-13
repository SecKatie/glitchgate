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
	TotalCostUSD      float64
}

// LogRowWithCost wraps a RequestLogSummary with a computed estimated cost.
// EstimatedCostUSD is nil when pricing is not configured for the model.
type LogRowWithCost struct {
	store.RequestLogSummary
	EstimatedCostUSD *float64
}

// enrichLogs computes estimated costs for a slice of log summaries.
func enrichLogs(logs []store.RequestLogSummary, calc *pricing.Calculator) []LogRowWithCost {
	rows := make([]LogRowWithCost, len(logs))
	for i, log := range logs {
		row := LogRowWithCost{RequestLogSummary: log}
		if calc != nil {
			if entry, ok := calc.Lookup(log.ProviderName, log.ModelUpstream); ok {
				cost := float64(log.InputTokens)*entry.InputPerMillion/1_000_000 +
					float64(log.CacheCreationInputTokens)*entry.CacheWritePerMillion/1_000_000 +
					float64(log.CacheReadInputTokens)*entry.CacheReadPerMillion/1_000_000 +
					float64(log.OutputTokens)*entry.OutputPerMillion/1_000_000
				row.EstimatedCostUSD = &cost
			}
		}
		rows[i] = row
	}
	return rows
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

// buildBreakdownCosts returns a map from breakdown group key to estimated cost USD.
// The key dimension depends on groupBy: "model" uses model_requested, "provider" uses the
// display provider name (via providerNames), "key" uses proxy_key_prefix.
// Models/providers/keys without pricing data are omitted.
func buildBreakdownCosts(groups []store.CostPricingGroup, calc *pricing.Calculator, groupBy string, providerNames map[string]string) map[string]float64 {
	if calc == nil || len(groups) == 0 {
		return nil
	}
	costs := make(map[string]float64)
	for _, group := range groups {
		entry, ok := calc.Lookup(group.ProviderName, group.ModelUpstream)
		if !ok {
			continue
		}
		cost := float64(group.InputTokens)*entry.InputPerMillion/1_000_000 +
			float64(group.CacheCreationTokens)*entry.CacheWritePerMillion/1_000_000 +
			float64(group.CacheReadTokens)*entry.CacheReadPerMillion/1_000_000 +
			float64(group.OutputTokens)*entry.OutputPerMillion/1_000_000

		var key string
		switch groupBy {
		case "provider":
			key = group.ProviderName
			if providerNames != nil {
				if name, ok := providerNames[group.ProviderName]; ok && name != "" {
					key = name
				}
			}
		case "key":
			key = group.ProxyKeyPrefix
		default: // "model"
			key = group.ModelRequested
		}
		if key == "" {
			continue
		}
		costs[key] += cost
	}
	if len(costs) == 0 {
		return nil
	}
	return costs
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
	cb.TotalCostUSD = cb.TotalInputCostUSD + cb.OutputCostUSD
	cb.PricingKnown = cb.HasAnyPricing && !hasUnknownUsage
	cb.PartialPricing = cb.HasAnyPricing && hasUnknownUsage
	return cb
}
