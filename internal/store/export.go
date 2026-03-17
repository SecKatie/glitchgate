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
	"strconv"
	"strings"
	"sync"
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

// ProgressFunc is called during export/import to report per-table progress.
// It receives the table name and the cumulative row count for that table.
type ProgressFunc func(table string, rows int64)

// BackupStore defines the export and import operations used for data backup and migration.
type BackupStore interface {
	Export(ctx context.Context, w io.Writer, progress ProgressFunc) error
	Import(ctx context.Context, r io.Reader, progress ProgressFunc, workers int) (*ImportStats, error)
}

// exportFromDB writes all persistent data as a gzip-compressed SQL dump to w.
// Each row is emitted as an INSERT OR IGNORE statement so the dump can be
// loaded into an existing database without conflicting with existing rows.
func exportFromDB(ctx context.Context, db *sql.DB, w io.Writer, progress ProgressFunc) error {
	gz := gzip.NewWriter(w)

	_, _ = fmt.Fprintln(gz, "-- glitchgate database export")
	_, _ = fmt.Fprintf(gz, "-- version: %d\n", exportVersion)
	_, _ = fmt.Fprintf(gz, "-- exported_at: %s\n", time.Now().UTC().Format(time.RFC3339))
	_, _ = fmt.Fprintf(gz, "-- tables: %s\n", strings.Join(exportTables, ", "))
	_, _ = fmt.Fprintln(gz)

	for _, table := range exportTables {
		n, err := writeTableSQL(ctx, db, gz, table)
		if err != nil {
			_ = gz.Close()
			return fmt.Errorf("export table %s: %w", table, err)
		}
		if progress != nil {
			progress(table, n)
		}
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("close gzip: %w", err)
	}
	return nil
}

// importBatchSize controls how many rows are batched into a single multi-value
// INSERT statement. Kept small because tables like request_logs have large
// text columns (request/response bodies) that dominate transfer time.
const importBatchSize = 100

// importBatch accumulates parsed rows for a single table and flushes them as a
// multi-value INSERT when the batch is full or the table changes.
type importBatch struct {
	table   string
	cols    []string
	rows    [][]any
	dialect string
}

func (b *importBatch) flush(ctx context.Context, tx *sql.Tx, stats *ImportStats) error {
	if len(b.rows) == 0 {
		return nil
	}

	nCols := len(b.cols)
	var sb strings.Builder
	// Pre-size: rough estimate to avoid repeated growth.
	sb.Grow(64 + len(b.rows)*nCols*8)

	if b.dialect == "postgres" {
		fmt.Fprintf(&sb, "INSERT INTO %s (%s) VALUES ", //nolint:gosec // table/col names from exportTables
			b.table, strings.Join(b.cols, ", "))
	} else {
		fmt.Fprintf(&sb, "INSERT OR IGNORE INTO %s (%s) VALUES ", //nolint:gosec // table/col names from exportTables
			b.table, strings.Join(b.cols, ", "))
	}

	args := make([]any, 0, len(b.rows)*nCols)
	for i, row := range b.rows {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteByte('(')
		for j := range row {
			if j > 0 {
				sb.WriteString(", ")
			}
			if b.dialect == "postgres" {
				fmt.Fprintf(&sb, "$%d", len(args)+1)
			} else {
				sb.WriteByte('?')
			}
			args = append(args, row[j])
		}
		sb.WriteByte(')')
	}

	if b.dialect == "postgres" {
		sb.WriteString(" ON CONFLICT DO NOTHING")
	}

	res, err := tx.ExecContext(ctx, sb.String(), args...)
	if err != nil {
		return fmt.Errorf("batch insert %s: %w", b.table, err)
	}
	n, _ := res.RowsAffected()
	stats.add(b.table, n)

	b.rows = b.rows[:0]
	return nil
}

