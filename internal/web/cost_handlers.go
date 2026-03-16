// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web provides HTTP handlers and templates for the embedded web UI.
package web

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/proxy"
	"github.com/seckatie/glitchgate/internal/store"
	"golang.org/x/sync/errgroup"
)

// CostHandlers groups the HTTP handlers for cost dashboard endpoints.
type CostHandlers struct {
	store                        store.CostQueryStore
	budgetStore                  store.BudgetCheckStore
	budgetAdminStore             store.BudgetAdminStore
	keyStore                     store.ProxyKeyStore
	templates                    *TemplateSet
	tz                           *time.Location
	calc                         *pricing.Calculator
	providerNames                map[string]string  // provider name or legacy raw key → display name
	providerMonthlySubscriptions map[string]float64 // provider display name → configured monthly subscription cost
}

// NewCostHandlers creates a new CostHandlers with the given store, template set,
// display timezone (pass nil or time.UTC for UTC), and provider name map
// (provider name or legacy raw key → display name). Pass nil if no providers are configured.
func NewCostHandlers(s store.CostQueryStore, bs store.BudgetCheckStore, bas store.BudgetAdminStore, ks store.ProxyKeyStore, tmpl *TemplateSet, tz *time.Location, calc *pricing.Calculator, providerNames map[string]string, providerMonthlySubscriptions map[string]float64) *CostHandlers {
	if tz == nil {
		tz = time.UTC
	}
	return &CostHandlers{
		store:                        s,
		budgetStore:                  bs,
		budgetAdminStore:             bas,
		keyStore:                     ks,
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

// costParamsLast30Days builds a CostParams for the last 30 local days with
// the given groupBy dimension. Used by dashboard and providers pages.
func costParamsLast30Days(tz *time.Location, groupBy string) store.CostParams {
	now := time.Now().In(tz)
	fromLocal := startOfDay(now.AddDate(0, 0, -30), tz)
	toLocalEnd := startOfDay(now, tz).AddDate(0, 0, 1).Add(-time.Second)
	_, offsetSecs := fromLocal.Zone()
	return store.CostParams{
		From:            fromLocal.UTC().Format("2006-01-02 15:04:05"),
		To:              toLocalEnd.UTC().Format("2006-01-02 15:04:05"),
		GroupBy:         groupBy,
		TzOffsetSeconds: offsetSecs,
		TzLocation:      tz,
	}
}

// costParamsMonthToDate builds a CostParams from the 1st of the current
// calendar month (local time) up to the current moment.
func costParamsMonthToDate(tz *time.Location, groupBy string) store.CostParams {
	now := time.Now().In(tz)
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, tz)
	_, offsetSecs := monthStart.Zone()
	return store.CostParams{
		From:            monthStart.UTC().Format("2006-01-02 15:04:05"),
		To:              now.UTC().Format("2006-01-02 15:04:05"),
		GroupBy:         groupBy,
		TzOffsetSeconds: offsetSecs,
		TzLocation:      tz,
	}
}

// costParamsToday builds a CostParams for today with the given groupBy dimension.
func costParamsToday(tz *time.Location, groupBy string) store.CostParams {
	now := time.Now().In(tz)
	todayStart := startOfDay(now, tz)
	todayEnd := todayStart.AddDate(0, 0, 1).Add(-time.Second)
	_, offsetSecs := todayStart.Zone()
	return store.CostParams{
		From:            todayStart.UTC().Format("2006-01-02 15:04:05"),
		To:              todayEnd.UTC().Format("2006-01-02 15:04:05"),
		GroupBy:         groupBy,
		TzOffsetSeconds: offsetSecs,
		TzLocation:      tz,
	}
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

	setNavData(data, auth.SessionFromContext(r.Context()))
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

// buildBudgetStatusEntries delegates to the package-level BuildBudgetStatusEntries.
func (h *CostHandlers) buildBudgetStatusEntries(ctx context.Context, budgets []store.ApplicableBudget) []BudgetStatusEntry {
	return BuildBudgetStatusEntries(ctx, budgets, h.budgetStore, h.keyStore, h.tz)
}

// BuildBudgetStatusEntries computes budget utilization for each configured budget.
func BuildBudgetStatusEntries(ctx context.Context, budgets []store.ApplicableBudget, budgetStore store.BudgetCheckStore, keyStore store.ProxyKeyStore, tz *time.Location) []BudgetStatusEntry {
	// Pre-fetch key summaries for key-scoped budget labels.
	keyLabels := make(map[string]string)
	if keyStore != nil {
		for _, b := range budgets {
			if b.Scope == "key" {
				keyLabels[b.ScopeID] = "" // mark for lookup
			}
		}
		if len(keyLabels) > 0 {
			if keys, err := keyStore.ListActiveProxyKeys(ctx); err == nil {
				for _, k := range keys {
					if _, needed := keyLabels[k.ID]; needed {
						lbl := "Key: " + k.KeyPrefix + "..."
						if k.Label != "" {
							lbl += " (" + k.Label + ")"
						}
						keyLabels[k.ID] = lbl
					}
				}
			}
		}
	}

	now := time.Now()
	var entries []BudgetStatusEntry

	for _, b := range budgets {
		start := proxy.PeriodStart(b.Period, now, tz)
		spend, err := budgetStore.GetSpendSince(ctx, b.Scope, b.ScopeID, start)
		if err != nil {
			slog.Warn("budget status: failed to get spend", "scope", b.Scope, "error", err)
			continue
		}

		remaining := b.LimitUSD - spend
		if remaining < 0 {
			remaining = 0
		}
		pct := 0.0
		if b.LimitUSD > 0 {
			pct = (spend / b.LimitUSD) * 100
		}

		status := "ok"
		if pct >= 100 {
			status = "exceeded"
		} else if pct >= 80 {
			status = "warning"
		}

		resetAt := proxy.PeriodResetAt(b.Period, now, tz)

		label := strings.ToUpper(b.Scope[:1]) + b.Scope[1:]
		if b.Scope == "key" && b.ScopeID != "" {
			if kl, ok := keyLabels[b.ScopeID]; ok && kl != "" {
				label = kl
			} else {
				label = "Key: " + b.ScopeID[:min(8, len(b.ScopeID))] + "..."
			}
		} else if b.ScopeID != "" {
			label += ": " + b.ScopeID
		}

		entries = append(entries, BudgetStatusEntry{
			Scope:          b.Scope,
			ScopeID:        b.ScopeID,
			ScopeLabel:     label,
			Period:         b.Period,
			LimitUSD:       b.LimitUSD,
			SpendUSD:       spend,
			RemainingUSD:   remaining,
			UtilizationPct: pct,
			ResetAtFmt:     resetAt.In(tz).Format("Jan 2 3:04 PM"),
			Status:         status,
		})
	}

	return entries
}

// --------------------------------------------------------------------------
// Budget management handlers (P3)
// --------------------------------------------------------------------------

var validPeriods = map[string]bool{"daily": true, "weekly": true, "monthly": true}

// parseBudgetForm extracts and validates limit and period from a POST form.
func parseBudgetForm(r *http.Request) (float64, string, error) {
	limitStr := strings.TrimSpace(r.FormValue("limit"))
	period := strings.TrimSpace(r.FormValue("period"))

	if limitStr == "" {
		return 0, "", fmt.Errorf("limit is required")
	}
	if !validPeriods[period] {
		return 0, "", fmt.Errorf("period must be daily, weekly, or monthly")
	}

	var limit float64
	if _, err := fmt.Sscanf(limitStr, "%f", &limit); err != nil {
		return 0, "", fmt.Errorf("invalid limit value")
	}
	if limit <= 0 {
		return 0, "", fmt.Errorf("limit must be positive (use clear to remove)")
	}
	// Enforce max 2 decimal places.
	rounded := float64(int64(limit*100)) / 100
	if limit != rounded {
		return 0, "", fmt.Errorf("limit may have at most 2 decimal places")
	}

	return limit, period, nil
}

// renderBudgetFragment re-renders the budget_status fragment after a mutation.
func (h *CostHandlers) renderBudgetFragment(w http.ResponseWriter, r *http.Request) {
	sc := auth.SessionFromContext(r.Context())
	scopeType, scopeUserID, scopeTeamID := buildScopeParams(sc)

	budgets, err := h.budgetStore.GetBudgetsForScope(r.Context(), scopeType, scopeUserID, scopeTeamID)
	if err != nil {
		http.Error(w, "Failed to load budgets", http.StatusInternalServerError)
		return
	}

	entries := h.buildBudgetStatusEntries(r.Context(), budgets)
	isGA := sc != nil && (sc.IsMasterKey || sc.Role == "global_admin")
	isAdmin := sc != nil && (sc.IsMasterKey || sc.Role == "global_admin" || sc.Role == "team_admin")

	data := map[string]any{
		"BudgetEntries": entries,
		"IsGA":          isGA,
		"IsAdmin":       isAdmin,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteNamed(w, "budget_status", data); err != nil {
		slog.Error("render budget fragment", "error", err)
	}
}

// SetGlobalBudgetHandler handles POST /ui/api/budgets/global.
func (h *CostHandlers) SetGlobalBudgetHandler(w http.ResponseWriter, r *http.Request) {
	limit, period, err := parseBudgetForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.budgetAdminStore.SetGlobalBudget(r.Context(), limit, period); err != nil {
		http.Error(w, "Failed to set global budget", http.StatusInternalServerError)
		return
	}

	_ = h.budgetAdminStore.RecordAuditEvent(r.Context(), "budget.global.set",
		"", fmt.Sprintf("limit=$%.2f period=%s", limit, period), sessionActorEmail(r.Context()))

	h.renderBudgetFragment(w, r)
}

// ClearGlobalBudgetHandler handles POST /ui/api/budgets/global/clear.
func (h *CostHandlers) ClearGlobalBudgetHandler(w http.ResponseWriter, r *http.Request) {
	if err := h.budgetAdminStore.ClearGlobalBudget(r.Context()); err != nil {
		http.Error(w, "Failed to clear global budget", http.StatusInternalServerError)
		return
	}

	_ = h.budgetAdminStore.RecordAuditEvent(r.Context(), "budget.global.clear", "", "", sessionActorEmail(r.Context()))

	h.renderBudgetFragment(w, r)
}

// SetUserBudgetHandler handles POST /ui/api/budgets/user/{id}.
func (h *CostHandlers) SetUserBudgetHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		http.Error(w, "user ID required", http.StatusBadRequest)
		return
	}

	limit, period, err := parseBudgetForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.budgetAdminStore.SetUserBudget(r.Context(), userID, limit, period); err != nil {
		http.Error(w, "Failed to set user budget", http.StatusInternalServerError)
		return
	}

	_ = h.budgetAdminStore.RecordAuditEvent(r.Context(), "budget.user.set",
		"", fmt.Sprintf("user=%s limit=$%.2f period=%s", userID, limit, period), sessionActorEmail(r.Context()))

	h.renderBudgetFragment(w, r)
}

// ClearUserBudgetHandler handles POST /ui/api/budgets/user/{id}/clear.
func (h *CostHandlers) ClearUserBudgetHandler(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		http.Error(w, "user ID required", http.StatusBadRequest)
		return
	}

	if err := h.budgetAdminStore.ClearUserBudget(r.Context(), userID); err != nil {
		http.Error(w, "Failed to clear user budget", http.StatusInternalServerError)
		return
	}

	_ = h.budgetAdminStore.RecordAuditEvent(r.Context(), "budget.user.clear",
		"", fmt.Sprintf("user=%s", userID), sessionActorEmail(r.Context()))

	h.renderBudgetFragment(w, r)
}

