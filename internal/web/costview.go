// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

type tokenUsage struct {
	InputTokens      int64
	CacheWriteTokens int64
	CacheReadTokens  int64
	OutputTokens     int64
	ReasoningTokens  int64
}

type pricedUsage struct {
	InputCostUSD      float64
	CacheWriteCostUSD float64
	CacheReadCostUSD  float64
	ReasoningCostUSD  float64
	OutputCostUSD     float64
	TotalInputCostUSD float64
	TotalCostUSD      float64
}

func priceUsage(entry pricing.Entry, usage tokenUsage) pricedUsage {
	priced := pricedUsage{
		InputCostUSD:      float64(usage.InputTokens) * entry.InputPerMillion / 1_000_000,
		CacheWriteCostUSD: float64(usage.CacheWriteTokens) * entry.CacheWritePerMillion / 1_000_000,
		CacheReadCostUSD:  float64(usage.CacheReadTokens) * entry.CacheReadPerMillion / 1_000_000,
		ReasoningCostUSD:  float64(usage.ReasoningTokens) * entry.OutputPerMillion / 1_000_000,
		OutputCostUSD:     float64(usage.OutputTokens) * entry.OutputPerMillion / 1_000_000,
	}
	priced.TotalInputCostUSD = priced.InputCostUSD + priced.CacheWriteCostUSD + priced.CacheReadCostUSD
	priced.TotalCostUSD = priced.TotalInputCostUSD + priced.OutputCostUSD
	return priced
}

func lookupPricedUsage(calc *pricing.Calculator, providerName, modelUpstream string, usage tokenUsage) (pricedUsage, pricing.Entry, bool) {
	if calc == nil {
		return pricedUsage{}, pricing.Entry{}, false
	}
	entry, ok := calc.Lookup(providerName, modelUpstream)
	if !ok {
		return pricedUsage{}, pricing.Entry{}, false
	}
	return priceUsage(entry, usage), entry, true
}

func lookupPricedUsageWithAliases(calc *pricing.Calculator, providerName, modelUpstream string, usage tokenUsage, providerNames map[string]string) (pricedUsage, pricing.Entry, bool) {
	priced, entry, ok := lookupPricedUsage(calc, providerName, modelUpstream, usage)
	if ok {
		return priced, entry, true
	}
	if providerNames == nil {
		return pricedUsage{}, pricing.Entry{}, false
	}
	displayName, mapped := providerDisplayName(providerName, providerNames)
	if !mapped || displayName == providerName {
		return pricedUsage{}, pricing.Entry{}, false
	}
	return lookupPricedUsage(calc, displayName, modelUpstream, usage)
}

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
		priced, _, ok := lookupPricedUsage(calc, log.ProviderName, log.ModelUpstream, tokenUsage{
			InputTokens:      log.InputTokens,
			CacheWriteTokens: log.CacheCreationInputTokens,
			CacheReadTokens:  log.CacheReadInputTokens,
			OutputTokens:     log.OutputTokens,
		})
		if ok {
			cost := priced.TotalCostUSD
			row.EstimatedCostUSD = &cost
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

	priced, entry, ok := lookupPricedUsage(calc, log.ProviderName, log.ModelUpstream, tokenUsage{
		InputTokens:      log.InputTokens,
		CacheWriteTokens: log.CacheCreationInputTokens,
		CacheReadTokens:  log.CacheReadInputTokens,
		OutputTokens:     log.OutputTokens,
		ReasoningTokens:  log.ReasoningTokens,
	})
	if !ok {
		return cb
	}

	cb.PricingKnown = true

	cb.TotalInputCostUSD = &priced.TotalInputCostUSD
	cb.InputCostUSD = &priced.InputCostUSD
	cb.CacheWriteCostUSD = &priced.CacheWriteCostUSD
	cb.CacheReadCostUSD = &priced.CacheReadCostUSD
	cb.ReasoningCostUSD = &priced.ReasoningCostUSD
	cb.OutputCostUSD = &priced.OutputCostUSD
	cb.TotalCostUSD = &priced.TotalCostUSD

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
		priced, _, ok := lookupPricedUsageWithAliases(calc, group.ProviderName, group.ModelUpstream, tokenUsage{
			InputTokens:      group.InputTokens,
			CacheWriteTokens: group.CacheCreationTokens,
			CacheReadTokens:  group.CacheReadTokens,
			OutputTokens:     group.OutputTokens,
		}, providerNames)
		if !ok {
			continue
		}

		var key string
		switch groupBy {
		case "provider":
			key = group.ProviderName
			if providerNames != nil {
				if name, ok := providerDisplayName(group.ProviderName, providerNames); ok {
					key = name
				}
			}
		case "key":
			key = group.ProxyKeyGroup
			if key == "" {
				key = group.ProxyKeyPrefix
			}
		default: // "model"
			key = group.ModelRequested
		}
		if key == "" {
			continue
		}
		costs[key] += priced.TotalCostUSD
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
	return computeAggregateCostBreakdownWithAliases(groups, calc, nil)
}

func computeAggregateCostBreakdownWithAliases(groups []store.CostPricingGroup, calc *pricing.Calculator, providerNames map[string]string) *AggregateCostBreakdown {
	cb := &AggregateCostBreakdown{}
	if calc == nil {
		return cb
	}

	hasUnknownUsage := false
	for _, group := range groups {
		priced, _, ok := lookupPricedUsageWithAliases(calc, group.ProviderName, group.ModelUpstream, tokenUsage{
			InputTokens:      group.InputTokens,
			CacheWriteTokens: group.CacheCreationTokens,
			CacheReadTokens:  group.CacheReadTokens,
			OutputTokens:     group.OutputTokens,
		}, providerNames)
		if !ok {
			if group.InputTokens > 0 || group.OutputTokens > 0 || group.CacheCreationTokens > 0 || group.CacheReadTokens > 0 {
				hasUnknownUsage = true
			}
			continue
		}

		cb.InputCostUSD += priced.InputCostUSD
		cb.CacheWriteCostUSD += priced.CacheWriteCostUSD
		cb.CacheReadCostUSD += priced.CacheReadCostUSD
		cb.OutputCostUSD += priced.OutputCostUSD
		cb.HasAnyPricing = true
	}

	cb.TotalInputCostUSD = cb.InputCostUSD + cb.CacheWriteCostUSD + cb.CacheReadCostUSD
	cb.TotalCostUSD = cb.TotalInputCostUSD + cb.OutputCostUSD
	cb.PricingKnown = cb.HasAnyPricing && !hasUnknownUsage
	cb.PartialPricing = cb.HasAnyPricing && hasUnknownUsage
	return cb
}
