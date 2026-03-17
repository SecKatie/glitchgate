-- +goose Up
ALTER TABLE request_logs DROP COLUMN estimated_cost_usd;

-- +goose Down
-- Cannot add back column with existing data; this migration is one-way.