// SetTeamBudgetHandler handles POST /ui/api/budgets/team/{id}.
func (h *CostHandlers) SetTeamBudgetHandler(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "id")
	if teamID == "" {
		http.Error(w, "team ID required", http.StatusBadRequest)
		return
	}

	limit, period, err := parseBudgetForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.budgetAdminStore.SetTeamBudget(r.Context(), teamID, limit, period); err != nil {
		http.Error(w, "Failed to set team budget", http.StatusInternalServerError)
		return
	}

	_ = h.budgetAdminStore.RecordAuditEvent(r.Context(), "budget.team.set",
		"", fmt.Sprintf("team=%s limit=$%.2f period=%s", teamID, limit, period), sessionActorEmail(r.Context()))

	h.renderBudgetFragment(w, r)
}

// ClearTeamBudgetHandler handles POST /ui/api/budgets/team/{id}/clear.
func (h *CostHandlers) ClearTeamBudgetHandler(w http.ResponseWriter, r *http.Request) {
	teamID := chi.URLParam(r, "id")
	if teamID == "" {
		http.Error(w, "team ID required", http.StatusBadRequest)
		return
	}

	if err := h.budgetAdminStore.ClearTeamBudget(r.Context(), teamID); err != nil {
		http.Error(w, "Failed to clear team budget", http.StatusInternalServerError)
		return
	}

	_ = h.budgetAdminStore.RecordAuditEvent(r.Context(), "budget.team.clear",
		"", fmt.Sprintf("team=%s", teamID), sessionActorEmail(r.Context()))

	h.renderBudgetFragment(w, r)
}

