-- +goose Up
ALTER TABLE request_logs ADD COLUMN cost_usd REAL;
CREATE INDEX idx_request_logs_budget ON request_logs(proxy_key_id, timestamp, cost_usd);

-- +goose Down
DROP INDEX IF EXISTS idx_request_logs_budget;
