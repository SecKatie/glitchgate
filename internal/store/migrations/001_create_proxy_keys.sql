-- +goose Up
CREATE TABLE proxy_keys (
    id TEXT PRIMARY KEY,
    key_hash TEXT NOT NULL UNIQUE,
    key_prefix TEXT NOT NULL,
    label TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    revoked_at DATETIME
);

-- +goose Down
DROP TABLE proxy_keys;
