-- +goose Up
CREATE TABLE audit_events (
    id         BIGSERIAL PRIMARY KEY,
    action     TEXT NOT NULL,
    key_prefix TEXT NOT NULL,
    detail     TEXT,
    created_at TIMESTAMPTZ NOT NULL
);

-- +goose Down
DROP TABLE audit_events;
