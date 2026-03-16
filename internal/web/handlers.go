// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/store"
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

// HandlersStore combines the store operations needed by the main web handlers
// (logs, keys, models, and audit events).
type HandlersStore interface {
	store.ProxyKeyStore
	store.RequestLogStore
	store.ModelUsageStore
	RecordAuditEvent(ctx context.Context, action, keyPrefix, detail, actorEmail string) error
}

// Handlers groups the HTTP handlers for the web UI.
type Handlers struct {
	store         HandlersStore
	sessions      *auth.UISessionStore
	masterKey     string
	templates     *TemplateSet
	calc          *pricing.Calculator
	oidc          OIDCProvider // nil when OIDC not configured
	cfg           *config.Config
	providerMap   map[string]config.ProviderConfig // name → config for O(1) lookup
	providerNames map[string]string

	// TTL cache for GetAllModelUsageSummaries (models list page).
	modelUsageMu        sync.RWMutex
	modelUsageCache     map[string]*store.ModelUsageSummary
	modelUsageCacheTime time.Time
}

// OIDCProvider is the minimal interface Handlers needs from the OIDC package.
// Using an interface here avoids a circular import and allows easy test fakes.
type OIDCProvider interface {
	Enabled() bool
}

// NewHandlers creates web UI handlers.
func NewHandlers(s HandlersStore, sessions *auth.UISessionStore, masterKey string, calc *pricing.Calculator, tmpl *TemplateSet, oidcProvider OIDCProvider, cfg *config.Config, providerNames map[string]string) *Handlers {
	pm := make(map[string]config.ProviderConfig, len(cfg.Providers))
	for _, pc := range cfg.Providers {
		pm[pc.Name] = pc
	}
	return &Handlers{
		store:         s,
		sessions:      sessions,
		masterKey:     masterKey,
		templates:     tmpl,
		calc:          calc,
		oidc:          oidcProvider,
		cfg:           cfg,
		providerMap:   pm,
		providerNames: providerNames,
	}
}

// sessionActorEmail returns the email of the authenticated user for audit logging.
// For master-key sessions it returns "master_key"; for unauthenticated calls it returns "".
func sessionActorEmail(ctx context.Context) string {
	sess := auth.SessionFromContext(ctx)
	if sess == nil {
		return ""
	}
	if sess.IsMasterKey {
		return "master_key"
	}
	if sess.User != nil {
		return sess.User.Email
	}
	return ""
}

// templateFuncs returns the shared template function map for the given timezone.
func templateFuncs(tz *time.Location) template.FuncMap {
	if tz == nil {
		tz = time.UTC
	}
	return template.FuncMap{
		"fmtET": func(t time.Time, layout string) string {
			return t.In(tz).Format(layout)
		},
		"not": func(b bool) bool { return !b },
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
		"toFloat64": func(n int64) float64 { return float64(n) },
		"div": func(a, b float64) float64 {
			if b == 0 {
				return 0
			}
			return a / b
		},
		"barPct": func(value, total float64) float64 {
			if total <= 0 {
				return 0
			}
			pct := (value / total) * 100
			if pct > 0 && pct < 1 {
				return 1
			}
			return pct
		},
		"shortDate": func(dateStr string) string {
			if len(dateStr) >= 10 {
				return dateStr[5:10] // "MM-DD"
			}
			return dateStr
		},
		"fmtTokens": func(n int64) string {
			switch {
			case n >= 1_000_000_000:
				return fmt.Sprintf("%.1fB", float64(n)/1_000_000_000)
			case n >= 1_000_000:
				return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
			case n >= 1_000:
				return fmt.Sprintf("%.1fK", float64(n)/1_000)
			default:
				p := message.NewPrinter(language.English)
				return p.Sprintf("%d", n)
			}
		},
		"fmtPctInt64": func(part, total int64) string {
			if total <= 0 {
				return "0%"
			}
			return fmt.Sprintf("%.1f%%", (float64(part)/float64(total))*100)
		},
		"fmtPctFloat": func(part, total float64) string {
			if total <= 0 {
				return "0%"
			}
			return fmt.Sprintf("%.1f%%", (part/total)*100)
		},
		"urlPathEscape": url.PathEscape,
	}
}

