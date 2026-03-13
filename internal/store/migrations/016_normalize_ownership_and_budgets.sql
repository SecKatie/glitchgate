-- +goose Up
-- +goose NO TRANSACTION
PRAGMA foreign_keys = OFF;
BEGIN;

CREATE TABLE proxy_key_owners (
    proxy_key_id  TEXT PRIMARY KEY REFERENCES proxy_keys(id) ON DELETE CASCADE,
    owner_user_id TEXT NOT NULL REFERENCES oidc_users(id) ON DELETE CASCADE,
    assigned_at   DATETIME NOT NULL
);

INSERT INTO proxy_key_owners (proxy_key_id, owner_user_id, assigned_at)
SELECT id, owner_user_id, created_at
FROM proxy_keys
WHERE owner_user_id IS NOT NULL;

CREATE INDEX idx_proxy_key_owners_owner_user_id ON proxy_key_owners(owner_user_id);

CREATE TABLE user_budgets (
    user_id     TEXT PRIMARY KEY REFERENCES oidc_users(id) ON DELETE CASCADE,
    limit_usd   REAL,
    period      TEXT,
    created_at  DATETIME NOT NULL,
    updated_at  DATETIME NOT NULL
);

INSERT INTO user_budgets (user_id, limit_usd, period, created_at, updated_at)
SELECT id, budget_limit_usd, budget_period, created_at, created_at
FROM oidc_users
WHERE budget_limit_usd IS NOT NULL OR budget_period IS NOT NULL;

CREATE TABLE team_budgets (
    team_id      TEXT PRIMARY KEY REFERENCES teams(id) ON DELETE CASCADE,
    limit_usd    REAL,
    period       TEXT,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL
);

INSERT INTO team_budgets (team_id, limit_usd, period, created_at, updated_at)
SELECT id, budget_limit_usd, budget_period, created_at, created_at
FROM teams
WHERE budget_limit_usd IS NOT NULL OR budget_period IS NOT NULL;

CREATE TABLE proxy_key_budgets (
    proxy_key_id TEXT PRIMARY KEY REFERENCES proxy_keys(id) ON DELETE CASCADE,
    limit_usd    REAL,
    period       TEXT,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL
);

INSERT INTO proxy_key_budgets (proxy_key_id, limit_usd, period, created_at, updated_at)
SELECT id, budget_limit_usd, budget_period, created_at, created_at
FROM proxy_keys
WHERE budget_limit_usd IS NOT NULL OR budget_period IS NOT NULL;

CREATE TABLE global_budget_settings (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    limit_usd  REAL,
    period     TEXT,
    updated_at DATETIME NOT NULL
);

INSERT INTO global_budget_settings (id, limit_usd, period, updated_at)
SELECT
    1,
    CAST(NULLIF(MAX(CASE WHEN key = 'budget_limit_usd' THEN value END), 'null') AS REAL),
    NULLIF(MAX(CASE WHEN key = 'budget_period' THEN value END), 'null'),
    CURRENT_TIMESTAMP
FROM global_config;

CREATE TABLE proxy_keys_new (
    id         TEXT PRIMARY KEY,
    key_hash   TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL UNIQUE,
    label      TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    revoked_at DATETIME
);

INSERT INTO proxy_keys_new (id, key_hash, key_prefix, label, created_at, revoked_at)
SELECT id, key_hash, key_prefix, label, created_at, revoked_at
FROM proxy_keys;

DROP TABLE proxy_keys;
ALTER TABLE proxy_keys_new RENAME TO proxy_keys;

CREATE TABLE oidc_users_new (
    id           TEXT PRIMARY KEY,
    subject      TEXT NOT NULL UNIQUE,
    email        TEXT NOT NULL,
    display_name TEXT NOT NULL,
    role         TEXT NOT NULL CHECK(role IN ('global_admin', 'team_admin', 'member')),
    active       INTEGER NOT NULL DEFAULT 1,
    last_seen_at DATETIME,
    created_at   DATETIME NOT NULL
);

INSERT INTO oidc_users_new (id, subject, email, display_name, role, active, last_seen_at, created_at)
SELECT id, subject, email, display_name, role, active, last_seen_at, created_at
FROM oidc_users;

DROP TABLE oidc_users;
ALTER TABLE oidc_users_new RENAME TO oidc_users;
CREATE INDEX idx_oidc_users_subject ON oidc_users(subject);
CREATE INDEX idx_oidc_users_email ON oidc_users(email);
CREATE INDEX idx_oidc_users_role ON oidc_users(role);

