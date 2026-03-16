// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

// ExportData is the top-level structure written to / read from an export file.
type ExportData struct {
	Version         int              `json:"version"`
	ExportedAt      time.Time        `json:"exported_at"`
	Tables          []string         `json:"tables"`
	OIDCUsers       []map[string]any `json:"oidc_users"`
	Teams           []map[string]any `json:"teams"`
	TeamMemberships []map[string]any `json:"team_memberships"`
	ProxyKeys       []map[string]any `json:"proxy_keys"`
	RequestLogs     []map[string]any `json:"request_logs"`
	AuditEvents     []map[string]any `json:"audit_events"`
}

// exportTables lists the persistent tables in FK-safe insertion order.
// Ephemeral tables (ui_sessions, oidc_state) are intentionally excluded.
// global_config was removed in migration 016; budgets are inline on entities.
var exportTables = []string{
	"oidc_users",
	"teams",
	"team_memberships",
	"proxy_keys",
	"request_logs",
	"audit_events",
}

// Export writes all persistent data as JSON to w.
func (s *SQLiteStore) Export(ctx context.Context, w io.Writer) error {
	data := ExportData{
		Version:    1,
		ExportedAt: time.Now().UTC(),
		Tables:     exportTables,
	}

	for _, table := range exportTables {
		rows, err := dumpTable(ctx, s.db, table)
		if err != nil {
			return fmt.Errorf("export table %s: %w", table, err)
		}
		switch table {
		case "oidc_users":
			data.OIDCUsers = rows
		case "teams":
			data.Teams = rows
		case "team_memberships":
			data.TeamMemberships = rows
		case "proxy_keys":
			data.ProxyKeys = rows
		case "request_logs":
			data.RequestLogs = rows
		case "audit_events":
			data.AuditEvents = rows
		}
	}

	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(data); err != nil {
		return fmt.Errorf("encode JSON: %w", err)
	}
	return nil
}

// Import reads an export JSON from r and inserts all rows into the database.
// The target database must already have migrations applied. Existing rows with
// conflicting primary keys are skipped (INSERT OR IGNORE).
func (s *SQLiteStore) Import(ctx context.Context, r io.Reader) (*ImportStats, error) {
	var data ExportData
	if err := json.NewDecoder(r).Decode(&data); err != nil {
		return nil, fmt.Errorf("decode JSON: %w", err)
	}
	if data.Version != 1 {
		return nil, fmt.Errorf("unsupported export version: %d", data.Version)
	}

	stats := &ImportStats{}

	// Disable FK checks during import so we can insert in any order without
	// worrying about transient FK violations within the transaction.
	if _, err := s.db.ExecContext(ctx, "PRAGMA foreign_keys=OFF"); err != nil {
		return nil, fmt.Errorf("disable foreign keys: %w", err)
	}
	defer func() {
		_, _ = s.db.ExecContext(ctx, "PRAGMA foreign_keys=ON")
	}()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	tableRows := map[string][]map[string]any{
		"oidc_users":       data.OIDCUsers,
		"teams":            data.Teams,
		"team_memberships": data.TeamMemberships,
		"proxy_keys":       data.ProxyKeys,
		"request_logs":     data.RequestLogs,
		"audit_events":     data.AuditEvents,
	}

	for _, table := range exportTables {
		rows := tableRows[table]
		if len(rows) == 0 {
			continue
		}
		n, insertErr := importTable(ctx, tx, table, rows)
		if insertErr != nil {
			err = fmt.Errorf("import table %s: %w", table, insertErr)
			return nil, err
		}
		stats.add(table, n)
	}

	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit transaction: %w", err)
	}
	return stats, nil
}

// ImportStats tracks how many rows were imported per table.
type ImportStats struct {
	Tables []TableImportStat
}

// TableImportStat records the row count imported for a single table.
type TableImportStat struct {
	Table string
	Rows  int64
}

func (s *ImportStats) add(table string, rows int64) {
	if rows > 0 {
		s.Tables = append(s.Tables, TableImportStat{Table: table, Rows: rows})
	}
}

// dumpTable reads all rows from a table and returns them as generic maps.
func dumpTable(ctx context.Context, db *sql.DB, table string) ([]map[string]any, error) {
	// Table name is from our hardcoded list, not user input.
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+table) //nolint:gosec // table name from exportTables constant
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	var result []map[string]any
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}

		row := make(map[string]any, len(cols))
		for i, col := range cols {
			v := values[i]
			// Convert []byte to string for JSON serialization.
			if b, ok := v.([]byte); ok {
				v = string(b)
			}
			row[col] = v
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

// importTable inserts rows into the named table using INSERT OR IGNORE.
func importTable(ctx context.Context, tx *sql.Tx, table string, rows []map[string]any) (int64, error) {
	if len(rows) == 0 {
		return 0, nil
	}

	// Use columns from the first row; all rows are expected to have the same keys.
	cols := sortedKeys(rows[0])
	placeholders := make([]string, len(cols))
	for i := range placeholders {
		placeholders[i] = "?"
	}

	// Table and column names are from our hardcoded export format, not user input.
	query := fmt.Sprintf( //nolint:gosec // table/cols from exportTables constant and export data keys
		"INSERT OR IGNORE INTO %s (%s) VALUES (%s)",
		table,
		strings.Join(cols, ", "),
		strings.Join(placeholders, ", "),
	)

	stmt, err := tx.PrepareContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("prepare: %w", err)
	}
	defer func() { _ = stmt.Close() }()

	var imported int64
	for _, row := range rows {
		args := make([]any, len(cols))
		for i, col := range cols {
			args[i] = row[col]
		}
		res, err := stmt.ExecContext(ctx, args...)
		if err != nil {
			return imported, fmt.Errorf("insert row: %w", err)
		}
		n, _ := res.RowsAffected()
		imported += n
	}
	return imported, nil
}

// sortedKeys returns map keys in sorted order for deterministic column ordering.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// Sort for determinism.
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
