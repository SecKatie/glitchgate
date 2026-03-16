package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/pressly/goose/v3"
	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/pricing"
	"github.com/seckatie/glitchgate/internal/provider/copilot"
	_ "modernc.org/sqlite" // SQLite driver (pure Go, no CGO).
)

// SQLiteStore implements Store backed by a SQLite database.
type SQLiteStore struct {
	db *sql.DB
}

// Compile-time check that SQLiteStore satisfies Store.
var _ Store = (*SQLiteStore)(nil)

// NewSQLiteStore opens (or creates) the SQLite database at dbPath and returns
// a ready-to-use store. WAL mode and foreign keys are enabled automatically.
func NewSQLiteStore(dbPath string) (*SQLiteStore, error) {
	if dir := filepath.Dir(dbPath); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create database directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// Enable WAL mode for better concurrent read performance.
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable WAL mode: %w", err)
	}

	// Enable foreign-key constraint enforcement.
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}

	// Set busy timeout so concurrent writers retry instead of failing
	// immediately with SQLITE_BUSY.
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}

	// SQLite serializes writes, so limit open connections to avoid
	// contention. Keep a small idle pool for the async logger.
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)

	return &SQLiteStore{db: db}, nil
}

// Migrate runs all pending goose migrations embedded in the binary.
func (s *SQLiteStore) Migrate(ctx context.Context) error {
	goose.SetBaseFS(migrations)

	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.UpContext(ctx, s.db, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}

type providerNameRewrite struct {
	oldName     string
	newName     string
	modelExact  string
	modelPrefix string
}

// NormalizeLoggedProviderNames rewrites legacy canonical provider keys in
// request_logs to the configured provider names used for runtime identity.
func (s *SQLiteStore) NormalizeLoggedProviderNames(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return nil
	}

	rewrites, err := providerNameRewrites(cfg)
	if err != nil {
		return err
	}
	if len(rewrites) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin provider-name normalization: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, rewrite := range rewrites {
		query := `UPDATE request_logs SET provider_name = ? WHERE provider_name = ? AND model_requested = ?`
		args := []any{rewrite.newName, rewrite.oldName, rewrite.modelExact}
		if rewrite.modelPrefix != "" {
			query = `UPDATE request_logs SET provider_name = ? WHERE provider_name = ? AND model_requested LIKE ? ESCAPE '\'`
			args = []any{rewrite.newName, rewrite.oldName, escapeLikePattern(rewrite.modelPrefix) + "%"}
		}
		if _, err = tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("normalize provider name %q -> %q: %w", rewrite.oldName, rewrite.newName, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit provider-name normalization: %w", err)
	}
	return nil
}

func providerNameRewrites(cfg *config.Config) ([]providerNameRewrite, error) {
	exact := make([]providerNameRewrite, 0)
	wildcards := make([]providerNameRewrite, 0)
	seen := make(map[string]string)

	for _, mm := range cfg.ModelList {
		if mm.Provider == "" || len(mm.Fallbacks) > 0 {
			continue
		}

		pc, err := cfg.FindProvider(mm.Provider)
		if err != nil {
			return nil, fmt.Errorf("find provider for model %q: %w", mm.ModelName, err)
		}

		oldName := legacyLoggedProviderName(*pc)
		if oldName == "" || oldName == pc.Name {
			continue
		}

		rewrite := providerNameRewrite{
			oldName: oldName,
			newName: pc.Name,
		}
		if strings.HasSuffix(mm.ModelName, "/*") {
			rewrite.modelPrefix = strings.TrimSuffix(mm.ModelName, "*")
		} else {
			rewrite.modelExact = mm.ModelName
		}

		conflictKey := rewrite.oldName + "\x00" + rewrite.modelExact + "\x00" + rewrite.modelPrefix
		if existing, ok := seen[conflictKey]; ok {
			if existing != rewrite.newName {
				return nil, fmt.Errorf(
					"ambiguous provider-name normalization for %q and model pattern %q: %q vs %q",
					rewrite.oldName,
					rewritePattern(rewrite),
					existing,
					rewrite.newName,
				)
			}
			continue
		}
		seen[conflictKey] = rewrite.newName

		if rewrite.modelPrefix != "" {
			wildcards = append(wildcards, rewrite)
			continue
		}
		exact = append(exact, rewrite)
	}

	return append(exact, wildcards...), nil
}

func legacyLoggedProviderName(pc config.ProviderConfig) string {
	baseURL := pc.BaseURL
	if pc.Type == "github_copilot" && baseURL == "" {
		baseURL = copilot.DefaultAPIURL
	}
	return pricing.ProviderKey(pc.Type, baseURL)
}

func rewritePattern(rewrite providerNameRewrite) string {
	if rewrite.modelExact != "" {
		return rewrite.modelExact
	}
	return rewrite.modelPrefix + "*"
}

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
