// SPDX-License-Identifier: AGPL-3.0-or-later

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
func (s *PostgreSQLStore) CreateProxyKey(ctx context.Context, id, keyHash, keyPrefix, label string) error {
	const query = `INSERT INTO proxy_keys (id, key_hash, key_prefix, label, created_at) VALUES ($1, $2, $3, $4, $5)`

	_, err := s.db.ExecContext(ctx, query, id, keyHash, keyPrefix, label, time.Now().UTC())
	if err != nil {
		return fmt.Errorf("create proxy key: %w", err)
	}

	return nil
}

// GetActiveProxyKeyByPrefix returns a single active (non-revoked) proxy key
// matching the given prefix, or sql.ErrNoRows if none is found.
func (s *PostgreSQLStore) GetActiveProxyKeyByPrefix(ctx context.Context, prefix string) (*ProxyKey, error) {
	const query = `SELECT id, key_hash, key_prefix, label, created_at, revoked_at FROM proxy_keys WHERE key_prefix = $1 AND revoked_at IS NULL`

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
func (s *PostgreSQLStore) ListActiveProxyKeys(ctx context.Context) ([]ProxyKeySummary, error) {
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

	if keys == nil {
		keys = []ProxyKeySummary{}
	}

	return keys, nil
}

// RevokeProxyKey soft-deletes a proxy key by setting its revoked_at timestamp.
func (s *PostgreSQLStore) RevokeProxyKey(ctx context.Context, prefix string) error {
	const query = `UPDATE proxy_keys SET revoked_at = $1 WHERE key_prefix = $2 AND revoked_at IS NULL`

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
func (s *PostgreSQLStore) UpdateKeyLabel(ctx context.Context, prefix, label string) error {
	const query = `UPDATE proxy_keys SET label = $1 WHERE key_prefix = $2 AND revoked_at IS NULL`

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
func (s *PostgreSQLStore) RecordAuditEvent(ctx context.Context, action, keyPrefix, detail, actorEmail string) error {
	const query = `INSERT INTO audit_events (action, key_prefix, detail, actor_email, created_at) VALUES ($1, $2, $3, $4, $5)`

	now := time.Now().UTC()

	_, err := s.db.ExecContext(ctx, query, action, keyPrefix, detail, actorEmail, now)
	if err != nil {
		if !isPostgresConstraintViolation(err, "audit_events_pkey") {
			return fmt.Errorf("record audit event: %w", err)
		}

		if repairErr := syncAuditEventsSequence(ctx, s.db); repairErr != nil {
			return fmt.Errorf("record audit event: %w", repairErr)
		}

		if _, retryErr := s.db.ExecContext(ctx, query, action, keyPrefix, detail, actorEmail, now); retryErr != nil {
			return fmt.Errorf("record audit event: %w", retryErr)
		}
	}

	return nil
}

// --------------------------------------------------------------------------
// Scoped proxy key operations
// --------------------------------------------------------------------------

// ListProxyKeysByOwner returns all active proxy keys owned by the given user.
func (s *PostgreSQLStore) ListProxyKeysByOwner(ctx context.Context, ownerUserID string) ([]ProxyKeySummary, error) {
	const q = `SELECT id, key_prefix, label, created_at
		FROM proxy_keys pk
		JOIN proxy_key_owners pko ON pko.proxy_key_id = pk.id
		WHERE pko.owner_user_id = $1 AND pk.revoked_at IS NULL
		ORDER BY created_at DESC`

	return s.pgScanProxyKeySummaries(ctx, q, ownerUserID)
}

// ListProxyKeysByTeam returns all active proxy keys owned by users in the given team.
func (s *PostgreSQLStore) ListProxyKeysByTeam(ctx context.Context, teamID string) ([]ProxyKeySummary, error) {
	const q = `SELECT pk.id, pk.key_prefix, pk.label, pk.created_at
		FROM proxy_keys pk
		JOIN proxy_key_owners pko ON pko.proxy_key_id = pk.id
		JOIN team_memberships tm ON tm.user_id = pko.owner_user_id
		WHERE tm.team_id = $1 AND pk.revoked_at IS NULL
		ORDER BY pk.created_at DESC`

	return s.pgScanProxyKeySummaries(ctx, q, teamID)
}

// CreateProxyKeyForUser inserts a new proxy key associated with an OIDC user.
func (s *PostgreSQLStore) CreateProxyKeyForUser(ctx context.Context, id, keyHash, keyPrefix, label, ownerUserID string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("create proxy key for user: begin tx: %w", err)
	}

	defer func() { _ = tx.Rollback() }()

	now := time.Now().UTC()

	const insertKey = `INSERT INTO proxy_keys (id, key_hash, key_prefix, label, created_at)
		VALUES ($1, $2, $3, $4, $5)`
	if _, err := tx.ExecContext(ctx, insertKey, id, keyHash, keyPrefix, label, now); err != nil {
		return fmt.Errorf("create proxy key for user: insert key: %w", err)
	}

	const insertOwner = `INSERT INTO proxy_key_owners (proxy_key_id, owner_user_id, assigned_at)
		VALUES ($1, $2, $3)`
	if _, err := tx.ExecContext(ctx, insertOwner, id, ownerUserID, now); err != nil {
		return fmt.Errorf("create proxy key for user: insert owner: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("create proxy key for user: commit: %w", err)
	}

	return nil
}

// pgScanProxyKeySummaries executes a query with a single string arg and scans ProxyKeySummary rows.
func (s *PostgreSQLStore) pgScanProxyKeySummaries(ctx context.Context, query, arg string) ([]ProxyKeySummary, error) {
	rows, err := s.db.QueryContext(ctx, query, arg)
	if err != nil {
		return nil, fmt.Errorf("scan proxy key summaries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var keys []ProxyKeySummary
	for rows.Next() {
		var k ProxyKeySummary
		if err := rows.Scan(&k.ID, &k.KeyPrefix, &k.Label, &k.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan proxy key summary: %w", err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate proxy key summaries: %w", err)
	}
	if keys == nil {
		keys = []ProxyKeySummary{}
	}
	return keys, nil
}
