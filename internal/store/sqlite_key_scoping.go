// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
)

// GetKeyAllowedModels returns the model patterns allowed for a proxy key.
// An empty slice means unrestricted access.
func (s *SQLiteStore) GetKeyAllowedModels(ctx context.Context, keyID string) ([]string, error) {
	const query = `SELECT model_pattern FROM proxy_key_allowed_models WHERE proxy_key_id = ? ORDER BY model_pattern`
	rows, err := s.db.QueryContext(ctx, query, keyID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var patterns []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		patterns = append(patterns, p)
	}
	return patterns, rows.Err()
}

// SetKeyAllowedModels replaces the model allowlist for a proxy key.
// An empty patterns slice clears the allowlist (unrestricted access).
func (s *SQLiteStore) SetKeyAllowedModels(ctx context.Context, keyID string, patterns []string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM proxy_key_allowed_models WHERE proxy_key_id = ?`, keyID); err != nil {
		return err
	}

	if len(patterns) > 0 {
		stmt, err := tx.PrepareContext(ctx, `INSERT INTO proxy_key_allowed_models (proxy_key_id, model_pattern) VALUES (?, ?)`)
		if err != nil {
			return err
		}
		defer func() { _ = stmt.Close() }()

		for _, p := range patterns {
			if _, err := stmt.ExecContext(ctx, keyID, p); err != nil {
				return err
			}
		}
	}

	return tx.Commit()
}

// GetKeyRateLimit returns per-key rate limit overrides.
// ok is false if no custom limit is configured for this key.
func (s *SQLiteStore) GetKeyRateLimit(ctx context.Context, keyID string) (perMinute, burst int, ok bool, err error) {
	const query = `SELECT requests_per_minute, burst FROM proxy_key_rate_limits WHERE proxy_key_id = ?`
	err = s.db.QueryRowContext(ctx, query, keyID).Scan(&perMinute, &burst)
	if err == sql.ErrNoRows {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, err
	}
	return perMinute, burst, true, nil
}

// SetKeyRateLimit creates or updates per-key rate limit overrides.
func (s *SQLiteStore) SetKeyRateLimit(ctx context.Context, keyID string, perMinute, burst int) error {
	const query = `INSERT INTO proxy_key_rate_limits (proxy_key_id, requests_per_minute, burst)
		VALUES (?, ?, ?)
		ON CONFLICT (proxy_key_id) DO UPDATE SET requests_per_minute = excluded.requests_per_minute, burst = excluded.burst`
	_, err := s.db.ExecContext(ctx, query, keyID, perMinute, burst)
	return err
}

// ClearKeyRateLimit removes per-key rate limit overrides.
func (s *SQLiteStore) ClearKeyRateLimit(ctx context.Context, keyID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM proxy_key_rate_limits WHERE proxy_key_id = ?`, keyID)
	return err
}
