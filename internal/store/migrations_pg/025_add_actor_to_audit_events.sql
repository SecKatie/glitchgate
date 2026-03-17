-- +goose Up
ALTER TABLE audit_events ADD COLUMN actor_email TEXT;

-- +goose Down
ALTER TABLE audit_events DROP COLUMN actor_email;
