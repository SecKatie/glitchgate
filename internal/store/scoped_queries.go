// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"time"
)

// ListProxyKeysByOwner returns all active proxy keys owned by the given user.
func (s *SQLiteStore) ListProxyKeysByOwner(ctx context.Context, ownerUserID string) ([]ProxyKeySummary, error) {
	const q = `SELECT id, key_prefix, label, created_at
		FROM proxy_keys
		WHERE owner_user_id = ? AND revoked_at IS NULL
		ORDER BY created_at DESC`

	return s.scanProxyKeySummaries(ctx, q, ownerUserID)
}

// ListProxyKeysByTeam returns all active proxy keys owned by users in the given team.
func (s *SQLiteStore) ListProxyKeysByTeam(ctx context.Context, teamID string) ([]ProxyKeySummary, error) {
	const q = `SELECT pk.id, pk.key_prefix, pk.label, pk.created_at
		FROM proxy_keys pk
		JOIN oidc_users u ON u.id = pk.owner_user_id
		JOIN team_memberships tm ON tm.user_id = u.id
		WHERE tm.team_id = ? AND pk.revoked_at IS NULL
		ORDER BY pk.created_at DESC`

	return s.scanProxyKeySummaries(ctx, q, teamID)
}

// CreateProxyKeyForUser inserts a new proxy key associated with an OIDC user.
func (s *SQLiteStore) CreateProxyKeyForUser(ctx context.Context, id, keyHash, keyPrefix, label, ownerUserID string) error {
	const q = `INSERT INTO proxy_keys (id, key_hash, key_prefix, label, owner_user_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, q, id, keyHash, keyPrefix, label, ownerUserID, time.Now().UTC()); err != nil {
		return fmt.Errorf("create proxy key for user: %w", err)
	}
	return nil
}

// scanProxyKeySummaries executes a query with a single string arg and scans ProxyKeySummary rows.
func (s *SQLiteStore) scanProxyKeySummaries(ctx context.Context, query, arg string) ([]ProxyKeySummary, error) {
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
