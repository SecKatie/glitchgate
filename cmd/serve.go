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
	"codeberg.org/kglitchy/llm-proxy/internal/pricing"
	"codeberg.org/kglitchy/llm-proxy/internal/provider"
	"codeberg.org/kglitchy/llm-proxy/internal/provider/anthropic"
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
	sessions := auth.NewSessionStore(24 * time.Hour)
	tmpl := web.ParseTemplates()
	webHandlers := web.NewHandlers(st, sessions, cfg.MasterKey, calc, tmpl)
	costHandlers := web.NewCostHandlers(st, tmpl)

	// Serve embedded static assets (no session required).
	staticFS, _ := fs.Sub(web.Static, "static")
	r.Handle("/ui/static/*", http.StripPrefix("/ui/static/", http.FileServer(http.FS(staticFS))))

	// Public web routes (no session required).
	r.Get("/ui/login", webHandlers.LoginPage)
	r.Post("/ui/api/login", webHandlers.LoginHandler)

	// Protected web routes.
	r.Route("/ui", func(r chi.Router) {
		r.Use(web.SessionMiddleware(sessions))
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