// importWorkerPool routes row batches to a fixed set of goroutines, each of
// which executes its batch in its own database transaction. A per-table barrier
// (sync.WaitGroup) ensures FK-safe ordering: all inserts for table N must
// commit before any insert for table N+1 begins.
type importWorkerPool struct {
	ctx     context.Context
	db      *sql.DB
	dialect string
	stats   *ImportStats
	jobs    chan importPoolJob
	mu      sync.Mutex
	poolErr error
	wg      sync.WaitGroup
}

type importPoolJob struct {
	table   string
	cols    []string
	rows    [][]any
	barrier *sync.WaitGroup
}

func newImportWorkerPool(ctx context.Context, db *sql.DB, dialect string, n int, stats *ImportStats) *importWorkerPool {
	if n < 1 {
		n = 1
	}
	p := &importWorkerPool{
		ctx:     ctx,
		db:      db,
		dialect: dialect,
		stats:   stats,
		jobs:    make(chan importPoolJob, n*2),
	}
	for i := 0; i < n; i++ {
		p.wg.Add(1)
		go p.run()
	}
	return p
}

func (p *importWorkerPool) run() {
	defer p.wg.Done()
	for job := range p.jobs {
		if err := p.exec(job); err != nil {
			p.mu.Lock()
			if p.poolErr == nil {
				p.poolErr = err
			}
			p.mu.Unlock()
		}
		job.barrier.Done()
	}
}

func (p *importWorkerPool) exec(job importPoolJob) error {
	tx, err := p.db.BeginTx(p.ctx, nil)
	if err != nil {
		return err
	}
	b := &importBatch{table: job.table, cols: job.cols, rows: job.rows, dialect: p.dialect}
	if err := b.flush(p.ctx, tx, p.stats); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// submit copies rows, increments the barrier, and enqueues the job.
// Returns the first worker error seen so far, if any.
func (p *importWorkerPool) submit(barrier *sync.WaitGroup, table string, cols []string, rows [][]any) error {
	p.mu.Lock()
	err := p.poolErr
	p.mu.Unlock()
	if err != nil {
		return err
	}
	cp := make([][]any, len(rows))
	copy(cp, rows)
	barrier.Add(1)
	p.jobs <- importPoolJob{table: table, cols: cols, rows: cp, barrier: barrier}
	return nil
}

// firstErr returns the first error from any worker, if any.
func (p *importWorkerPool) firstErr() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.poolErr
}

// stop closes the job channel, waits for all workers to exit, and returns
// the first error encountered (if any).
func (p *importWorkerPool) stop() error {
	close(p.jobs)
	p.wg.Wait()
	return p.firstErr()
}

