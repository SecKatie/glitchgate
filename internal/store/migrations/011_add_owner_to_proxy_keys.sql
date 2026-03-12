-- +goose Up
-- Migration 011: Add owner_user_id to proxy_keys

ALTER TABLE proxy_keys ADD COLUMN owner_user_id TEXT REFERENCES oidc_users(id);

CREATE INDEX idx_proxy_keys_owner_user_id ON proxy_keys(owner_user_id);

-- +goose Down
DROP INDEX IF EXISTS idx_proxy_keys_owner_user_id;
