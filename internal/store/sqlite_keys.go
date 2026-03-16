package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// --------------------------------------------------------------------------
// Proxy key operations
// --------------------------------------------------------------------------

// CreateProxyKey inserts a new proxy key record.
func (s *SQLiteStore) CreateProxyKey(ctx context.Context, id, keyHash, keyPrefix, label string) error {
	const query = `INSERT INTO proxy_keys (id, key_hash, key_prefix, label, created_at) VALUES (?, ?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, query, id, keyHash, keyPrefix, label, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("create proxy key: %w", err)
	}

	return nil
}

// GetActiveProxyKeyByPrefix returns a single active (non-revoked) proxy key
// matching the given prefix, or sql.ErrNoRows if none is found.
func (s *SQLiteStore) GetActiveProxyKeyByPrefix(ctx context.Context, prefix string) (*ProxyKey, error) {
	const query = `SELECT id, key_hash, key_prefix, label, created_at, revoked_at FROM proxy_keys WHERE key_prefix = ? AND revoked_at IS NULL`

	row := s.db.QueryRowContext(ctx, query, prefix)

	var pk ProxyKey
	var revokedAt sql.NullTime

	if err := row.Scan(&pk.ID, &pk.KeyHash, &pk.KeyPrefix, &pk.Label, &pk.CreatedAt, &revokedAt); err != nil {
		return nil, fmt.Errorf("get proxy key by prefix: %w", err)
	}

	if revokedAt.Valid {
		pk.RevokedAt = &revokedAt.Time
	}

	return &pk, nil
}

// ListActiveProxyKeys returns all non-revoked proxy keys ordered by creation
// date descending.
func (s *SQLiteStore) ListActiveProxyKeys(ctx context.Context) ([]ProxyKeySummary, error) {
	const query = `SELECT id, key_prefix, label, created_at FROM proxy_keys WHERE revoked_at IS NULL ORDER BY created_at DESC`

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list active proxy keys: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []ProxyKeySummary

	for rows.Next() {
		var k ProxyKeySummary
		if err := rows.Scan(&k.ID, &k.KeyPrefix, &k.Label, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan proxy key: %w", err)
		}

		keys = append(keys, k)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxy keys: %w", err)
	}

	// Return empty slice rather than nil to satisfy JSON serialisation.
	if keys == nil {
		keys = []ProxyKeySummary{}
	}

	return keys, nil
}

// RevokeProxyKey soft-deletes a proxy key by setting its revoked_at timestamp.
func (s *SQLiteStore) RevokeProxyKey(ctx context.Context, prefix string) error {
	const query = `UPDATE proxy_keys SET revoked_at = ? WHERE key_prefix = ? AND revoked_at IS NULL`

	res, err := s.db.ExecContext(ctx, query, time.Now().UTC(), prefix)
	if err != nil {
		return fmt.Errorf("revoke proxy key: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("no active proxy key found with prefix %q", prefix)
	}

	return nil
}

// UpdateKeyLabel updates the label of an active proxy key identified by prefix.
func (s *SQLiteStore) UpdateKeyLabel(ctx context.Context, prefix, label string) error {
	const query = `UPDATE proxy_keys SET label = ? WHERE key_prefix = ? AND revoked_at IS NULL`

	res, err := s.db.ExecContext(ctx, query, label, prefix)
	if err != nil {
		return fmt.Errorf("update key label: %w", err)
	}

	affected, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}

	if affected == 0 {
		return fmt.Errorf("no active proxy key found with prefix %q", prefix)
	}

	return nil
}

// RecordAuditEvent inserts a new audit trail entry.
func (s *SQLiteStore) RecordAuditEvent(ctx context.Context, action, keyPrefix, detail, actorEmail string) error {
	const query = `INSERT INTO audit_events (action, key_prefix, detail, actor_email, created_at) VALUES (?, ?, ?, ?, ?)`

	_, err := s.db.ExecContext(ctx, query, action, keyPrefix, detail, actorEmail, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("record audit event: %w", err)
	}

	return nil
}
