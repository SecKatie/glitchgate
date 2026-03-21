// SPDX-License-Identifier: AGPL-3.0-or-later

// Package billing computes cost breakdowns, projections, and subsidy analysis
// from raw token-usage data and pricing tables.
package billing

import (
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/store"
)

// TokenUsage holds per-category token counts for a single request or group.
type TokenUsage struct {
	InputTokens      int64
	CacheWriteTokens int64
	CacheReadTokens  int64
	OutputTokens     int64
	ReasoningTokens  int64
}

// PricedUsage holds per-category costs computed from token counts and pricing.
type PricedUsage struct {
	InputCostUSD      float64
	CacheWriteCostUSD float64
	CacheReadCostUSD  float64
	ReasoningCostUSD  float64
	OutputCostUSD     float64
	TotalInputCostUSD float64
	TotalCostUSD      float64
}

// PriceUsage computes per-category costs from a pricing entry and token counts.
func PriceUsage(entry pricing.Entry, usage TokenUsage) PricedUsage {
	priced := PricedUsage{
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

// LookupPricedUsage looks up pricing for a provider/model pair and computes costs.
func LookupPricedUsage(calc *pricing.Calculator, providerName, modelUpstream string, usage TokenUsage) (PricedUsage, pricing.Entry, bool) {
	if calc == nil {
		return PricedUsage{}, pricing.Entry{}, false
	}
	entry, ok := calc.Lookup(providerName, modelUpstream)
	if !ok {
		return PricedUsage{}, pricing.Entry{}, false
	}
	return PriceUsage(entry, usage), entry, true
}

// LookupPricedUsageWithAliases tries a direct lookup first, then falls back to
// the display name from providerNames.
func LookupPricedUsageWithAliases(calc *pricing.Calculator, providerName, modelUpstream string, usage TokenUsage, providerNames map[string]string) (PricedUsage, pricing.Entry, bool) {
	priced, entry, ok := LookupPricedUsage(calc, providerName, modelUpstream, usage)
	if ok {
		return priced, entry, true
	}
	if providerNames == nil {
		return PricedUsage{}, pricing.Entry{}, false
	}
	displayName, mapped := ProviderDisplayName(providerName, providerNames)
	if !mapped || displayName == providerName {
		return PricedUsage{}, pricing.Entry{}, false
	}
	return LookupPricedUsage(calc, displayName, modelUpstream, usage)
}

// ProviderDisplayName maps a raw provider key to its display name via the
// providerNames map. Returns the original key and false if no mapping exists.
func ProviderDisplayName(rawKey string, providerNames map[string]string) (string, bool) {
	if providerNames == nil {
		return rawKey, false
	}
	if mapped, ok := providerNames[rawKey]; ok && mapped != "" {
		return mapped, true
	}
	return rawKey, false
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

// ProviderSpendComparison compares provider token spend for the selected
// filter window against an operator-configured monthly subscription cost.
type ProviderSpendComparison struct {
	TokenCostUSD                  float64
	MonthlySubscriptionCost       float64
	TokenMinusSubscriptionUSD     float64
	TokenVsSubscriptionPct        *float64
	TotalTokens                   int64
	EffectiveTokenCostPerMTok     *float64
	AverageRealTokenCostPerMTok   *float64
	EffectiveMinusRealCostPerMTok *float64
	EffectiveVsRealPct            *float64
}

// ProviderSpendComparisonSummary aggregates provider subscription comparisons
// across the currently visible provider rows.
type ProviderSpendComparisonSummary struct {
	HasAnySubscription            bool
	ComparedProviders             int
	TotalTokenCostUSD             float64
	TotalSubscriptionCost         float64
	TokenMinusSubscription        float64
	TokenVsSubscriptionPct        *float64
	TotalTokens                   int64
	EffectiveTokenCostPerMTok     *float64
	AverageRealTokenCostPerMTok   *float64
	EffectiveMinusRealCostPerMTok *float64
	EffectiveVsRealPct            *float64
}

// PricePerMillionTokens computes the cost per million tokens, returning nil
// when totalTokens is zero or negative.
func PricePerMillionTokens(amountUSD float64, totalTokens int64) *float64 {
	if totalTokens <= 0 {
		return nil
	}
	value := amountUSD * 1_000_000 / float64(totalTokens)
	return &value
}

// PercentDelta computes (delta / baseline) * 100, returning nil when
// baseline is zero.
func PercentDelta(delta, baseline float64) *float64 {
	if baseline == 0 {
		return nil
	}
	value := (delta / baseline) * 100
	return &value
}

// LogRowWithCost wraps a RequestLogSummary with a computed estimated cost.
// EstimatedCostUSD is nil when pricing is not configured for the model.
type LogRowWithCost struct {
	store.RequestLogSummary
	EstimatedCostUSD *float64
}

// EnrichLogs computes estimated costs for a slice of log summaries.
func EnrichLogs(logs []store.RequestLogSummary, calc *pricing.Calculator) []LogRowWithCost {
	rows := make([]LogRowWithCost, len(logs))
	for i, log := range logs {
		row := LogRowWithCost{RequestLogSummary: log}
		priced, _, ok := LookupPricedUsage(calc, log.ProviderName, log.ModelUpstream, TokenUsage{
			InputTokens:      log.InputTokens,
			CacheWriteTokens: log.CacheCreationInputTokens,
			CacheReadTokens:  log.CacheReadInputTokens,
			OutputTokens:     log.OutputTokens,
			ReasoningTokens:  log.ReasoningTokens,
		})
		if ok {
			cost := priced.TotalCostUSD
			row.EstimatedCostUSD = &cost
		}
		rows[i] = row
	}
	return rows
}

// ComputeCostBreakdown computes per-category token costs from a log entry
// and a pricing calculator. If the model is not in the pricing table,
// PricingKnown is false and all cost fields are nil.
func ComputeCostBreakdown(log *store.RequestLogDetail, calc *pricing.Calculator) *CostBreakdown {
	cb := &CostBreakdown{
		TotalInputTokens: log.InputTokens + log.CacheCreationInputTokens + log.CacheReadInputTokens,
		InputTokens:      log.InputTokens,
		CacheWriteTokens: log.CacheCreationInputTokens,
		CacheReadTokens:  log.CacheReadInputTokens,
		OutputTokens:     log.OutputTokens,
		ReasoningTokens:  log.ReasoningTokens,
	}

	priced, entry, ok := LookupPricedUsage(calc, log.ProviderName, log.ModelUpstream, TokenUsage{
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

// BuildBreakdownCosts returns a map from breakdown group key to estimated cost USD.
// The key dimension depends on groupBy: "model" uses model_requested, "provider" uses the
// display provider name (via providerNames), "key" uses proxy_key_prefix.
// Models/providers/keys without pricing data are omitted.
func BuildBreakdownCosts(groups []store.CostPricingGroup, calc *pricing.Calculator, groupBy string, providerNames map[string]string) map[string]float64 {
	if calc == nil || len(groups) == 0 {
		return nil
	}
	costs := make(map[string]float64)
	for _, group := range groups {
		priced, _, ok := LookupPricedUsageWithAliases(calc, group.ProviderName, group.ModelUpstream, TokenUsage{
			InputTokens:      group.InputTokens,
			CacheWriteTokens: group.CacheCreationTokens,
			CacheReadTokens:  group.CacheReadTokens,
			OutputTokens:     group.OutputTokens,
			ReasoningTokens:  group.ReasoningTokens,
		}, providerNames)
		if !ok {
			continue
		}

		var key string
		switch groupBy {
		case "provider":
			key = group.ProviderName
			if providerNames != nil {
				if name, ok := ProviderDisplayName(group.ProviderName, providerNames); ok {
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

// ComputeAggregateCostBreakdown computes per-category costs across many logs by
// grouping token totals by exact provider/model pair before applying pricing.
// If any non-zero usage group lacks pricing, PricingKnown is false.
func ComputeAggregateCostBreakdown(groups []store.CostPricingGroup, calc *pricing.Calculator) *AggregateCostBreakdown {
	return ComputeAggregateCostBreakdownWithAliases(groups, calc, nil)
}

// ComputeAggregateCostBreakdownWithAliases is like ComputeAggregateCostBreakdown
// but uses providerNames for alias-based pricing lookup.
func ComputeAggregateCostBreakdownWithAliases(groups []store.CostPricingGroup, calc *pricing.Calculator, providerNames map[string]string) *AggregateCostBreakdown {
	cb := &AggregateCostBreakdown{}
	if calc == nil {
		return cb
	}

	hasUnknownUsage := false
	for _, group := range groups {
		priced, _, ok := LookupPricedUsageWithAliases(calc, group.ProviderName, group.ModelUpstream, TokenUsage{
			InputTokens:      group.InputTokens,
			CacheWriteTokens: group.CacheCreationTokens,
			CacheReadTokens:  group.CacheReadTokens,
			OutputTokens:     group.OutputTokens,
			ReasoningTokens:  group.ReasoningTokens,
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

// DeriveSummaryFromPricingGroups computes CostSummary by summing token counts
// and request counts across all pricing groups. This avoids a separate SQL query.
func DeriveSummaryFromPricingGroups(groups []store.CostPricingGroup) *store.CostSummary {
	var s store.CostSummary
	for _, g := range groups {
		s.TotalInputTokens += g.InputTokens
		s.TotalOutputTokens += g.OutputTokens
		s.TotalCacheCreationTokens += g.CacheCreationTokens
		s.TotalCacheReadTokens += g.CacheReadTokens
		s.TotalRequests += g.Requests
	}
	return &s
}

// DeriveBreakdownFromPricingGroups aggregates pricing groups into breakdown
// entries by the given dimension (model, provider, or key). This avoids a
// separate SQL query since pricing groups are strictly finer-grained.
func DeriveBreakdownFromPricingGroups(groups []store.CostPricingGroup, groupBy string, providerNames map[string]string) []store.CostBreakdownEntry {
	type accumulator struct {
		entry store.CostBreakdownEntry
		order int // preserve first-seen order for stable sorting
	}
	byGroup := make(map[string]*accumulator)
	nextOrder := 0

	for _, g := range groups {
		var key string
		switch groupBy {
		case "key":
			key = g.ProxyKeyGroup
			if key == "" {
				key = g.ProxyKeyPrefix
			}
		case "provider":
			key, _ = ProviderDisplayName(g.ProviderName, providerNames)
			if key == "" {
				key = g.ProviderName
			}
		default: // "model"
			key = g.ModelRequested
		}
		if key == "" {
			continue
		}

		acc, ok := byGroup[key]
		if !ok {
			acc = &accumulator{
				entry: store.CostBreakdownEntry{Group: key},
				order: nextOrder,
			}
			nextOrder++
			byGroup[key] = acc
		}
		acc.entry.InputTokens += g.InputTokens
		acc.entry.OutputTokens += g.OutputTokens
		acc.entry.CacheCreationTokens += g.CacheCreationTokens
		acc.entry.CacheReadTokens += g.CacheReadTokens
		acc.entry.Requests += g.Requests
	}

	entries := make([]store.CostBreakdownEntry, 0, len(byGroup))
	for _, acc := range byGroup {
		entries = append(entries, acc.entry)
	}

	// Sort by request count descending, then group name ascending (matches SQL ORDER BY).
	slices.SortFunc(entries, func(a, b store.CostBreakdownEntry) int {
		if a.Requests != b.Requests {
			if a.Requests > b.Requests {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Group, b.Group)
	})

	if entries == nil {
		entries = []store.CostBreakdownEntry{}
	}
	return entries
}

// BuildProviderSpendComparisons computes per-provider spend comparisons against
// configured monthly subscriptions.
func BuildProviderSpendComparisons(breakdown []store.CostBreakdownEntry, breakdownCosts map[string]float64, subscriptions map[string]float64) (map[string]*ProviderSpendComparison, ProviderSpendComparisonSummary) {
	if len(breakdown) == 0 || len(subscriptions) == 0 {
		return nil, ProviderSpendComparisonSummary{}
	}

	comparisons := make(map[string]*ProviderSpendComparison, len(breakdown))
	var summary ProviderSpendComparisonSummary

	for _, entry := range breakdown {
		subscription, ok := subscriptions[entry.Group]
		if !ok {
			continue
		}

		tokenCost, ok := breakdownCosts[entry.Group]
		if !ok {
			continue
		}
		totalTokens := entry.InputTokens + entry.CacheCreationTokens + entry.CacheReadTokens + entry.OutputTokens
		comparison := &ProviderSpendComparison{
			TokenCostUSD:              tokenCost,
			MonthlySubscriptionCost:   subscription,
			TokenMinusSubscriptionUSD: tokenCost - subscription,
			TotalTokens:               totalTokens,
		}
		comparison.TokenVsSubscriptionPct = PercentDelta(comparison.TokenMinusSubscriptionUSD, subscription)
		comparison.EffectiveTokenCostPerMTok = PricePerMillionTokens(subscription, totalTokens)
		comparison.AverageRealTokenCostPerMTok = PricePerMillionTokens(tokenCost, totalTokens)
		if comparison.EffectiveTokenCostPerMTok != nil && comparison.AverageRealTokenCostPerMTok != nil {
			delta := *comparison.EffectiveTokenCostPerMTok - *comparison.AverageRealTokenCostPerMTok
			comparison.EffectiveMinusRealCostPerMTok = &delta
			comparison.EffectiveVsRealPct = PercentDelta(delta, *comparison.AverageRealTokenCostPerMTok)
		}
		comparisons[entry.Group] = comparison

		summary.HasAnySubscription = true
		summary.ComparedProviders++
		summary.TotalTokenCostUSD += tokenCost
		summary.TotalSubscriptionCost += subscription
		summary.TokenMinusSubscription += comparison.TokenMinusSubscriptionUSD
		summary.TotalTokens += totalTokens
	}

	if len(comparisons) == 0 {
		return nil, ProviderSpendComparisonSummary{}
	}
	summary.EffectiveTokenCostPerMTok = PricePerMillionTokens(summary.TotalSubscriptionCost, summary.TotalTokens)
	summary.AverageRealTokenCostPerMTok = PricePerMillionTokens(summary.TotalTokenCostUSD, summary.TotalTokens)
	summary.TokenVsSubscriptionPct = PercentDelta(summary.TokenMinusSubscription, summary.TotalSubscriptionCost)
	if summary.EffectiveTokenCostPerMTok != nil && summary.AverageRealTokenCostPerMTok != nil {
		delta := *summary.EffectiveTokenCostPerMTok - *summary.AverageRealTokenCostPerMTok
		summary.EffectiveMinusRealCostPerMTok = &delta
		summary.EffectiveVsRealPct = PercentDelta(delta, *summary.AverageRealTokenCostPerMTok)
	}

	return comparisons, summary
}

// --------------------------------------------------------------------------
// Monthly Spend Projection
// --------------------------------------------------------------------------

// MonthlyProjection holds the current month-to-date spend and a projected
// full-month figure extrapolated from the current daily burn rate.
type MonthlyProjection struct {
	// MTDSpendUSD is the actual API cost from the 1st of the month to now.
	MTDSpendUSD float64
	// EstMonthlySpendUSD is the projected full-month API cost at the current rate.
	EstMonthlySpendUSD float64
	// DaysElapsed is the number of calendar days elapsed so far this month (including today).
	DaysElapsed int
	// DaysInMonth is the total number of days in the current calendar month.
	DaysInMonth int
	// EstMonthlySubsidyUSD is the projected monthly API cost minus the fixed monthly
	// subscription cost. Only set when subscriptionCostUSD > 0. Can be negative if
	// usage is below the subscription break-even point.
	EstMonthlySubsidyUSD *float64
}

// BuildMonthlyProjection computes a MonthlyProjection from month-to-date cost.
// subscriptionCostUSD is the total fixed monthly subscription cost across all
// configured providers; pass 0 when no subscriptions are configured.
func BuildMonthlyProjection(mtdCost float64, tz *time.Location, subscriptionCostUSD float64) *MonthlyProjection {
	now := time.Now().In(tz)
	y, m := now.Year(), now.Month()
	daysInMonth := time.Date(y, m+1, 1, 0, 0, 0, 0, tz).AddDate(0, 0, -1).Day()
	daysElapsed := now.Day()
	if daysElapsed < 1 {
		daysElapsed = 1
	}

	mp := &MonthlyProjection{
		MTDSpendUSD: mtdCost,
		DaysElapsed: daysElapsed,
		DaysInMonth: daysInMonth,
	}

	if daysElapsed >= daysInMonth {
		mp.EstMonthlySpendUSD = mtdCost
	} else {
		mp.EstMonthlySpendUSD = mtdCost * float64(daysInMonth) / float64(daysElapsed)
	}

	if subscriptionCostUSD > 0 {
		estSubsidy := mp.EstMonthlySpendUSD - subscriptionCostUSD
		mp.EstMonthlySubsidyUSD = &estSubsidy
	}

	return mp
}

// --------------------------------------------------------------------------
// Subscription Subsidy Analysis
// --------------------------------------------------------------------------

// SubsidyAnalysis holds data for the hero subsidy section of the cost dashboard.
// Only populated when at least one provider has a configured subscription.
type SubsidyAnalysis struct {
	ProviderSubsidyUSD  float64  // token_cost - subscription_cost (what providers "lost")
	TrueCostUSD         float64  // what user would pay at API rates
	SubscriptionCostUSD float64  // what user actually pays
	SubsidyPct          *float64 // savings as % of API cost: (API - Sub) / API * 100

	TotalTokens    int64
	TotalInputRate SubsidyCategoryRate // aggregate: Input + Cache Write + Cache Read
	OutputRate     SubsidyCategoryRate // Output category (duplicated for easy template access)
	Categories     []SubsidyCategoryRate
	// InputChildren holds only Input, Cache Write, Cache Read for the indented rows.
	InputChildren        []SubsidyCategoryRate
	CumulativeData       []SubsidyCumulativeEntry
	MaxCumulativeSubsidy float64 // for bar height scaling in chart
}

// SubsidyCategoryRate compares effective subscription rate to API rate for a
// single token category (Input, Cache Write, Cache Read, Output).
type SubsidyCategoryRate struct {
	Category         string
	TotalTokens      int64
	APICostUSD       float64
	EffectiveCostUSD float64 // subscription allocated proportionally by API cost share
	APIRatePerMTok   float64
	EffRatePerMTok   float64
	SavingsPct       *float64 // (API - Eff) / API * 100
}

// SubsidyCumulativeEntry tracks daily running totals for the cumulative
// provider subsidy chart.
type SubsidyCumulativeEntry struct {
	Date               string
	DailyAPICost       float64
	DailySubAllocation float64
	CumulativeAPICost  float64
	CumulativeSubCost  float64
	CumulativeSubsidy  float64 // cumulative(API - Sub), positive = provider losing
}

// BuildSubsidyAnalysis constructs the subsidy analysis from existing pricing
// groups and timeseries data. Returns nil if no subscriptions are configured
// or no matching usage data exists.
func BuildSubsidyAnalysis(
	pricingGroups []store.CostPricingGroup,
	timeseriesPricingGroups []store.CostTimeseriesPricingGroup,
	calc *pricing.Calculator,
	providerNames map[string]string,
	subscriptions map[string]float64,
	numDaysInRange int,
) *SubsidyAnalysis {
	if len(subscriptions) == 0 || calc == nil || numDaysInRange <= 0 {
		return nil
	}

	// Accumulate per-category API costs and token counts for subscription providers.
	type categoryAccum struct {
		tokens int64
		cost   float64
	}
	categories := map[string]*categoryAccum{
		"Input":       {},
		"Cache Write": {},
		"Cache Read":  {},
		"Output":      {},
	}
	var totalAPICost float64
	matchedSubscriptions := make(map[string]float64) // display name -> monthly cost

	for _, g := range pricingGroups {
		displayName, _ := ProviderDisplayName(g.ProviderName, providerNames)
		if displayName == "" {
			displayName = g.ProviderName
		}

		if _, ok := subscriptions[displayName]; !ok {
			continue
		}
		matchedSubscriptions[displayName] = subscriptions[displayName]

		priced, _, ok := LookupPricedUsageWithAliases(calc, g.ProviderName, g.ModelUpstream, TokenUsage{
			InputTokens:      g.InputTokens,
			CacheWriteTokens: g.CacheCreationTokens,
			CacheReadTokens:  g.CacheReadTokens,
			OutputTokens:     g.OutputTokens,
			ReasoningTokens:  g.ReasoningTokens,
		}, providerNames)
		if !ok {
			continue
		}

		categories["Input"].tokens += g.InputTokens
		categories["Input"].cost += priced.InputCostUSD
		categories["Cache Write"].tokens += g.CacheCreationTokens
		categories["Cache Write"].cost += priced.CacheWriteCostUSD
		categories["Cache Read"].tokens += g.CacheReadTokens
		categories["Cache Read"].cost += priced.CacheReadCostUSD
		categories["Output"].tokens += g.OutputTokens
		categories["Output"].cost += priced.OutputCostUSD
		totalAPICost += priced.TotalCostUSD
	}

	if len(matchedSubscriptions) == 0 || totalAPICost == 0 {
		return nil
	}

	var totalSubscriptionCost float64
	for _, cost := range matchedSubscriptions {
		totalSubscriptionCost += cost
	}

	subsidy := totalAPICost - totalSubscriptionCost

	sa := &SubsidyAnalysis{
		ProviderSubsidyUSD:  subsidy,
		TrueCostUSD:         totalAPICost,
		SubscriptionCostUSD: totalSubscriptionCost,
		SubsidyPct:          PercentDelta(subsidy, totalAPICost),
	}

	// Build per-category rates with proportional subscription allocation.
	categoryOrder := []string{"Input", "Cache Write", "Cache Read", "Output"}
	for _, name := range categoryOrder {
		cat := categories[name]
		if cat.tokens == 0 {
			continue
		}

		// Allocate subscription proportionally based on API cost share.
		effectiveCost := 0.0
		if totalAPICost > 0 {
			effectiveCost = totalSubscriptionCost * (cat.cost / totalAPICost)
		}

		apiRate := cat.cost * 1_000_000 / float64(cat.tokens)
		effRate := effectiveCost * 1_000_000 / float64(cat.tokens)

		var savingsPct *float64
		if apiRate > 0 {
			pct := (apiRate - effRate) / apiRate * 100
			savingsPct = &pct
		}

		sa.Categories = append(sa.Categories, SubsidyCategoryRate{
			Category:         name,
			TotalTokens:      cat.tokens,
			APICostUSD:       cat.cost,
			EffectiveCostUSD: effectiveCost,
			APIRatePerMTok:   apiRate,
			EffRatePerMTok:   effRate,
			SavingsPct:       savingsPct,
		})
	}

	// Build aggregated Total Input and Output rows, plus InputChildren for the template hierarchy.
	for _, cat := range sa.Categories {
		sa.TotalTokens += cat.TotalTokens
		if cat.Category == "Output" {
			sa.OutputRate = cat
		} else {
			sa.InputChildren = append(sa.InputChildren, cat)
			sa.TotalInputRate.TotalTokens += cat.TotalTokens
			sa.TotalInputRate.APICostUSD += cat.APICostUSD
			sa.TotalInputRate.EffectiveCostUSD += cat.EffectiveCostUSD
		}
	}
	sa.TotalInputRate.Category = "Total Input"
	if sa.TotalInputRate.APICostUSD > 0 {
		pct := (sa.TotalInputRate.APICostUSD - sa.TotalInputRate.EffectiveCostUSD) / sa.TotalInputRate.APICostUSD * 100
		sa.TotalInputRate.SavingsPct = &pct
	}

	sa.CumulativeData = BuildSubsidyTimeseries(
		timeseriesPricingGroups, calc, providerNames,
		matchedSubscriptions, totalSubscriptionCost, numDaysInRange,
	)
	for _, entry := range sa.CumulativeData {
		if entry.CumulativeSubsidy > sa.MaxCumulativeSubsidy {
			sa.MaxCumulativeSubsidy = entry.CumulativeSubsidy
		}
	}

	return sa
}

// BuildSubsidyTimeseries produces daily cumulative subsidy entries from
// timeseries pricing groups, filtering to only subscription providers.
func BuildSubsidyTimeseries(
	groups []store.CostTimeseriesPricingGroup,
	calc *pricing.Calculator,
	providerNames map[string]string,
	matchedSubscriptions map[string]float64,
	totalSubscriptionCost float64,
	numDaysInRange int,
) []SubsidyCumulativeEntry {
	if len(groups) == 0 {
		return nil
	}

	dailySubAllocation := totalSubscriptionCost / float64(numDaysInRange)

	// Aggregate daily API costs for subscription providers.
	dailyCosts := make(map[string]float64)
	for _, g := range groups {
		displayName, _ := ProviderDisplayName(g.ProviderName, providerNames)
		if displayName == "" {
			displayName = g.ProviderName
		}
		if _, ok := matchedSubscriptions[displayName]; !ok {
			continue
		}

		priced, _, ok := LookupPricedUsageWithAliases(calc, g.ProviderName, g.ModelUpstream, TokenUsage{
			InputTokens:      g.InputTokens,
			CacheWriteTokens: g.CacheCreationTokens,
			CacheReadTokens:  g.CacheReadTokens,
			OutputTokens:     g.OutputTokens,
		}, providerNames)
		if !ok {
			continue
		}
		dailyCosts[g.Date] += priced.TotalCostUSD
	}

	if len(dailyCosts) == 0 {
		return nil
	}

	// Sort dates and build cumulative entries.
	dates := make([]string, 0, len(dailyCosts))
	for d := range dailyCosts {
		dates = append(dates, d)
	}
	sort.Strings(dates)

	entries := make([]SubsidyCumulativeEntry, 0, len(dates))
	var cumAPICost, cumSubCost float64
	for _, date := range dates {
		cumAPICost += dailyCosts[date]
		cumSubCost += dailySubAllocation
		entries = append(entries, SubsidyCumulativeEntry{
			Date:               date,
			DailyAPICost:       dailyCosts[date],
			DailySubAllocation: dailySubAllocation,
			CumulativeAPICost:  cumAPICost,
			CumulativeSubCost:  cumSubCost,
			CumulativeSubsidy:  cumAPICost - cumSubCost,
		})
	}

	return entries
}

// BudgetStatusEntry holds budget utilization data for dashboard display.
type BudgetStatusEntry struct {
	Scope          string
	ScopeID        string
	ScopeLabel     string
	Period         string
	LimitUSD       float64
	SpendUSD       float64
	RemainingUSD   float64
	UtilizationPct float64
	ResetAtFmt     string
	Status         string // "ok", "warning", "exceeded"
}
