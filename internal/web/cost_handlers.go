// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web provides HTTP handlers and templates for the embedded web UI.
package web

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/auth"
	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/store"
	"golang.org/x/sync/errgroup"
)

// CostHandlers groups the HTTP handlers for cost dashboard endpoints.
type CostHandlers struct {
	store                        store.CostQueryStore
	templates                    *TemplateSet
	tz                           *time.Location
	calc                         *pricing.Calculator
	providerNames                map[string]string  // provider name or legacy raw key → display name
	providerMonthlySubscriptions map[string]float64 // provider display name → configured monthly subscription cost
}

// NewCostHandlers creates a new CostHandlers with the given store, template set,
// display timezone (pass nil or time.UTC for UTC), and provider name map
// (provider name or legacy raw key → display name). Pass nil if no providers are configured.
func NewCostHandlers(s store.CostQueryStore, tmpl *TemplateSet, tz *time.Location, calc *pricing.Calculator, providerNames map[string]string, providerMonthlySubscriptions map[string]float64) *CostHandlers {
	if tz == nil {
		tz = time.UTC
	}
	return &CostHandlers{
		store:                        s,
		templates:                    tmpl,
		tz:                           tz,
		calc:                         calc,
		providerNames:                providerNames,
		providerMonthlySubscriptions: providerMonthlySubscriptions,
	}
}

// --------------------------------------------------------------------------
// JSON API responses
// --------------------------------------------------------------------------

type costSummaryResponse struct {
	TotalCostUSD                     float64                  `json:"total_cost_usd"`
	TotalInputTokens                 int64                    `json:"total_input_tokens"`
	TotalOutputTokens                int64                    `json:"total_output_tokens"`
	TotalCacheCreationTokens         int64                    `json:"total_cache_creation_tokens"`
	TotalCacheReadTokens             int64                    `json:"total_cache_read_tokens"`
	TotalRequests                    int64                    `json:"total_requests"`
	TotalMonthlySubscriptionCostUSD  *float64                 `json:"total_monthly_subscription_cost_usd,omitempty"`
	TotalTokenMinusSubscriptionUSD   *float64                 `json:"total_token_minus_subscription_usd,omitempty"`
	TotalTokenVsSubscriptionPct      *float64                 `json:"total_token_vs_subscription_pct,omitempty"`
	EffectiveTokenCostPerMTokUSD     *float64                 `json:"effective_token_cost_per_mtok_usd,omitempty"`
	AverageRealTokenCostPerMTokUSD   *float64                 `json:"average_real_token_cost_per_mtok_usd,omitempty"`
	EffectiveMinusRealCostPerMTokUSD *float64                 `json:"effective_minus_real_cost_per_mtok_usd,omitempty"`
	EffectiveVsRealPct               *float64                 `json:"effective_vs_real_pct,omitempty"`
	Breakdown                        []costBreakdownEntryJSON `json:"breakdown"`
	From                             string                   `json:"from"`
	To                               string                   `json:"to"`
}

type costBreakdownEntryJSON struct {
	Group                         string   `json:"group"`
	CostUSD                       float64  `json:"cost_usd"`
	InputTokens                   int64    `json:"input_tokens"`
	OutputTokens                  int64    `json:"output_tokens"`
	CacheCreationTokens           int64    `json:"cache_creation_tokens"`
	CacheReadTokens               int64    `json:"cache_read_tokens"`
	Requests                      int64    `json:"requests"`
	MonthlySubscriptionCostUSD    *float64 `json:"monthly_subscription_cost_usd,omitempty"`
	TokenMinusSubscriptionUSD     *float64 `json:"token_minus_subscription_usd,omitempty"`
	TokenVsSubscriptionPct        *float64 `json:"token_vs_subscription_pct,omitempty"`
	EffectiveTokenCostPerMTok     *float64 `json:"effective_token_cost_per_mtok_usd,omitempty"`
	AverageRealTokenCostPerMTok   *float64 `json:"average_real_token_cost_per_mtok_usd,omitempty"`
	EffectiveMinusRealCostPerMTok *float64 `json:"effective_minus_real_cost_per_mtok_usd,omitempty"`
	EffectiveVsRealPct            *float64 `json:"effective_vs_real_pct,omitempty"`
}

