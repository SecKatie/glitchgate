-- +goose Up
-- Covering index for cost dashboard queries.
-- Includes all columns needed by GetCostSummary, GetCostBreakdown, GetCostPricingGroups,
-- and GetCostTimeseries so SQLite can satisfy these queries using only the index.
CREATE INDEX idx_request_logs_cost_covering ON request_logs(
    timestamp,
    model_requested,
    provider_name,
    proxy_key_id,
    input_tokens,
    output_tokens,
    cache_creation_input_tokens,
    cache_read_input_tokens
);

-- +goose Down
DROP INDEX IF EXISTS idx_request_logs_cost_covering;
