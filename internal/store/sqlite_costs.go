package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// --------------------------------------------------------------------------
// Cost operations
// --------------------------------------------------------------------------

// costQueryFilter holds the common JOIN/WHERE/args fragment shared by all cost
// queries. Use buildCostFilter to construct one from CostParams.
type costQueryFilter struct {
	joins string // e.g. " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id ..."
	where string // e.g. " WHERE rl.timestamp >= ? AND ...", or empty
	args  []any
}

// buildCostFilter constructs the JOIN, WHERE, and args clauses that are shared
// across every cost query. modelFilterCol controls which column is used for the
// GroupFilter in model mode (pass "" to skip group filtering entirely).
func buildCostFilter(params CostParams, forceKeyJoin bool, modelFilterCol string) costQueryFilter {
	needsKeyJoin := forceKeyJoin || params.KeyPrefix != "" || params.ScopeType == "user" || params.ScopeType == "team"

	var joins string
	if needsKeyJoin {
		joins = " JOIN proxy_keys pk ON pk.id = rl.proxy_key_id"
	}
	joins = addOwnerScopeJoins(joins, params.ScopeType)

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
	if modelFilterCol != "" {
		switch params.GroupBy {
		case "provider":
			conditions, args = appendProviderConditions(conditions, args, "rl.provider_name", params)
		default:
			if params.GroupFilter != "" {
				conditions = append(conditions, modelFilterCol+` LIKE ? ESCAPE '\'`)
				args = append(args, escapeLikePattern(params.GroupFilter)+"%")
			}
		}
	}
	conditions, args = appendOwnerScopeConditions(conditions, args, params.ScopeType, params.ScopeUserID, params.ScopeTeamID)

	var where string
	if len(conditions) > 0 {
		where = " WHERE " + strings.Join(conditions, " AND ")
	}
	return costQueryFilter{joins: joins, where: where, args: args}
}

// GetCostSummary returns aggregated cost totals for the given date range.
func (s *SQLiteStore) GetCostSummary(ctx context.Context, params CostParams) (*CostSummary, error) {
	f := buildCostFilter(params, false, "rl.model_requested")

	query := `SELECT
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_input_tokens), 0),
		COALESCE(SUM(cache_read_input_tokens), 0),
		COUNT(*)
	FROM request_logs rl` + f.joins + f.where // #nosec G202 -- joins/where built from constant strings; user values bound via args

	var cs CostSummary
	if err := s.db.QueryRowContext(ctx, query, f.args...).Scan(
		&cs.TotalInputTokens, &cs.TotalOutputTokens,
		&cs.TotalCacheCreationTokens, &cs.TotalCacheReadTokens, &cs.TotalRequests,
	); err != nil {
		return nil, fmt.Errorf("get cost summary: %w", err)
	}

	return &cs, nil
}