// importFromDump reads a gzip-compressed SQL dump from r and inserts rows
// using a pool of worker goroutines that each operate in their own transaction.
// A per-table barrier ensures FK-safe ordering between tables. The target
// database must already have migrations applied. Existing rows with conflicting
// primary keys are skipped. dialect must be "sqlite" or "postgres".
func importFromDump(ctx context.Context, db *sql.DB, r io.Reader, progress ProgressFunc, dialect string, workers int) (*ImportStats, error) {
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
			_, _ = fmt.Sscanf(line, "-- version: %d", &version)
		}
		if !strings.HasPrefix(line, "--") && strings.TrimSpace(line) != "" {
			firstDataLine = line
			break
		}
	}
	if version != exportVersion {
		return nil, fmt.Errorf("unsupported export version: %d", version)
	}

	// SQLite-specific: disable FK checks and apply bulk-import performance
	// pragmas. synchronous=NORMAL is crash-safe in WAL mode (fsyncs only at
	// checkpoint, not every commit) while still being much faster than FULL.
	// cache_size=-65536 grants 64 MB of page cache to reduce evictions on
	// large tables. Both are restored to safe defaults when the import finishes.
	if dialect == "sqlite" {
		for _, pragma := range []string{
			"PRAGMA foreign_keys=OFF",
			"PRAGMA synchronous=NORMAL",
			"PRAGMA journal_mode=WAL",
			"PRAGMA cache_size=-65536",
			"PRAGMA temp_store=MEMORY",
		} {
			if _, err := db.ExecContext(ctx, pragma); err != nil {
				return nil, fmt.Errorf("set pragma: %w", err)
			}
		}
		defer func() {
			_, _ = db.ExecContext(ctx, "PRAGMA foreign_keys=ON")
			_, _ = db.ExecContext(ctx, "PRAGMA synchronous=NORMAL")
			_, _ = db.ExecContext(ctx, "PRAGMA cache_size=-2000")
		}()
	}

	// For PostgreSQL, query which columns are boolean so we can convert
	// the SQLite 0/1 integer literals to proper Go bool values before binding.
	var pgBools map[string]map[string]bool
	if dialect == "postgres" {
		pgBools, err = queryPGBoolColumns(ctx, db)
		if err != nil {
			return nil, fmt.Errorf("query boolean columns: %w", err)
		}
	}

	stats := &ImportStats{}
	pool := newImportWorkerPool(ctx, db, dialect, workers, stats)

	// barrier is incremented for each submitted job and decremented by the
	// worker on completion. Calling barrier.Wait() at table boundaries ensures
	// all inserts for the previous table have committed before the next begins
	// (required for FK constraints).
	var barrier sync.WaitGroup

	var batchTable string
	var batchCols []string
	var pendingRows [][]any

	var currentTable string
	var currentCount int64

	reportProgress := func() {
		if progress != nil && currentTable != "" {
			progress(currentTable, currentCount)
		}
	}

	submitPending := func() error {
		if len(pendingRows) == 0 {
			return nil
		}
		err := pool.submit(&barrier, batchTable, batchCols, pendingRows)
		pendingRows = nil
		return err
	}

	processLine := func(line string) error {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			return nil
		}

		table, cols, rawVals, parseErr := parseInsertLine(line)
		if parseErr != nil {
			return parseErr
		}

		// If the table changed, flush pending rows then wait for all in-flight
		// inserts for the old table to commit before proceeding (FK barrier).
		if table != batchTable {
			if err := submitPending(); err != nil {
				return err
			}
			barrier.Wait()
			if err := pool.firstErr(); err != nil {
				return err
			}
			if table != currentTable {
				reportProgress()
				currentTable = table
				currentCount = 0
			}
			batchTable = table
			batchCols = cols
		}

		tableBools := pgBools[table]
		args := make([]any, len(rawVals))
		for i, raw := range rawVals {
			val, convErr := parseSQLLiteralToGo(raw)
			if convErr != nil {
				return fmt.Errorf("column %q: %w", cols[i], convErr)
			}
			if tableBools[cols[i]] {
				if n, ok := val.(int64); ok {
					val = n != 0
				}
			}
			// PostgreSQL enforces valid UTF-8 and rejects null bytes; SQLite
			// TEXT columns may contain arbitrary bytes (e.g. gzip-compressed
			// bodies, embedded NULs). Replace invalid sequences with the
			// Unicode replacement character and strip null bytes so the import
			// succeeds while preserving all other column data.
			if dialect == "postgres" {
				if s, ok := val.(string); ok {
					s = strings.ToValidUTF8(s, "\uFFFD")
					s = strings.ReplaceAll(s, "\x00", "")
					val = s
				}
			}
			args[i] = val
		}

		pendingRows = append(pendingRows, args)
		currentCount++

		if len(pendingRows) >= importBatchSize {
			if err := submitPending(); err != nil {
				return err
			}
			reportProgress()
		}
		return nil
	}

	// Process the first data line that broke us out of the header loop.
	if firstDataLine != "" {
		if err = processLine(firstDataLine); err != nil {
			_ = pool.stop()
			return nil, err
		}
	}

	for scanner.Scan() {
		if err = processLine(scanner.Text()); err != nil {
			_ = pool.stop()
			return nil, err
		}
	}
	if err = scanner.Err(); err != nil {
		_ = pool.stop()
		return nil, fmt.Errorf("read SQL: %w", err)
	}

	// Flush the final pending batch and wait for all workers to finish.
	if err = submitPending(); err != nil {
		_ = pool.stop()
		return nil, err
	}
	barrier.Wait()
	if err = pool.stop(); err != nil {
		return nil, err
	}
	reportProgress()

	return stats, nil
}