CREATE TABLE teams_new (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    description TEXT,
    created_at  DATETIME NOT NULL
);

INSERT INTO teams_new (id, name, description, created_at)
SELECT id, name, description, created_at
FROM teams;

DROP TABLE teams;
ALTER TABLE teams_new RENAME TO teams;

DROP TABLE global_config;

COMMIT;
PRAGMA foreign_keys = ON;

-- +goose Down
-- +goose NO TRANSACTION
PRAGMA foreign_keys = OFF;
BEGIN;

CREATE TABLE proxy_keys_legacy (
    id               TEXT PRIMARY KEY,
    key_hash         TEXT NOT NULL UNIQUE,
    key_prefix       TEXT NOT NULL,
    label            TEXT NOT NULL,
    created_at       DATETIME NOT NULL,
    revoked_at       DATETIME,
    owner_user_id    TEXT REFERENCES oidc_users(id),
    budget_limit_usd REAL,
    budget_period    TEXT
);

INSERT INTO proxy_keys_legacy (
    id, key_hash, key_prefix, label, created_at, revoked_at,
    owner_user_id, budget_limit_usd, budget_period
)
SELECT
    pk.id, pk.key_hash, pk.key_prefix, pk.label, pk.created_at, pk.revoked_at,
    pko.owner_user_id, pkb.limit_usd, pkb.period
FROM proxy_keys pk
LEFT JOIN proxy_key_owners pko ON pko.proxy_key_id = pk.id
LEFT JOIN proxy_key_budgets pkb ON pkb.proxy_key_id = pk.id;

CREATE TABLE oidc_users_legacy (
    id               TEXT PRIMARY KEY,
    subject          TEXT NOT NULL UNIQUE,
    email            TEXT NOT NULL,
    display_name     TEXT NOT NULL,
    role             TEXT NOT NULL CHECK(role IN ('global_admin', 'team_admin', 'member')),
    active           INTEGER NOT NULL DEFAULT 1,
    last_seen_at     DATETIME,
    created_at       DATETIME NOT NULL,
    budget_limit_usd REAL,
    budget_period    TEXT
);

INSERT INTO oidc_users_legacy (
    id, subject, email, display_name, role, active, last_seen_at, created_at,
    budget_limit_usd, budget_period
)
SELECT
    u.id, u.subject, u.email, u.display_name, u.role, u.active, u.last_seen_at, u.created_at,
    ub.limit_usd, ub.period
FROM oidc_users u
LEFT JOIN user_budgets ub ON ub.user_id = u.id;

CREATE TABLE teams_legacy (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL UNIQUE,
    description      TEXT,
    created_at       DATETIME NOT NULL,
    budget_limit_usd REAL,
    budget_period    TEXT
);

INSERT INTO teams_legacy (id, name, description, created_at, budget_limit_usd, budget_period)
SELECT
    t.id, t.name, t.description, t.created_at, tb.limit_usd, tb.period
FROM teams t
LEFT JOIN team_budgets tb ON tb.team_id = t.id;

CREATE TABLE global_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO global_config (key, value)
SELECT 'budget_limit_usd', COALESCE(CAST(limit_usd AS TEXT), 'null')
FROM global_budget_settings
WHERE id = 1;

INSERT INTO global_config (key, value)
SELECT 'budget_period', COALESCE(period, 'null')
FROM global_budget_settings
WHERE id = 1;

DROP TABLE proxy_keys;
ALTER TABLE proxy_keys_legacy RENAME TO proxy_keys;
CREATE INDEX idx_proxy_keys_owner_user_id ON proxy_keys(owner_user_id);

DROP TABLE oidc_users;
ALTER TABLE oidc_users_legacy RENAME TO oidc_users;
CREATE INDEX idx_oidc_users_subject ON oidc_users(subject);
CREATE INDEX idx_oidc_users_email ON oidc_users(email);
CREATE INDEX idx_oidc_users_role ON oidc_users(role);

DROP TABLE teams;
ALTER TABLE teams_legacy RENAME TO teams;

DROP TABLE proxy_key_owners;
DROP TABLE user_budgets;
DROP TABLE team_budgets;
DROP TABLE proxy_key_budgets;
DROP TABLE global_budget_settings;

COMMIT;
PRAGMA foreign_keys = ON;
