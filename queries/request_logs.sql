-- name: InsertRequestLog :exec
INSERT INTO request_logs (
    id, proxy_key_id, timestamp, source_format, provider_name,
    model_requested, model_upstream, input_tokens, output_tokens,
    cache_creation_input_tokens, cache_read_input_tokens,
    latency_ms, status, request_body, response_body,
    estimated_cost_usd, error_details, is_streaming
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);

-- name: ListRequestLogs :many
SELECT
    rl.id, rl.timestamp, rl.source_format, rl.provider_name,
    rl.model_requested, rl.model_upstream,
    pk.key_prefix AS proxy_key_prefix, pk.label AS proxy_key_label,
    rl.input_tokens, rl.output_tokens,
    rl.cache_creation_input_tokens, rl.cache_read_input_tokens,
    rl.latency_ms, rl.status,
    rl.estimated_cost_usd, rl.is_streaming, rl.error_details
FROM request_logs rl
JOIN proxy_keys pk ON pk.id = rl.proxy_key_id
ORDER BY rl.timestamp DESC
LIMIT ? OFFSET ?;

-- name: CountRequestLogs :one
SELECT COUNT(*) FROM request_logs;

-- name: GetRequestLog :one
SELECT
    rl.id, rl.timestamp, rl.source_format, rl.provider_name,
    rl.model_requested, rl.model_upstream,
    pk.key_prefix AS proxy_key_prefix, pk.label AS proxy_key_label,
    rl.input_tokens, rl.output_tokens,
    rl.cache_creation_input_tokens, rl.cache_read_input_tokens,
    rl.latency_ms, rl.status,
    rl.request_body, rl.response_body,
    rl.estimated_cost_usd, rl.is_streaming, rl.error_details
FROM request_logs rl
JOIN proxy_keys pk ON pk.id = rl.proxy_key_id
WHERE rl.id = ?;
