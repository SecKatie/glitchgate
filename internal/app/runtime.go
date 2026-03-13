// SPDX-License-Identifier: AGPL-3.0-or-later

package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/config"
	oidcpkg "codeberg.org/kglitchy/glitchgate/internal/oidc"
	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/provider"
	"codeberg.org/kglitchy/glitchgate/internal/proxy"
	"codeberg.org/kglitchy/glitchgate/internal/store"
)

// Runtime holds the composed application dependencies needed by the serve
// command after configuration has been loaded.
type Runtime struct {
	Store         store.Store
	Providers     map[string]provider.Provider
	Calculator    *pricing.Calculator
	ProviderNames map[string]string
	AsyncLogger   *proxy.AsyncLogger
	Timezone      *time.Location
	OIDCProvider  *oidcpkg.Provider
}

// Bootstrap opens the store, runs startup migrations, compiles provider
// runtime state, and initializes shared services.
func Bootstrap(ctx context.Context, cfg *config.Config) (*Runtime, error) {
	if cfg == nil {
		return nil, fmt.Errorf("bootstrap requires config")
	}

	st, err := store.NewSQLiteStore(cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}

	cleanupOnErr := true
	defer func() {
		if cleanupOnErr {
			_ = st.Close()
		}
	}()

	if err := st.Migrate(ctx); err != nil {
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	if err := st.NormalizeLoggedProviderNames(ctx, cfg); err != nil {
		return nil, fmt.Errorf("normalizing logged provider names: %w", err)
	}

	registry, err := NewProviderRegistry(cfg, positiveDuration(cfg.UpstreamRequestTimeout, config.DefaultUpstreamRequestTimeout))
	if err != nil {
		return nil, err
	}

	asyncLogger := proxy.NewAsyncLoggerWithOptions(st, proxy.AsyncLoggerOptions{
		BufferSize:      positiveInt(cfg.AsyncLogBufferSize, config.DefaultAsyncLogBufferSize),
		WriteTimeout:    positiveDuration(cfg.AsyncLogWriteTimeout, config.DefaultAsyncLogWriteTimeout),
		EnqueueTimeout:  100 * time.Millisecond,
		SummaryInterval: time.Minute,
		BodyMaxBytes:    positiveInt(cfg.RequestLogBodyMaxBytes, config.DefaultRequestLogBodyMaxBytes),
	})

	tz, err := loadTimezone(cfg.Timezone)
	if err != nil {
		asyncLogger.Close()
		return nil, err
	}

	oidcProvider, err := newOIDCProvider(ctx, cfg)
	if err != nil {
		asyncLogger.Close()
		return nil, err
	}

	cleanupOnErr = false
	return &Runtime{
		Store:         st,
		Providers:     registry.Providers(),
		Calculator:    registry.Calculator(),
		ProviderNames: registry.ProviderNames(),
		AsyncLogger:   asyncLogger,
		Timezone:      tz,
		OIDCProvider:  oidcProvider,
	}, nil
}

// Close releases runtime-owned resources in shutdown order.
func (rt *Runtime) Close() error {
	if rt == nil {
		return nil
	}

	if rt.AsyncLogger != nil {
		rt.AsyncLogger.Close()
	}
	if rt.Store != nil {
		return rt.Store.Close()
	}
	return nil
}

// StartMaintenance launches the periodic cleanup and request-log pruning loop.
func (rt *Runtime) StartMaintenance(ctx context.Context, cfg *config.Config) {
	if rt == nil || rt.Store == nil || cfg == nil {
		return
	}
	go runMaintenanceLoop(ctx, rt.Store, cfg) // #nosec G118 -- This is a process-lifetime maintenance goroutine controlled by the caller-provided app context.
}

func runMaintenanceLoop(ctx context.Context, st store.Store, cfg *config.Config) {
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
		case <-ctx.Done():
			return
		case <-cleanupTicker.C:
			if err := st.CleanupExpiredSessions(context.Background()); err != nil {
				slog.Warn("cleanup sessions", "error", err)
			}
			if err := st.CleanupExpiredOIDCState(context.Background()); err != nil {
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
}

func loadTimezone(name string) (*time.Location, error) {
	tz, err := time.LoadLocation(name)
	if err != nil {
		slog.Warn("unknown timezone, falling back to UTC", "timezone", name, "error", err)
		return time.UTC, nil
	}
	return tz, nil
}

func newOIDCProvider(ctx context.Context, cfg *config.Config) (*oidcpkg.Provider, error) {
	if !cfg.OIDCEnabled() {
		return nil, nil
	}

	oidcProvider, err := oidcpkg.NewProvider(ctx, cfg.OIDC)
	if err != nil {
		return nil, fmt.Errorf("initialising OIDC provider: %w", err)
	}

	slog.Info("OIDC provider configured", "issuer_url", cfg.OIDC.IssuerURL)
	return oidcProvider, nil
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
