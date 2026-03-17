-- +goose Up
CREATE TABLE global_config (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

INSERT INTO global_config (key, value) VALUES ('budget_limit_usd', 'null');
INSERT INTO global_config (key, value) VALUES ('budget_period',    'null');

-- +goose Down
DROP TABLE IF EXISTS global_config;