type costTimeseriesResponse struct {
	Interval string                    `json:"interval"`
	Data     []costTimeseriesEntryJSON `json:"data"`
	From     string                    `json:"from"`
	To       string                    `json:"to"`
}

type costTimeseriesEntryJSON struct {
	Date     string  `json:"date"`
	CostUSD  float64 `json:"cost_usd"`
	Requests int64   `json:"requests"`
}

type pricedTimeseriesEntry struct {
	Date     string
	CostUSD  float64
	Requests int64
}

// --------------------------------------------------------------------------
// Helper: parse cost query parameters
// --------------------------------------------------------------------------

func (h *CostHandlers) parseCostParams(r *http.Request) store.CostParams {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	groupBy := r.URL.Query().Get("group_by")
	filterValue := r.URL.Query().Get("filter")

	now := time.Now().In(h.tz)

	// Default: last 30 days.
	if from == "" {
		from = now.AddDate(0, 0, -30).Format("2006-01-02")
	}
	if to == "" {
		to = now.Format("2006-01-02")
	}
	if groupBy == "" {
		groupBy = "model"
	}
	if groupBy == "key" && filterValue == "" {
		// Backward compatibility for older URLs that still use ?key=...
		filterValue = r.URL.Query().Get("key")
	}

	keyPrefix := ""
	groupFilter := ""
	if groupBy == "key" {
		keyPrefix = filterValue
	} else {
		groupFilter = filterValue
	}

	// Convert local date strings to UTC datetime boundaries so that the SQL
	// comparisons against UTC-stored timestamps are correct.
	fromLocal, err := time.ParseInLocation("2006-01-02", from, h.tz)
	if err != nil {
		fromLocal = startOfDay(now.AddDate(0, 0, -30), h.tz)
	}
	toLocal, err := time.ParseInLocation("2006-01-02", to, h.tz)
	if err != nil {
		toLocal = startOfDay(now, h.tz)
	}
	// toLocal is midnight at the start of `to`; advance to the next local
	// midnight with calendar math so DST transition days stay exact.
	toLocalEnd := toLocal.AddDate(0, 0, 1).Add(-time.Second)

	_, offsetSecs := fromLocal.Zone()

	return store.CostParams{
		From:            fromLocal.UTC().Format("2006-01-02 15:04:05"),
		To:              toLocalEnd.UTC().Format("2006-01-02 15:04:05"),
		GroupBy:         groupBy,
		KeyPrefix:       keyPrefix,
		GroupFilter:     groupFilter,
		TzOffsetSeconds: offsetSecs,
		TzLocation:      h.tz,
	}
}

func startOfDay(t time.Time, tz *time.Location) time.Time {
	local := t.In(tz)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, tz)
}

// daysInRange returns the number of days between two date strings (inclusive).
// Falls back to 30 if either date fails to parse.
func daysInRange(from, to string) int {
	fromT, err := time.Parse("2006-01-02", from)
	if err != nil {
		return 30
	}
	toT, err := time.Parse("2006-01-02", to)
	if err != nil {
		return 30
	}
	days := int(toT.Sub(fromT).Hours()/24) + 1
	if days < 1 {
		return 1
	}
	return days
}

func (h *CostHandlers) costParamsForRequest(r *http.Request) store.CostParams {
	params := h.parseCostParams(r)
	h.applyProviderFilter(&params)
	return params
}