// SetKeyBudgetHandler handles POST /ui/api/budgets/key/{id}.
func (h *CostHandlers) SetKeyBudgetHandler(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "id")
	if keyID == "" {
		http.Error(w, "key ID required", http.StatusBadRequest)
		return
	}

	limit, period, err := parseBudgetForm(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if err := h.budgetAdminStore.SetKeyBudget(r.Context(), keyID, limit, period); err != nil {
		http.Error(w, "Failed to set key budget", http.StatusInternalServerError)
		return
	}

	_ = h.budgetAdminStore.RecordAuditEvent(r.Context(), "budget.key.set",
		"", fmt.Sprintf("key=%s limit=$%.2f period=%s", keyID, limit, period), sessionActorEmail(r.Context()))

	h.renderBudgetFragment(w, r)
}

// ClearKeyBudgetHandler handles POST /ui/api/budgets/key/{id}/clear.
func (h *CostHandlers) ClearKeyBudgetHandler(w http.ResponseWriter, r *http.Request) {
	keyID := chi.URLParam(r, "id")
	if keyID == "" {
		http.Error(w, "key ID required", http.StatusBadRequest)
		return
	}

	if err := h.budgetAdminStore.ClearKeyBudget(r.Context(), keyID); err != nil {
		http.Error(w, "Failed to clear key budget", http.StatusInternalServerError)
		return
	}

	_ = h.budgetAdminStore.RecordAuditEvent(r.Context(), "budget.key.clear",
		"", fmt.Sprintf("key=%s", keyID), sessionActorEmail(r.Context()))

	h.renderBudgetFragment(w, r)
}

