-- +goose Up
-- Add resolved_model_name column to track the actual model used (handles fallback/virtual model resolution)
ALTER TABLE request_logs ADD COLUMN resolved_model_name TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE request_logs DROP COLUMN IF EXISTS resolved_model_name;