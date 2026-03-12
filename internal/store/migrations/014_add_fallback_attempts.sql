-- +goose Up
ALTER TABLE request_logs ADD COLUMN fallback_attempts INTEGER NOT NULL DEFAULT 1;

-- +goose Down
-- SQLite does not support DROP COLUMN in older versions; this migration is intentionally
-- irreversible. To roll back, recreate the table without this column.
