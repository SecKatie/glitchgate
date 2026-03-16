-- +goose Up
-- Add cost_usd to the model usage covering index so SUM(cost_usd) in
-- GetAllModelUsageSummaries and GetModelUsageSummary can be satisfied from
-- the index alone without a table lookup.
DROP INDEX IF EXISTS idx_request_logs_model_usage;
CREATE INDEX idx_request_logs_model_usage ON request_logs(
    model_requested,
    resolved_model_name,
    provider_name,
    model_upstream,
    input_tokens,
    output_tokens,
    cache_creation_input_tokens,
    cache_read_input_tokens,
    latency_ms,
    cost_usd
);

-- +goose Down
DROP INDEX IF EXISTS idx_request_logs_model_usage;
CREATE INDEX idx_request_logs_model_usage ON request_logs(
    model_requested,
    resolved_model_name,
    provider_name,
    model_upstream,
    input_tokens,
    output_tokens,
    cache_creation_input_tokens,
    cache_read_input_tokens,
    latency_ms
);
