// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"time"
)

// --------------------------------------------------------------------------
// Cost operations
// --------------------------------------------------------------------------

// GetCostSummary returns aggregated cost totals for the given date range.
func (s *PostgreSQLStore) GetCostSummary(ctx context.Context, params CostParams) (*CostSummary, error) {
	f := buildCostFilter(params, false, "rl.model_requested")

	query := rebindForPostgres(`SELECT
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cache_creation_input_tokens), 0),
		COALESCE(SUM(cache_read_input_tokens), 0),
		COUNT(*)
	FROM request_logs rl` + f.joins + f.where) // #nosec G202 -- joins/where built from constant strings; user values bound via args

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
func (s *PostgreSQLStore) GetCostBreakdown(ctx context.Context, params CostParams) ([]CostBreakdownEntry, error) {
	var query string
	var args []any

	switch params.GroupBy {
	case "key":
		f := buildCostFilter(params, true, "")
		query = rebindForPostgres(`SELECT
			pk.key_prefix || ' (' || pk.label || ')' AS group_name,
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(rl.cache_creation_input_tokens), 0),
			COALESCE(SUM(rl.cache_read_input_tokens), 0),
			COUNT(*)
		FROM request_logs rl` + f.joins + f.where +
			" GROUP BY rl.proxy_key_id, pk.key_prefix, pk.label ORDER BY COUNT(*) DESC, group_name ASC") // #nosec G202 -- joins/where built from constant strings; user values bound via args
		args = f.args

	case "provider":
		f := buildCostFilter(params, false, "rl.model_requested")
		query = rebindForPostgres(`SELECT
			rl.provider_name AS group_name,
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(rl.cache_creation_input_tokens), 0),
			COALESCE(SUM(rl.cache_read_input_tokens), 0),
			COUNT(*)
		FROM request_logs rl` + f.joins + f.where +
			" GROUP BY rl.provider_name ORDER BY COUNT(*) DESC, group_name ASC") // #nosec G202 -- joins/where built from constant strings; user values bound via args
		args = f.args

	default: // "model" or unset
		f := buildCostFilter(params, false, "rl.model_requested")
		query = rebindForPostgres(`SELECT
			rl.model_requested AS group_name,
			COALESCE(SUM(rl.input_tokens), 0),
			COALESCE(SUM(rl.output_tokens), 0),
			COALESCE(SUM(rl.cache_creation_input_tokens), 0),
			COALESCE(SUM(rl.cache_read_input_tokens), 0),
			COUNT(*)
		FROM request_logs rl` + f.joins + f.where +
			" GROUP BY rl.model_requested ORDER BY COUNT(*) DESC, group_name ASC") // #nosec G202 -- joins/where built from constant strings; user values bound via args
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
func (s *PostgreSQLStore) GetCostPricingGroups(ctx context.Context, params CostParams) ([]CostPricingGroup, error) {
	groupByKey := params.GroupBy == "key"
	f := buildCostFilter(params, groupByKey, "rl.model_requested")

	keyPrefixSelect := ""
	keyPrefixGroupBy := ""
	if groupByKey {
		keyPrefixSelect = "\n\t\tCOALESCE(pk.key_prefix, ''),\n\t\tCOALESCE(pk.key_prefix || ' (' || pk.label || ')', ''),"
		keyPrefixGroupBy = ", pk.key_prefix, pk.label"
	}

	query := rebindForPostgres(`SELECT
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
		" ORDER BY rl.model_requested, rl.provider_name, rl.model_upstream" + keyPrefixGroupBy) // #nosec G202 -- joins/where built from constant strings; user values bound via args

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
func (s *PostgreSQLStore) GetCostTimeseries(ctx context.Context, params CostParams) ([]CostTimeseriesEntry, error) {
	if params.TzLocation != nil {
		return s.pgGetCostTimeseriesWithLocation(ctx, params)
	}

	f := buildCostFilter(params, false, "rl.model_upstream")

	// Shift the stored UTC TIMESTAMPTZ to local time before bucketing by date.
	// #nosec G201 -- offset is server-computed from IANA timezone, not user input
	dateExpr := fmt.Sprintf("TO_CHAR(rl.timestamp + (%d * INTERVAL '1 second'), 'YYYY-MM-DD')", params.TzOffsetSeconds)
	query := rebindForPostgres(fmt.Sprintf(`SELECT
		%s AS date,
		COUNT(*)
	FROM request_logs rl`, dateExpr) + f.joins + f.where +
		" GROUP BY " + dateExpr + " ORDER BY date") // #nosec G201,G202 -- offset is server-computed; joins/where are constant strings

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
func (s *PostgreSQLStore) GetCostTimeseriesPricingGroups(ctx context.Context, params CostParams) ([]CostTimeseriesPricingGroup, error) {
	if params.TzLocation != nil {
		return s.pgGetCostTimeseriesPricingGroupsWithLocation(ctx, params)
	}

	f := buildCostFilter(params, false, "rl.model_requested")

	// Shift the stored UTC TIMESTAMPTZ to local time before bucketing by date.
	// #nosec G201 -- offset is server-computed from IANA timezone, not user input
	dateExpr := fmt.Sprintf("TO_CHAR(rl.timestamp + (%d * INTERVAL '1 second'), 'YYYY-MM-DD')", params.TzOffsetSeconds)
	query := rebindForPostgres(fmt.Sprintf(`SELECT
		%s AS date,
		rl.provider_name,
		rl.model_upstream,
		COALESCE(SUM(rl.input_tokens), 0),
		COALESCE(SUM(rl.output_tokens), 0),
		COALESCE(SUM(rl.cache_creation_input_tokens), 0),
		COALESCE(SUM(rl.cache_read_input_tokens), 0),
		COUNT(*)
	FROM request_logs rl`, dateExpr) + f.joins + f.where +
		" GROUP BY " + dateExpr + ", rl.provider_name, rl.model_upstream ORDER BY date, rl.provider_name, rl.model_upstream") // #nosec G201,G202 -- offset is server-computed; joins/where are constant strings

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

func (s *PostgreSQLStore) pgGetCostTimeseriesWithLocation(ctx context.Context, params CostParams) ([]CostTimeseriesEntry, error) {
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

func (s *PostgreSQLStore) pgGetCostTimeseriesPricingGroupsWithLocation(ctx context.Context, params CostParams) ([]CostTimeseriesPricingGroup, error) {
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
