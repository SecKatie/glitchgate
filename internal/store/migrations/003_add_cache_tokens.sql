-- +goose Up
ALTER TABLE request_logs
    ADD COLUMN cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs
    ADD COLUMN cache_read_input_tokens INTEGER NOT NULL DEFAULT 0;

-- +goose Down
-- SQLite does not support DROP COLUMN in older versions; this migration is intentionally
-- irreversible. To roll back, recreate the table without these columns.
