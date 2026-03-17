-- +goose Up
CREATE TABLE oidc_state (
    state         TEXT PRIMARY KEY,
    pkce_verifier TEXT NOT NULL,
    redirect_to   TEXT,
    created_at    TIMESTAMPTZ NOT NULL,
    expires_at    TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_oidc_state_expires_at ON oidc_state(expires_at);

-- +goose Down
DROP TABLE IF EXISTS oidc_state;
