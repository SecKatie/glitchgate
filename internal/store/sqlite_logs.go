package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// fmtTime formats a time.Time as RFC3339Nano in UTC for consistent SQLite
// storage. Using a canonical string format ensures lexicographic ORDER BY
// works correctly (Go's time.Time.String() format does not sort properly).
func fmtTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

// normalizeToDate appends end-of-day if the value is a bare date (YYYY-MM-DD)
// so that "to" filters include the entire day. Also normalizes space-separated
// datetime formats to RFC3339 for consistent comparison.
func normalizeToDate(v string) string {
	if len(v) == 10 { // bare date like "2026-03-16"
		return v + "T23:59:59.999999999Z"
	}
	return normalizeTimestamp(v)
}

// normalizeTimestamp converts space-separated datetime formats to RFC3339 so
// that string comparisons against RFC3339 timestamps work correctly.
// Handles: "2026-03-10 00:00:00" → "2026-03-10T00:00:00Z"
func normalizeTimestamp(v string) string {
	if len(v) >= 19 && v[10] == ' ' {
		return v[:10] + "T" + v[11:19] + "Z"
	}
	return v
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
		reasoning_tokens, latency_ms, status, request_body, response_body,
		error_details, is_streaming, fallback_attempts, resolved_model_name,
		cost_usd
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

	isStreaming := 0
	if entry.IsStreaming {
		isStreaming = 1
	}

	fallbackAttempts := entry.FallbackAttempts
	if fallbackAttempts < 1 {
		fallbackAttempts = 1
	}

	// ResolvedModelName defaults to ModelRequested if not set (for backwards compatibility)
	resolvedModelName := entry.ResolvedModelName
	if resolvedModelName == "" {
		resolvedModelName = entry.ModelRequested
	}

	_, err := s.db.ExecContext(ctx, query,
		entry.ID,
		entry.ProxyKeyID,
		fmtTime(entry.Timestamp),
		entry.SourceFormat,
		entry.ProviderName,
		entry.ModelRequested,
		entry.ModelUpstream,
		entry.InputTokens,
		entry.OutputTokens,
		entry.CacheCreationInputTokens,
		entry.CacheReadInputTokens,
		entry.ReasoningTokens,
		entry.LatencyMs,
		entry.Status,
		entry.RequestBody,
		entry.ResponseBody,
		entry.ErrorDetails,
		isStreaming,
		fallbackAttempts,
		resolvedModelName,
		entry.CostUSD,
	)
	if err != nil {
		return fmt.Errorf("insert request log: %w", err)
	}

	return nil
}

// PruneRequestLogs deletes request log rows older than before in bounded batches.
func (s *SQLiteStore) PruneRequestLogs(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}

	const query = `
		DELETE FROM request_logs
		WHERE id IN (
			SELECT id
			FROM request_logs
			WHERE timestamp < ?
			ORDER BY timestamp ASC
			LIMIT ?
		)`

	res, err := s.db.ExecContext(ctx, query, fmtTime(before), limit)
	if err != nil {
		return 0, fmt.Errorf("prune request logs: %w", err)
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune request logs rows affected: %w", err)
	}

	return deleted, nil
}

// TrimRequestLogBodies replaces request_body and response_body with '[trimmed]'
// for log rows older than before, in bounded batches. Already-trimmed rows are
// skipped, making the operation idempotent.
func (s *SQLiteStore) TrimRequestLogBodies(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}

	const query = `
		UPDATE request_logs
		SET request_body = '[trimmed]', response_body = '[trimmed]'
		WHERE id IN (
			SELECT id
			FROM request_logs
			WHERE timestamp < ?
			AND request_body != '[trimmed]'
			ORDER BY timestamp ASC
			LIMIT ?
		)`

	res, err := s.db.ExecContext(ctx, query, fmtTime(before), limit)
	if err != nil {
		return 0, fmt.Errorf("trim request log bodies: %w", err)
	}

	trimmed, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("trim request log bodies rows affected: %w", err)
	}

	return trimmed, nil
}

// CountTrimmableLogBodies returns how many log rows older than before still
// have untrimmed bodies. Used for --dry-run estimates.
func (s *SQLiteStore) CountTrimmableLogBodies(ctx context.Context, before time.Time) (int64, error) {
	const query = `
		SELECT COUNT(*)
		FROM request_logs
		WHERE timestamp < ?
		AND request_body != '[trimmed]'`

	var count int64
	if err := s.db.QueryRowContext(ctx, query, fmtTime(before)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count trimmable log bodies: %w", err)
	}
	return count, nil
}

// --------------------------------------------------------------------------
// Request log listing and detail
// --------------------------------------------------------------------------

