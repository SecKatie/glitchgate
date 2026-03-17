-- +goose Up
ALTER TABLE request_logs ADD COLUMN fallback_attempts INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE request_logs DROP COLUMN fallback_attempts;
