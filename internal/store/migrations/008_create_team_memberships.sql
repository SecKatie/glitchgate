-- +goose Up
-- Migration 008: Create team_memberships table

CREATE TABLE team_memberships (
    user_id   TEXT PRIMARY KEY REFERENCES oidc_users(id) ON DELETE CASCADE,
    team_id   TEXT NOT NULL REFERENCES teams(id) ON DELETE RESTRICT,
    joined_at DATETIME NOT NULL
);

CREATE INDEX idx_team_memberships_team_id ON team_memberships(team_id);

-- +goose Down
DROP TABLE IF EXISTS team_memberships;
