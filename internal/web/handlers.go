// SPDX-License-Identifier: AGPL-3.0-or-later

package web

import (
	"context"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"sync"
	"time"

	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/store"
	"github.com/seckatie/glitchgate/internal/web/conversation"
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
	store.KeyScopingStore
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
	fmtInTz := func(t time.Time, layout string) string {
		return t.In(tz).Format(layout)
	}
	// Compute the abbreviation once (using current time for DST awareness).
	tzAbbrev := func() string {
		abbr, _ := time.Now().In(tz).Zone()
		return abbr
	}
	return template.FuncMap{
		"fmtTz":    fmtInTz,
		"fmtET":    fmtInTz, // legacy alias
		"tzAbbrev": tzAbbrev,
		"not":      func(b bool) bool { return !b },
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
		"wrapTurn": func(t *conversation.ConvTurn) []conversation.ConvTurn {
			if t == nil {
				return nil
			}
			return []conversation.ConvTurn{*t}
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
