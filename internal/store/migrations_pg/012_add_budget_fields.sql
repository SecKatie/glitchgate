-- +goose Up
ALTER TABLE proxy_keys ADD COLUMN budget_limit_usd REAL;
ALTER TABLE proxy_keys ADD COLUMN budget_period    TEXT;

-- +goose Down
ALTER TABLE proxy_keys DROP COLUMN budget_period;
ALTER TABLE proxy_keys DROP COLUMN budget_limit_usd;
