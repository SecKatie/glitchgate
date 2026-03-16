// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"bufio"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"
)

const exportVersion = 1

// exportTables lists the persistent tables in FK-safe insertion order.
// Ephemeral tables (ui_sessions, oidc_state) are intentionally excluded.
var exportTables = []string{
	"oidc_users",
	"teams",
	"team_memberships",
	"proxy_keys",
	"request_logs",
	"audit_events",
}

// Export writes all persistent data as a gzip-compressed SQL dump to w.
// Each row is emitted as an INSERT OR IGNORE statement so the dump can be
// loaded into an existing database without conflicting with existing rows.
func (s *SQLiteStore) Export(ctx context.Context, w io.Writer) error {
	gz := gzip.NewWriter(w)

	fmt.Fprintln(gz, "-- glitchgate database export")
	fmt.Fprintf(gz, "-- version: %d\n", exportVersion)
	fmt.Fprintf(gz, "-- exported_at: %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Fprintf(gz, "-- tables: %s\n", strings.Join(exportTables, ", "))
	fmt.Fprintln(gz)

	for _, table := range exportTables {
		if err := writeTableSQL(ctx, s.db, gz, table); err != nil {
			_ = gz.Close()
			return fmt.Errorf("export table %s: %w", table, err)
		}
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}
	return nil
}

// Import reads a gzip-compressed SQL dump from r and executes the INSERT
// statements within a transaction. The target database must already have
// migrations applied. Existing rows with conflicting primary keys are
// skipped (INSERT OR IGNORE).
func (s *SQLiteStore) Import(ctx context.Context, r io.Reader) (*ImportStats, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("open gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	scanner := bufio.NewScanner(gz)
	// request_body / response_body can be large; allow up to 10 MB per line.
	scanner.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)

	// Parse header comments to validate the export version.
	version := 0
	var firstDataLine string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "-- version: ") {
			fmt.Sscanf(line, "-- version: %d", &version) //nolint:errcheck // best-effort parse
		}
		if !strings.HasPrefix(line, "--") && strings.TrimSpace(line) != "" {
			firstDataLine = line
			break
		}
	}
	if version != exportVersion {
		return nil, fmt.Errorf("unsupported export version: %d", version)
	}

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

	stats := &ImportStats{}

	execLine := func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			return nil
		}
		table := extractTableName(line)
		res, execErr := tx.ExecContext(ctx, line)
		if execErr != nil {
			return fmt.Errorf("execute SQL for %s: %w", table, execErr)
		}
		n, _ := res.RowsAffected()
		stats.add(table, n)
		return nil
	}

	// Process the first data line that broke us out of the header loop.
	if firstDataLine != "" {
		if err = execLine(firstDataLine); err != nil {
			return nil, err
		}
	}

	for scanner.Scan() {
		if err = execLine(scanner.Text()); err != nil {
			return nil, err
		}
	}
	if err = scanner.Err(); err != nil {
		return nil, fmt.Errorf("read SQL: %w", err)
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
	if rows == 0 {
		return
	}
	for i := range s.Tables {
		if s.Tables[i].Table == table {
			s.Tables[i].Rows += rows
			return
		}
	}
	s.Tables = append(s.Tables, TableImportStat{Table: table, Rows: rows})
}

// writeTableSQL writes INSERT OR IGNORE statements for every row in table.
func writeTableSQL(ctx context.Context, db *sql.DB, w io.Writer, table string) error {
	// Table name is from our hardcoded list, not user input.
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+table) //nolint:gosec // table name from exportTables constant
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return err
	}

	prefix := fmt.Sprintf("INSERT OR IGNORE INTO %s (%s) VALUES", table, strings.Join(cols, ", "))

	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}

		literals := make([]string, len(values))
		for i, v := range values {
			literals[i] = sqlLiteral(v)
		}
		if _, err := fmt.Fprintf(w, "%s (%s);\n", prefix, strings.Join(literals, ", ")); err != nil {
			return err
		}
	}
	return rows.Err()
}

// sqlLiteral formats a Go value as a SQL literal suitable for an INSERT statement.
func sqlLiteral(v any) string {
	switch val := v.(type) {
	case nil:
		return "NULL"
	case int64:
		return fmt.Sprintf("%d", val)
	case float64:
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "1"
		}
		return "0"
	case string:
		return quoteSQL(val)
	case []byte:
		return "X'" + hex.EncodeToString(val) + "'"
	case time.Time:
		return quoteSQL(val.Format(time.RFC3339Nano))
	default:
		return quoteSQL(fmt.Sprintf("%v", val))
	}
}

// quoteSQL wraps a string in single quotes with proper escaping.
// Newlines and carriage returns are replaced with char() concatenation
// so that every SQL statement stays on a single line.
func quoteSQL(s string) string {
	s = strings.ReplaceAll(s, "'", "''")
	if !strings.ContainsAny(s, "\n\r\x00") {
		return "'" + s + "'"
	}
	// Replace in order: \r\n first (two-char sequence), then lone \n / \r.
	s = strings.ReplaceAll(s, "\r\n", "' || char(13,10) || '")
	s = strings.ReplaceAll(s, "\n", "' || char(10) || '")
	s = strings.ReplaceAll(s, "\r", "' || char(13) || '")
	s = strings.ReplaceAll(s, "\x00", "' || char(0) || '")
	return "'" + s + "'"
}

// extractTableName parses the table name from an INSERT OR IGNORE statement.
func extractTableName(line string) string {
	const prefix = "INSERT OR IGNORE INTO "
	if !strings.HasPrefix(line, prefix) {
		return "unknown"
	}
	rest := line[len(prefix):]
	if idx := strings.IndexByte(rest, ' '); idx > 0 {
		return rest[:idx]
	}
	return "unknown"
}
