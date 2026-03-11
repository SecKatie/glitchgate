package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
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

// Close closes the underlying database connection.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// --------------------------------------------------------------------------
// Proxy key operations
// --------------------------------------------------------------------------

// CreateProxyKey inserts a new proxy key record.
func (s *SQLiteStore) CreateProxyKey(ctx context.Context, id, keyHash, keyPrefix, label string) error {
	const query = `INSERT INTO proxy_keys (id, key_hash, key_prefix, label, created_at) VALUES (?, ?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, query, id, keyHash, keyPrefix, label, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("create proxy key: %w", err)
	}

	return nil
}

// GetActiveProxyKeyByPrefix returns a single active (non-revoked) proxy key
// matching the given prefix, or sql.ErrNoRows if none is found.
func (s *SQLiteStore) GetActiveProxyKeyByPrefix(ctx context.Context, prefix string) (*ProxyKey, error) {
	const query = `SELECT id, key_hash, key_prefix, label, created_at, revoked_at FROM proxy_keys WHERE key_prefix = ? AND revoked_at IS NULL`

	row := s.db.QueryRowContext(ctx, query, prefix)

	var pk ProxyKey
	var revokedAt sql.NullTime

	if err := row.Scan(&pk.ID, &pk.KeyHash, &pk.KeyPrefix, &pk.Label, &pk.CreatedAt, &revokedAt); err != nil {
		return nil, fmt.Errorf("get proxy key by prefix: %w", err)
	}

	if revokedAt.Valid {
		pk.RevokedAt = &revokedAt.Time
	}

	return &pk, nil
}

// ListActiveProxyKeys returns all non-revoked proxy keys ordered by creation
// date descending.
func (s *SQLiteStore) ListActiveProxyKeys(ctx context.Context) ([]ProxyKeySummary, error) {
	const query = `SELECT id, key_prefix, label, created_at FROM proxy_keys WHERE revoked_at IS NULL ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list active proxy keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []ProxyKeySummary

	for rows.Next() {
		var k ProxyKeySummary
		if err := rows.Scan(&k.ID, &k.KeyPrefix, &k.Label, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan proxy key: %w", err)
		}

		keys = append(keys, k)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxy keys: %w", err)
	}

	// Return empty slice rather than nil to satisfy JSON serialisation.
	if keys == nil {
		keys = []ProxyKeySummary{}
	}

	return keys, nil
}

