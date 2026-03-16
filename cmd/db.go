// SPDX-License-Identifier: AGPL-3.0-or-later

package cmd

import (
	"context"
	"fmt"
	"os"

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
	Short: "Export database contents to a JSON file",
	Long: `Export all persistent data (keys, logs, users, teams, audit events)
to a JSON file. Ephemeral data (sessions, OIDC state) is excluded.

If no file is specified, output is written to stdout.`,
	Args: cobra.MaximumNArgs(1),
	RunE: runDBExport,
}

var dbImportCmd = &cobra.Command{
	Use:   "import <file>",
	Short: "Import data from a JSON export into the database",
	Long: `Import data from a previously exported JSON file into the database.
Rows with conflicting primary keys are skipped (existing data is preserved).

The target database is determined by the config file (--config flag).`,
	Args: cobra.ExactArgs(1),
	RunE: runDBImport,
}

func init() {
	dbCmd.AddCommand(dbExportCmd)
	dbCmd.AddCommand(dbImportCmd)
	rootCmd.AddCommand(dbCmd)
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

	if err := st.Export(context.Background(), w); err != nil {
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

	stats, err := st.Import(context.Background(), f)
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
