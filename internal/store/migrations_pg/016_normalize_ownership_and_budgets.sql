-- +goose Up
-- Create proxy_key_owners table and migrate data from proxy_keys.owner_user_id
CREATE TABLE proxy_key_owners (
    proxy_key_id  TEXT PRIMARY KEY REFERENCES proxy_keys(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES oidc_users(id) ON DELETE CASCADE,
    assigned_at   TIMESTAMPTZ NOT NULL
);

INSERT INTO proxy_key_owners (proxy_key_id, owner_user_id, assigned_at)
SELECT id, owner_user_id, created_at
FROM proxy_keys
WHERE owner_user_id IS NOT NULL;

CREATE INDEX idx_proxy_key_owners_owner_user_id ON proxy_key_owners(owner_user_id);

-- Create user_budgets table and migrate data from oidc_users budget columns
CREATE TABLE user_budgets (
    user_id     TEXT PRIMARY KEY REFERENCES oidc_users(id) ON DELETE CASCADE,
    limit_usd   REAL,
    period      TEXT,
    created_at  TIMESTAMPTZ NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL
);

INSERT INTO user_budgets (user_id, limit_usd, period, created_at, updated_at)
SELECT id, budget_limit_usd, budget_period, created_at, created_at
FROM oidc_users
WHERE budget_limit_usd IS NOT NULL OR budget_period IS NOT NULL;

-- Create team_budgets table and migrate data from teams budget columns
CREATE TABLE team_budgets (
    team_id      TEXT PRIMARY KEY REFERENCES teams(id) ON DELETE CASCADE,
    limit_usd    REAL,
    period       TEXT,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL
);

INSERT INTO team_budgets (team_id, limit_usd, period, created_at, updated_at)
SELECT id, budget_limit_usd, budget_period, created_at, created_at
FROM teams
WHERE budget_limit_usd IS NOT NULL OR budget_period IS NOT NULL;

-- Create proxy_key_budgets table and migrate data from proxy_keys budget columns
CREATE TABLE proxy_key_budgets (
    proxy_key_id TEXT PRIMARY KEY REFERENCES proxy_keys(id) ON DELETE CASCADE,
    limit_usd    REAL,
    period       TEXT,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL
);

INSERT INTO proxy_key_budgets (proxy_key_id, limit_usd, period, created_at, updated_at)
SELECT id, budget_limit_usd, budget_period, created_at, created_at
FROM proxy_keys
WHERE budget_limit_usd IS NOT NULL OR budget_period IS NOT NULL;

-- Create global_budget_settings and migrate from global_config
CREATE TABLE global_budget_settings (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    limit_usd  REAL,
    period     TEXT,
    updated_at TIMESTAMPTZ NOT NULL
);

INSERT INTO global_budget_settings (id, limit_usd, period, updated_at)
SELECT
    1,
    CASE WHEN MAX(CASE WHEN key = 'budget_limit_usd' THEN value END) = 'null' THEN NULL
         ELSE CAST(MAX(CASE WHEN key = 'budget_limit_usd' THEN value END) AS REAL)
    END,
    CASE WHEN MAX(CASE WHEN key = 'budget_period' THEN value END) = 'null' THEN NULL
         ELSE MAX(CASE WHEN key = 'budget_period' THEN value END)
    END,
    NOW()
FROM global_config;

-- Add UNIQUE constraint on key_prefix now that ownership is separated
ALTER TABLE proxy_keys ADD CONSTRAINT proxy_keys_key_prefix_unique UNIQUE (key_prefix);

-- Drop old columns that have been normalized into separate tables
ALTER TABLE proxy_keys DROP COLUMN owner_user_id;
ALTER TABLE proxy_keys DROP COLUMN budget_limit_usd;
ALTER TABLE proxy_keys DROP COLUMN budget_period;
ALTER TABLE oidc_users DROP COLUMN budget_limit_usd;
ALTER TABLE oidc_users DROP COLUMN budget_period;
ALTER TABLE teams DROP COLUMN budget_limit_usd;
ALTER TABLE teams DROP COLUMN budget_period;

DROP TABLE global_config;

-- +goose Down
-- Recreate global_config from global_budget_settings
CREATE TABLE global_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO global_config (key, value)
SELECT 'budget_limit_usd', COALESCE(CAST(limit_usd AS TEXT), 'null')
FROM global_budget_settings WHERE id = 1;

INSERT INTO global_config (key, value)
SELECT 'budget_period', COALESCE(period, 'null')
FROM global_budget_settings WHERE id = 1;

-- Restore columns to proxy_keys, oidc_users, teams
ALTER TABLE proxy_keys ADD COLUMN owner_user_id TEXT REFERENCES oidc_users(id);
ALTER TABLE proxy_keys ADD COLUMN budget_limit_usd REAL;
ALTER TABLE proxy_keys ADD COLUMN budget_period TEXT;
ALTER TABLE oidc_users ADD COLUMN budget_limit_usd REAL;
ALTER TABLE oidc_users ADD COLUMN budget_period TEXT;
ALTER TABLE teams ADD COLUMN budget_limit_usd REAL;
ALTER TABLE teams ADD COLUMN budget_period TEXT;

-- Restore data
UPDATE proxy_keys pk SET owner_user_id = pko.owner_user_id
FROM proxy_key_owners pko WHERE pko.proxy_key_id = pk.id;

UPDATE proxy_keys pk SET budget_limit_usd = pkb.limit_usd, budget_period = pkb.period
FROM proxy_key_budgets pkb WHERE pkb.proxy_key_id = pk.id;

UPDATE oidc_users u SET budget_limit_usd = ub.limit_usd, budget_period = ub.period
FROM user_budgets ub WHERE ub.user_id = u.id;

UPDATE teams t SET budget_limit_usd = tb.limit_usd, budget_period = tb.period
FROM team_budgets tb WHERE tb.team_id = t.id;

CREATE INDEX idx_proxy_keys_owner_user_id ON proxy_keys(owner_user_id);

ALTER TABLE proxy_keys DROP CONSTRAINT proxy_keys_key_prefix_unique;

DROP TABLE global_budget_settings;
DROP TABLE proxy_key_budgets;
DROP TABLE team_budgets;
DROP TABLE user_budgets;
DROP TABLE proxy_key_owners;
