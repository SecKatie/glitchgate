-- +goose Up
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
    cache_read_input_tokens,
    reasoning_tokens
);

-- +goose Down
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
