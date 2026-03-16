//go:build ignore

// normalize_provider_names is a one-time script that rewrites legacy provider
// names (e.g. "openai_responses:chatgpt.com") in the request_logs table to
// the current configured provider names (e.g. "chatgpt-pro").
//
// Usage:
//
//	go run scripts/normalize_provider_names.go [-config config.yaml] [-dry-run]
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	_ "modernc.org/sqlite"

	"github.com/seckatie/glitchgate/internal/app"
	"github.com/seckatie/glitchgate/internal/config"
)

func main() {
	cfgFile := flag.String("config", "", "config file path (default: auto-discover)")
	dryRun := flag.Bool("dry-run", false, "show what would change without updating")
	flag.Parse()

	cfg, err := config.Load(*cfgFile)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	// Build provider registry to get the legacy → current name mapping.
	registry, err := app.NewProviderRegistry(cfg, 30*time.Second)
	if err != nil {
		log.Fatalf("build providers: %v", err)
	}
	providerNames := registry.ProviderNames()

	// Build legacy → current mapping (only entries where key != value).
	renames := make(map[string]string)
	for legacyName, currentName := range providerNames {
		if legacyName != currentName {
			renames[legacyName] = currentName
		}
	}

	if len(renames) == 0 {
		fmt.Fprintln(os.Stderr, "No legacy provider name aliases found. Nothing to normalize.")
		return
	}

	db, err := sql.Open("sqlite", cfg.DatabasePath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	ctx := context.Background()

	for legacy, current := range renames {
		var count int64
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM request_logs WHERE provider_name = ?", legacy).Scan(&count); err != nil {
			log.Fatalf("count rows for %q: %v", legacy, err)
		}

		if count == 0 {
			continue
		}

		fmt.Fprintf(os.Stderr, "%q → %q: %d rows\n", legacy, current, count)

		if *dryRun {
			continue
		}

		result, err := db.ExecContext(ctx, "UPDATE request_logs SET provider_name = ? WHERE provider_name = ?", current, legacy)
		if err != nil {
			log.Fatalf("update %q → %q: %v", legacy, current, err)
		}
		affected, _ := result.RowsAffected()
		fmt.Fprintf(os.Stderr, "  updated %d rows\n", affected)
	}

	if *dryRun {
		fmt.Fprintln(os.Stderr, "Dry run — no changes made.")
	} else {
		fmt.Fprintln(os.Stderr, "Done.")
	}
}
