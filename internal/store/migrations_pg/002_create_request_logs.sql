-- +goose Up
CREATE TABLE request_logs (
    id           TEXT PRIMARY KEY,
    proxy_key_id TEXT NOT NULL REFERENCES proxy_keys(id),
    timestamp    TIMESTAMPTZ NOT NULL,
    source_format TEXT NOT NULL,
    provider_name TEXT NOT NULL,
    model_requested TEXT NOT NULL,
    model_upstream  TEXT NOT NULL,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    latency_ms    INTEGER NOT NULL,
    status        INTEGER NOT NULL,
    request_body  TEXT NOT NULL,
    response_body TEXT NOT NULL,
    estimated_cost_usd REAL,
    error_details TEXT,
    is_streaming  BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE INDEX idx_request_logs_timestamp     ON request_logs(timestamp);
CREATE INDEX idx_request_logs_proxy_key_id  ON request_logs(proxy_key_id);
CREATE INDEX idx_request_logs_model_requested ON request_logs(model_requested);
CREATE INDEX idx_request_logs_status        ON request_logs(status);

-- +goose Down
DROP INDEX IF EXISTS idx_request_logs_status;
DROP INDEX IF EXISTS idx_request_logs_model_requested;
DROP INDEX IF EXISTS idx_request_logs_proxy_key_id;
DROP INDEX IF EXISTS idx_request_logs_timestamp;
DROP TABLE request_logs;