func (h *CostHandlers) applyProviderFilter(params *store.CostParams) {
	if params.GroupBy != "provider" || params.GroupFilter == "" || len(h.providerNames) == 0 {
		return
	}

	filter := strings.ToLower(params.GroupFilter)
	providerGroups := make([]string, 0, len(h.providerNames))
	for providerName, displayName := range h.providerNames {
		if strings.HasPrefix(strings.ToLower(providerName), filter) || strings.HasPrefix(strings.ToLower(displayName), filter) {
			providerGroups = append(providerGroups, providerName)
		}
	}

	if len(providerGroups) == 0 {
		return
	}

	slices.Sort(providerGroups)
	params.ProviderGroups = slices.Compact(providerGroups)
}

// --------------------------------------------------------------------------
// T050: GET /ui/api/costs — JSON cost summary with breakdown
// --------------------------------------------------------------------------

// CostSummaryHandler returns aggregated cost data with a breakdown grouped
// by model, provider, or key. Query parameters: from, to, group_by (model|provider|key), filter.
func (h *CostHandlers) CostSummaryHandler(w http.ResponseWriter, r *http.Request) {
	params := h.costParamsForRequest(r)
	applyScopeToCostParams(auth.SessionFromContext(r.Context()), &params)

	pricingGroups, err := h.store.GetCostPricingGroups(r.Context(), params)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// Derive summary and breakdown from pricing groups in-memory
	// instead of running separate SQL queries.
	summary := deriveSummaryFromPricingGroups(pricingGroups)
	breakdown := deriveBreakdownFromPricingGroups(pricingGroups, params.GroupBy, h.providerNames)

	tokenCosts := computeAggregateCostBreakdownWithAliases(pricingGroups, h.calc, h.providerNames)
	breakdownCosts := buildBreakdownCosts(pricingGroups, h.calc, params.GroupBy, h.providerNames)
	providerComparisons, providerComparisonSummary := buildProviderSpendComparisons(breakdown, breakdownCosts, h.providerMonthlySubscriptions)

	bd := make([]costBreakdownEntryJSON, len(breakdown))
	for i, e := range breakdown {
		bd[i] = costBreakdownEntryJSON{
			Group:               e.Group,
			CostUSD:             breakdownCosts[e.Group],
			InputTokens:         e.InputTokens,
			OutputTokens:        e.OutputTokens,
			CacheCreationTokens: e.CacheCreationTokens,
			CacheReadTokens:     e.CacheReadTokens,
			Requests:            e.Requests,
		}
		if pc, ok := providerComparisons[e.Group]; ok {
			monthly := pc.MonthlySubscriptionCost
			delta := pc.TokenMinusSubscriptionUSD
			bd[i].MonthlySubscriptionCostUSD = &monthly
			bd[i].TokenMinusSubscriptionUSD = &delta
			bd[i].TokenVsSubscriptionPct = pc.TokenVsSubscriptionPct
			bd[i].EffectiveTokenCostPerMTok = pc.EffectiveTokenCostPerMTok
			bd[i].AverageRealTokenCostPerMTok = pc.AverageRealTokenCostPerMTok
			bd[i].EffectiveMinusRealCostPerMTok = pc.EffectiveMinusRealCostPerMTok
			bd[i].EffectiveVsRealPct = pc.EffectiveVsRealPct
		}
	}

	fromDate, toDate := h.defaultDateRange(r)

	resp := costSummaryResponse{
		TotalCostUSD:             tokenCosts.TotalCostUSD,
		TotalInputTokens:         summary.TotalInputTokens,
		TotalOutputTokens:        summary.TotalOutputTokens,
		TotalCacheCreationTokens: summary.TotalCacheCreationTokens,
		TotalCacheReadTokens:     summary.TotalCacheReadTokens,
		TotalRequests:            summary.TotalRequests,
		Breakdown:                bd,
		From:                     fromDate,
		To:                       toDate,
	}
	if params.GroupBy == "provider" && providerComparisonSummary.HasAnySubscription {
		totalMonthly := providerComparisonSummary.TotalSubscriptionCost
		totalDelta := providerComparisonSummary.TokenMinusSubscription
		resp.TotalMonthlySubscriptionCostUSD = &totalMonthly
		resp.TotalTokenMinusSubscriptionUSD = &totalDelta
		resp.TotalTokenVsSubscriptionPct = providerComparisonSummary.TokenVsSubscriptionPct
		resp.EffectiveTokenCostPerMTokUSD = providerComparisonSummary.EffectiveTokenCostPerMTok
		resp.AverageRealTokenCostPerMTokUSD = providerComparisonSummary.AverageRealTokenCostPerMTok
		resp.EffectiveMinusRealCostPerMTokUSD = providerComparisonSummary.EffectiveMinusRealCostPerMTok
		resp.EffectiveVsRealPct = providerComparisonSummary.EffectiveVsRealPct
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("write cost summary response", "error", err)
	}
}

