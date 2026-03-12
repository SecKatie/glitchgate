-- +goose Up
-- Migration 012: Add budget fields to proxy_keys

ALTER TABLE proxy_keys ADD COLUMN budget_limit_usd REAL;
ALTER TABLE proxy_keys ADD COLUMN budget_period    TEXT;

-- +goose Down
