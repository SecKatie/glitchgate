// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/store"
	"github.com/seckatie/glitchgate/internal/web/billing"
	"github.com/seckatie/glitchgate/internal/web/conversation"
)

// LogDetailData is the top-level struct passed to log_detail.html.
type LogDetailData struct {
	ActiveTab    string
	CurrentUser  string
	Log          *store.RequestLogDetail
	Conversation *conversation.Data
	Cost         *billing.CostBreakdown
}

// LogsPage renders the log list page.
func (h *Handlers) LogsPage(w http.ResponseWriter, r *http.Request) {
	params := parseLogParams(r)
	applyScopeToParams(auth.SessionFromContext(r.Context()), &params)

	logs, total, err := h.store.ListRequestLogs(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	models, err := h.store.ListDistinctModels(r.Context())
	if err != nil {
		slog.Warn("list distinct models", "error", err)
		models = []string{}
	}

	statuses, err := h.store.ListDistinctStatuses(r.Context())
	if err != nil {
		slog.Warn("list distinct statuses", "error", err)
		statuses = []int{}
	}

	perPage := params.PerPage
	if perPage <= 0 {
		perPage = 50
	}
	totalInt := int64(perPage) // safe: perPage is validated > 0
	totalPages := int(total / totalInt)
	if total%totalInt > 0 {
		totalPages++
	}
	if totalPages == 0 {
		totalPages = 1
	}

	firstID := ""
	if len(logs) > 0 {
		firstID = logs[0].ID
	}

	data := map[string]any{
		"ActiveTab":    "logs",
		"Logs":         billing.EnrichLogs(logs, h.calc),
		"Page":         params.Page,
		"TotalPages":   totalPages,
		"Total":        total,
		"FirstID":      firstID,
		"Model":        params.Model,
		"Models":       models,
		"Statuses":     statuses,
		"StatusFilter": strconv.Itoa(params.Status),
		"KeyPrefix":    params.KeyPrefix,
		"From":         params.From,
		"To":           params.To,
	}

	setNavData(data, auth.SessionFromContext(r.Context()))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := h.templates.ExecuteTemplate(w, "logs.html", data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// LogsAPIHandler returns log data as JSON or an HTMX HTML fragment.
func (h *Handlers) LogsAPIHandler(w http.ResponseWriter, r *http.Request) {
	params := parseLogParams(r)
	applyScopeToParams(auth.SessionFromContext(r.Context()), &params)

	logs, total, err := h.store.ListRequestLogs(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	// HTMX requests get an HTML fragment.
	if r.Header.Get("HX-Request") == "true" {
		perPage := params.PerPage
		if perPage <= 0 {
			perPage = 50
		}
		totalInt := int64(perPage)
		totalPages := int(total / totalInt)
		if total%totalInt > 0 {
			totalPages++
		}

		// If since_id is provided and we're on page > 1, count new entries.
		sinceID := r.URL.Query().Get("since_id")
		if sinceID != "" && params.Page > 1 {
			count, err := h.store.CountLogsSince(r.Context(), sinceID, params)
			if err != nil {
				slog.Warn("count logs since ID", "error", err)
			} else {
				w.Header().Set("X-New-Count", strconv.FormatInt(count, 10))
			}
		}

		firstID := ""
		if len(logs) > 0 {
			firstID = logs[0].ID
		}
		data := map[string]any{
			"Logs":       billing.EnrichLogs(logs, h.calc),
			"Page":       params.Page,
			"TotalPages": totalPages,
			"Total":      total,
			"FirstID":    firstID,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.templates.ExecuteNamed(w, "log_fragment", data); err != nil {
			slog.Error("render log_rows fragment", "error", err)
		}
		return
	}

	// JSON response.
	perPage := params.PerPage
	if perPage <= 0 {
		perPage = 50
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"logs":     logs,
		"total":    total,
		"page":     params.Page,
		"per_page": perPage,
	}); err != nil {
		slog.Error("write logs JSON response", "error", err)
	}
}

// LogDetailPage renders the log detail page.
func (h *Handlers) LogDetailPage(w http.ResponseWriter, r *http.Request, id string) {
	logEntry, err := h.store.GetRequestLog(r.Context(), id)
	if err != nil {
		http.Error(w, "Log not found", http.StatusNotFound)
		return
	}

	if !h.canAccessLogEntry(r, logEntry) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	conv := conversation.ParseConversation(logEntry.RequestBody, logEntry.ResponseBody, logEntry.SourceFormat)
	costBreakdown := billing.ComputeCostBreakdown(logEntry, h.calc)

	data := LogDetailData{
		ActiveTab:    "logs",
		Log:          logEntry,
		Conversation: conv,
		Cost:         costBreakdown,
	}
	if sc := auth.SessionFromContext(r.Context()); sc != nil {
		if sc.IsMasterKey {
			data.CurrentUser = "admin"
		} else if sc.User != nil {
			data.CurrentUser = sc.User.DisplayName
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "log_detail.html", data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// LogDetailAPIHandler returns a single log entry as JSON.
func (h *Handlers) LogDetailAPIHandler(w http.ResponseWriter, r *http.Request, id string) {
	logEntry, err := h.store.GetRequestLog(r.Context(), id)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		if _, err := w.Write([]byte(`{"error":"Log not found"}`)); err != nil {
			slog.Error("write log-not-found response", "error", err)
		}
		return
	}

	if !h.canAccessLogEntry(r, logEntry) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(logEntry); err != nil {
		slog.Error("write log detail response", "error", err)
	}
}

// canAccessLogEntry checks whether the current session is allowed to view a
// log entry. Returns true for GA/master-key sessions or when the log's key
// prefix appears in the session's visible key set.
func (h *Handlers) canAccessLogEntry(r *http.Request, logEntry *store.RequestLogDetail) bool {
	sc := auth.SessionFromContext(r.Context())
	if sc == nil || sc.IsMasterKey || sc.Role == "global_admin" {
		return true
	}
	scopeType, _, _ := buildScopeParams(sc)
	if scopeType == "all" {
		return true
	}
	visibleKeys, err := h.listKeysForSession(r)
	if err != nil {
		return false
	}
	for _, k := range visibleKeys {
		if k.KeyPrefix == logEntry.ProxyKeyPrefix {
			return true
		}
	}
	return false
}

func parseLogParams(r *http.Request) store.ListLogsParams {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page <= 0 {
		page = 1
	}
	perPage, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if perPage <= 0 {
		perPage = 50
	}
	status, _ := strconv.Atoi(r.URL.Query().Get("status"))

	keyPrefix := r.URL.Query().Get("key_prefix")
	if keyPrefix == "" {
		keyPrefix = r.URL.Query().Get("key")
	}

	return store.ListLogsParams{
		Page:      page,
		PerPage:   perPage,
		Model:     r.URL.Query().Get("model"),
		Status:    status,
		KeyPrefix: keyPrefix,
		From:      r.URL.Query().Get("from"),
		To:        r.URL.Query().Get("to"),
		Sort:      r.URL.Query().Get("sort"),
		Order:     r.URL.Query().Get("order"),
		ScopeType: "all",
	}
}

func parseCommaSeparated(s string) []string {
	parts := strings.Split(s, ",")
	var out []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
