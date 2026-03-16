-- +goose Up
-- Normalize request_logs.timestamp values that were stored using Go's
-- time.Time.String() format ("2006-01-02 15:04:05.999999999 +0000 UTC")
-- to RFC3339Nano ("2006-01-02T15:04:05.999999999Z") so that lexicographic
-- ORDER BY works correctly.
UPDATE request_logs
SET timestamp = REPLACE(SUBSTR(timestamp, 1, INSTR(timestamp, ' +0000 UTC') - 1), ' ', 'T') || 'Z'
WHERE timestamp LIKE '% +0000 UTC';

-- +goose Down
-- One-way normalization; no rollback.