// queryPGBoolColumns returns a set of column names that are of type boolean
// for each table in the public schema.
func queryPGBoolColumns(ctx context.Context, db *sql.DB) (map[string]map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_name, column_name
		FROM information_schema.columns
		WHERE table_schema = 'public' AND data_type = 'boolean'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]map[string]bool)
	for rows.Next() {
		var table, col string
		if scanErr := rows.Scan(&table, &col); scanErr != nil {
			return nil, scanErr
		}
		if result[table] == nil {
			result[table] = make(map[string]bool)
		}
		result[table][col] = true
	}
	return result, rows.Err()
}

// parseInsertLine parses an "INSERT OR IGNORE INTO table (cols) VALUES (vals);"
// line into its components.
func parseInsertLine(line string) (table string, cols []string, rawVals []string, err error) {
	const prefix = "INSERT OR IGNORE INTO "
	if !strings.HasPrefix(line, prefix) {
		return "unknown", nil, nil, fmt.Errorf("not an INSERT OR IGNORE statement")
	}
	rest := line[len(prefix):]

	spaceIdx := strings.IndexByte(rest, ' ')
	if spaceIdx < 0 {
		return "unknown", nil, nil, fmt.Errorf("malformed INSERT: no space after table name")
	}
	table = rest[:spaceIdx]
	rest = rest[spaceIdx+1:]

	if !strings.HasPrefix(rest, "(") {
		return table, nil, nil, fmt.Errorf("malformed INSERT: expected ( after table name")
	}
	colEnd := strings.Index(rest, ")")
	if colEnd < 0 {
		return table, nil, nil, fmt.Errorf("malformed INSERT: unclosed column list")
	}
	for _, c := range strings.Split(rest[1:colEnd], ", ") {
		cols = append(cols, strings.TrimSpace(c))
	}
	rest = rest[colEnd+1:]

	const valuesKw = " VALUES "
	if !strings.HasPrefix(rest, valuesKw) {
		return table, nil, nil, fmt.Errorf("malformed INSERT: expected VALUES keyword")
	}
	rest = rest[len(valuesKw):]

	rest = strings.TrimSuffix(rest, ";")
	if !strings.HasPrefix(rest, "(") || !strings.HasSuffix(rest, ")") {
		return table, nil, nil, fmt.Errorf("malformed INSERT: expected (...) around values")
	}
	rawVals = splitSQLValues(rest[1 : len(rest)-1])
	return table, cols, rawVals, nil
}

// splitSQLValues splits a comma-separated SQL values string into individual
// raw value tokens, respecting string literals and nested parentheses.
func splitSQLValues(s string) []string {
	var values []string
	depth := 0
	inStr := false
	start := 0

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inStr {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++ // skip '' escape
				} else {
					inStr = false
				}
			}
		} else {
			switch c {
			case '\'':
				inStr = true
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth == 0 {
					values = append(values, strings.TrimSpace(s[start:i]))
					start = i + 1
				}
			}
		}
	}
	values = append(values, strings.TrimSpace(s[start:]))
	return values
}

