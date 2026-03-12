// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/cobra"

	"codeberg.org/kglitchy/llm-proxy/internal/auth"
	"codeberg.org/kglitchy/llm-proxy/internal/config"
	oidcpkg "codeberg.org/kglitchy/llm-proxy/internal/oidc"
	"codeberg.org/kglitchy/llm-proxy/internal/pricing"
	"codeberg.org/kglitchy/llm-proxy/internal/provider"
	"codeberg.org/kglitchy/llm-proxy/internal/provider/anthropic"
	"codeberg.org/kglitchy/llm-proxy/internal/provider/copilot"
	"codeberg.org/kglitchy/llm-proxy/internal/proxy"
	"codeberg.org/kglitchy/llm-proxy/internal/store"
	"codeberg.org/kglitchy/llm-proxy/internal/web"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "Start the proxy server",
	RunE:  runServe,
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

func runServe(_ *cobra.Command, _ []string) error {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Open database and run migrations.
	st, err := store.NewSQLiteStore(cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.Migrate(context.Background()); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}

	// Build provider map from config.
	providers := make(map[string]provider.Provider)
	for _, pc := range cfg.Providers {
		switch pc.Type {
		case "anthropic":
			providers[pc.Name] = anthropic.NewClient(pc.Name, pc.BaseURL, pc.AuthMode, pc.APIKey, pc.DefaultVersion)
		case "github_copilot":
			tokenDir := pc.TokenDir
			if tokenDir == "" {
				tokenDir = copilot.DefaultTokenDir()
			}
			providers[pc.Name] = copilot.NewClient(pc.Name, tokenDir)
		default:
			return fmt.Errorf("unsupported provider type %q for provider %q", pc.Type, pc.Name)
		}
	}

	// Build pricing calculator: merge defaults with user config overrides.
	pricingMap := make(map[string]pricing.Entry)
	for k, v := range pricing.DefaultPricing {
		pricingMap[k] = v
	}
	for _, pe := range cfg.Pricing {
		pricingMap[pe.Model] = pricing.Entry{
			InputPerMillion:  pe.InputPerMillion,
			OutputPerMillion: pe.OutputPerMillion,
		}
	}
	calc := pricing.NewCalculator(pricingMap)

	// Create async logger.
	asyncLogger := proxy.NewAsyncLogger(st, 1000)
	defer asyncLogger.Close()

	// Load display timezone (default UTC).
	tz, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		log.Printf("WARNING: unknown timezone %q, falling back to UTC: %v", cfg.Timezone, err)
		tz = time.UTC
	}

	// Build the proxy handlers.
	proxyHandler := proxy.NewHandler(cfg, providers, calc, asyncLogger)
	openaiHandler := proxy.NewOpenAIHandler(cfg, providers, calc, asyncLogger)

	// Build chi router.
	r := chi.NewRouter()
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)

	// Proxy routes — authenticated with proxy API key.
	r.Route("/v1", func(r chi.Router) {
		r.Use(proxy.AuthMiddleware(st))
		r.Post("/messages", proxyHandler.ServeHTTP)
		r.Post("/chat/completions", openaiHandler.ServeHTTP)
	})

	// Web UI.
	sessions := auth.NewUISessionStore(st)
	tmpl := web.ParseTemplates(tz)

	// Build OIDC provider (nil when not configured).
	var oidcProvider *oidcpkg.Provider
	if cfg.OIDCEnabled() {
		var oidcErr error
		oidcProvider, oidcErr = oidcpkg.NewProvider(context.Background(), cfg.OIDC)
		if oidcErr != nil {
			return fmt.Errorf("initialising OIDC provider: %w", oidcErr)
		}
		log.Printf("OIDC provider configured: %s", cfg.OIDC.IssuerURL)
	}

	webHandlers := web.NewHandlers(st, sessions, cfg.MasterKey, calc, tmpl, oidcProvider)
	authHandlers := web.NewAuthHandlers(st, sessions, oidcProvider)
	costHandlers := web.NewCostHandlers(st, tmpl, tz)
	userHandlers := web.NewUserHandlers(st, sessions, tmpl)
	teamHandlers := web.NewTeamHandlers(st, sessions, tmpl)

	// Start background cleanup goroutine for expired sessions and OIDC state.
	go func() {
		for {
			time.Sleep(time.Hour)
			ctx := context.Background()
			if err := st.CleanupExpiredSessions(ctx); err != nil {
				log.Printf("WARNING: cleanup sessions: %v", err)
			}
			if err := st.CleanupExpiredOIDCState(ctx); err != nil {
				log.Printf("WARNING: cleanup oidc state: %v", err)
			}
		}
	}()

	// Serve embedded static assets (no session required).
	staticFS, _ := fs.Sub(web.Static, "static")
	r.Handle("/ui/static/*", http.StripPrefix("/ui/static/", http.FileServer(http.FS(staticFS))))

	// Public web routes (no session required).
	r.Get("/ui/login", webHandlers.LoginPage)
	r.Post("/ui/api/login", webHandlers.LoginHandler)

	// OIDC auth routes (public — no session required).
	r.Get("/ui/auth/oidc", authHandlers.OIDCStartHandler)
	r.Get("/ui/auth/callback", authHandlers.OIDCCallbackHandler)

	// Protected web routes.
	r.Route("/ui", func(r chi.Router) {
		r.Use(web.UISessionMiddleware(sessions, st))
		r.Post("/api/logout", webHandlers.LogoutHandler)

		r.Get("/", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/ui/logs", http.StatusSeeOther)
		})

		// Logs.
		r.Get("/logs", webHandlers.LogsPage)
		r.Get("/api/logs", webHandlers.LogsAPIHandler)
		r.Get("/logs/{id}", func(w http.ResponseWriter, r *http.Request) {
			webHandlers.LogDetailPage(w, r, chi.URLParam(r, "id"))
		})
		r.Get("/api/logs/{id}", func(w http.ResponseWriter, r *http.Request) {
			webHandlers.LogDetailAPIHandler(w, r, chi.URLParam(r, "id"))
		})

		// Costs.
		r.Get("/costs", costHandlers.CostsPageHandler)
		r.Get("/api/costs", costHandlers.CostSummaryHandler)
		r.Get("/api/costs/timeseries", costHandlers.CostTimeseriesHandler)
		r.Get("/api/costs/fragment", costHandlers.CostSummaryFragmentHandler)

		// Keys.
		r.Get("/keys", webHandlers.KeysPage)
		r.Get("/api/keys", webHandlers.KeysAPIHandler)
		r.Post("/api/keys", webHandlers.CreateKeyHandler)
		r.Post("/api/keys/{prefix}/update", func(w http.ResponseWriter, r *http.Request) {
			webHandlers.UpdateKeyLabelHandler(w, r, chi.URLParam(r, "prefix"))
		})
		r.Post("/api/keys/{prefix}/revoke", func(w http.ResponseWriter, r *http.Request) {
			webHandlers.RevokeKeyHandler(w, r, chi.URLParam(r, "prefix"))
		})

		// Users (GA-only except deactivate which allows TA for own team).
		r.Route("/", func(r chi.Router) {
			r.Use(web.RequireGlobalAdmin)
			r.Get("/users", userHandlers.UsersPage)
			r.Get("/api/users", userHandlers.UsersAPIHandler)
			r.Post("/api/users/{id}/role", userHandlers.ChangeRoleHandler)
			r.Post("/api/users/{id}/reactivate", userHandlers.ReactivateUserHandler)
		})
		r.With(web.RequireAdminOrTeamAdmin).Post("/api/users/{id}/deactivate", userHandlers.DeactivateUserHandler)

		// Teams (TA/GA can view; only GA can create).
		r.With(web.RequireAdminOrTeamAdmin).Get("/teams", teamHandlers.TeamsPage)
		r.With(web.RequireAdminOrTeamAdmin).Get("/api/teams", teamHandlers.TeamsAPIHandler)
		r.With(web.RequireGlobalAdmin).Post("/api/teams", teamHandlers.CreateTeamHandler)
		r.With(web.RequireAdminOrTeamAdmin).Post("/api/teams/{id}/members", teamHandlers.AddTeamMemberHandler)
		r.With(web.RequireAdminOrTeamAdmin).Delete("/api/teams/{id}/members/{userID}", teamHandlers.RemoveTeamMemberHandler)
	})

	// Root redirect to UI.
	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/logs", http.StatusSeeOther)
	})

	// Health check.
	r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	srv := &http.Server{
		Addr:         cfg.Listen,
		Handler:      r,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 5 * time.Minute, // Long timeout for streaming responses.
		IdleTimeout:  120 * time.Second,
	}

	// Graceful shutdown.
	errCh := make(chan error, 1)
	go func() {
		log.Printf("llm-proxy listening on %s", cfg.Listen)
		errCh <- srv.ListenAndServe()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		log.Printf("received signal %s, shutting down...", sig)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}