// GetCostBreakdown returns cost aggregated by model, key, or provider.
func (s *SQLiteStore) GetCostBreakdown(ctx context.Context, params CostParams) ([]CostBreakdownEntry, error) {
	var query string
	var args []any

	switch params.GroupBy {
	case "key":
		f := buildCostFilter(params, true, "")
		query = `SELECT
			pk.key_prefix || ' (' || pk.label || ')' AS group_name,
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(rl.cache_creation_input_tokens), 0),
			COALESCE(SUM(rl.cache_read_input_tokens), 0),
			COUNT(*)
		FROM request_logs rl` + f.joins + f.where +
			" GROUP BY rl.proxy_key_id ORDER BY COUNT(*) DESC, group_name ASC" // #nosec G202 -- joins/where built from constant strings; user values bound via args
		args = f.args

	case "provider":
		f := buildCostFilter(params, false, "rl.model_requested")
		query = `SELECT
			rl.provider_name AS group_name,
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(rl.cache_creation_input_tokens), 0),
			COALESCE(SUM(rl.cache_read_input_tokens), 0),
			COUNT(*)
		FROM request_logs rl` + f.joins + f.where +
			" GROUP BY rl.provider_name ORDER BY COUNT(*) DESC, group_name ASC" // #nosec G202 -- joins/where built from constant strings; user values bound via args
		args = f.args

	default: // "model" or unset
		f := buildCostFilter(params, false, "rl.model_requested")
		query = `SELECT
			rl.model_requested AS group_name,
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(rl.cache_creation_input_tokens), 0),
			COALESCE(SUM(rl.cache_read_input_tokens), 0),
			COUNT(*)
		FROM request_logs rl` + f.joins + f.where +
			" GROUP BY rl.model_requested ORDER BY COUNT(*) DESC, group_name ASC" // #nosec G202 -- joins/where built from constant strings; user values bound via args
		args = f.args
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
	f := buildCostFilter(params, groupByKey, "rl.model_requested")

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
		COALESCE(SUM(rl.cache_read_input_tokens), 0),
		COALESCE(SUM(rl.reasoning_tokens), 0),
		COUNT(*)
	FROM request_logs rl` + f.joins + f.where +
		" GROUP BY rl.model_requested, rl.provider_name, rl.model_upstream" + keyPrefixGroupBy +
		" ORDER BY rl.model_requested, rl.provider_name, rl.model_upstream" + keyPrefixGroupBy // #nosec G202 -- joins/where built from constant strings; user values bound via args

	rows, err := s.db.QueryContext(ctx, query, f.args...)
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
				&group.ReasoningTokens,
				&group.Requests,
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
				&group.ReasoningTokens,
				&group.Requests,
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
	if params.TzLocation != nil {
		return s.getCostTimeseriesWithLocation(ctx, params)
	}

	f := buildCostFilter(params, false, "rl.model_upstream")

	// Shift the stored UTC timestamp to local time before bucketing by date.
	// SQLite's datetime() accepts '+N seconds' modifiers.
	// #nosec G201 -- offset is server-computed from IANA timezone, not user input
	dateExpr := fmt.Sprintf("SUBSTR(datetime(SUBSTR(CAST(rl.timestamp AS TEXT), 1, 19), '%+d seconds'), 1, 10)", params.TzOffsetSeconds)
	query := fmt.Sprintf(`SELECT
		%s AS date,
		COUNT(*)
	FROM request_logs rl`, dateExpr) + f.joins + f.where +
		" GROUP BY " + dateExpr + " ORDER BY date" // #nosec G201,G202 -- offset is server-computed; joins/where are constant strings

	rows, err := s.db.QueryContext(ctx, query, f.args...)
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
	if params.TzLocation != nil {
		return s.getCostTimeseriesPricingGroupsWithLocation(ctx, params)
	}

	f := buildCostFilter(params, false, "rl.model_requested")

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
	FROM request_logs rl`, dateExpr) + f.joins + f.where +
		" GROUP BY " + dateExpr + ", rl.provider_name, rl.model_upstream ORDER BY date, rl.provider_name, rl.model_upstream" // #nosec G201,G202 -- offset is server-computed; joins/where are constant strings

	rows, err := s.db.QueryContext(ctx, query, f.args...)
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

func (s *SQLiteStore) getCostTimeseriesWithLocation(ctx context.Context, params CostParams) ([]CostTimeseriesEntry, error) {
	// Compute a representative UTC offset from TzLocation and delegate to the
	// SQL-aggregation path. This avoids fetching every individual row. The
	// trade-off is a potential 1-hour bucketing error on a DST transition day,
	// which is acceptable for a cost dashboard.
	refTime := time.Now()
	if params.From != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", params.From); err == nil {
			refTime = t
		}
	}
	_, offset := refTime.In(params.TzLocation).Zone()
	params.TzOffsetSeconds = offset
	params.TzLocation = nil
	return s.GetCostTimeseries(ctx, params)
}

func (s *SQLiteStore) getCostTimeseriesPricingGroupsWithLocation(ctx context.Context, params CostParams) ([]CostTimeseriesPricingGroup, error) {
	// Compute a representative UTC offset from TzLocation and delegate to the
	// SQL-aggregation path. This avoids fetching every individual row. The
	// trade-off is a potential 1-hour bucketing error on a DST transition day,
	// which is acceptable for a cost dashboard.
	refTime := time.Now()
	if params.From != "" {
		if t, err := time.Parse("2006-01-02 15:04:05", params.From); err == nil {
			refTime = t
		}
	}
	_, offset := refTime.In(params.TzLocation).Zone()
	params.TzOffsetSeconds = offset
	params.TzLocation = nil
	return s.GetCostTimeseriesPricingGroups(ctx, params)
}

// --------------------------------------------------------------------------
// Cost query helpers
// --------------------------------------------------------------------------

func appendProviderConditions(conditions []string, args []any, column string, params CostParams) ([]string, []any) {
	if params.GroupFilter == "" && len(params.ProviderGroups) == 0 {
		return conditions, args
	}

	var providerConditions []string
	if params.GroupFilter != "" {
		providerConditions = append(providerConditions, column+` LIKE ? ESCAPE '\'`)
		args = append(args, escapeLikePattern(params.GroupFilter)+"%")
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

// escapeLikePattern escapes SQL LIKE wildcard characters (% and _) so that
// the pattern matches literal text. Use with SQLite's ESCAPE clause:
//
//	WHERE col LIKE ? ESCAPE '\'
func escapeLikePattern(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	s = strings.ReplaceAll(s, `_`, `\_`)
	return s
}
