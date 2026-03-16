package store

import (
	"context"
	"fmt"
)

// --------------------------------------------------------------------------
// Model usage operations
// --------------------------------------------------------------------------

// GetModelUsageSummary returns aggregated usage stats for the given model_requested value.
// Returns a zero-value summary (not an error) when no logs exist for the model.
func (s *SQLiteStore) GetModelUsageSummary(ctx context.Context, modelName string) (*ModelUsageSummary, error) {
	const query = `SELECT
		COUNT(*),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(cache_creation_input_tokens), 0),
		COALESCE(SUM(cache_read_input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cost_usd), 0),
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
		&summary.LogCostUSD,
		&summary.ProviderName,
		&summary.UpstreamModel,
	); err != nil {
		return nil, fmt.Errorf("get model usage summary: %w", err)
	}

	return &summary, nil
}

// GetModelUsageSummaryByUpstream returns aggregated usage for a specific
// provider + upstream model combination. Used for wildcard-resolved models
// where the client-facing name differs from what's in model_requested.
func (s *SQLiteStore) GetModelUsageSummaryByUpstream(ctx context.Context, providerName, upstreamModel string) (*ModelUsageSummary, error) {
	const query = `SELECT
		COUNT(*),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(cache_creation_input_tokens), 0),
		COALESCE(SUM(cache_read_input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cost_usd), 0),
		COALESCE(MAX(provider_name), ''),
		COALESCE(MAX(model_upstream), '')
	FROM request_logs
	WHERE provider_name = ? AND model_upstream = ?`

	var summary ModelUsageSummary
	if err := s.db.QueryRowContext(ctx, query, providerName, upstreamModel).Scan(
		&summary.RequestCount,
		&summary.InputTokens,
		&summary.CacheCreationInputTokens,
		&summary.CacheReadInputTokens,
		&summary.OutputTokens,
		&summary.LogCostUSD,
		&summary.ProviderName,
		&summary.UpstreamModel,
	); err != nil {
		return nil, fmt.Errorf("get model usage summary by upstream: %w", err)
	}

	return &summary, nil
}

// GetAllModelUsageSummaries returns aggregated usage stats for every model seen in request_logs,
// keyed by model name. Uses resolved_model_name to capture the actual model used after fallback resolution.
func (s *SQLiteStore) GetAllModelUsageSummaries(ctx context.Context) (map[string]*ModelUsageSummary, error) {
	const query = `SELECT
		COALESCE(NULLIF(resolved_model_name, ''), model_requested) AS model_name,
		COUNT(*),
		COALESCE(SUM(input_tokens), 0),
		COALESCE(SUM(cache_creation_input_tokens), 0),
		COALESCE(SUM(cache_read_input_tokens), 0),
		COALESCE(SUM(output_tokens), 0),
		COALESCE(SUM(cost_usd), 0),
		COALESCE(MAX(provider_name), ''),
		COALESCE(MAX(model_upstream), '')
	FROM request_logs
	GROUP BY COALESCE(NULLIF(resolved_model_name, ''), model_requested)`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("get all model usage summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make(map[string]*ModelUsageSummary)
	for rows.Next() {
		var modelName string
		var summary ModelUsageSummary
		if err := rows.Scan(&modelName, &summary.RequestCount, &summary.InputTokens, &summary.CacheCreationInputTokens, &summary.CacheReadInputTokens, &summary.OutputTokens, &summary.LogCostUSD, &summary.ProviderName, &summary.UpstreamModel); err != nil {
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

// GetModelLatencyTimeseries returns hourly avg ms-per-output-token for the given model,
// useful for spotting provider throttling over time.
func (s *SQLiteStore) GetModelLatencyTimeseries(ctx context.Context, modelName string) ([]ModelLatencyTimeseriesEntry, error) {
	const query = `SELECT
		SUBSTR(CAST(timestamp AS TEXT), 1, 13) AS bucket,
		COALESCE(SUM(latency_ms), 0),
		COALESCE(SUM(output_tokens), 0),
		COUNT(*)
	FROM request_logs
	WHERE model_requested = ? AND output_tokens > 0
	GROUP BY bucket
	ORDER BY bucket`

	rows, err := s.db.QueryContext(ctx, query, modelName)
	if err != nil {
		return nil, fmt.Errorf("get model latency timeseries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var entries []ModelLatencyTimeseriesEntry
	for rows.Next() {
		var e ModelLatencyTimeseriesEntry
		if err := rows.Scan(&e.Bucket, &e.TotalLatencyMs, &e.TotalOutputTokens, &e.Requests); err != nil {
			return nil, fmt.Errorf("scan model latency timeseries: %w", err)
		}
		if e.TotalOutputTokens > 0 {
			e.AvgMsPerOutputToken = float64(e.TotalLatencyMs) / float64(e.TotalOutputTokens)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate model latency timeseries: %w", err)
	}
	if entries == nil {
		entries = []ModelLatencyTimeseriesEntry{}
	}
	return entries, nil
}