// allowedSortColumns prevents SQL injection in ORDER BY clauses.
var allowedSortColumns = map[string]string{ // #nosec G101 -- column allowlist, not credentials
	"timestamp":     "rl.timestamp",
	"latency_ms":    "rl.latency_ms",
	"input_tokens":  "rl.input_tokens",
	"output_tokens": "rl.output_tokens",
	"status":        "rl.status",
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
		args = append(args, normalizeTimestamp(params.From))
	}
	if params.To != "" {
		where = append(where, "rl.timestamp <= ?")
		args = append(args, normalizeToDate(params.To))
	}

	// Scope filtering.
	switch params.ScopeType {
	case "user":
		where = append(where, "pko.owner_user_id = ?")
		args = append(args, params.ScopeUserID)
	case "team":
		where = append(where, "tm.team_id = ?")
		args = append(args, params.ScopeTeamID)
	}

	whereClause := ""
	if len(where) > 0 {
		whereClause = "WHERE " + strings.Join(where, " AND ")
	}

	baseFrom := `FROM request_logs rl JOIN proxy_keys pk ON pk.id = rl.proxy_key_id`
	baseFrom = addOwnerScopeJoins(baseFrom, params.ScopeType)

	// Count total matching rows.
	countQuery := fmt.Sprintf(`SELECT COUNT(*) %s %s`, baseFrom, whereClause)
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
			rl.reasoning_tokens,
			rl.latency_ms, rl.status,
			rl.is_streaming, rl.error_details,
			rl.fallback_attempts
		%s
		%s
		ORDER BY %s %s
		LIMIT ? OFFSET ?`, baseFrom, whereClause, sortCol, order)

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
			&l.ReasoningTokens,
			&l.LatencyMs, &l.Status,
			&isStreaming, &l.ErrorDetails,
			&l.FallbackAttempts,
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
			rl.reasoning_tokens,
			rl.latency_ms, rl.status,
			rl.is_streaming, rl.error_details,
			rl.request_body, rl.response_body,
			rl.fallback_attempts
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
		&d.ReasoningTokens,
		&d.LatencyMs, &d.Status,
		&isStreaming, &d.ErrorDetails,
		&d.RequestBody, &d.ResponseBody,
		&d.FallbackAttempts,
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
		args = append(args, normalizeTimestamp(params.From))
	}
	if params.To != "" {
		where = append(where, "rl.timestamp <= ?")
		args = append(args, normalizeToDate(params.To))
	}

	whereClause := "WHERE " + strings.Join(where, " AND ")
	query := fmt.Sprintf(`SELECT COUNT(*) FROM request_logs rl JOIN proxy_keys pk ON pk.id = rl.proxy_key_id %s`, whereClause) // #nosec G201 -- whereClause built from constant strings only; values bound via args

	var count int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count logs since: %w", err)
	}

	return count, nil
}

// ListDistinctModels returns all distinct model_requested values from request_logs.
func (s *SQLiteStore) ListDistinctModels(ctx context.Context) ([]string, error) {
	const query = `SELECT DISTINCT model_requested FROM request_logs ORDER BY model_requested`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list distinct models: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var models []string
	for rows.Next() {
		var m string
		if err := rows.Scan(&m); err != nil {
			return nil, fmt.Errorf("scan distinct model: %w", err)
		}
		models = append(models, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate distinct models: %w", err)
	}
	if models == nil {
		models = []string{}
	}

	return models, nil
}

// ListDistinctStatuses returns all distinct response_status values from request_logs, ordered numerically.
func (s *SQLiteStore) ListDistinctStatuses(ctx context.Context) ([]int, error) {
	const query = `SELECT DISTINCT status FROM request_logs ORDER BY status`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list distinct statuses: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var statuses []int
	for rows.Next() {
		var st int
		if err := rows.Scan(&st); err != nil {
			return nil, fmt.Errorf("scan distinct status: %w", err)
		}
		statuses = append(statuses, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate distinct statuses: %w", err)
	}
	if statuses == nil {
		statuses = []int{}
	}

	return statuses, nil
}

// GetActivityStats returns aggregate request metrics since the given timestamp.
func (s *SQLiteStore) GetActivityStats(ctx context.Context, since time.Time) (*ActivityStats, error) {
	const query = `SELECT
		COUNT(*),
		COUNT(CASE WHEN status >= 400 THEN 1 END),
		COALESCE(AVG(latency_ms), 0)
		FROM request_logs WHERE timestamp >= ?`

	sinceUTC := since.UTC().Format("2006-01-02 15:04:05")
	var stats ActivityStats
	if err := s.db.QueryRowContext(ctx, query, sinceUTC).Scan(
		&stats.TotalRequests, &stats.ErrorCount, &stats.AvgLatencyMs,
	); err != nil {
		return nil, fmt.Errorf("get activity stats: %w", err)
	}
	return &stats, nil
}
