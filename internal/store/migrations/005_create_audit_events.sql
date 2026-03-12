-- +goose Up
CREATE TABLE audit_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    action TEXT NOT NULL,
    key_prefix TEXT NOT NULL,
    detail TEXT,
    created_at DATETIME NOT NULL
);

-- +goose Down
DROP TABLE audit_events;
