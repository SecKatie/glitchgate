-- +goose Up
ALTER TABLE request_logs
    ADD COLUMN cache_creation_input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE request_logs
    ADD COLUMN cache_read_input_tokens INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE request_logs DROP COLUMN cache_read_input_tokens;
ALTER TABLE request_logs DROP COLUMN cache_creation_input_tokens;