// --------------------------------------------------------------------------
// T051: GET /ui/api/costs/timeseries — JSON timeseries data
// --------------------------------------------------------------------------

// CostTimeseriesHandler returns cost data bucketed over time.
// Query parameters: from, to, interval (day|week|month), filter.
func (h *CostHandlers) CostTimeseriesHandler(w http.ResponseWriter, r *http.Request) {
	params := h.costParamsForRequest(r)
	applyScopeToCostParams(auth.SessionFromContext(r.Context()), &params)

	interval := r.URL.Query().Get("interval")
	if interval == "" {
		interval = "day"
	}

	pricingGroups, err := h.store.GetCostTimeseriesPricingGroups(r.Context(), params)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	aggregated := aggregatePricedTimeseries(priceTimeseriesGroups(pricingGroups, h.calc, h.providerNames), interval)

	data := make([]costTimeseriesEntryJSON, len(aggregated))
	for i, e := range aggregated {
		data[i] = costTimeseriesEntryJSON(e)
	}

	fromDate, toDate := h.defaultDateRange(r)

	resp := costTimeseriesResponse{
		Interval: interval,
		Data:     data,
		From:     fromDate,
		To:       toDate,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		slog.Error("write cost timeseries response", "error", err)
	}
}

func priceTimeseriesGroups(groups []store.CostTimeseriesPricingGroup, calc *pricing.Calculator, providerNames map[string]string) []pricedTimeseriesEntry {
	if len(groups) == 0 {
		return []pricedTimeseriesEntry{}
	}

	entries := make([]pricedTimeseriesEntry, 0, len(groups))
	for _, group := range groups {
		cost := 0.0
		if priced, _, ok := lookupPricedUsageWithAliases(calc, group.ProviderName, group.ModelUpstream, tokenUsage{
			InputTokens:      group.InputTokens,
			CacheWriteTokens: group.CacheCreationTokens,
			CacheReadTokens:  group.CacheReadTokens,
			OutputTokens:     group.OutputTokens,
		}, providerNames); ok {
			cost = priced.TotalCostUSD
		}
		entries = append(entries, pricedTimeseriesEntry{
			Date:     group.Date,
			CostUSD:  cost,
			Requests: group.Requests,
		})
	}
	return entries
}

func aggregatePricedTimeseries(entries []pricedTimeseriesEntry, interval string) []pricedTimeseriesEntry {
	if len(entries) == 0 {
		return entries
	}

	bucketKey := func(dateStr string) string {
		t, err := time.Parse("2006-01-02", dateStr)
		if err != nil {
			return dateStr
		}
		switch interval {
		case "week":
			weekday := int(t.Weekday())
			if weekday == 0 {
				weekday = 7
			}
			monday := t.AddDate(0, 0, -(weekday - 1))
			return monday.Format("2006-01-02")
		case "month":
			return t.Format("2006-01")
		default:
			return dateStr
		}
	}

	var buckets []pricedTimeseriesEntry
	seen := map[string]int{}

	for _, entry := range entries {
		bk := bucketKey(entry.Date)
		if idx, ok := seen[bk]; ok {
			buckets[idx].CostUSD += entry.CostUSD
			buckets[idx].Requests += entry.Requests
			continue
		}
		seen[bk] = len(buckets)
		buckets = append(buckets, pricedTimeseriesEntry{
			Date:     bk,
			CostUSD:  entry.CostUSD,
			Requests: entry.Requests,
		})
	}

	return buckets
}

