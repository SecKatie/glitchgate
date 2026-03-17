-- +goose Up
CREATE TABLE ui_sessions (
    id           TEXT PRIMARY KEY,
    token        TEXT NOT NULL UNIQUE,
    session_type TEXT NOT NULL CHECK(session_type IN ('oidc', 'master_key')),
    user_id      TEXT REFERENCES oidc_users(id) ON DELETE CASCADE,
    created_at   TIMESTAMPTZ NOT NULL,
    expires_at   TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_ui_sessions_token      ON ui_sessions(token);
CREATE INDEX idx_ui_sessions_expires_at ON ui_sessions(expires_at);
CREATE INDEX idx_ui_sessions_user_id    ON ui_sessions(user_id);

-- +goose Down
DROP TABLE IF EXISTS ui_sessions;
