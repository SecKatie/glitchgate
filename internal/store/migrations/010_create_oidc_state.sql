-- +goose Up
-- Migration 010: Create oidc_state table

CREATE TABLE oidc_state (
    state         TEXT PRIMARY KEY,
    pkce_verifier TEXT NOT NULL,
    redirect_to   TEXT,
    created_at    DATETIME NOT NULL,
    expires_at    DATETIME NOT NULL
);

CREATE INDEX idx_oidc_state_expires_at ON oidc_state(expires_at);

-- +goose Down
DROP TABLE IF EXISTS oidc_state;
