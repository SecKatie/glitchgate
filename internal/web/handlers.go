// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strconv"
	"time"

	"codeberg.org/kglitchy/llm-proxy/internal/auth"
	"codeberg.org/kglitchy/llm-proxy/internal/pricing"
	"codeberg.org/kglitchy/llm-proxy/internal/store"
)

// TemplateSet holds per-page template clones so that each page can define its
// own "title", "content", and "head" blocks without colliding.
type TemplateSet struct {
	pages map[string]*template.Template
}

// ExecuteTemplate renders the named page template into w.
func (ts *TemplateSet) ExecuteTemplate(w http.ResponseWriter, name string, data any) error {
	t, ok := ts.pages[name]
	if !ok {
		return fmt.Errorf("template %q not found", name)
	}
	return t.ExecuteTemplate(w, name, data)
}

// ExecuteNamed renders a named sub-template (e.g. a fragment like "log_rows")
// from any of the parsed page templates.
func (ts *TemplateSet) ExecuteNamed(w http.ResponseWriter, name string, data any) error {
	// Fragments are available in every page clone; pick any.
	for _, t := range ts.pages {
		if sub := t.Lookup(name); sub != nil {
			return sub.Execute(w, data)
		}
	}
	return fmt.Errorf("template %q not found", name)
}

// Handlers groups the HTTP handlers for the web UI.
type Handlers struct {
	store     store.Store
	sessions  *auth.SessionStore
	masterKey string
	templates *TemplateSet
	calc      *pricing.Calculator
}

// NewHandlers creates web UI handlers.
func NewHandlers(s store.Store, sessions *auth.SessionStore, masterKey string, calc *pricing.Calculator, tmpl *TemplateSet) *Handlers {
	return &Handlers{
		store:     s,
		sessions:  sessions,
		masterKey: masterKey,
		templates: tmpl,
		calc:      calc,
	}
}

// templateFuncs returns the shared template function map.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"deref": func(p any) any {
			switch v := p.(type) {
			case *float64:
				if v != nil {
					return *v
				}
				return float64(0)
			case *string:
				if v != nil {
					return *v
				}
				return ""
			default:
				return p
			}
		},
		"minus":    func(a, b int) int { return a - b },
		"plus":     func(a, b int) int { return a + b },
		"subtract": func(a, b int) int { return a - b },
		"add":      func(a, b int) int { return a + b },
		"addInt64": func(a, b int64) int64 { return a + b },
		"sumTokens": func(vals ...int64) int64 {
			var total int64
			for _, v := range vals {
				total += v
			}
			return total
		},
		"wrapTurn": func(t *ConvTurn) []ConvTurn {
			if t == nil {
				return nil
			}
			return []ConvTurn{*t}
		},
		"barPct": func(value, total float64) float64 {
			if total <= 0 {
				return 0
			}
			return (value / total) * 100
		},
		"shortDate": func(dateStr string) string {
			if len(dateStr) >= 10 {
				return dateStr[5:10] // "MM-DD"
			}
			return dateStr
		},
	}
}

// ParseTemplates parses per-page template sets from the embedded filesystem.
// Each page template is cloned from a base that includes layout and fragments,
// ensuring that block definitions (title, content, head) don't collide.
func ParseTemplates() *TemplateSet {
	funcs := templateFuncs()

	// Parse the shared templates (layout + fragments) into a base.
	base := template.Must(
		template.New("base").Funcs(funcs).ParseFS(Templates,
			"templates/layout.html",
			"templates/fragments/*.html",
		),
	)

	// Discover all page templates (everything in templates/ except layout).
	pageFiles, err := fs.Glob(Templates, "templates/*.html")
	if err != nil {
		panic(fmt.Sprintf("glob page templates: %v", err))
	}

	ts := &TemplateSet{pages: make(map[string]*template.Template)}

	for _, path := range pageFiles {
		name := path[len("templates/"):] // e.g. "logs.html"
		if name == "layout.html" {
			continue
		}

		// Clone the base set and parse this page into the clone.
		clone := template.Must(base.Clone())
		content, err := fs.ReadFile(Templates, path)
		if err != nil {
			panic(fmt.Sprintf("read template %s: %v", path, err))
		}
		template.Must(clone.New(name).Parse(string(content)))
		ts.pages[name] = clone
	}

	return ts
}

