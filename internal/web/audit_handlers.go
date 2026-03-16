// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/store"
)

// AuditHandlers groups the HTTP handlers for the audit log page.
type AuditHandlers struct {
	store     store.AuditStore
	templates *TemplateSet
	tz        *time.Location

	actionsMu        sync.RWMutex
	actionsCache     []string
	actionsCacheTime time.Time
}

// NewAuditHandlers creates a new AuditHandlers.
func NewAuditHandlers(s store.AuditStore, tmpl *TemplateSet, tz *time.Location) *AuditHandlers {
	if tz == nil {
		tz = time.UTC
	}
	return &AuditHandlers{store: s, templates: tmpl, tz: tz}
}

const (
	auditPageSize        = 50
	auditActionsCacheTTL = 60 * time.Second
)

func (h *AuditHandlers) cachedActions(r *http.Request) []string {
	h.actionsMu.RLock()
	if h.actionsCache != nil && time.Since(h.actionsCacheTime) < auditActionsCacheTTL {
		cached := h.actionsCache
		h.actionsMu.RUnlock()
		return cached
	}
	h.actionsMu.RUnlock()

	actions, err := h.store.ListDistinctAuditActions(r.Context())
	if err != nil {
		slog.Error("audit page: list actions", "error", err)
		return []string{}
	}

	h.actionsMu.Lock()
	h.actionsCache = actions
	h.actionsCacheTime = time.Now()
	h.actionsMu.Unlock()
	return actions
}

// AuditPageHandler renders the audit log page (GA-only).
func (h *AuditHandlers) AuditPageHandler(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}

	fromDate := q.Get("from")
	toDate := q.Get("to")

	// Convert local date strings to UTC datetime boundaries.
	fromUTC := ""
	toUTC := ""
	if fromDate != "" {
		if t, err := time.ParseInLocation("2006-01-02", fromDate, h.tz); err == nil {
			fromUTC = t.UTC().Format("2006-01-02 15:04:05")
		}
	}
	if toDate != "" {
		if t, err := time.ParseInLocation("2006-01-02", toDate, h.tz); err == nil {
			toUTC = t.AddDate(0, 0, 1).Add(-time.Second).UTC().Format("2006-01-02 15:04:05")
		}
	}

	params := store.ListAuditParams{
		Action: q.Get("action"),
		From:   fromUTC,
		To:     toUTC,
		Page:   page,
		Limit:  auditPageSize,
	}

	events, total, err := h.store.ListAuditEvents(r.Context(), params)
	if err != nil {
		slog.Error("audit page: list events", "error", err)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	actions := h.cachedActions(r)

	totalPages := int(total) / auditPageSize
	if int(total)%auditPageSize > 0 {
		totalPages++
	}
	if totalPages < 1 {
		totalPages = 1
	}

	data := map[string]any{
		"ActiveTab":    "audit",
		"Title":        "Audit Log",
		"Events":       events,
		"Actions":      actions,
		"FilterAction": params.Action,
		"From":         fromDate,
		"To":           toDate,
		"Page":         page,
		"TotalPages":   totalPages,
		"TotalEvents":  total,
		"Tz":           h.tz,
	}

	setNavData(data, auth.SessionFromContext(r.Context()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "audit.html", data); err != nil {
		http.Error(w, fmt.Sprintf("Template error: %v", err), http.StatusInternalServerError)
	}
}
