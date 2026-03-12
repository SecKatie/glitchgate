// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateUISession inserts a new UI session row.
// userID may be empty string for master_key sessions (stored as NULL).
func (s *SQLiteStore) CreateUISession(ctx context.Context, id, token, sessionType, userID string, expiresAt time.Time) error {
	var userIDVal any
	if userID != "" {
		userIDVal = userID
	}
	const q = `INSERT INTO ui_sessions (id, token, session_type, user_id, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, q, id, token, sessionType, userIDVal, time.Now().UTC(), expiresAt); err != nil {
		return fmt.Errorf("create ui session: %w", err)
	}
	return nil
}

// GetUISessionByToken returns the active (non-expired) session matching the token.
func (s *SQLiteStore) GetUISessionByToken(ctx context.Context, token string) (*UISession, error) {
	const q = `SELECT id, token, session_type, user_id, created_at, expires_at
		FROM ui_sessions WHERE token = ? AND expires_at > ?`

	var sess UISession
	var userID sql.NullString

	if err := s.db.QueryRowContext(ctx, q, token, time.Now().UTC()).Scan(
		&sess.ID, &sess.Token, &sess.SessionType, &userID, &sess.CreatedAt, &sess.ExpiresAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil //nolint:nilnil // "not found" pattern
		}
		return nil, fmt.Errorf("get ui session by token: %w", err)
	}
	if userID.Valid {
		sess.UserID = &userID.String
	}
	return &sess, nil
}

// DeleteUISession removes a session by token (used for logout).
func (s *SQLiteStore) DeleteUISession(ctx context.Context, token string) error {
	const q = `DELETE FROM ui_sessions WHERE token = ?`
	if _, err := s.db.ExecContext(ctx, q, token); err != nil {
		return fmt.Errorf("delete ui session: %w", err)
	}
	return nil
}

// DeleteUISessionsByUserID removes all sessions for the given user (used on deactivation).
func (s *SQLiteStore) DeleteUISessionsByUserID(ctx context.Context, userID string) error {
	const q = `DELETE FROM ui_sessions WHERE user_id = ?`
	if _, err := s.db.ExecContext(ctx, q, userID); err != nil {
		return fmt.Errorf("delete ui sessions by user id: %w", err)
	}
	return nil
}

// CleanupExpiredSessions deletes all expired session rows.
func (s *SQLiteStore) CleanupExpiredSessions(ctx context.Context) error {
	const q = `DELETE FROM ui_sessions WHERE expires_at <= ?`
	if _, err := s.db.ExecContext(ctx, q, time.Now().UTC()); err != nil {
		return fmt.Errorf("cleanup expired sessions: %w", err)
	}
	return nil
}
