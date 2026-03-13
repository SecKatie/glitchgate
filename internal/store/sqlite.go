package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codeberg.org/kglitchy/glitchgate/internal/config"
	"codeberg.org/kglitchy/glitchgate/internal/pricing"
	"codeberg.org/kglitchy/glitchgate/internal/provider/copilot"
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

type providerNameRewrite struct {
	oldName     string
	newName     string
	modelExact  string
	modelPrefix string
}

// NormalizeLoggedProviderNames rewrites legacy canonical provider keys in
// request_logs to the configured provider names used for runtime identity.
func (s *SQLiteStore) NormalizeLoggedProviderNames(ctx context.Context, cfg *config.Config) error {
	if cfg == nil {
		return nil
	}

	rewrites, err := providerNameRewrites(cfg)
	if err != nil {
		return err
	}
	if len(rewrites) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin provider-name normalization: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	for _, rewrite := range rewrites {
		query := `UPDATE request_logs SET provider_name = ? WHERE provider_name = ? AND model_requested = ?`
		args := []any{rewrite.newName, rewrite.oldName, rewrite.modelExact}
		if rewrite.modelPrefix != "" {
			query = `UPDATE request_logs SET provider_name = ? WHERE provider_name = ? AND model_requested LIKE ?`
			args = []any{rewrite.newName, rewrite.oldName, rewrite.modelPrefix + "%"}
		}
		if _, err = tx.ExecContext(ctx, query, args...); err != nil {
			return fmt.Errorf("normalize provider name %q -> %q: %w", rewrite.oldName, rewrite.newName, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit provider-name normalization: %w", err)
	}
	return nil
}

func providerNameRewrites(cfg *config.Config) ([]providerNameRewrite, error) {
	exact := make([]providerNameRewrite, 0)
	wildcards := make([]providerNameRewrite, 0)
	seen := make(map[string]string)

	for _, mm := range cfg.ModelList {
		if mm.Provider == "" || len(mm.Fallbacks) > 0 {
			continue
		}

		pc, err := cfg.FindProvider(mm.Provider)
		if err != nil {
			return nil, fmt.Errorf("find provider for model %q: %w", mm.ModelName, err)
		}

		oldName := legacyLoggedProviderName(*pc)
		if oldName == "" || oldName == pc.Name {
			continue
		}

		rewrite := providerNameRewrite{
			oldName: oldName,
			newName: pc.Name,
		}
		if strings.HasSuffix(mm.ModelName, "/*") {
			rewrite.modelPrefix = strings.TrimSuffix(mm.ModelName, "*")
		} else {
			rewrite.modelExact = mm.ModelName
		}

		conflictKey := rewrite.oldName + "\x00" + rewrite.modelExact + "\x00" + rewrite.modelPrefix
		if existing, ok := seen[conflictKey]; ok {
			if existing != rewrite.newName {
				return nil, fmt.Errorf(
					"ambiguous provider-name normalization for %q and model pattern %q: %q vs %q",
					rewrite.oldName,
					rewritePattern(rewrite),
					existing,
					rewrite.newName,
				)
			}
			continue
		}
		seen[conflictKey] = rewrite.newName

		if rewrite.modelPrefix != "" {
			wildcards = append(wildcards, rewrite)
			continue
		}
		exact = append(exact, rewrite)
	}

	return append(exact, wildcards...), nil
}

func legacyLoggedProviderName(pc config.ProviderConfig) string {
	baseURL := pc.BaseURL
	if pc.Type == "github_copilot" && baseURL == "" {
		baseURL = copilot.DefaultAPIURL
	}
	return pricing.ProviderKey(pc.Type, baseURL)
}

func rewritePattern(rewrite providerNameRewrite) string {
	if rewrite.modelExact != "" {
		return rewrite.modelExact
	}
	return rewrite.modelPrefix + "*"
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

// UpdateKeyLabel updates the label of an active proxy key identified by prefix.
func (s *SQLiteStore) UpdateKeyLabel(ctx context.Context, prefix, label string) error {
	const query = `UPDATE proxy_keys SET label = ? WHERE key_prefix = ? AND revoked_at IS NULL`

	res, err := s.db.ExecContext(ctx, query, label, prefix)
	if err != nil {
		return fmt.Errorf("update key label: %w", err)
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

// RecordAuditEvent inserts a new audit trail entry.
func (s *SQLiteStore) RecordAuditEvent(ctx context.Context, action, keyPrefix, detail string) error {
	const query = `INSERT INTO audit_events (action, key_prefix, detail, created_at) VALUES (?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, query, action, keyPrefix, detail, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("record audit event: %w", err)
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
		reasoning_tokens, latency_ms, status, request_body, response_body,
		error_details, is_streaming, fallback_attempts, resolved_model_name
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`

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
		entry.Timestamp,
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

	res, err := s.db.ExecContext(ctx, query, before, limit)
	if err != nil {
		return 0, fmt.Errorf("prune request logs: %w", err)
	}

	deleted, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune request logs rows affected: %w", err)
	}

	return deleted, nil
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
		args = append(args, params.From)
	}
	if params.To != "" {
		where = append(where, "rl.timestamp <= ?")
		args = append(args, params.To)
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

// GetModelUsageSummary returns aggregated usage stats for the given model_requested value.
// Returns a zero-value summary (not an error) when no logs exist for the model.
func (s *SQLiteStore) GetModelUsageSummary(ctx context.Context, modelName string) (*ModelUsageSummary, error) {
	const query = `SELECT
		COUNT(*),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(cache_creation_input_tokens), 0),
		COALESCE(SUM(cache_read_input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(MAX(provider_name), ''),
		COALESCE(MAX(model_upstream), '')
	FROM request_logs
	WHERE model_requested = ?`

	var summary ModelUsageSummary
	if err := s.db.QueryRowContext(ctx, query, modelName).Scan(
		&summary.RequestCount,
		&summary.InputTokens,
		&summary.CacheCreationInputTokens,
		&summary.CacheReadInputTokens,
		&summary.OutputTokens,
		&summary.ProviderName,
		&summary.UpstreamModel,
	); err != nil {
		return nil, fmt.Errorf("get model usage summary: %w", err)
	}

	return &summary, nil
}

// GetAllModelUsageSummaries returns aggregated usage stats for every model seen in request_logs,
// keyed by model name. Uses resolved_model_name to capture the actual model used after fallback resolution.
func (s *SQLiteStore) GetAllModelUsageSummaries(ctx context.Context) (map[string]*ModelUsageSummary, error) {
	const query = `SELECT
		COALESCE(resolved_model_name, model_requested) AS model_name,
		COUNT(*),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(cache_creation_input_tokens), 0),
		COALESCE(SUM(cache_read_input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(MAX(provider_name), ''),
		COALESCE(MAX(model_upstream), '')
	FROM request_logs
	GROUP BY COALESCE(resolved_model_name, model_requested)`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get all model usage summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]*ModelUsageSummary)
	for rows.Next() {
		var modelName string
		var summary ModelUsageSummary
		if err := rows.Scan(&modelName, &summary.RequestCount, &summary.InputTokens, &summary.CacheCreationInputTokens, &summary.CacheReadInputTokens, &summary.OutputTokens, &summary.ProviderName, &summary.UpstreamModel); err != nil {
			return nil, fmt.Errorf("scan model usage summary: %w", err)
		}
		result[modelName] = &summary
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model usage summaries: %w", err)
	}
	return result, nil
}

// GetModelCostPricingGroups returns token totals for a specific model_requested value,
// grouped by provider/model pair so the web layer can apply pricing rates accurately.
func (s *SQLiteStore) GetModelCostPricingGroups(ctx context.Context, modelName string) ([]CostPricingGroup, error) {
	const query = `SELECT
		model_requested,
		COALESCE(provider_name, '') AS provider_name,
		COALESCE(model_upstream, '') AS model_upstream,
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_input_tokens), 0),
		COALESCE(SUM(cache_read_input_tokens), 0)
	FROM request_logs
	WHERE model_requested = ?
	GROUP BY provider_name, model_upstream`

	rows, err := s.db.QueryContext(ctx, query, modelName)
	if err != nil {
		return nil, fmt.Errorf("get model cost pricing groups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var groups []CostPricingGroup
	for rows.Next() {
		var g CostPricingGroup
		if err := rows.Scan(&g.ModelRequested, &g.ProviderName, &g.ModelUpstream, &g.InputTokens, &g.OutputTokens, &g.CacheCreationTokens, &g.CacheReadTokens); err != nil {
			return nil, fmt.Errorf("scan model cost pricing group: %w", err)
		}
		groups = append(groups, g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model cost pricing groups: %w", err)
	}
	return groups, nil
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

// --------------------------------------------------------------------------
// Cost operations
// --------------------------------------------------------------------------

// GetCostSummary returns aggregated cost totals for the given date range.
func (s *SQLiteStore) GetCostSummary(ctx context.Context, params CostParams) (*CostSummary, error) {
	needsKeyJoin := params.KeyPrefix != "" || params.ScopeType == "user" || params.ScopeType == "team"

	baseQuery := `SELECT
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_input_tokens), 0),
		COALESCE(SUM(cache_read_input_tokens), 0),
		COUNT(*)
	FROM request_logs rl`

	if needsKeyJoin {
		baseQuery += " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
	}
	baseQuery = addOwnerScopeJoins(baseQuery, params.ScopeType)

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
		conditions = append(conditions, "pk.key_prefix = ?")
		args = append(args, params.KeyPrefix)
	}
	switch params.GroupBy {
	case "provider":
		conditions, args = appendProviderConditions(conditions, args, "rl.provider_name", params)
	default: // "model"
		if params.GroupFilter != "" {
			conditions = append(conditions, "rl.model_requested LIKE ?")
			args = append(args, params.GroupFilter+"%")
		}
	}
	conditions, args = appendOwnerScopeConditions(conditions, args, params.ScopeType, params.ScopeUserID, params.ScopeTeamID)

	if len(conditions) > 0 {
		baseQuery += " WHERE " + strings.Join(conditions, " AND ")
	}

	var cs CostSummary
	if err := s.db.QueryRowContext(ctx, baseQuery, args...).Scan(
		&cs.TotalInputTokens, &cs.TotalOutputTokens,
		&cs.TotalCacheCreationTokens, &cs.TotalCacheReadTokens, &cs.TotalRequests,
	); err != nil {
		return nil, fmt.Errorf("get cost summary: %w", err)
	}

	return &cs, nil
}

// GetCostBreakdown returns cost aggregated by model, key, or provider.
func (s *SQLiteStore) GetCostBreakdown(ctx context.Context, params CostParams) ([]CostBreakdownEntry, error) {
	needsKeyJoin := params.KeyPrefix != "" || params.ScopeType == "user" || params.ScopeType == "team"

	var query string
	var args []any

	switch params.GroupBy {
	case "key":
		query = `SELECT
			pk.key_prefix || ' (' || pk.label || ')' AS group_name,
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(rl.cache_creation_input_tokens), 0),
			COALESCE(SUM(rl.cache_read_input_tokens), 0),
			COUNT(*)
		FROM request_logs rl
		JOIN proxy_keys pk ON pk.id = rl.proxy_key_id`
		query = addOwnerScopeJoins(query, params.ScopeType)

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
		conditions, args = appendOwnerScopeConditions(conditions, args, params.ScopeType, params.ScopeUserID, params.ScopeTeamID)
		if len(conditions) > 0 {
			query += " WHERE " + strings.Join(conditions, " AND ")
		}
		query += " GROUP BY rl.proxy_key_id ORDER BY COUNT(*) DESC, group_name ASC"

	case "provider":
		query = `SELECT
			rl.provider_name AS group_name,
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(rl.cache_creation_input_tokens), 0),
			COALESCE(SUM(rl.cache_read_input_tokens), 0),
			COUNT(*)
		FROM request_logs rl`

		if needsKeyJoin {
			query += " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
		}
		query = addOwnerScopeJoins(query, params.ScopeType)

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
		conditions, args = appendProviderConditions(conditions, args, "rl.provider_name", params)
		conditions, args = appendOwnerScopeConditions(conditions, args, params.ScopeType, params.ScopeUserID, params.ScopeTeamID)
		if len(conditions) > 0 {
			query += " WHERE " + strings.Join(conditions, " AND ")
		}
		query += " GROUP BY rl.provider_name ORDER BY COUNT(*) DESC, group_name ASC"

	default: // "model" or unset
		query = `SELECT
			rl.model_requested AS group_name,
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(rl.cache_creation_input_tokens), 0),
			COALESCE(SUM(rl.cache_read_input_tokens), 0),
			COUNT(*)
		FROM request_logs rl`

		if needsKeyJoin {
			query += " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
		}
		query = addOwnerScopeJoins(query, params.ScopeType)

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
		if params.GroupFilter != "" {
			conditions = append(conditions, "rl.model_requested LIKE ?")
			args = append(args, params.GroupFilter+"%")
		}
		conditions, args = appendOwnerScopeConditions(conditions, args, params.ScopeType, params.ScopeUserID, params.ScopeTeamID)
		if len(conditions) > 0 {
			query += " WHERE " + strings.Join(conditions, " AND ")
		}
		query += " GROUP BY rl.model_requested ORDER BY COUNT(*) DESC, group_name ASC"
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get cost breakdown: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []CostBreakdownEntry
	for rows.Next() {
		var e CostBreakdownEntry
		if err := rows.Scan(&e.Group, &e.InputTokens, &e.OutputTokens, &e.CacheCreationTokens, &e.CacheReadTokens, &e.Requests); err != nil {
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

// GetCostPricingGroups returns token totals grouped by exact provider/model
// pair so pricing can be recomputed accurately in the web layer.
func (s *SQLiteStore) GetCostPricingGroups(ctx context.Context, params CostParams) ([]CostPricingGroup, error) {
	groupByKey := params.GroupBy == "key"
	needsKeyJoin := groupByKey || params.KeyPrefix != "" || params.ScopeType == "user" || params.ScopeType == "team"

	keyPrefixSelect := ""
	keyPrefixGroupBy := ""
	if groupByKey {
		keyPrefixSelect = "\n\t\tCOALESCE(pk.key_prefix, ''),\n\t\tCOALESCE(pk.key_prefix || ' (' || pk.label || ')', ''),"
		keyPrefixGroupBy = ", pk.key_prefix, pk.label"
	}

	query := `SELECT
		rl.model_requested,
		rl.provider_name,
		rl.model_upstream,` + keyPrefixSelect + `
		COALESCE(SUM(rl.input_tokens), 0),
		COALESCE(SUM(rl.output_tokens), 0),
		COALESCE(SUM(rl.cache_creation_input_tokens), 0),
		COALESCE(SUM(rl.cache_read_input_tokens), 0)
	FROM request_logs rl`

	if needsKeyJoin {
		query += " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
	}
	query = addOwnerScopeJoins(query, params.ScopeType)

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
		conditions = append(conditions, "pk.key_prefix = ?")
		args = append(args, params.KeyPrefix)
	}
	switch params.GroupBy {
	case "provider":
		conditions, args = appendProviderConditions(conditions, args, "rl.provider_name", params)
	default:
		if params.GroupFilter != "" {
			conditions = append(conditions, "rl.model_requested LIKE ?")
			args = append(args, params.GroupFilter+"%")
		}
	}
	conditions, args = appendOwnerScopeConditions(conditions, args, params.ScopeType, params.ScopeUserID, params.ScopeTeamID)

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ") // #nosec G202 -- conditions are constant strings; user values bound via ? placeholders in args
	}
	query += " GROUP BY rl.model_requested, rl.provider_name, rl.model_upstream" + keyPrefixGroupBy +
		" ORDER BY rl.model_requested, rl.provider_name, rl.model_upstream" + keyPrefixGroupBy

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get cost pricing groups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var groups []CostPricingGroup
	for rows.Next() {
		var group CostPricingGroup
		var scanErr error
		if groupByKey {
			scanErr = rows.Scan(
				&group.ModelRequested,
				&group.ProviderName,
				&group.ModelUpstream,
				&group.ProxyKeyPrefix,
				&group.ProxyKeyGroup,
				&group.InputTokens,
				&group.OutputTokens,
				&group.CacheCreationTokens,
				&group.CacheReadTokens,
			)
		} else {
			scanErr = rows.Scan(
				&group.ModelRequested,
				&group.ProviderName,
				&group.ModelUpstream,
				&group.InputTokens,
				&group.OutputTokens,
				&group.CacheCreationTokens,
				&group.CacheReadTokens,
			)
		}
		if scanErr != nil {
			return nil, fmt.Errorf("scan cost pricing group: %w", scanErr)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost pricing groups: %w", err)
	}
	if groups == nil {
		groups = []CostPricingGroup{}
	}

	return groups, nil
}

// GetCostTimeseries returns daily cost data for charting.
func (s *SQLiteStore) GetCostTimeseries(ctx context.Context, params CostParams) ([]CostTimeseriesEntry, error) {
	needsKeyJoin := params.KeyPrefix != "" || params.ScopeType == "user" || params.ScopeType == "team"

	if params.TzLocation != nil {
		return s.getCostTimeseriesWithLocation(ctx, params, needsKeyJoin)
	}

	// Shift the stored UTC timestamp to local time before bucketing by date.
	// SQLite's datetime() accepts '+N seconds' modifiers.
	// #nosec G201 -- offset is server-computed from IANA timezone, not user input
	dateExpr := fmt.Sprintf("SUBSTR(datetime(SUBSTR(CAST(rl.timestamp AS TEXT), 1, 19), '%+d seconds'), 1, 10)", params.TzOffsetSeconds)
	query := fmt.Sprintf(`SELECT
		%s AS date,
		COUNT(*)
	FROM request_logs rl`, dateExpr) // #nosec G201

	if needsKeyJoin {
		query += " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
	}
	query = addOwnerScopeJoins(query, params.ScopeType)

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
		conditions = append(conditions, "pk.key_prefix = ?")
		args = append(args, params.KeyPrefix)
	}
	switch params.GroupBy {
	case "provider":
		conditions, args = appendProviderConditions(conditions, args, "rl.provider_name", params)
	default: // "model"
		if params.GroupFilter != "" {
			conditions = append(conditions, "rl.model_upstream LIKE ?")
			args = append(args, params.GroupFilter+"%")
		}
	}
	conditions, args = appendOwnerScopeConditions(conditions, args, params.ScopeType, params.ScopeUserID, params.ScopeTeamID)

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ") // #nosec G202 -- conditions are hardcoded strings; user input is in parameterized args
	}
	query += " GROUP BY " + dateExpr + " ORDER BY date" // #nosec G201

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get cost timeseries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []CostTimeseriesEntry
	for rows.Next() {
		var e CostTimeseriesEntry
		if err := rows.Scan(&e.Date, &e.Requests); err != nil {
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

// GetCostTimeseriesPricingGroups returns daily token totals grouped by exact
// provider/model pair so pricing can be computed accurately in the web layer.
func (s *SQLiteStore) GetCostTimeseriesPricingGroups(ctx context.Context, params CostParams) ([]CostTimeseriesPricingGroup, error) {
	needsKeyJoin := params.KeyPrefix != "" || params.ScopeType == "user" || params.ScopeType == "team"

	if params.TzLocation != nil {
		return s.getCostTimeseriesPricingGroupsWithLocation(ctx, params, needsKeyJoin)
	}

	// Shift the stored UTC timestamp to local time before bucketing by date.
	// #nosec G201 -- offset is server-computed from IANA timezone, not user input
	dateExpr := fmt.Sprintf("SUBSTR(datetime(SUBSTR(CAST(rl.timestamp AS TEXT), 1, 19), '%+d seconds'), 1, 10)", params.TzOffsetSeconds)
	query := fmt.Sprintf(`SELECT
		%s AS date,
		rl.provider_name,
		rl.model_upstream,
		COALESCE(SUM(rl.input_tokens), 0),
		COALESCE(SUM(rl.output_tokens), 0),
		COALESCE(SUM(rl.cache_creation_input_tokens), 0),
		COALESCE(SUM(rl.cache_read_input_tokens), 0),
		COUNT(*)
	FROM request_logs rl`, dateExpr) // #nosec G201

	if needsKeyJoin {
		query += " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
	}
	query = addOwnerScopeJoins(query, params.ScopeType)

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
		conditions = append(conditions, "pk.key_prefix = ?")
		args = append(args, params.KeyPrefix)
	}
	switch params.GroupBy {
	case "provider":
		conditions, args = appendProviderConditions(conditions, args, "rl.provider_name", params)
	default:
		if params.GroupFilter != "" {
			conditions = append(conditions, "rl.model_requested LIKE ?")
			args = append(args, params.GroupFilter+"%")
		}
	}
	conditions, args = appendOwnerScopeConditions(conditions, args, params.ScopeType, params.ScopeUserID, params.ScopeTeamID)

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ") // #nosec G202 -- conditions are hardcoded strings; user input is in parameterized args
	}
	query += " GROUP BY " + dateExpr + ", rl.provider_name, rl.model_upstream ORDER BY date, rl.provider_name, rl.model_upstream" // #nosec G201

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get cost timeseries pricing groups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var groups []CostTimeseriesPricingGroup
	for rows.Next() {
		var group CostTimeseriesPricingGroup
		if err := rows.Scan(
			&group.Date,
			&group.ProviderName,
			&group.ModelUpstream,
			&group.InputTokens,
			&group.OutputTokens,
			&group.CacheCreationTokens,
			&group.CacheReadTokens,
			&group.Requests,
		); err != nil {
			return nil, fmt.Errorf("scan cost timeseries pricing group: %w", err)
		}
		groups = append(groups, group)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost timeseries pricing groups: %w", err)
	}
	if groups == nil {
		groups = []CostTimeseriesPricingGroup{}
	}

	return groups, nil
}

func (s *SQLiteStore) getCostTimeseriesWithLocation(ctx context.Context, params CostParams, needsKeyJoin bool) ([]CostTimeseriesEntry, error) {
	query := `SELECT
		rl.timestamp
	FROM request_logs rl`

	if needsKeyJoin {
		query += " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
	}
	query = addOwnerScopeJoins(query, params.ScopeType)

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
		conditions = append(conditions, "pk.key_prefix = ?")
		args = append(args, params.KeyPrefix)
	}
	switch params.GroupBy {
	case "provider":
		conditions, args = appendProviderConditions(conditions, args, "rl.provider_name", params)
	default:
		if params.GroupFilter != "" {
			conditions = append(conditions, "rl.model_requested LIKE ?")
			args = append(args, params.GroupFilter+"%")
		}
	}
	conditions, args = appendOwnerScopeConditions(conditions, args, params.ScopeType, params.ScopeUserID, params.ScopeTeamID)

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ") // #nosec G202 -- conditions are hardcoded strings; user input is in parameterized args
	}
	query += " ORDER BY rl.timestamp"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get cost timeseries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	buckets := make(map[string]int64)
	var dates []string
	for rows.Next() {
		var timestamp time.Time
		if err := rows.Scan(&timestamp); err != nil {
			return nil, fmt.Errorf("scan cost timeseries: %w", err)
		}

		date := timestamp.In(params.TzLocation).Format("2006-01-02")
		if _, ok := buckets[date]; !ok {
			dates = append(dates, date)
		}
		buckets[date]++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost timeseries: %w", err)
	}

	entries := make([]CostTimeseriesEntry, 0, len(dates))
	for _, date := range dates {
		entries = append(entries, CostTimeseriesEntry{
			Date:     date,
			Requests: buckets[date],
		})
	}
	if entries == nil {
		entries = []CostTimeseriesEntry{}
	}

	return entries, nil
}

func (s *SQLiteStore) getCostTimeseriesPricingGroupsWithLocation(ctx context.Context, params CostParams, needsKeyJoin bool) ([]CostTimeseriesPricingGroup, error) {
	query := `SELECT
		rl.timestamp,
		rl.provider_name,
		rl.model_upstream,
		rl.input_tokens,
		rl.output_tokens,
		rl.cache_creation_input_tokens,
		rl.cache_read_input_tokens
	FROM request_logs rl`

	if needsKeyJoin {
		query += " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
	}
	query = addOwnerScopeJoins(query, params.ScopeType)

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
		conditions = append(conditions, "pk.key_prefix = ?")
		args = append(args, params.KeyPrefix)
	}
	switch params.GroupBy {
	case "provider":
		conditions, args = appendProviderConditions(conditions, args, "rl.provider_name", params)
	default:
		if params.GroupFilter != "" {
			conditions = append(conditions, "rl.model_requested LIKE ?")
			args = append(args, params.GroupFilter+"%")
		}
	}
	conditions, args = appendOwnerScopeConditions(conditions, args, params.ScopeType, params.ScopeUserID, params.ScopeTeamID)

	if len(conditions) > 0 {
		query += " WHERE " + strings.Join(conditions, " AND ") // #nosec G202 -- conditions are hardcoded strings; user input is in parameterized args
	}
	query += " ORDER BY rl.timestamp, rl.provider_name, rl.model_upstream"

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("get cost timeseries pricing groups: %w", err)
	}
	defer func() { _ = rows.Close() }()

	type bucket struct {
		group CostTimeseriesPricingGroup
	}

	orderedKeys := make([]string, 0)
	buckets := make(map[string]*bucket)

	for rows.Next() {
		var (
			timestamp           time.Time
			providerName        string
			modelUpstream       string
			inputTokens         int64
			outputTokens        int64
			cacheCreationTokens int64
			cacheReadTokens     int64
		)
		if err := rows.Scan(
			&timestamp,
			&providerName,
			&modelUpstream,
			&inputTokens,
			&outputTokens,
			&cacheCreationTokens,
			&cacheReadTokens,
		); err != nil {
			return nil, fmt.Errorf("scan cost timeseries pricing group: %w", err)
		}

		date := timestamp.In(params.TzLocation).Format("2006-01-02")
		key := date + "\x00" + providerName + "\x00" + modelUpstream
		b, ok := buckets[key]
		if !ok {
			orderedKeys = append(orderedKeys, key)
			b = &bucket{
				group: CostTimeseriesPricingGroup{
					Date:          date,
					ProviderName:  providerName,
					ModelUpstream: modelUpstream,
				},
			}
			buckets[key] = b
		}
		b.group.InputTokens += inputTokens
		b.group.OutputTokens += outputTokens
		b.group.CacheCreationTokens += cacheCreationTokens
		b.group.CacheReadTokens += cacheReadTokens
		b.group.Requests++
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate cost timeseries pricing groups: %w", err)
	}

	groups := make([]CostTimeseriesPricingGroup, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		groups = append(groups, buckets[key].group)
	}
	if groups == nil {
		groups = []CostTimeseriesPricingGroup{}
	}

	return groups, nil
}

func appendProviderConditions(conditions []string, args []any, column string, params CostParams) ([]string, []any) {
	if params.GroupFilter == "" && len(params.ProviderGroups) == 0 {
		return conditions, args
	}

	var providerConditions []string
	if params.GroupFilter != "" {
		providerConditions = append(providerConditions, column+" LIKE ?")
		args = append(args, params.GroupFilter+"%")
	}
	if len(params.ProviderGroups) > 0 {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(params.ProviderGroups)), ",")
		providerConditions = append(providerConditions, column+" IN ("+placeholders+")")
		for _, providerGroup := range params.ProviderGroups {
			args = append(args, providerGroup)
		}
	}

	return append(conditions, "("+strings.Join(providerConditions, " OR ")+")"), args
}

func addOwnerScopeJoins(query string, scopeType string) string {
	switch scopeType {
	case "user":
		return query + " JOIN proxy_key_owners pko ON pko.proxy_key_id = pk.id"
	case "team":
		return query + " JOIN proxy_key_owners pko ON pko.proxy_key_id = pk.id JOIN team_memberships tm ON tm.user_id = pko.owner_user_id"
	default:
		return query
	}
}

func appendOwnerScopeConditions(conditions []string, args []any, scopeType, userID, teamID string) ([]string, []any) {
	switch scopeType {
	case "user":
		conditions = append(conditions, "pko.owner_user_id = ?")
		args = append(args, userID)
	case "team":
		conditions = append(conditions, "tm.team_id = ?")
		args = append(args, teamID)
	}

	return conditions, args
}