// ParseTemplates parses per-page template sets from the embedded filesystem.
// Each page template is cloned from a base that includes layout and fragments,
// ensuring that block definitions (title, content, head) don't collide.
// tz is used by the fmtET template function; pass nil to use UTC.
func ParseTemplates(tz *time.Location) *TemplateSet {
	funcs := templateFuncs(tz)

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
func (h *Handlers) LoginPage(w http.ResponseWriter, r *http.Request) {
	// Redirect authenticated users directly to the main page.
	if c, err := r.Cookie("llmp_session"); err == nil && c.Value != "" {
		if sess, _ := h.sessions.Validate(r.Context(), c.Value); sess != nil {
			http.Redirect(w, r, "/ui/", http.StatusSeeOther)
			return
		}
	}

	oidcEnabled := h.oidc != nil && h.oidc.Enabled()
	showMasterKeyForm := !oidcEnabled || r.URL.Query().Get("master") == "1"

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "login.html", map[string]any{
		"Error":             "",
		"OIDCEnabled":       oidcEnabled,
		"ShowMasterKeyForm": showMasterKeyForm,
	}); err != nil {
		slog.Error("render login page", "error", err)
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
			slog.Warn("decode login body", "error", err)
		}
		masterKey = body.MasterKey
	}

	if masterKey != h.masterKey {
		if err := h.store.RecordAuditEvent(r.Context(), "master_key.login_failed", "", "", ""); err != nil {
			slog.Warn("record audit event", "error", err)
		}
		contentType := r.Header.Get("Content-Type")
		if contentType == "application/json" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			if _, err := w.Write([]byte(`{"error":"Invalid master key"}`)); err != nil {
				slog.Error("write login error response", "error", err)
			}
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.templates.ExecuteTemplate(w, "login.html", map[string]any{"Error": "Invalid master key"}); err != nil {
			slog.Error("render login page", "error", err)
		}
		return
	}

	sess, err := h.sessions.Create(r.Context(), "master_key", "")
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "llmp_session",
		Value:    sess.Token,
		Path:     "/ui",
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   28800,
	})

	if err := h.store.RecordAuditEvent(r.Context(), "master_key.login", "", "", "master_key"); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	contentType := r.Header.Get("Content-Type")
	if contentType == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"session_token": sess.Token,
			"expires_at":    sess.ExpiresAt.Format(time.RFC3339),
		}); err != nil {
			slog.Error("write login response", "error", err)
		}
		return
	}

	http.Redirect(w, r, "/ui/logs", http.StatusSeeOther)
}

// LogoutHandler invalidates the session and redirects to login.
func (h *Handlers) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie("llmp_session"); err == nil {
		if delErr := h.sessions.Delete(r.Context(), c.Value); delErr != nil {
			slog.Warn("delete session", "error", delErr)
		}
	}
	if err := h.store.RecordAuditEvent(r.Context(), "session.logout", "", "", sessionActorEmail(r.Context())); err != nil {
		slog.Warn("record audit event", "error", err)
	}
	for _, name := range []string{"llmp_session", "session"} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/ui",
			HttpOnly: true,
			MaxAge:   -1,
		})
	}
	http.Redirect(w, r, "/ui/login", http.StatusSeeOther)
}

