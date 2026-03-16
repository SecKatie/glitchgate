// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/cobra"

	"github.com/seckatie/glitchgate/internal/app"
	"github.com/seckatie/glitchgate/internal/auth"
	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/proxy"
	"github.com/seckatie/glitchgate/internal/ratelimit"
	"github.com/seckatie/glitchgate/internal/web"
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

	// Set up structured JSON logger writing to the configured log file and stdout.
	logFile, err := os.OpenFile(cfg.LogPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("opening log file %q: %w", cfg.LogPath, err)
	}
	defer func() { _ = logFile.Close() }()
	logDest := io.MultiWriter(logFile, os.Stdout)
	slog.SetDefault(slog.New(slog.NewJSONHandler(logDest, &slog.HandlerOptions{Level: slog.LevelInfo})))

	runtime, err := app.Bootstrap(context.Background(), cfg)
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Close() }()

	maintenanceCtx, maintenanceCancel := context.WithCancel(context.Background())
	defer maintenanceCancel()
	runtime.StartMaintenance(maintenanceCtx, cfg)

	// Build the proxy handlers.
	proxyHandler := proxy.NewHandler(cfg, runtime.Providers, runtime.Calculator, runtime.AsyncLogger, proxy.NewBudgetChecker(runtime.Store, runtime.Timezone))
	openaiHandler := proxy.NewOpenAIHandler(cfg, runtime.Providers, runtime.Calculator, runtime.AsyncLogger, proxy.NewBudgetChecker(runtime.Store, runtime.Timezone))
	responsesHandler := proxy.NewResponsesHandler(cfg, runtime.Providers, runtime.Calculator, runtime.AsyncLogger, proxy.NewBudgetChecker(runtime.Store, runtime.Timezone))

	// Build chi router.
	r := chi.NewRouter()
	r.Use(chimw.RealIP)
	r.Use(chimw.Recoverer)
	r.Use(web.SecurityHeadersMiddleware)
	r.Use(warnOnNotFound)

	loginRateLimiter := ratelimit.New(
		positiveInt(cfg.LoginRateLimitPerMinute, config.DefaultLoginRateLimitPerMinute),
		positiveInt(cfg.LoginRateLimitBurst, config.DefaultLoginRateLimitBurst),
		15*time.Minute,
	)
	proxyKeyRateLimiter := ratelimit.New(
		positiveInt(cfg.ProxyRateLimitPerMinute, config.DefaultProxyRateLimitPerMinute),
		positiveInt(cfg.ProxyRateLimitBurst, config.DefaultProxyRateLimitBurst),
		15*time.Minute,
	)
	proxyIPRateLimiter := ratelimit.New(
		positiveInt(cfg.ProxyIPRateLimitPerMinute, config.DefaultProxyIPRateLimitPerMinute),
		positiveInt(cfg.ProxyIPRateLimitBurst, config.DefaultProxyIPRateLimitBurst),
		15*time.Minute,
	)

	// Proxy routes — authenticated with proxy API key.
	r.Route("/v1", func(r chi.Router) {
		r.Use(proxy.IPRateLimitMiddleware(proxyIPRateLimiter))
		r.Use(proxy.AuthMiddleware(runtime.Store))
		r.Use(proxy.KeyRateLimitMiddleware(proxyKeyRateLimiter))
		r.Post("/messages", proxyHandler.ServeHTTP)
		r.Post("/chat/completions", openaiHandler.ServeHTTP)
		r.Post("/responses", responsesHandler.ServeHTTP)
	})

	// Web UI.
	sessions := auth.NewUISessionStore(runtime.Store)
	tmpl := web.ParseTemplates(runtime.Timezone)

	webHandlers := web.NewHandlers(runtime.Store, sessions, cfg.MasterKey, runtime.Calculator, tmpl, runtime.OIDCProvider, cfg.ModelList, cfg.Providers, runtime.ProviderNames)
	authHandlers := web.NewAuthHandlers(runtime.Store, sessions, runtime.OIDCProvider)
	costHandlers := web.NewCostHandlers(runtime.Store, runtime.Store, runtime.Store, tmpl, runtime.Timezone, runtime.Calculator, runtime.ProviderNames, runtime.ProviderMonthlySubscriptions)
	userHandlers := web.NewUserHandlers(runtime.Store, sessions, tmpl)
	teamHandlers := web.NewTeamHandlers(runtime.Store, sessions, tmpl)

	// Serve embedded static assets (no session required).
	staticFS, _ := fs.Sub(web.Static, "static")
	r.Handle("/ui/static/*", http.StripPrefix("/ui/static/", http.FileServer(http.FS(staticFS))))

	// Public web routes (no session required).
	r.Get("/ui/login", webHandlers.LoginPage)
	r.With(web.LoginRateLimitMiddleware(loginRateLimiter)).Post("/ui/api/login", webHandlers.LoginHandler)

	// OIDC auth routes (public — no session required).
	r.Get("/ui/auth/oidc", authHandlers.OIDCStartHandler)
	r.Get("/ui/auth/callback", authHandlers.OIDCCallbackHandler)

	// Protected web routes.
	r.Route("/ui", func(r chi.Router) {
		r.Use(web.UISessionMiddleware(sessions, runtime.Store))
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

		// Budget management (GA-only for global/user, GA/TA for team).
		r.With(web.RequireGlobalAdmin).Post("/api/budgets/global", costHandlers.SetGlobalBudgetHandler)
		r.With(web.RequireGlobalAdmin).Post("/api/budgets/global/clear", costHandlers.ClearGlobalBudgetHandler)
		r.With(web.RequireGlobalAdmin).Post("/api/budgets/user/{id}", costHandlers.SetUserBudgetHandler)
		r.With(web.RequireGlobalAdmin).Post("/api/budgets/user/{id}/clear", costHandlers.ClearUserBudgetHandler)
		r.With(web.RequireAdminOrTeamAdmin).Post("/api/budgets/team/{id}", costHandlers.SetTeamBudgetHandler)
		r.With(web.RequireAdminOrTeamAdmin).Post("/api/budgets/team/{id}/clear", costHandlers.ClearTeamBudgetHandler)

		// Models.
		r.Get("/models", webHandlers.ModelsPage)
		r.Get("/models/*", webHandlers.ModelDetailPage)

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
		r.With(web.RequireGlobalAdmin).Delete("/api/teams/{id}", teamHandlers.DeleteTeamHandler)
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
		slog.Info("glitchgate listening", "addr", cfg.Listen)
		errCh <- srv.ListenAndServe()
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-quit:
		slog.Info("received signal, shutting down", "signal", sig)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("server error: %w", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(ctx)
}

func positiveInt(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}