func providerDisplayName(rawKey string, providerNames map[string]string) (string, bool) {
	if providerNames == nil {
		return rawKey, false
	}
	if mapped, ok := providerNames[rawKey]; ok && mapped != "" {
		return mapped, true
	}
	return rawKey, false
}

// --------------------------------------------------------------------------
// Shared cost dashboard data computation
// --------------------------------------------------------------------------

// defaultDateRange returns from/to date strings from query params, defaulting
// to the last 30 days in the handler's timezone.
func (h *CostHandlers) defaultDateRange(r *http.Request) (fromDate, toDate string) {
	fromDate = r.URL.Query().Get("from")
	if fromDate == "" {
		fromDate = time.Now().In(h.tz).AddDate(0, 0, -30).Format("2006-01-02")
	}
	toDate = r.URL.Query().Get("to")
	if toDate == "" {
		toDate = time.Now().In(h.tz).Format("2006-01-02")
	}
	return fromDate, toDate
}

// costDashboardData holds all the computed data needed by both the full cost
// dashboard page and the HTMX cost summary fragment.
type costDashboardData struct {
	Summary                   *store.CostSummary
	TokenCosts                *AggregateCostBreakdown
	Breakdown                 []store.CostBreakdownEntry
	BreakdownCosts            map[string]float64
	ProviderComparisons       map[string]*ProviderSpendComparison
	ProviderComparisonSummary ProviderSpendComparisonSummary
	Timeseries                []pricedTimeseriesEntry
	MaxCost                   float64
	MaxBreakdownRequests      int64
	HasIncompleteData         bool
	GroupBy                   string
	SubsidyAnalysis           *SubsidyAnalysis
	FromDate                  string
	ToDate                    string
}

// buildCostDashboardData fetches pricing groups from the store and computes
// all derived data for the cost dashboard. Used by both the full page handler
// and the HTMX fragment handler.
func (h *CostHandlers) buildCostDashboardData(r *http.Request) (*costDashboardData, error) {
	params := h.costParamsForRequest(r)
	applyScopeToCostParams(auth.SessionFromContext(r.Context()), &params)

	var (
		pricingGroups           []store.CostPricingGroup
		timeseriesPricingGroups []store.CostTimeseriesPricingGroup
	)

	g, ctx := errgroup.WithContext(r.Context())
	g.Go(func() error {
		var err error
		pricingGroups, err = h.store.GetCostPricingGroups(ctx, params)
		return err
	})
	g.Go(func() error {
		var err error
		timeseriesPricingGroups, err = h.store.GetCostTimeseriesPricingGroups(ctx, params)
		return err
	})
	if err := g.Wait(); err != nil {
		return nil, err
	}

	summary := deriveSummaryFromPricingGroups(pricingGroups)
	breakdown := deriveBreakdownFromPricingGroups(pricingGroups, params.GroupBy, h.providerNames)

	aggregatedTimeseries := aggregatePricedTimeseries(
		priceTimeseriesGroups(timeseriesPricingGroups, h.calc, h.providerNames), "day",
	)

	var maxCost float64
	for _, e := range aggregatedTimeseries {
		if e.CostUSD > maxCost {
			maxCost = e.CostUSD
		}
	}
	var maxBreakdownRequests int64
	for _, e := range breakdown {
		if e.Requests > maxBreakdownRequests {
			maxBreakdownRequests = e.Requests
		}
	}

	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = "model"
	}

	hasIncompleteData := false
	for _, e := range breakdown {
		if e.InputTokens == 0 && e.OutputTokens == 0 && e.Requests > 0 {
			hasIncompleteData = true
			break
		}
	}

	tokenCosts := computeAggregateCostBreakdownWithAliases(pricingGroups, h.calc, h.providerNames)
	breakdownCosts := buildBreakdownCosts(pricingGroups, h.calc, groupBy, h.providerNames)
	providerComparisons, providerComparisonSummary := buildProviderSpendComparisons(breakdown, breakdownCosts, h.providerMonthlySubscriptions)

	fromDate, toDate := h.defaultDateRange(r)

	subsidyAnalysis := buildSubsidyAnalysis(
		pricingGroups, timeseriesPricingGroups,
		h.calc, h.providerNames, h.providerMonthlySubscriptions,
		daysInRange(fromDate, toDate),
	)

	return &costDashboardData{
		Summary:                   summary,
		TokenCosts:                tokenCosts,
		Breakdown:                 breakdown,
		BreakdownCosts:            breakdownCosts,
		ProviderComparisons:       providerComparisons,
		ProviderComparisonSummary: providerComparisonSummary,
		Timeseries:                aggregatedTimeseries,
		MaxCost:                   maxCost,
		MaxBreakdownRequests:      maxBreakdownRequests,
		HasIncompleteData:         hasIncompleteData,
		GroupBy:                   groupBy,
		SubsidyAnalysis:           subsidyAnalysis,
		FromDate:                  fromDate,
		ToDate:                    toDate,
	}, nil
}

