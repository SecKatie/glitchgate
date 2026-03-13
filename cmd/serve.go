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
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/spf13/cobra"

	"codeberg.org/kglitchy/glitchgate/internal/auth"
	"codeberg.org/kglitchy/glitchgate/internal/config"
	oidcpkg "codeberg.org/kglitchy/glitchgate/internal/oidc"
	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/provider"
	"codeberg.org/kglitchy/glitchgate/internal/provider/anthropic"
	"codeberg.org/kglitchy/glitchgate/internal/provider/copilot"
	openaiprov "codeberg.org/kglitchy/glitchgate/internal/provider/openai"
	"codeberg.org/kglitchy/glitchgate/internal/proxy"
	"codeberg.org/kglitchy/glitchgate/internal/ratelimit"
	"codeberg.org/kglitchy/glitchgate/internal/store"
	"codeberg.org/kglitchy/glitchgate/internal/web"
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

	// Open database and run migrations.
	st, err := store.NewSQLiteStore(cfg.DatabasePath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer func() { _ = st.Close() }()

	if err := st.Migrate(context.Background()); err != nil {
		return fmt.Errorf("running migrations: %w", err)
	}
	if err := st.NormalizeLoggedProviderNames(context.Background(), cfg); err != nil {
		return fmt.Errorf("normalizing logged provider names: %w", err)
	}

	requestTimeout := positiveDuration(cfg.UpstreamRequestTimeout, config.DefaultUpstreamRequestTimeout)
	asyncLogBufferSize := positiveInt(cfg.AsyncLogBufferSize, config.DefaultAsyncLogBufferSize)
	asyncLogWriteTimeout := positiveDuration(cfg.AsyncLogWriteTimeout, config.DefaultAsyncLogWriteTimeout)
	requestLogBodyMaxBytes := positiveInt(cfg.RequestLogBodyMaxBytes, config.DefaultRequestLogBodyMaxBytes)

	// Build provider map from config.
	providers := make(map[string]provider.Provider)
	for _, pc := range cfg.Providers {
		switch pc.Type {
		case "anthropic":
			client := anthropic.NewClient(pc.Name, pc.BaseURL, pc.AuthMode, pc.APIKey, pc.DefaultVersion)
			client.SetTimeouts(requestTimeout)
			providers[pc.Name] = client
		case "github_copilot":
			tokenDir := pc.TokenDir
			if tokenDir == "" {
				tokenDir = copilot.DefaultTokenDir()
			}
			client := copilot.NewClient(pc.Name, tokenDir)
			client.SetTimeouts(requestTimeout)
			providers[pc.Name] = client
		case "openai":
			baseURL := pc.BaseURL
			if baseURL == "" {
				baseURL = "https://api.openai.com"
			}
			client := openaiprov.NewClient(pc.Name, baseURL, pc.AuthMode, pc.APIKey, openaiprov.APITypeChatCompletions)
			client.SetTimeouts(requestTimeout)
			providers[pc.Name] = client
		case "openai_responses":
			baseURL := pc.BaseURL
			if baseURL == "" {
				baseURL = "https://api.openai.com"
			}
			client := openaiprov.NewClient(pc.Name, baseURL, pc.AuthMode, pc.APIKey, openaiprov.APITypeResponses)
			client.SetTimeouts(requestTimeout)
			providers[pc.Name] = client
		default:
			return fmt.Errorf("unsupported provider type %q for provider %q", pc.Type, pc.Name)
		}
	}

	// Build pricing calculator.
	// Pass 1: seed type-based defaults per configured provider.
	pricingMap := make(map[string]pricing.Entry)
	for _, pc := range cfg.Providers {
		baseURL := pc.BaseURL
		if pc.Type == "github_copilot" && baseURL == "" {
			baseURL = copilot.DefaultAPIURL
		}
		switch pc.Type {
		case "github_copilot":
			for model, entry := range pricing.CopilotDefaults {
				pricingMap[pc.Name+"/"+model] = entry
			}
		case "anthropic":
			if pricing.IsOfficialAnthropicURL(baseURL) {
				for model, entry := range pricing.AnthropicDefaults {
					pricingMap[pc.Name+"/"+model] = entry
				}
			}
		case "openai", "openai_responses":
			if pricing.IsOfficialOpenAIURL(baseURL) {
				for model, entry := range pricing.OpenAIDefaults {
					pricingMap[pc.Name+"/"+model] = entry
				}
			} else if pricing.IsChutesURL(baseURL) {
				for model, entry := range pricing.ChutesDefaults {
					pricingMap[pc.Name+"/"+model] = entry
				}
			} else if pricing.IsSegmentURL(baseURL) {
				for model, entry := range pricing.SegmentDefaults {
					pricingMap[pc.Name+"/"+model] = entry
				}
			}
		}
	}
	// Pass 2: model_list metadata entries override defaults.
	for _, mm := range cfg.ModelList {
		if strings.HasSuffix(mm.ModelName, "/*") || len(mm.Fallbacks) > 0 || mm.Metadata == nil {
			continue
		}
		pc, err := cfg.FindProvider(mm.Provider)
		if err != nil {
			continue
		}
		pricingMap[pc.Name+"/"+mm.UpstreamModel] = pricing.Entry{
			InputPerMillion:      mm.Metadata.InputTokenCost,
			OutputPerMillion:     mm.Metadata.OutputTokenCost,
			CacheReadPerMillion:  mm.Metadata.CacheReadCost,
			CacheWritePerMillion: mm.Metadata.CacheWriteCost,
		}
	}
	calc := pricing.NewCalculator(pricingMap)

	// Create async logger.
	asyncLogger := proxy.NewAsyncLoggerWithOptions(st, proxy.AsyncLoggerOptions{
		BufferSize:      asyncLogBufferSize,
		WriteTimeout:    asyncLogWriteTimeout,
		EnqueueTimeout:  100 * time.Millisecond,
		SummaryInterval: time.Minute,
		BodyMaxBytes:    requestLogBodyMaxBytes,
	})
	defer asyncLogger.Close()

	// Load display timezone (default UTC).
	tz, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		slog.Warn("unknown timezone, falling back to UTC", "timezone", cfg.Timezone, "error", err)
		tz = time.UTC
	}

	// Build the proxy handlers.
	proxyHandler := proxy.NewHandler(cfg, providers, calc, asyncLogger)
	openaiHandler := proxy.NewOpenAIHandler(cfg, providers, calc, asyncLogger)
	responsesHandler := proxy.NewResponsesHandler(cfg, providers, calc, asyncLogger)

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
		r.Use(proxy.AuthMiddleware(st))
		r.Use(proxy.KeyRateLimitMiddleware(proxyKeyRateLimiter))
		r.Post("/messages", proxyHandler.ServeHTTP)
		r.Post("/chat/completions", openaiHandler.ServeHTTP)
		r.Post("/responses", responsesHandler.ServeHTTP)
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
		slog.Info("OIDC provider configured", "issuer_url", cfg.OIDC.IssuerURL)
	}

	providerNames := make(map[string]string, len(cfg.Providers))
	for _, p := range cfg.Providers {
		providerNames[p.Name] = p.Name
	}

	webHandlers := web.NewHandlers(st, sessions, cfg.MasterKey, calc, tmpl, oidcProvider, cfg.ModelList, cfg.Providers)
	authHandlers := web.NewAuthHandlers(st, sessions, oidcProvider)
	costHandlers := web.NewCostHandlers(st, tmpl, tz, calc, providerNames)
	userHandlers := web.NewUserHandlers(st, sessions, tmpl)
	teamHandlers := web.NewTeamHandlers(st, sessions, tmpl)

	// Start background cleanup goroutine for expired sessions and OIDC state.
	go func() {
		cleanupTicker := time.NewTicker(time.Hour)
		defer cleanupTicker.Stop()

		retention := cfg.RequestLogRetention
		if retention < 0 {
			retention = config.DefaultRequestLogRetention
		}

		pruneInterval := cfg.RequestLogPruneInterval
		if retention > 0 {
			pruneInterval = positiveDuration(pruneInterval, config.DefaultRequestLogPruneInterval)
		}

		var pruneTicker *time.Ticker
		if retention > 0 {
			pruneTicker = time.NewTicker(pruneInterval)
			defer pruneTicker.Stop()
		}

		for {
			select {
			case <-cleanupTicker.C:
				ctx := context.Background()
				if err := st.CleanupExpiredSessions(ctx); err != nil {
					slog.Warn("cleanup sessions", "error", err)
				}
				if err := st.CleanupExpiredOIDCState(ctx); err != nil {
					slog.Warn("cleanup oidc state", "error", err)
				}
			case <-pruneTick(pruneTicker):
				cutoff := time.Now().UTC().Add(-retention)
				batchSize := positiveInt(cfg.RequestLogPruneBatchSize, config.DefaultRequestLogPruneBatchSize)
				var total int64
				for {
					deleted, err := st.PruneRequestLogs(context.Background(), cutoff, batchSize)
					if err != nil {
						slog.Warn("prune request logs", "error", err)
						break
					}
					total += deleted
					if deleted < int64(batchSize) {
						break
					}
				}
				if total > 0 {
					slog.Info("pruned request logs", "deleted", total, "cutoff", cutoff)
				}
			}
		}
	}()

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

func positiveDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}

func pruneTick(ticker *time.Ticker) <-chan time.Time {
	if ticker == nil {
		return nil
	}
	return ticker.C
}
