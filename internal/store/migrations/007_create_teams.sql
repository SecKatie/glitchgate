-- +goose Up
-- Migration 007: Create teams table

CREATE TABLE teams (
    id               TEXT PRIMARY KEY,
    name             TEXT NOT NULL UNIQUE,
    description      TEXT,
    created_at       DATETIME NOT NULL,
    budget_limit_usd REAL,
    budget_period    TEXT
);

-- +goose Down
DROP TABLE IF EXISTS teams;
