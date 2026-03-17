-- +goose Up
CREATE TABLE oidc_users (
    id           TEXT PRIMARY KEY,
    subject      TEXT NOT NULL UNIQUE,
    email        TEXT NOT NULL,
    display_name TEXT NOT NULL,
    role         TEXT NOT NULL CHECK(role IN ('global_admin', 'team_admin', 'member')),
    active       BOOLEAN NOT NULL DEFAULT TRUE,
    last_seen_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL,
    budget_limit_usd REAL,
    budget_period    TEXT
);

CREATE INDEX idx_oidc_users_subject ON oidc_users(subject);
CREATE INDEX idx_oidc_users_email   ON oidc_users(email);
CREATE INDEX idx_oidc_users_role    ON oidc_users(role);

-- +goose Down
DROP TABLE IF EXISTS oidc_users;
