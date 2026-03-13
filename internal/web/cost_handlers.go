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
)

// CostHandlers groups the HTTP handlers for cost dashboard endpoints.
type CostHandlers struct {
	store         store.Store
	templates     *TemplateSet
	tz            *time.Location
	calc          *pricing.Calculator
	providerNames map[string]string // provider name or legacy raw key → display name
}

// NewCostHandlers creates a new CostHandlers with the given store, template set,
// display timezone (pass nil or time.UTC for UTC), and provider name map
// (provider name or legacy raw key → display name). Pass nil if no providers are configured.
func NewCostHandlers(s store.Store, tmpl *TemplateSet, tz *time.Location, calc *pricing.Calculator, providerNames map[string]string) *CostHandlers {
	if tz == nil {
		tz = time.UTC
	}
	return &CostHandlers{
		store:         s,
		templates:     tmpl,
		tz:            tz,
		calc:          calc,
		providerNames: providerNames,
	}
}

// --------------------------------------------------------------------------
// JSON API responses
// --------------------------------------------------------------------------

type costSummaryResponse struct {
	TotalCostUSD             float64                  `json:"total_cost_usd"`
	TotalInputTokens         int64                    `json:"total_input_tokens"`
	TotalOutputTokens        int64                    `json:"total_output_tokens"`
	TotalCacheCreationTokens int64                    `json:"total_cache_creation_tokens"`
	TotalCacheReadTokens     int64                    `json:"total_cache_read_tokens"`
	TotalRequests            int64                    `json:"total_requests"`
	Breakdown                []costBreakdownEntryJSON `json:"breakdown"`
	From                     string                   `json:"from"`
	To                       string                   `json:"to"`
}

