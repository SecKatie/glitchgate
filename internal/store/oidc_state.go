// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateOIDCState inserts a short-lived state + PKCE verifier row.
func (s *SQLiteStore) CreateOIDCState(ctx context.Context, state, pkceVerifier, redirectTo string, expiresAt time.Time) error {
	const q = `INSERT INTO oidc_state (state, pkce_verifier, redirect_to, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, q, state, pkceVerifier, redirectTo, time.Now().UTC(), expiresAt); err != nil {
		return fmt.Errorf("create oidc state: %w", err)
	}
	return nil
}

// ConsumeOIDCState atomically reads and deletes the state row.
// Returns nil if the state is not found or has expired.
func (s *SQLiteStore) ConsumeOIDCState(ctx context.Context, state string) (*OIDCState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("consume oidc state — begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const selectQ = `SELECT state, pkce_verifier, COALESCE(redirect_to,''), created_at, expires_at
		FROM oidc_state WHERE state = ? AND expires_at > ?`

	var os OIDCState
	if err := tx.QueryRowContext(ctx, selectQ, state, time.Now().UTC()).Scan(
		&os.State, &os.PKCEVerifier, &os.RedirectTo, &os.CreatedAt, &os.ExpiresAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil //nolint:nilnil // "not found or expired" is valid caller outcome
		}
		return nil, fmt.Errorf("consume oidc state — select: %w", err)
	}

	const deleteQ = `DELETE FROM oidc_state WHERE state = ?`
	if _, err := tx.ExecContext(ctx, deleteQ, state); err != nil {
		return nil, fmt.Errorf("consume oidc state — delete: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("consume oidc state — commit: %w", err)
	}
	return &os, nil
}

// CleanupExpiredOIDCState deletes expired oidc_state rows.
func (s *SQLiteStore) CleanupExpiredOIDCState(ctx context.Context) error {
	const q = `DELETE FROM oidc_state WHERE expires_at <= ?`
	if _, err := s.db.ExecContext(ctx, q, time.Now().UTC()); err != nil {
		return fmt.Errorf("cleanup expired oidc state: %w", err)
	}
	return nil
}
