-- +goose Up
-- Covering index for model usage aggregation queries (Models page).
-- Keyed on model_requested so GetAllModelUsageSummaries, GetModelUsageSummary,
-- GetModelCostPricingGroups, and GetModelLatencyTimeseries can satisfy their
-- GROUP BY + SUM/COUNT from the index alone without touching the table.
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

-- +goose Down
DROP INDEX IF EXISTS idx_request_logs_model_usage;
