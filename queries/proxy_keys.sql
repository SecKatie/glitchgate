-- name: CreateProxyKey :exec
INSERT INTO proxy_keys (id, key_hash, key_prefix, label, created_at)
VALUES (?, ?, ?, ?, ?);

-- name: GetProxyKeyByHash :one
SELECT * FROM proxy_keys WHERE key_hash = ? AND revoked_at IS NULL;

-- name: ListActiveProxyKeys :many
SELECT id, key_prefix, label, created_at FROM proxy_keys WHERE revoked_at IS NULL ORDER BY created_at DESC;

-- name: ListAllProxyKeys :many
SELECT id, key_prefix, label, created_at, revoked_at FROM proxy_keys ORDER BY created_at DESC;

-- name: RevokeProxyKey :exec
UPDATE proxy_keys SET revoked_at = ? WHERE key_prefix = ? AND revoked_at IS NULL;

-- name: GetProxyKeyByPrefix :one
SELECT * FROM proxy_keys WHERE key_prefix = ? AND revoked_at IS NULL;