// ---------------------------------------------------------------------------
// Logs
// ---------------------------------------------------------------------------

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
		"Logs":         enrichLogs(logs, h.calc),
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
			"Logs":       enrichLogs(logs, h.calc),
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

	conv := parseConversation(logEntry.RequestBody, logEntry.ResponseBody, logEntry.SourceFormat)
	costBreakdown := computeCostBreakdown(logEntry, h.calc)

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

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// validateLabel checks that a key label is non-empty and within the max length.
func validateLabel(label string) error {
	if label == "" {
		return fmt.Errorf("label is required")
	}
	if len(label) > 64 {
		return fmt.Errorf("label must be 64 characters or fewer")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Keys
// ---------------------------------------------------------------------------

// KeysPage renders the key management page.
func (h *Handlers) KeysPage(w http.ResponseWriter, r *http.Request) {
	keys, err := h.listKeysForSession(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"ActiveTab": "keys",
		"Keys":      keys,
	}
	setNavData(data, auth.SessionFromContext(r.Context()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "keys.html", data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// KeysAPIHandler returns key list as HTMX fragment or JSON.
func (h *Handlers) KeysAPIHandler(w http.ResponseWriter, r *http.Request) {
	keys, err := h.listKeysForSession(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		data := map[string]any{"Keys": keys}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.templates.ExecuteNamed(w, "key_rows", data); err != nil {
			slog.Error("render key_rows fragment", "error", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"keys": keys}); err != nil {
		slog.Error("write keys JSON response", "error", err)
	}
}

// listKeysForSession returns the keys visible to the current session.
func (h *Handlers) listKeysForSession(r *http.Request) ([]store.ProxyKeySummary, error) {
	scope := visibleKeyScope(auth.SessionFromContext(r.Context()))
	switch scope.scopeType {
	case "all":
		return h.store.ListActiveProxyKeys(r.Context())
	case "team":
		return h.store.ListProxyKeysByTeam(r.Context(), scope.scopeTeamID)
	case "user":
		return h.store.ListProxyKeysByOwner(r.Context(), scope.scopeUserID)
	default:
		return []store.ProxyKeySummary{}, nil
	}
}

// canMutateKey returns true if the session is allowed to modify the key with the given prefix.
// Global admins and master-key sessions can modify any key.
// Other users can only modify keys returned by listKeysForSession.
func (h *Handlers) canMutateKey(r *http.Request, prefix string) (bool, error) {
	sc := auth.SessionFromContext(r.Context())
	if sc == nil || sc.IsMasterKey || sc.Role == "global_admin" {
		return true, nil
	}
	visible, err := h.listKeysForSession(r)
	if err != nil {
		return false, err
	}
	for _, k := range visible {
		if k.KeyPrefix == prefix {
			return true, nil
		}
	}
	return false, nil
}

// CreateKeyHandler creates a new proxy key.
func (h *Handlers) CreateKeyHandler(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	label := r.FormValue("label")
	if err := validateLabel(label); err != nil {
		keys, _ := h.listKeysForSession(r)
		data := map[string]any{
			"ActiveTab":  "keys",
			"Keys":       keys,
			"LabelError": err.Error(),
			"LabelValue": label,
		}
		setNavData(data, auth.SessionFromContext(r.Context()))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusBadRequest)
		if err := h.templates.ExecuteTemplate(w, "keys.html", data); err != nil {
			slog.Error("render keys page with error", "error", err)
		}
		return
	}

	plaintext, hash, prefix, err := auth.GenerateKey()
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	id := uuid.New().String()
	sc := auth.SessionFromContext(r.Context())
	if sc != nil && !sc.IsMasterKey && sc.User != nil {
		if err := h.store.CreateProxyKeyForUser(r.Context(), id, hash, prefix, label, sc.User.ID); err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	} else {
		if err := h.store.CreateProxyKey(r.Context(), id, hash, prefix, label); err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	sc2 := auth.SessionFromContext(r.Context())
	auditAction := "key_created"
	if sc2 != nil && !sc2.IsMasterKey && sc2.User != nil {
		auditAction = "key.created_for_user"
	}
	if err := h.store.RecordAuditEvent(r.Context(), auditAction, prefix, label, sessionActorEmail(r.Context())); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	keys, err := h.listKeysForSession(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	data := map[string]any{
		"ActiveTab":     "keys",
		"Keys":          keys,
		"CreatedKey":    plaintext,
		"CreatedPrefix": prefix,
		"CreatedLabel":  label,
	}
	setNavData(data, auth.SessionFromContext(r.Context()))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.templates.ExecuteTemplate(w, "keys.html", data); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

// UpdateKeyLabelHandler updates a key's label.
func (h *Handlers) UpdateKeyLabelHandler(w http.ResponseWriter, r *http.Request, prefix string) {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	label := r.FormValue("label")
	if err := validateLabel(label); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	allowed, err := h.canMutateKey(r, prefix)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := h.store.UpdateKeyLabel(r.Context(), prefix, label); err != nil {
		http.Error(w, "Key not found", http.StatusNotFound)
		return
	}

	keys, err := h.listKeysForSession(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		data := map[string]any{"Keys": keys}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.templates.ExecuteNamed(w, "key_rows", data); err != nil {
			slog.Error("render key_rows fragment", "error", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true}); err != nil {
		slog.Error("write update key response", "error", err)
	}
}

// RevokeKeyHandler revokes a proxy key.
func (h *Handlers) RevokeKeyHandler(w http.ResponseWriter, r *http.Request, prefix string) {
	allowed, err := h.canMutateKey(r, prefix)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	if !allowed {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	if err := h.store.RevokeProxyKey(r.Context(), prefix); err != nil {
		http.Error(w, "Key not found", http.StatusNotFound)
		return
	}

	if err := h.store.RecordAuditEvent(r.Context(), "key.revoked", prefix, "", sessionActorEmail(r.Context())); err != nil {
		slog.Warn("record audit event", "error", err)
	}

	keys, err := h.listKeysForSession(r)
	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if r.Header.Get("HX-Request") == "true" {
		data := map[string]any{"Keys": keys}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if err := h.templates.ExecuteNamed(w, "key_rows", data); err != nil {
			slog.Error("render key_rows fragment", "error", err)
		}
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"ok": true}); err != nil {
		slog.Error("write revoke key response", "error", err)
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
		ScopeType: "all",
	}
}