type costBreakdownEntryJSON struct {
	Group               string  `json:"group"`
	CostUSD             float64 `json:"cost_usd"`
	InputTokens         int64   `json:"input_tokens"`
	OutputTokens        int64   `json:"output_tokens"`
	CacheCreationTokens int64   `json:"cache_creation_tokens"`
	CacheReadTokens     int64   `json:"cache_read_tokens"`
	Requests            int64   `json:"requests"`
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

	summary, err := h.store.GetCostSummary(r.Context(), params)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	breakdown, err := h.store.GetCostBreakdown(r.Context(), params)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}
	if params.GroupBy == "provider" {
		breakdown = aggregateProviderBreakdown(breakdown, h.providerNames)
	}

	pricingGroups, err := h.store.GetCostPricingGroups(r.Context(), params)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	tokenCosts := computeAggregateCostBreakdownWithAliases(pricingGroups, h.calc, h.providerNames)
	breakdownCosts := buildBreakdownCosts(pricingGroups, h.calc, params.GroupBy, h.providerNames)

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
	}

	// Strip time portion from params for the response.
	fromDate := r.URL.Query().Get("from")
	if fromDate == "" {
		fromDate = time.Now().In(h.tz).AddDate(0, 0, -30).Format("2006-01-02")
	}
	toDate := r.URL.Query().Get("to")
	if toDate == "" {
		toDate = time.Now().In(h.tz).Format("2006-01-02")
	}

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

	fromDate := r.URL.Query().Get("from")
	if fromDate == "" {
		fromDate = time.Now().In(h.tz).AddDate(0, 0, -30).Format("2006-01-02")
	}
	toDate := r.URL.Query().Get("to")
	if toDate == "" {
		toDate = time.Now().In(h.tz).Format("2006-01-02")
	}

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
	if interval == "day" || len(entries) == 0 {
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

func aggregateProviderBreakdown(entries []store.CostBreakdownEntry, providerNames map[string]string) []store.CostBreakdownEntry {
	if len(entries) == 0 || len(providerNames) == 0 {
		return entries
	}

	combined := make(map[string]store.CostBreakdownEntry, len(entries))
	for _, entry := range entries {
		name, _ := providerDisplayName(entry.Group, providerNames)

		agg := combined[name]
		agg.Group = name
		agg.InputTokens += entry.InputTokens
		agg.OutputTokens += entry.OutputTokens
		agg.CacheCreationTokens += entry.CacheCreationTokens
		agg.CacheReadTokens += entry.CacheReadTokens
		agg.Requests += entry.Requests
		combined[name] = agg
	}

	result := make([]store.CostBreakdownEntry, 0, len(combined))
	for _, entry := range combined {
		result = append(result, entry)
	}

	slices.SortFunc(result, func(a, b store.CostBreakdownEntry) int {
		switch {
		case a.Requests > b.Requests:
			return -1
		case a.Requests < b.Requests:
			return 1
		case a.Group < b.Group:
			return -1
		case a.Group > b.Group:
			return 1
		default:
			return 0
		}
	})

	return result
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
// T052: GET /ui/costs — render full cost dashboard page
// --------------------------------------------------------------------------

// CostsPageHandler renders the full cost dashboard HTML page.
func (h *CostHandlers) CostsPageHandler(w http.ResponseWriter, r *http.Request) {
	params := h.costParamsForRequest(r)
	applyScopeToCostParams(auth.SessionFromContext(r.Context()), &params)

	summary, err := h.store.GetCostSummary(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	breakdown, err := h.store.GetCostBreakdown(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if params.GroupBy == "provider" {
		breakdown = aggregateProviderBreakdown(breakdown, h.providerNames)
	}

	pricingGroups, err := h.store.GetCostPricingGroups(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	timeseries, err := h.store.GetCostTimeseries(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	var maxRequests int64
	for _, e := range timeseries {
		if e.Requests > maxRequests {
			maxRequests = e.Requests
		}
	}
	var maxBreakdownRequests int64
	for _, e := range breakdown {
		if e.Requests > maxBreakdownRequests {
			maxBreakdownRequests = e.Requests
		}
	}

	fromDate := r.URL.Query().Get("from")
	if fromDate == "" {
		fromDate = time.Now().In(h.tz).AddDate(0, 0, -30).Format("2006-01-02")
	}
	toDate := r.URL.Query().Get("to")
	if toDate == "" {
		toDate = time.Now().In(h.tz).Format("2006-01-02")
	}

	groupBy := r.URL.Query().Get("group_by")
	if groupBy == "" {
		groupBy = "model"
	}
	groupFilter := r.URL.Query().Get("filter")
	if groupBy == "key" && groupFilter == "" {
		groupFilter = r.URL.Query().Get("key")
	}

	// Check for incomplete data (rows with 0 tokens but requests).
	hasIncompleteData := false
	for _, e := range breakdown {
		if e.InputTokens == 0 && e.OutputTokens == 0 && e.Requests > 0 {
			hasIncompleteData = true
			break
		}
	}

	tokenCosts := computeAggregateCostBreakdownWithAliases(pricingGroups, h.calc, h.providerNames)
	breakdownCosts := buildBreakdownCosts(pricingGroups, h.calc, groupBy, h.providerNames)

	data := map[string]any{
		"ActiveTab":            "costs",
		"Title":                "Cost Dashboard",
		"Summary":              summary,
		"TokenCosts":           tokenCosts,
		"Breakdown":            breakdown,
		"BreakdownCosts":       breakdownCosts,
		"Timeseries":           timeseries,
		"MaxRequests":          float64(maxRequests),
		"MaxBreakdownRequests": float64(maxBreakdownRequests),
		"HasIncompleteData":    hasIncompleteData,
		"From":                 fromDate,
		"To":                   toDate,
		"GroupBy":              groupBy,
		"GroupFilter":          groupFilter,
		"TotalAllInputTokens":  summary.TotalInputTokens + summary.TotalCacheCreationTokens + summary.TotalCacheReadTokens,
		"ProviderNames":        h.providerNames,
	}

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
	params := h.costParamsForRequest(r)
	applyScopeToCostParams(auth.SessionFromContext(r.Context()), &params)

	summary, err := h.store.GetCostSummary(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	breakdown, err := h.store.GetCostBreakdown(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if params.GroupBy == "provider" {
		breakdown = aggregateProviderBreakdown(breakdown, h.providerNames)
	}

	pricingGroups, err := h.store.GetCostPricingGroups(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	timeseries, err := h.store.GetCostTimeseries(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	var maxRequests int64
	for _, e := range timeseries {
		if e.Requests > maxRequests {
			maxRequests = e.Requests
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

	tokenCosts := computeAggregateCostBreakdown(pricingGroups, h.calc)
	breakdownCosts := buildBreakdownCosts(pricingGroups, h.calc, groupBy, h.providerNames)

	data := map[string]any{
		"Summary":              summary,
		"TokenCosts":           tokenCosts,
		"Breakdown":            breakdown,
		"BreakdownCosts":       breakdownCosts,
		"Timeseries":           timeseries,
		"MaxRequests":          float64(maxRequests),
		"MaxBreakdownRequests": float64(maxBreakdownRequests),
		"HasIncompleteData":    hasIncompleteData,
		"GroupBy":              groupBy,
		"TotalAllInputTokens":  summary.TotalInputTokens + summary.TotalCacheCreationTokens + summary.TotalCacheReadTokens,
		"ProviderNames":        h.providerNames,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteNamed(w, "cost_summary", data); err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}
