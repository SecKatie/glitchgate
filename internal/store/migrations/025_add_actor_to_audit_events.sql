-- +goose Up
ALTER TABLE audit_events ADD COLUMN actor_email TEXT;

-- +goose Down
-- SQLite prior to 3.35 does not support DROP COLUMN; leave empty.
