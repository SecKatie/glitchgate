-- +goose Up
CREATE TABLE proxy_key_allowed_models (
    proxy_key_id TEXT NOT NULL REFERENCES proxy_keys(id) ON DELETE CASCADE,
    model_pattern TEXT NOT NULL,
    PRIMARY KEY (proxy_key_id, model_pattern)
);

CREATE TABLE proxy_key_rate_limits (
    proxy_key_id TEXT PRIMARY KEY REFERENCES proxy_keys(id) ON DELETE CASCADE,
    requests_per_minute INTEGER NOT NULL,
    burst INTEGER NOT NULL
);

-- +goose Down
DROP TABLE proxy_key_rate_limits;
DROP TABLE proxy_key_allowed_models;