// ---------------------------------------------------------------------------
// Login / logout
// ---------------------------------------------------------------------------

// LoginPage renders the login form.
func (h *Handlers) LoginPage(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "login.html", map[string]any{"Error": ""}); err != nil {
		log.Printf("ERROR: render login page: %v", err)
	}
}

// LoginHandler processes the login form submission.
func (h *Handlers) LoginHandler(w http.ResponseWriter, r *http.Request) {
	// Limit request body to 1MB to prevent memory exhaustion.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	// Support both form and JSON.
	masterKey := r.FormValue("master_key")
	if masterKey == "" {
		var body struct {
			MasterKey string `json:"master_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			log.Printf("WARNING: decode login body: %v", err)
		}
		masterKey = body.MasterKey
	}

	if masterKey != h.masterKey {
		contentType := r.Header.Get("Content-Type")
		if contentType == "application/json" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			if _, err := w.Write([]byte(`{"error":"Invalid master key"}`)); err != nil {
				log.Printf("ERROR: write login error response: %v", err)
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.templates.ExecuteTemplate(w, "login.html", map[string]any{"Error": "Invalid master key"}); err != nil {
			log.Printf("ERROR: render login page: %v", err)
		}
		return
	}

	sess, err := h.sessions.Create()
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    sess.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  sess.ExpiresAt,
	})

	contentType := r.Header.Get("Content-Type")
	if contentType == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"session_token": sess.Token,
			"expires_at":    sess.ExpiresAt.Format(time.RFC3339),
		}); err != nil {
			log.Printf("ERROR: write login response: %v", err)
		}
		return
	}

	http.Redirect(w, r, "/ui/logs", http.StatusSeeOther)
}

// LogoutHandler invalidates the session and redirects to login.
func (h *Handlers) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("session"); err == nil {
		h.sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

// LogsPage renders the log list page.
func (h *Handlers) LogsPage(w http.ResponseWriter, r *http.Request) {
	params := parseLogParams(r)

	logs, total, err := h.store.ListRequestLogs(r.Context(), params)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
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
		"Logs":         logs,
		"Page":         params.Page,
		"TotalPages":   totalPages,
		"Total":        total,
		"FirstID":      firstID,
		"Model":        params.Model,
		"StatusFilter": strconv.Itoa(params.Status),
		"KeyPrefix":    params.KeyPrefix,
		"From":         params.From,
		"To":           params.To,
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "logs.html", data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// LogsAPIHandler returns log data as JSON or an HTMX HTML fragment.
func (h *Handlers) LogsAPIHandler(w http.ResponseWriter, r *http.Request) {
	params := parseLogParams(r)

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
				log.Printf("WARNING: count logs since <id>: %v", err) //nolint:gosec // G706: sinceID not included in log output
			} else {
				w.Header().Set("X-New-Count", strconv.FormatInt(count, 10))
			}
		}

		data := map[string]any{
			"Logs":       logs,
			"Page":       params.Page,
			"TotalPages": totalPages,
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.templates.ExecuteNamed(w, "log_rows", data); err != nil {
			log.Printf("ERROR: render log_rows fragment: %v", err)
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
		log.Printf("ERROR: write logs JSON response: %v", err)
	}
}

// LogDetailPage renders the log detail page.
func (h *Handlers) LogDetailPage(w http.ResponseWriter, r *http.Request, id string) {
	logEntry, err := h.store.GetRequestLog(r.Context(), id)
	if err != nil {
		http.Error(w, "Log not found", http.StatusNotFound)
		return
	}

	conv := parseConversation(logEntry.RequestBody, logEntry.ResponseBody)
	costBreakdown := computeCostBreakdown(logEntry, h.calc)

	data := LogDetailData{
		ActiveTab:    "logs",
		Log:          logEntry,
		Conversation: conv,
		Cost:         costBreakdown,
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
			log.Printf("ERROR: write log-not-found response: %v", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(logEntry); err != nil {
		log.Printf("ERROR: write log detail response: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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
	}
}
