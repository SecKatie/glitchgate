-- +goose Up
-- No-op for PostgreSQL: timestamps are stored as TIMESTAMPTZ and are always
-- in a normalized format. This migration exists only to maintain version
-- parity with the SQLite migration sequence.

-- +goose Down