// parseSQLLiteralToGo converts a SQL literal string from the dump format
// into an appropriate Go value for use as a query parameter.
func parseSQLLiteralToGo(s string) (any, error) {
	s = strings.TrimSpace(s)
	if strings.EqualFold(s, "NULL") {
		return nil, nil
	}
	// 'string' or 'it''s'
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'"), nil
	}
	// CAST(X'hex' AS TEXT) — strings with newlines/control chars
	if strings.HasPrefix(s, "CAST(X'") && strings.HasSuffix(s, "' AS TEXT)") {
		hexStr := s[7 : len(s)-10]
		b, err := hex.DecodeString(hexStr)
		if err != nil {
			return nil, fmt.Errorf("decode hex literal: %w", err)
		}
		return string(b), nil
	}
	// X'hex' — binary blob
	if len(s) >= 4 && strings.HasPrefix(s, "X'") && s[len(s)-1] == '\'' {
		b, err := hex.DecodeString(s[2 : len(s)-1])
		if err != nil {
			return nil, fmt.Errorf("decode blob literal: %w", err)
		}
		return b, nil
	}
	// Integer
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n, nil
	}
	// Float
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f, nil
	}
	return nil, fmt.Errorf("unrecognised SQL literal: %q", s)
}

// Export writes all persistent data as a gzip-compressed SQL dump to w.
func (s *SQLiteStore) Export(ctx context.Context, w io.Writer, progress ProgressFunc) error {
	return exportFromDB(ctx, s.db, w, progress)
}

// Import reads a gzip-compressed SQL dump from r and executes the INSERT
// statements. workers is ignored for SQLite because SQLite serialises all
// writes; extra goroutines add overhead without benefit.
func (s *SQLiteStore) Import(ctx context.Context, r io.Reader, progress ProgressFunc, _ int) (*ImportStats, error) {
	return importFromDump(ctx, s.db, r, progress, "sqlite", 1)
}

// Export writes all persistent data as a gzip-compressed SQL dump to w.
func (s *PostgreSQLStore) Export(ctx context.Context, w io.Writer, progress ProgressFunc) error {
	return exportFromDB(ctx, s.db, w, progress)
}

// Import reads a gzip-compressed SQL dump from r and inserts rows using a
// pool of worker goroutines, each operating in its own transaction. SQLite
// integer 0/1 values are automatically converted to booleans for columns
// declared as BOOLEAN in the PostgreSQL schema.
func (s *PostgreSQLStore) Import(ctx context.Context, r io.Reader, progress ProgressFunc, workers int) (*ImportStats, error) {
	return importFromDump(ctx, s.db, r, progress, "postgres", workers)
}

// ImportStats tracks how many rows were imported per table.
type ImportStats struct {
	mu     sync.Mutex
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
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Tables {
		if s.Tables[i].Table == table {
			s.Tables[i].Rows += rows
			return
		}
	}
	s.Tables = append(s.Tables, TableImportStat{Table: table, Rows: rows})
}

// writeTableSQL writes INSERT OR IGNORE statements for every row in table
// and returns the number of rows written.
func writeTableSQL(ctx context.Context, db *sql.DB, w io.Writer, table string) (int64, error) {
	// Table name is from our hardcoded list, not user input.
	// #nosec G202 -- table name from exportTables constant list
	rows, err := db.QueryContext(ctx, "SELECT * FROM "+table)
	if err != nil {
		return 0, err
	}
	defer func() { _ = rows.Close() }()

	cols, err := rows.Columns()
	if err != nil {
		return 0, err
	}

	prefix := fmt.Sprintf("INSERT OR IGNORE INTO %s (%s) VALUES", table, strings.Join(cols, ", "))

	var count int64
	for rows.Next() {
		values := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return count, err
		}

		literals := make([]string, len(values))
		for i, v := range values {
			literals[i] = sqlLiteral(v)
		}
		if _, err := fmt.Fprintf(w, "%s (%s);\n", prefix, strings.Join(literals, ", ")); err != nil {
			return count, err
		}
		count++
	}
	return count, rows.Err()
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
// Strings containing newlines, carriage returns, or null bytes are encoded
// as hex via CAST(X'...' AS TEXT) to keep each SQL statement on one line
// and avoid deeply nested expression trees that exceed SQLite's depth limit.
func quoteSQL(s string) string {
	if !strings.ContainsAny(s, "\n\r\x00") {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return "CAST(X'" + hex.EncodeToString([]byte(s)) + "' AS TEXT)"
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
