// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// --------------------------------------------------------------------------
// Request log operations
// --------------------------------------------------------------------------

// InsertRequestLog persists a single proxied-request log entry.
func (s *PostgreSQLStore) InsertRequestLog(ctx context.Context, entry *RequestLogEntry) error {
	const query = `INSERT INTO request_logs (
		id, proxy_key_id, timestamp, source_format, provider_name,
		model_requested, model_upstream, input_tokens, output_tokens,
		cache_creation_input_tokens, cache_read_input_tokens,
		reasoning_tokens, latency_ms, status, request_body, response_body,
		error_details, is_streaming, fallback_attempts, resolved_model_name,
		cost_usd
	) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21)`

	fallbackAttempts := entry.FallbackAttempts
	if fallbackAttempts < 1 {
		fallbackAttempts = 1
	}

	resolvedModelName := entry.ResolvedModelName
	if resolvedModelName == "" {
		resolvedModelName = entry.ModelRequested
	}

	_, err := s.db.ExecContext(ctx, query,
		entry.ID,
		entry.ProxyKeyID,
		entry.Timestamp.UTC(),
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
		entry.IsStreaming,
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
func (s *PostgreSQLStore) PruneRequestLogs(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}

	const query = `
		DELETE FROM request_logs
		WHERE id IN (
			SELECT id
			FROM request_logs
			WHERE timestamp < $1
			ORDER BY timestamp ASC
			LIMIT $2
		)`

	res, err := s.db.ExecContext(ctx, query, before.UTC(), limit)
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
// for log rows older than before, in bounded batches.
func (s *PostgreSQLStore) TrimRequestLogBodies(ctx context.Context, before time.Time, limit int) (int64, error) {
	if limit <= 0 {
		limit = 1000
	}

	const query = `
		UPDATE request_logs
		SET request_body = '[trimmed]', response_body = '[trimmed]'
		WHERE id IN (
			SELECT id
			FROM request_logs
			WHERE timestamp < $1
			AND request_body != '[trimmed]'
			ORDER BY timestamp ASC
			LIMIT $2
		)`

	res, err := s.db.ExecContext(ctx, query, before.UTC(), limit)
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
// have untrimmed bodies.
func (s *PostgreSQLStore) CountTrimmableLogBodies(ctx context.Context, before time.Time) (int64, error) {
	const query = `
		SELECT COUNT(*)
		FROM request_logs
		WHERE timestamp < $1
		AND request_body != '[trimmed]'`

	var count int64
	if err := s.db.QueryRowContext(ctx, query, before.UTC()).Scan(&count); err != nil {
		return 0, fmt.Errorf("count trimmable log bodies: %w", err)
	}
	return count, nil
}

// --------------------------------------------------------------------------
// Request log listing and detail
// --------------------------------------------------------------------------

// ListRequestLogs returns a filtered, sorted, paginated list of log summaries
// and the total count matching the filters.
func (s *PostgreSQLStore) ListRequestLogs(ctx context.Context, params ListLogsParams) ([]RequestLogSummary, int64, error) {
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

	countQuery := rebindForPostgres(fmt.Sprintf(`SELECT COUNT(*) %s %s`, baseFrom, whereClause)) // #nosec G201 -- baseFrom/whereClause built from constant strings; user values bound via args
	var total int64
	if err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count request logs: %w", err)
	}

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

	listQuery := rebindForPostgres(fmt.Sprintf(`
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
		LIMIT ? OFFSET ?`, baseFrom, whereClause, sortCol, order)) // #nosec G201,G202 -- constant strings; user values bound via args

	listArgs := append(args, perPage, offset)
	rows, err := s.db.QueryContext(ctx, listQuery, listArgs...)
	if err != nil {
		return nil, 0, fmt.Errorf("list request logs: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var logs []RequestLogSummary
	for rows.Next() {
		var l RequestLogSummary
		if err := rows.Scan(
			&l.ID, &l.Timestamp, &l.SourceFormat, &l.ProviderName,
			&l.ModelRequested, &l.ModelUpstream,
			&l.ProxyKeyPrefix, &l.ProxyKeyLabel,
			&l.InputTokens, &l.OutputTokens,
			&l.CacheCreationInputTokens, &l.CacheReadInputTokens,
			&l.ReasoningTokens,
			&l.LatencyMs, &l.Status,
			&l.IsStreaming, &l.ErrorDetails,
			&l.FallbackAttempts,
		); err != nil {
			return nil, 0, fmt.Errorf("scan request log: %w", err)
		}
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
func (s *PostgreSQLStore) GetRequestLog(ctx context.Context, id string) (*RequestLogDetail, error) {
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
		WHERE rl.id = $1`

	row := s.db.QueryRowContext(ctx, query, id)

	var d RequestLogDetail
	if err := row.Scan(
		&d.ID, &d.Timestamp, &d.SourceFormat, &d.ProviderName,
		&d.ModelRequested, &d.ModelUpstream,
		&d.ProxyKeyPrefix, &d.ProxyKeyLabel,
		&d.InputTokens, &d.OutputTokens,
		&d.CacheCreationInputTokens, &d.CacheReadInputTokens,
		&d.ReasoningTokens,
		&d.LatencyMs, &d.Status,
		&d.IsStreaming, &d.ErrorDetails,
		&d.RequestBody, &d.ResponseBody,
		&d.FallbackAttempts,
	); err != nil {
		return nil, fmt.Errorf("get request log: %w", err)
	}

	return &d, nil
}

// CountLogsSince returns the number of request log entries created after the
// entry with the given ID that also match the active filter in params.
func (s *PostgreSQLStore) CountLogsSince(ctx context.Context, sinceID string, params ListLogsParams) (int64, error) {
	if sinceID == "" {
		return 0, nil
	}

	var where []string
	var args []any

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
	query := rebindForPostgres(fmt.Sprintf(`SELECT COUNT(*) FROM request_logs rl JOIN proxy_keys pk ON pk.id = rl.proxy_key_id %s`, whereClause)) // #nosec G201 -- whereClause built from constant strings; user values bound via args

	var count int64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count logs since: %w", err)
	}

	return count, nil
}

// ListDistinctModels returns all distinct model_requested values from request_logs.
func (s *PostgreSQLStore) ListDistinctModels(ctx context.Context) ([]string, error) {
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

// ListDistinctStatuses returns all distinct response_status values from request_logs.
func (s *PostgreSQLStore) ListDistinctStatuses(ctx context.Context) ([]int, error) {
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
func (s *PostgreSQLStore) GetActivityStats(ctx context.Context, since time.Time) (*ActivityStats, error) {
	const query = `SELECT
		COUNT(*),
		COUNT(CASE WHEN status >= 400 THEN 1 END),
		COALESCE(AVG(latency_ms), 0)
		FROM request_logs WHERE timestamp >= $1`

	var stats ActivityStats
	if err := s.db.QueryRowContext(ctx, query, since.UTC()).Scan(
		&stats.TotalRequests, &stats.ErrorCount, &stats.AvgLatencyMs,
	); err != nil {
		return nil, fmt.Errorf("get activity stats: %w", err)
	}
	return &stats, nil
}
