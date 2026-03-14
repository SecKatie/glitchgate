-- +goose Up
-- Add model_upstream to the covering index so GetCostPricingGroups and
-- GetCostTimeseriesPricingGroups can satisfy queries from index-only scans.
DROP INDEX IF EXISTS idx_request_logs_cost_covering;
CREATE INDEX idx_request_logs_cost_covering ON request_logs(
    timestamp,
    model_requested,
    provider_name,
    proxy_key_id,
    model_upstream,
    input_tokens,
    output_tokens,
    cache_creation_input_tokens,
    cache_read_input_tokens
);

-- +goose Down
DROP INDEX IF EXISTS idx_request_logs_cost_covering;
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
