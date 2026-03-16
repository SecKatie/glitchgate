//go:build ignore

// backfill_costs is a one-time script that computes cost_usd for request_logs
// rows where it is NULL, using the same pricing calculator as the proxy.
//
// Usage:
//
//	go run scripts/backfill_costs.go [-config config.yaml] [-dry-run] [-batch 1000]
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
	dryRun := flag.Bool("dry-run", false, "count rows without updating")
	batchSize := flag.Int("batch", 1000, "rows per UPDATE batch")
	flag.Parse()

	cfg, err := config.Load(*cfgFile)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	registry, err := app.NewProviderRegistry(cfg, 30*time.Second)
	if err != nil {
		log.Fatalf("build providers: %v", err)
	}
	calc := registry.Calculator()

	db, err := sql.Open("sqlite", cfg.DatabasePath+"?_journal_mode=WAL&_foreign_keys=on&_busy_timeout=5000")
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Count NULL rows.
	var nullCount int64
	if err := db.QueryRow("SELECT COUNT(*) FROM request_logs WHERE cost_usd IS NULL").Scan(&nullCount); err != nil {
		log.Fatalf("count null rows: %v", err)
	}
	fmt.Fprintf(os.Stderr, "Found %d rows with NULL cost_usd\n", nullCount)

	if nullCount == 0 {
		fmt.Fprintln(os.Stderr, "Nothing to backfill.")
		return
	}

	if *dryRun {
		fmt.Fprintln(os.Stderr, "Dry run — no changes made.")
		return
	}

	// Process in batches.
	ctx := context.Background()
	var totalUpdated int64

	for {
		rows, err := db.QueryContext(ctx, `
			SELECT id, provider_name, model_upstream,
			       input_tokens, output_tokens,
			       cache_creation_input_tokens, cache_read_input_tokens,
			       COALESCE(reasoning_tokens, 0)
			FROM request_logs
			WHERE cost_usd IS NULL
			LIMIT ?`, *batchSize)
		if err != nil {
			log.Fatalf("select batch: %v", err)
		}

		type row struct {
			id                                                                              string
			providerName, modelUpstream                                                     string
			inputTokens, outputTokens, cacheCreation, cacheRead, reasoning int64
		}

		var batch []row
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.id, &r.providerName, &r.modelUpstream,
				&r.inputTokens, &r.outputTokens, &r.cacheCreation, &r.cacheRead, &r.reasoning); err != nil {
				_ = rows.Close()
				log.Fatalf("scan row: %v", err)
			}
			batch = append(batch, r)
		}
		_ = rows.Close()

		if len(batch) == 0 {
			break
		}

		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			log.Fatalf("begin tx: %v", err)
		}

		stmt, err := tx.PrepareContext(ctx, "UPDATE request_logs SET cost_usd = ? WHERE id = ?")
		if err != nil {
			_ = tx.Rollback()
			log.Fatalf("prepare update: %v", err)
		}

		for _, r := range batch {
			cost := calc.Calculate(r.providerName, r.modelUpstream,
				r.inputTokens, r.outputTokens, r.cacheCreation, r.cacheRead, r.reasoning)
			if cost != nil {
				if _, err := stmt.ExecContext(ctx, *cost, r.id); err != nil {
					_ = tx.Rollback()
					log.Fatalf("update row %s: %v", r.id, err)
				}
			} else {
				// Set to 0 so we don't re-process — pricing unknown.
				if _, err := stmt.ExecContext(ctx, 0.0, r.id); err != nil {
					_ = tx.Rollback()
					log.Fatalf("update row %s: %v", r.id, err)
				}
			}
		}

		_ = stmt.Close()
		if err := tx.Commit(); err != nil {
			log.Fatalf("commit: %v", err)
		}

		totalUpdated += int64(len(batch))
		fmt.Fprintf(os.Stderr, "  updated %d / %d\n", totalUpdated, nullCount)
	}

	fmt.Fprintf(os.Stderr, "Done. Backfilled %d rows.\n", totalUpdated)
}
