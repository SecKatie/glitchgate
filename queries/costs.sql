-- name: GetTotalCost :one
SELECT
    COALESCE(SUM(estimated_cost_usd), 0) AS total_cost_usd,
    COALESCE(SUM(input_tokens), 0) AS total_input_tokens,
    COALESCE(SUM(output_tokens), 0) AS total_output_tokens,
    COALESCE(SUM(cache_creation_input_tokens), 0) AS total_cache_creation_tokens,
    COALESCE(SUM(cache_read_input_tokens), 0) AS total_cache_read_tokens,
    COUNT(*) AS total_requests
FROM request_logs
WHERE timestamp >= ? AND timestamp <= ?;

-- name: GetCostByModel :many
SELECT
    model_upstream AS group_name,
    COALESCE(SUM(estimated_cost_usd), 0) AS cost_usd,
    COALESCE(SUM(input_tokens), 0) AS input_tokens,
    COALESCE(SUM(output_tokens), 0) AS output_tokens,
    COALESCE(SUM(cache_creation_input_tokens), 0) AS cache_creation_tokens,
    COALESCE(SUM(cache_read_input_tokens), 0) AS cache_read_tokens,
    COUNT(*) AS requests
FROM request_logs
WHERE timestamp >= ? AND timestamp <= ?
GROUP BY model_upstream
ORDER BY cost_usd DESC;

-- name: GetCostByKey :many
SELECT
    pk.key_prefix || ' (' || pk.label || ')' AS group_name,
    COALESCE(SUM(rl.estimated_cost_usd), 0) AS cost_usd,
    COALESCE(SUM(rl.input_tokens), 0) AS input_tokens,
    COALESCE(SUM(rl.output_tokens), 0) AS output_tokens,
    COALESCE(SUM(rl.cache_creation_input_tokens), 0) AS cache_creation_tokens,
    COALESCE(SUM(rl.cache_read_input_tokens), 0) AS cache_read_tokens,
    COUNT(*) AS requests
FROM request_logs rl
JOIN proxy_keys pk ON pk.id = rl.proxy_key_id
WHERE rl.timestamp >= ? AND rl.timestamp <= ?
GROUP BY rl.proxy_key_id
ORDER BY cost_usd DESC;

-- name: GetCostByProvider :many
SELECT
    provider_name AS group_name,
    COALESCE(SUM(estimated_cost_usd), 0) AS cost_usd,
    COALESCE(SUM(input_tokens), 0) AS input_tokens,
    COALESCE(SUM(output_tokens), 0) AS output_tokens,
    COALESCE(SUM(cache_creation_input_tokens), 0) AS cache_creation_tokens,
    COALESCE(SUM(cache_read_input_tokens), 0) AS cache_read_tokens,
    COUNT(*) AS requests
FROM request_logs
WHERE timestamp >= ? AND timestamp <= ?
GROUP BY provider_name
ORDER BY cost_usd DESC;

-- name: GetCostTimeseries :many
SELECT
    DATE(timestamp) AS date,
    COALESCE(SUM(estimated_cost_usd), 0) AS cost_usd,
    COUNT(*) AS requests
FROM request_logs
WHERE timestamp >= ? AND timestamp <= ?
GROUP BY DATE(timestamp)
ORDER BY date;