// --------------------------------------------------------------------------
// Budgets page
// --------------------------------------------------------------------------

// BudgetsPageHandler renders the dedicated budget management page.
func (h *CostHandlers) BudgetsPageHandler(w http.ResponseWriter, r *http.Request) {
	sc := auth.SessionFromContext(r.Context())
	scopeType, scopeUserID, scopeTeamID := buildScopeParams(sc)

	budgets, err := h.budgetStore.GetBudgetsForScope(r.Context(), scopeType, scopeUserID, scopeTeamID)
	if err != nil {
		slog.Error("budgets page: fetch budgets", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	entries := h.buildBudgetStatusEntries(r.Context(), budgets)
	isGA := sc != nil && (sc.IsMasterKey || sc.Role == "global_admin")
	isAdmin := sc != nil && (sc.IsMasterKey || sc.Role == "global_admin" || sc.Role == "team_admin")

	// Fetch active keys for the key-budget selector dropdown.
	var keys []store.ProxyKeySummary
	if isAdmin && h.keyStore != nil {
		switch scopeType {
		case "all":
			keys, _ = h.keyStore.ListActiveProxyKeys(r.Context())
		case "team":
			if scopeTeamID != "" {
				keys, _ = h.keyStore.ListProxyKeysByTeam(r.Context(), scopeTeamID)
			}
		}
	}

	data := map[string]any{
		"ActiveTab":     "budgets",
		"Title":         "Budgets",
		"BudgetEntries": entries,
		"IsGA":          isGA,
		"IsAdmin":       isAdmin,
		"Keys":          keys,
	}

	setNavData(data, sc)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "budgets.html", data); err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}