// RevokeProxyKey soft-deletes a proxy key by setting its revoked_at timestamp.
func (s *SQLiteStore) RevokeProxyKey(ctx context.Context, prefix string) error {
	const query = `UPDATE proxy_keys SET revoked_at = ? WHERE key_prefix = ? AND revoked_at IS NULL`

	res, err := s.db.ExecContext(ctx, query, time.Now().UTC(), prefix)
	if err != nil {
		return fmt.Errorf("revoke proxy key: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("no active proxy key found with prefix %q", prefix)
	}

	return nil
}

// --------------------------------------------------------------------------
// Request log operations
// --------------------------------------------------------------------------

// InsertRequestLog persists a single proxied-request log entry.
func (s *SQLiteStore) InsertRequestLog(ctx context.Context, entry *RequestLogEntry) error {
	const query = `INSERT INTO request_logs (
		id, proxy_key_id, timestamp, source_format, provider_name,
		model_requested, model_upstream, input_tokens, output_tokens,
		cache_creation_input_tokens, cache_read_input_tokens,
		latency_ms, status, request_body, response_body,
		estimated_cost_usd, error_details, is_streaming
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	isStreaming := 0
	if entry.IsStreaming {
		isStreaming = 1
	}

	_, err := s.db.ExecContext(ctx, query,
		entry.ID,
		entry.ProxyKeyID,
		entry.Timestamp,
		entry.SourceFormat,
		entry.ProviderName,
		entry.ModelRequested,
		entry.ModelUpstream,
		entry.InputTokens,
		entry.OutputTokens,
		entry.CacheCreationInputTokens,
		entry.CacheReadInputTokens,
		entry.LatencyMs,
		entry.Status,
		entry.RequestBody,
		entry.ResponseBody,
		entry.EstimatedCostUSD,
		entry.ErrorDetails,
		isStreaming,
	)
	if err != nil {
		return fmt.Errorf("insert request log: %w", err)
	}

	return nil
}

// --------------------------------------------------------------------------
// Request log listing and detail
// --------------------------------------------------------------------------

// allowedSortColumns prevents SQL injection in ORDER BY clauses.
var allowedSortColumns = map[string]string{ // #nosec G101 -- column allowlist, not credentials
	"timestamp":          "rl.timestamp",
	"latency_ms":         "rl.latency_ms",
	"input_tokens":       "rl.input_tokens",
	"output_tokens":      "rl.output_tokens",
	"estimated_cost_usd": "rl.estimated_cost_usd",
	"status":             "rl.status",
}

// ListRequestLogs returns a filtered, sorted, paginated list of log summaries
// and the total count matching the filters.
func (s *SQLiteStore) ListRequestLogs(ctx context.Context, params ListLogsParams) ([]RequestLogSummary, int64, error) {
	var where []string
	var args []any

	if params.Model != "" {
		where = append(where, "rl.model_requested = ?")
		args = append(args, params.Model)
	}
	if params.Status != 0 {
		where = append(where, "rl.status = ?")
		args = append(args, params.Status)
	}
	if params.KeyPrefix != "" {
		where = append(where, "pk.key_prefix = ?")
		args = append(args, params.KeyPrefix)
	}
	if params.From != "" {
		where = append(where, "rl.timestamp >= ?")
		args = append(args, params.From)
	}
	if params.To != "" {
		where = append(where, "rl.timestamp <= ?")
		args = append(args, params.To)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	// Count total matching rows.
	countQuery := fmt.Sprintf(`SELECT COUNT(*) FROM request_logs rl JOIN proxy_keys pk ON pk.id = rl.proxy_key_id %s`, whereClause)
	var total int64
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count request logs: %w", err)
	}

	// Determine sort column (safe against injection via allowlist).
	sortCol := "rl.timestamp"
	if col, ok := allowedSortColumns[params.Sort]; ok {
		sortCol = col
	}
	order := "DESC"
	if strings.EqualFold(params.Order, "asc") {
		order = "ASC"
	}

	perPage := params.PerPage
	if perPage <= 0 || perPage > 100 {
		perPage = 50
	}
	page := params.Page
	if page <= 0 {
		page = 1
	}
	offset := (page - 1) * perPage

	listQuery := fmt.Sprintf(`
		SELECT
			rl.id, rl.timestamp, rl.source_format, rl.provider_name,
			rl.model_requested, rl.model_upstream,
			pk.key_prefix, pk.label,
			rl.input_tokens, rl.output_tokens,
			rl.cache_creation_input_tokens, rl.cache_read_input_tokens,
			rl.latency_ms, rl.status,
			rl.estimated_cost_usd, rl.is_streaming, rl.error_details
		FROM request_logs rl
		JOIN proxy_keys pk ON pk.id = rl.proxy_key_id
		%s
		ORDER BY %s %s
		LIMIT ? OFFSET ?`, whereClause, sortCol, order)

	listArgs := append(args, perPage, offset)
	rows, err := s.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list request logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var logs []RequestLogSummary
	for rows.Next() {
		var l RequestLogSummary
		var isStreaming int
		if err := rows.Scan(
			&l.ID, &l.Timestamp, &l.SourceFormat, &l.ProviderName,
			&l.ModelRequested, &l.ModelUpstream,
			&l.ProxyKeyPrefix, &l.ProxyKeyLabel,
			&l.InputTokens, &l.OutputTokens,
			&l.CacheCreationInputTokens, &l.CacheReadInputTokens,
			&l.LatencyMs, &l.Status,
			&l.EstimatedCostUSD, &isStreaming, &l.ErrorDetails,
		); err != nil {
			return nil, 0, fmt.Errorf("scan request log: %w", err)
		}
		l.IsStreaming = isStreaming == 1
		logs = append(logs, l)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate request logs: %w", err)
	}
	if logs == nil {
		logs = []RequestLogSummary{}
	}

	return logs, total, nil
}

// GetRequestLog returns a single log entry with full request/response bodies.
func (s *SQLiteStore) GetRequestLog(ctx context.Context, id string) (*RequestLogDetail, error) {
	const query = `
		SELECT
			rl.id, rl.timestamp, rl.source_format, rl.provider_name,
			rl.model_requested, rl.model_upstream,
			pk.key_prefix, pk.label,
			rl.input_tokens, rl.output_tokens,
			rl.cache_creation_input_tokens, rl.cache_read_input_tokens,
			rl.latency_ms, rl.status,
			rl.estimated_cost_usd, rl.is_streaming, rl.error_details,
			rl.request_body, rl.response_body
		FROM request_logs rl
		JOIN proxy_keys pk ON pk.id = rl.proxy_key_id
		WHERE rl.id = ?`

	row := s.db.QueryRowContext(ctx, query, id)

	var d RequestLogDetail
	var isStreaming int
	if err := row.Scan(
		&d.ID, &d.Timestamp, &d.SourceFormat, &d.ProviderName,
		&d.ModelRequested, &d.ModelUpstream,
		&d.ProxyKeyPrefix, &d.ProxyKeyLabel,
		&d.InputTokens, &d.OutputTokens,
		&d.CacheCreationInputTokens, &d.CacheReadInputTokens,
		&d.LatencyMs, &d.Status,
		&d.EstimatedCostUSD, &isStreaming, &d.ErrorDetails,
		&d.RequestBody, &d.ResponseBody,
	); err != nil {
		return nil, fmt.Errorf("get request log: %w", err)
	}
	d.IsStreaming = isStreaming == 1

	return &d, nil
}

// CountLogsSince returns the number of request log entries created after the
// entry with the given ID that also match the active filter in params.
// Returns 0 if sinceID is empty or not found.
func (s *SQLiteStore) CountLogsSince(ctx context.Context, sinceID string, params ListLogsParams) (int64, error) {
	if sinceID == "" {
		return 0, nil
	}

	var where []string
	var args []any

	// The timestamp of the anchor entry (subquery).
	where = append(where, "rl.timestamp > (SELECT timestamp FROM request_logs WHERE id = ?)")
	args = append(args, sinceID)

	if params.Model != "" {
		where = append(where, "rl.model_requested = ?")
		args = append(args, params.Model)
	}
	if params.Status != 0 {
		where = append(where, "rl.status = ?")
		args = append(args, params.Status)
	}
	if params.KeyPrefix != "" {
		where = append(where, "pk.key_prefix = ?")
		args = append(args, params.KeyPrefix)
	}
	if params.From != "" {
		where = append(where, "rl.timestamp >= ?")
		args = append(args, params.From)
	}
	if params.To != "" {
		where = append(where, "rl.timestamp <= ?")
		args = append(args, params.To)
	}

	whereClause := "WHERE " + strings.Join(where, " AND ")
	query := fmt.Sprintf(`SELECT COUNT(*) FROM request_logs rl JOIN proxy_keys pk ON pk.id = rl.proxy_key_id %s`, whereClause) // #nosec G201 -- whereClause built from constant strings only; values bound via args

	var count int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count logs since: %w", err)
	}

	return count, nil
}

// --------------------------------------------------------------------------
// Cost operations
// --------------------------------------------------------------------------

// GetCostSummary returns aggregated cost totals for the given date range.
func (s *SQLiteStore) GetCostSummary(ctx context.Context, params CostParams) (*CostSummary, error) {
	baseQuery := `SELECT
		COALESCE(SUM(estimated_cost_usd), 0),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_input_tokens), 0),
		COALESCE(SUM(cache_read_input_tokens), 0),
		COUNT(*)
	FROM request_logs rl`

	var conditions []string
	var args []any

	if params.From != "" {
		conditions = append(conditions, "rl.timestamp >= ?")
		args = append(args, params.From)
	}
	if params.To != "" {
		conditions = append(conditions, "rl.timestamp <= ?")
		args = append(args, params.To)
	}
	if params.KeyPrefix != "" {
		baseQuery += " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
		conditions = append(conditions, "pk.key_prefix = ?")
		args = append(args, params.KeyPrefix)
	}

	if len(conditions) > 0 {
		baseQuery += " WHERE " + strings.Join(conditions, " AND ")
	}

	var cs CostSummary
	if err := s.db.QueryRowContext(ctx, baseQuery, args...).Scan(
		&cs.TotalCostUSD, &cs.TotalInputTokens, &cs.TotalOutputTokens,
		&cs.TotalCacheCreationTokens, &cs.TotalCacheReadTokens, &cs.TotalRequests,
	); err != nil {
		return nil, fmt.Errorf("get cost summary: %w", err)
	}

	return &cs, nil
}

// GetCostBreakdown returns cost aggregated by model or key.
func (s *SQLiteStore) GetCostBreakdown(ctx context.Context, params CostParams) ([]CostBreakdownEntry, error) {
	var query string
	var args []any

	switch params.GroupBy {
	case "key":
		query = `SELECT
			pk.key_prefix || ' (' || pk.label || ')' AS group_name,
			COALESCE(SUM(rl.estimated_cost_usd), 0),
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(rl.cache_creation_input_tokens), 0),
			COALESCE(SUM(rl.cache_read_input_tokens), 0),
			COUNT(*)
		FROM request_logs rl
		JOIN proxy_keys pk ON pk.id = rl.proxy_key_id`

		var conditions []string
		if params.From != "" {
			conditions = append(conditions, "rl.timestamp >= ?")
			args = append(args, params.From)
		}
		if params.To != "" {
			conditions = append(conditions, "rl.timestamp <= ?")
			args = append(args, params.To)
		}
		if params.KeyPrefix != "" {
			conditions = append(conditions, "pk.key_prefix = ?")
			args = append(args, params.KeyPrefix)
		}
		if len(conditions) > 0 {
			query += " WHERE " + strings.Join(conditions, " AND ")
		}
		query += " GROUP BY rl.proxy_key_id ORDER BY COALESCE(SUM(rl.estimated_cost_usd), 0) DESC"

	default: // "model" or unset
		query = `SELECT
			rl.model_upstream AS group_name,
			COALESCE(SUM(rl.estimated_cost_usd), 0),
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(rl.cache_creation_input_tokens), 0),
			COALESCE(SUM(rl.cache_read_input_tokens), 0),
			COUNT(*)
		FROM request_logs rl`

		var conditions []string
		if params.From != "" {
			conditions = append(conditions, "rl.timestamp >= ?")
			args = append(args, params.From)
		}
		if params.To != "" {
			conditions = append(conditions, "rl.timestamp <= ?")
			args = append(args, params.To)
		}
		if params.KeyPrefix != "" {
			query += " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
			conditions = append(conditions, "pk.key_prefix = ?")
			args = append(args, params.KeyPrefix)
		}
		if len(conditions) > 0 {
			query += " WHERE " + strings.Join(conditions, " AND ")
		}
		query += " GROUP BY rl.model_upstream ORDER BY COALESCE(SUM(rl.estimated_cost_usd), 0) DESC"
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get cost breakdown: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []CostBreakdownEntry
	for rows.Next() {
		var e CostBreakdownEntry
		if err := rows.Scan(&e.Group, &e.CostUSD, &e.InputTokens, &e.OutputTokens, &e.CacheCreationTokens, &e.CacheReadTokens, &e.Requests); err != nil {
			return nil, fmt.Errorf("scan cost breakdown: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost breakdown: %w", err)
	}
	if entries == nil {
		entries = []CostBreakdownEntry{}
	}

	return entries, nil
}

// GetCostTimeseries returns daily cost data for charting.
func (s *SQLiteStore) GetCostTimeseries(ctx context.Context, params CostParams) ([]CostTimeseriesEntry, error) {
	query := `SELECT
		SUBSTR(CAST(rl.timestamp AS TEXT), 1, 10) AS date,
		COALESCE(SUM(rl.estimated_cost_usd), 0),
		COUNT(*)
	FROM request_logs rl`

	var conditions []string
	var args []any

	if params.From != "" {
		conditions = append(conditions, "rl.timestamp >= ?")
		args = append(args, params.From)
	}
	if params.To != "" {
		conditions = append(conditions, "rl.timestamp <= ?")
		args = append(args, params.To)
	}
	if params.KeyPrefix != "" {
		query += " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
		conditions = append(conditions, "pk.key_prefix = ?")
		args = append(args, params.KeyPrefix)
	}

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ") // #nosec G202 -- conditions are hardcoded strings; user input is in parameterized args
	}
	query += " GROUP BY SUBSTR(CAST(rl.timestamp AS TEXT), 1, 10) ORDER BY date"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get cost timeseries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []CostTimeseriesEntry
	for rows.Next() {
		var e CostTimeseriesEntry
		if err := rows.Scan(&e.Date, &e.CostUSD, &e.Requests); err != nil {
			return nil, fmt.Errorf("scan cost timeseries: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost timeseries: %w", err)
	}
	if entries == nil {
		entries = []CostTimeseriesEntry{}
	}

	return entries, nil
}
