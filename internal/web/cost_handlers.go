// SPDX-License-Identifier: AGPL-3.0-or-later

// Package web provides HTTP handlers and templates for the embedded web UI.
package web

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/auth"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

// CostHandlers groups the HTTP handlers for cost dashboard endpoints.
type CostHandlers struct {
	store     store.Store
	templates *TemplateSet
	tz        *time.Location
}

// NewCostHandlers creates a new CostHandlers with the given store, template set,
// and display timezone (pass nil or time.UTC for UTC).
func NewCostHandlers(s store.Store, tmpl *TemplateSet, tz *time.Location) *CostHandlers {
	if tz == nil {
		tz = time.UTC
	}
	return &CostHandlers{
		store:     s,
		templates: tmpl,
		tz:        tz,
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

// --------------------------------------------------------------------------
// Helper: parse cost query parameters
// --------------------------------------------------------------------------

func (h *CostHandlers) parseCostParams(r *http.Request) store.CostParams {
	from := r.URL.Query().Get("from")
	to := r.URL.Query().Get("to")
	groupBy := r.URL.Query().Get("group_by")
	keyPrefix := r.URL.Query().Get("key")

	// Default: last 30 days.
	if from == "" {
		from = time.Now().In(h.tz).AddDate(0, 0, -30).Format("2006-01-02")
	}
	if to == "" {
		to = time.Now().In(h.tz).Format("2006-01-02")
	}
	if groupBy == "" {
		groupBy = "model"
	}

	return store.CostParams{
		From:      from,
		To:        to + " 23:59:59", // Make the end date inclusive of the full day.
		GroupBy:   groupBy,
		KeyPrefix: keyPrefix,
	}
}

// --------------------------------------------------------------------------
// T050: GET /ui/api/costs — JSON cost summary with breakdown
// --------------------------------------------------------------------------

// CostSummaryHandler returns aggregated cost data with a breakdown grouped
// by model or key. Query parameters: from, to, group_by (model|key), key.
func (h *CostHandlers) CostSummaryHandler(w http.ResponseWriter, r *http.Request) {
	params := h.parseCostParams(r)
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

	bd := make([]costBreakdownEntryJSON, len(breakdown))
	for i, e := range breakdown {
		bd[i] = costBreakdownEntryJSON{
			Group:               e.Group,
			CostUSD:             e.CostUSD,
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
		TotalCostUSD:             summary.TotalCostUSD,
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
		log.Printf("ERROR: write cost summary response: %v", err)
	}
}

// --------------------------------------------------------------------------
// T051: GET /ui/api/costs/timeseries — JSON timeseries data
// --------------------------------------------------------------------------

// CostTimeseriesHandler returns cost data bucketed over time.
// Query parameters: from, to, interval (day|week|month), key.
func (h *CostHandlers) CostTimeseriesHandler(w http.ResponseWriter, r *http.Request) {
	params := h.parseCostParams(r)
	applyScopeToCostParams(auth.SessionFromContext(r.Context()), &params)

	interval := r.URL.Query().Get("interval")
	if interval == "" {
		interval = "day"
	}

	entries, err := h.store.GetCostTimeseries(r.Context(), params)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusInternalServerError)
		return
	}

	// For week/month intervals, aggregate the daily data into larger buckets.
	aggregated := aggregateTimeseries(entries, interval)

	data := make([]costTimeseriesEntryJSON, len(aggregated))
	for i, e := range aggregated {
		data[i] = costTimeseriesEntryJSON{
			Date:     e.Date,
			CostUSD:  e.CostUSD,
			Requests: e.Requests,
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

	resp := costTimeseriesResponse{
		Interval: interval,
		Data:     data,
		From:     fromDate,
		To:       toDate,
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("ERROR: write cost timeseries response: %v", err)
	}
}

// aggregateTimeseries groups daily entries into week or month buckets.
// For "day" interval, entries are returned as-is.
func aggregateTimeseries(entries []store.CostTimeseriesEntry, interval string) []store.CostTimeseriesEntry {
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
			// ISO week: start on Monday.
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

	type bucket struct {
		key      string
		costUSD  float64
		requests int64
	}

	var buckets []bucket
	seen := map[string]int{} // key -> index in buckets

	for _, e := range entries {
		bk := bucketKey(e.Date)
		if idx, ok := seen[bk]; ok {
			buckets[idx].costUSD += e.CostUSD
			buckets[idx].requests += e.Requests
		} else {
			seen[bk] = len(buckets)
			buckets = append(buckets, bucket{
				key:      bk,
				costUSD:  e.CostUSD,
				requests: e.Requests,
			})
		}
	}

	result := make([]store.CostTimeseriesEntry, len(buckets))
	for i, b := range buckets {
		result[i] = store.CostTimeseriesEntry{
			Date:     b.key,
			CostUSD:  b.costUSD,
			Requests: b.requests,
		}
	}
	return result
}

// --------------------------------------------------------------------------
// T052: GET /ui/costs — render full cost dashboard page
// --------------------------------------------------------------------------

// CostsPageHandler renders the full cost dashboard HTML page.
func (h *CostHandlers) CostsPageHandler(w http.ResponseWriter, r *http.Request) {
	params := h.parseCostParams(r)
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

	timeseries, err := h.store.GetCostTimeseries(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// Determine max cost for bar chart scaling.
	var maxCost float64
	for _, e := range timeseries {
		if e.CostUSD > maxCost {
			maxCost = e.CostUSD
		}
	}

	// Determine max breakdown cost for bar chart scaling.
	var maxBreakdownCost float64
	for _, e := range breakdown {
		if e.CostUSD > maxBreakdownCost {
			maxBreakdownCost = e.CostUSD
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

	// Check for incomplete data (rows with 0 tokens but requests).
	hasIncompleteData := false
	for _, e := range breakdown {
		if e.InputTokens == 0 && e.OutputTokens == 0 && e.Requests > 0 {
			hasIncompleteData = true
			break
		}
	}

	data := map[string]any{
		"ActiveTab":         "costs",
		"Title":             "Cost Dashboard",
		"Summary":           summary,
		"Breakdown":         breakdown,
		"Timeseries":        timeseries,
		"MaxCost":           maxCost,
		"MaxBreakdownCost":  maxBreakdownCost,
		"HasIncompleteData": hasIncompleteData,
		"From":              fromDate,
		"To":                toDate,
		"GroupBy":           groupBy,
		"KeyFilter":         r.URL.Query().Get("key"),
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
	params := h.parseCostParams(r)
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

	timeseries, err := h.store.GetCostTimeseries(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	var maxCost float64
	for _, e := range timeseries {
		if e.CostUSD > maxCost {
			maxCost = e.CostUSD
		}
	}

	var maxBreakdownCost float64
	for _, e := range breakdown {
		if e.CostUSD > maxBreakdownCost {
			maxBreakdownCost = e.CostUSD
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

	data := map[string]any{
		"Summary":           summary,
		"Breakdown":         breakdown,
		"Timeseries":        timeseries,
		"MaxCost":           maxCost,
		"MaxBreakdownCost":  maxBreakdownCost,
		"HasIncompleteData": hasIncompleteData,
		"GroupBy":           groupBy,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteNamed(w, "cost_summary", data); err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}