// toTemplateData converts the computed dashboard data to a template data map.
func (d *costDashboardData) toTemplateData() map[string]any {
	return map[string]any{
		"Summary":                   d.Summary,
		"TokenCosts":                d.TokenCosts,
		"Breakdown":                 d.Breakdown,
		"BreakdownCosts":            d.BreakdownCosts,
		"ProviderComparisons":       d.ProviderComparisons,
		"ProviderComparisonSummary": d.ProviderComparisonSummary,
		"Timeseries":                d.Timeseries,
		"MaxCost":                   d.MaxCost,
		"MaxBreakdownRequests":      float64(d.MaxBreakdownRequests),
		"HasIncompleteData":         d.HasIncompleteData,
		"GroupBy":                   d.GroupBy,
		"TotalAllInputTokens":       d.Summary.TotalInputTokens + d.Summary.TotalCacheCreationTokens + d.Summary.TotalCacheReadTokens,
		"SubsidyAnalysis":           d.SubsidyAnalysis,
	}
}

// --------------------------------------------------------------------------
// T052: GET /ui/costs — render full cost dashboard page
// --------------------------------------------------------------------------

// CostsPageHandler renders the full cost dashboard HTML page.
func (h *CostHandlers) CostsPageHandler(w http.ResponseWriter, r *http.Request) {
	dd, err := h.buildCostDashboardData(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	data := dd.toTemplateData()
	data["ActiveTab"] = "costs"
	data["Title"] = "Cost Dashboard"
	data["From"] = dd.FromDate
	data["To"] = dd.ToDate
	data["ProviderNames"] = h.providerNames

	groupFilter := r.URL.Query().Get("filter")
	if dd.GroupBy == "key" && groupFilter == "" {
		groupFilter = r.URL.Query().Get("key")
	}
	data["GroupFilter"] = groupFilter

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "costs.html", data); err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}

// --------------------------------------------------------------------------
// T053: GET /ui/fragments/cost_summary — HTMX partial for breakdown table
// --------------------------------------------------------------------------

// CostSummaryFragmentHandler renders only the cost breakdown table as an
// HTMX partial, used when filters change without a full page reload.
func (h *CostHandlers) CostSummaryFragmentHandler(w http.ResponseWriter, r *http.Request) {
	dd, err := h.buildCostDashboardData(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	data := dd.toTemplateData()
	data["ProviderNames"] = h.providerNames

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteNamed(w, "cost_summary", data); err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}
