-- +goose Up
ALTER TABLE request_logs ADD COLUMN resolved_model_name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE request_logs DROP COLUMN resolved_model_name;
