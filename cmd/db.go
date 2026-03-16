// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/seckatie/glitchgate/internal/config"
	"github.com/seckatie/glitchgate/internal/store"
)

var dbCmd = &cobra.Command{
	Use:   "db",
	Short: "Database management commands",
}

var dbExportCmd = &cobra.Command{
	Use:   "export [file]",
	Short: "Export database to a compressed SQL dump (.sql.gz)",
	Long: `Export all persistent data (keys, logs, users, teams, audit events)
to a gzip-compressed SQL dump. Ephemeral data (sessions, OIDC state) is excluded.

The dump contains INSERT OR IGNORE statements that can be loaded into an
existing database without overwriting data. If no file is specified, output
is written to stdout.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDBExport,
}

var dbImportCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import data from a compressed SQL dump (.sql.gz)",
	Long: `Import data from a previously exported .sql.gz dump into the database.
Rows with conflicting primary keys are skipped (existing data is preserved).

The target database is determined by the config file (--config flag).`,
	Args: cobra.ExactArgs(1),
	RunE: runDBImport,
}

var dbTrimCmd = &cobra.Command{
	Use:   "trim",
	Short: "Replace request/response bodies with '[trimmed]' in old log entries",
	Long: `Strip large request_body and response_body fields from log entries older
than the specified threshold. The log metadata (timestamps, tokens, costs,
status codes) is preserved. Already-trimmed rows are skipped.

Use --dry-run to see how many rows would be affected without making changes.`,
	RunE: runDBTrim,
}

func init() {
	dbCmd.AddCommand(dbExportCmd)
	dbCmd.AddCommand(dbImportCmd)
	dbCmd.AddCommand(dbTrimCmd)
	rootCmd.AddCommand(dbCmd)

	dbTrimCmd.Flags().Duration("older-than", 7*24*time.Hour, "trim logs older than this duration")
	dbTrimCmd.Flags().Int("batch-size", 1000, "rows to update per batch")
	dbTrimCmd.Flags().Bool("dry-run", false, "show how many rows would be trimmed without making changes")
}

func openSQLiteStore() (*store.SQLiteStore, error) {
	cfg, err := config.Load(cfgFile)
	if err != nil {
		return nil, fmt.Errorf("loading config: %w", err)
	}
	st, err := store.NewSQLiteStore(cfg.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("opening database: %w", err)
	}
	if err := st.Migrate(context.Background()); err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("running migrations: %w", err)
	}
	return st, nil
}

func runDBExport(_ *cobra.Command, args []string) error {
	st, err := openSQLiteStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	w := os.Stdout
	if len(args) == 1 {
		f, err := os.Create(args[0])
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer func() { _ = f.Close() }()
		w = f
	}

	progress := func(table string, rows int64) {
		fmt.Fprintf(os.Stderr, "  %-20s %d rows\n", table, rows)
	}
	if err := st.Export(context.Background(), w, progress); err != nil {
		return fmt.Errorf("export: %w", err)
	}

	if w != os.Stdout {
		fmt.Fprintf(os.Stderr, "Exported database to %s\n", args[0])
	}
	return nil
}

func runDBImport(_ *cobra.Command, args []string) error {
	st, err := openSQLiteStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	f, err := os.Open(args[0])
	if err != nil {
		return fmt.Errorf("open import file: %w", err)
	}
	defer func() { _ = f.Close() }()

	progress := func(table string, rows int64) {
		fmt.Fprintf(os.Stderr, "  %-20s %d rows\n", table, rows)
	}
	stats, err := st.Import(context.Background(), f, progress)
	if err != nil {
		return fmt.Errorf("import: %w", err)
	}

	fmt.Println("Import complete:")
	for _, t := range stats.Tables {
		fmt.Printf("  %-20s %d rows\n", t.Table, t.Rows)
	}
	if len(stats.Tables) == 0 {
		fmt.Println("  (no new rows imported)")
	}
	return nil
}

func runDBTrim(cmd *cobra.Command, _ []string) error {
	st, err := openSQLiteStore()
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	olderThan, _ := cmd.Flags().GetDuration("older-than")
	batchSize, _ := cmd.Flags().GetInt("batch-size")
	dryRun, _ := cmd.Flags().GetBool("dry-run")

	ctx := context.Background()
	cutoff := time.Now().UTC().Add(-olderThan)

	if dryRun {
		count, err := st.CountTrimmableLogBodies(ctx, cutoff)
		if err != nil {
			return fmt.Errorf("count: %w", err)
		}
		fmt.Printf("Would trim %d rows (older than %s)\n", count, cutoff.Format("2006-01-02"))
		return nil
	}

	var total int64
	for {
		n, err := st.TrimRequestLogBodies(ctx, cutoff, batchSize)
		if err != nil {
			return fmt.Errorf("trim: %w", err)
		}
		if n == 0 {
			break
		}
		total += n
		fmt.Fprintf(os.Stderr, "  Trimmed %d rows (%d total)...\n", n, total)
	}

	fmt.Printf("Trim complete: %d rows trimmed (older than %s)\n", total, cutoff.Format("2006-01-02"))
	return nil
}
