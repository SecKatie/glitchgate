// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// --------------------------------------------------------------------------
// OIDC user operations
// --------------------------------------------------------------------------

// UpsertOIDCUser inserts a new OIDC user or updates email/display_name on conflict.
// The first user ever inserted automatically gets the 'global_admin' role.
func (s *PostgreSQLStore) UpsertOIDCUser(ctx context.Context, subject, email, displayName string) (*OIDCUser, error) {
	now := time.Now().UTC()

	existing, err := s.GetOIDCUserBySubject(ctx, subject)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("upsert oidc user — check existing: %w", err)
	}

	if existing != nil {
		const updateQ = `UPDATE oidc_users SET email = $1, display_name = $2, last_seen_at = $3 WHERE subject = $4`
		if _, err := s.db.ExecContext(ctx, updateQ, email, displayName, now, subject); err != nil {
			return nil, fmt.Errorf("upsert oidc user — update: %w", err)
		}
		existing.Email = email
		existing.DisplayName = displayName
		existing.LastSeenAt = &now
		return existing, nil
	}

	count, err := s.CountGlobalAdmins(ctx)
	if err != nil {
		return nil, fmt.Errorf("upsert oidc user — count admins: %w", err)
	}
	role := "member"
	if count == 0 {
		role = "global_admin"
	}

	id := uuid.New().String()
	const insertQ = `INSERT INTO oidc_users (id, subject, email, display_name, role, active, created_at)
		VALUES ($1, $2, $3, $4, $5, TRUE, $6)`
	if _, err := s.db.ExecContext(ctx, insertQ, id, subject, email, displayName, role, now); err != nil {
		return nil, fmt.Errorf("upsert oidc user — insert: %w", err)
	}

	return &OIDCUser{
		ID:          id,
		Subject:     subject,
		Email:       email,
		DisplayName: displayName,
		Role:        role,
		Active:      true,
		CreatedAt:   now,
	}, nil
}

// GetOIDCUserByID returns the OIDC user with the given internal ID.
func (s *PostgreSQLStore) GetOIDCUserByID(ctx context.Context, id string) (*OIDCUser, error) {
	const q = `SELECT
		u.id, u.subject, u.email, u.display_name, u.role, u.active, u.last_seen_at, u.created_at,
		ub.limit_usd, ub.period
	FROM oidc_users u
	LEFT JOIN user_budgets ub ON ub.user_id = u.id
	WHERE u.id = $1`
	return s.pgScanOIDCUser(s.db.QueryRowContext(ctx, q, id))
}

// GetOIDCUserBySubject returns the OIDC user matching the IDP subject claim.
func (s *PostgreSQLStore) GetOIDCUserBySubject(ctx context.Context, subject string) (*OIDCUser, error) {
	const q = `SELECT
		u.id, u.subject, u.email, u.display_name, u.role, u.active, u.last_seen_at, u.created_at,
		ub.limit_usd, ub.period
	FROM oidc_users u
	LEFT JOIN user_budgets ub ON ub.user_id = u.id
	WHERE u.subject = $1`
	return s.pgScanOIDCUser(s.db.QueryRowContext(ctx, q, subject))
}

// pgScanOIDCUser scans a single OIDCUser row. Uses BOOLEAN column for active.
func (s *PostgreSQLStore) pgScanOIDCUser(row *sql.Row) (*OIDCUser, error) {
	var u OIDCUser
	var lastSeen sql.NullTime
	var budgetUSD sql.NullFloat64
	var budgetPeriod sql.NullString

	if err := row.Scan(&u.ID, &u.Subject, &u.Email, &u.DisplayName, &u.Role, &u.Active,
		&lastSeen, &u.CreatedAt, &budgetUSD, &budgetPeriod); err != nil {
		return nil, err
	}
	if lastSeen.Valid {
		u.LastSeenAt = &lastSeen.Time
	}
	u.Budget = scanBudgetPolicy(budgetUSD, budgetPeriod)
	return &u, nil
}

// ListOIDCUsers returns all OIDC users ordered by created_at ascending.
func (s *PostgreSQLStore) ListOIDCUsers(ctx context.Context) ([]OIDCUser, error) {
	const q = `SELECT
		u.id, u.subject, u.email, u.display_name, u.role, u.active, u.last_seen_at, u.created_at,
		ub.limit_usd, ub.period
	FROM oidc_users u
	LEFT JOIN user_budgets ub ON ub.user_id = u.id
	ORDER BY u.created_at ASC`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list oidc users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []OIDCUser
	for rows.Next() {
		var u OIDCUser
		var lastSeen sql.NullTime
		var budgetUSD sql.NullFloat64
		var budgetPeriod sql.NullString

		if err := rows.Scan(&u.ID, &u.Subject, &u.Email, &u.DisplayName, &u.Role, &u.Active,
			&lastSeen, &u.CreatedAt, &budgetUSD, &budgetPeriod); err != nil {
			return nil, fmt.Errorf("scan oidc user: %w", err)
		}
		if lastSeen.Valid {
			u.LastSeenAt = &lastSeen.Time
		}
		u.Budget = scanBudgetPolicy(budgetUSD, budgetPeriod)
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate oidc users: %w", err)
	}
	if users == nil {
		users = []OIDCUser{}
	}
	return users, nil
}

// ListUsersWithTeams returns the user admin projection with optional team info.
func (s *PostgreSQLStore) ListUsersWithTeams(ctx context.Context) ([]UserWithTeam, error) {
	const q = `SELECT
		u.id, u.email, u.display_name, u.role, u.active, u.last_seen_at, u.created_at,
		tm.team_id, t.name
	FROM oidc_users u
	LEFT JOIN team_memberships tm ON tm.user_id = u.id
	LEFT JOIN teams t ON t.id = tm.team_id
	ORDER BY u.created_at ASC`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list users with teams: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var users []UserWithTeam
	for rows.Next() {
		var u UserWithTeam
		var lastSeen sql.NullTime
		var teamID sql.NullString
		var teamName sql.NullString

		if err := rows.Scan(
			&u.ID, &u.Email, &u.DisplayName, &u.Role, &u.Active, &lastSeen, &u.CreatedAt,
			&teamID, &teamName,
		); err != nil {
			return nil, fmt.Errorf("scan user with team: %w", err)
		}

		if lastSeen.Valid {
			u.LastSeenAt = &lastSeen.Time
		}
		if teamID.Valid {
			u.TeamID = &teamID.String
		}
		if teamName.Valid {
			u.TeamName = &teamName.String
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate users with teams: %w", err)
	}
	if users == nil {
		users = []UserWithTeam{}
	}
	return users, nil
}

// CountGlobalAdmins returns the number of active global_admin users.
func (s *PostgreSQLStore) CountGlobalAdmins(ctx context.Context) (int64, error) {
	const q = `SELECT COUNT(*) FROM oidc_users WHERE role = 'global_admin' AND active = TRUE`
	var n int64
	if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("count global admins: %w", err)
	}
	return n, nil
}

// UpdateOIDCUserRole changes the role of the user with the given ID.
func (s *PostgreSQLStore) UpdateOIDCUserRole(ctx context.Context, id, role string) error {
	const q = `UPDATE oidc_users SET role = $1 WHERE id = $2`
	res, err := s.db.ExecContext(ctx, q, role, id)
	if err != nil {
		return fmt.Errorf("update oidc user role: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("oidc user not found: %s", id)
	}
	return nil
}

// SetOIDCUserActive sets or clears the active flag for the given user.
func (s *PostgreSQLStore) SetOIDCUserActive(ctx context.Context, id string, active bool) error {
	const q = `UPDATE oidc_users SET active = $1 WHERE id = $2`
	res, err := s.db.ExecContext(ctx, q, active, id)
	if err != nil {
		return fmt.Errorf("set oidc user active: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("oidc user not found: %s", id)
	}
	return nil
}

// UpdateOIDCUserLastSeen sets last_seen_at for the given user to now.
func (s *PostgreSQLStore) UpdateOIDCUserLastSeen(ctx context.Context, id string) error {
	const q = `UPDATE oidc_users SET last_seen_at = $1 WHERE id = $2`
	_, err := s.db.ExecContext(ctx, q, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update oidc user last seen: %w", err)
	}
	return nil
}

// --------------------------------------------------------------------------
// OIDC state operations
// --------------------------------------------------------------------------

// CreateOIDCState inserts a short-lived state + PKCE verifier row.
func (s *PostgreSQLStore) CreateOIDCState(ctx context.Context, state, pkceVerifier, redirectTo string, expiresAt time.Time) error {
	const q = `INSERT INTO oidc_state (state, pkce_verifier, redirect_to, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5)`
	if _, err := s.db.ExecContext(ctx, q, state, pkceVerifier, redirectTo, time.Now().UTC(), expiresAt); err != nil {
		return fmt.Errorf("create oidc state: %w", err)
	}
	return nil
}

// ConsumeOIDCState atomically reads and deletes the state row.
// Returns nil if the state is not found or has expired.
func (s *PostgreSQLStore) ConsumeOIDCState(ctx context.Context, state string) (*OIDCState, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("consume oidc state — begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const selectQ = `SELECT state, pkce_verifier, COALESCE(redirect_to,''), created_at, expires_at
		FROM oidc_state WHERE state = $1 AND expires_at > $2`

	var os OIDCState
	if err := tx.QueryRowContext(ctx, selectQ, state, time.Now().UTC()).Scan(
		&os.State, &os.PKCEVerifier, &os.RedirectTo, &os.CreatedAt, &os.ExpiresAt,
	); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil //nolint:nilnil // "not found or expired" is valid caller outcome
		}
		return nil, fmt.Errorf("consume oidc state — select: %w", err)
	}

	const deleteQ = `DELETE FROM oidc_state WHERE state = $1`
	if _, err := tx.ExecContext(ctx, deleteQ, state); err != nil {
		return nil, fmt.Errorf("consume oidc state — delete: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("consume oidc state — commit: %w", err)
	}
	return &os, nil
}

// CleanupExpiredOIDCState deletes expired oidc_state rows.
func (s *PostgreSQLStore) CleanupExpiredOIDCState(ctx context.Context) error {
	const q = `DELETE FROM oidc_state WHERE expires_at <= $1`
	if _, err := s.db.ExecContext(ctx, q, time.Now().UTC()); err != nil {
		return fmt.Errorf("cleanup expired oidc state: %w", err)
	}
	return nil
}

// --------------------------------------------------------------------------
// UI session operations
// --------------------------------------------------------------------------

// CreateUISession inserts a new UI session row.
// userID may be empty string for master_key sessions (stored as NULL).
func (s *PostgreSQLStore) CreateUISession(ctx context.Context, id, token, sessionType, userID string, expiresAt time.Time) error {
	var userIDVal any
	if userID != "" {
		userIDVal = userID
	}
	const q = `INSERT INTO ui_sessions (id, token, session_type, user_id, created_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	if _, err := s.db.ExecContext(ctx, q, id, token, sessionType, userIDVal, time.Now().UTC(), expiresAt); err != nil {
		return fmt.Errorf("create ui session: %w", err)
	}
	return nil
}

// GetUISessionByToken returns the active (non-expired) session matching the token.
func (s *PostgreSQLStore) GetUISessionByToken(ctx context.Context, token string) (*UISession, error) {
	const q = `SELECT id, token, session_type, user_id, created_at, expires_at
		FROM ui_sessions WHERE token = $1 AND expires_at > $2`

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
func (s *PostgreSQLStore) DeleteUISession(ctx context.Context, token string) error {
	const q = `DELETE FROM ui_sessions WHERE token = $1`
	if _, err := s.db.ExecContext(ctx, q, token); err != nil {
		return fmt.Errorf("delete ui session: %w", err)
	}
	return nil
}

// DeleteUISessionsByUserID removes all sessions for the given user (used on deactivation).
func (s *PostgreSQLStore) DeleteUISessionsByUserID(ctx context.Context, userID string) error {
	const q = `DELETE FROM ui_sessions WHERE user_id = $1`
	if _, err := s.db.ExecContext(ctx, q, userID); err != nil {
		return fmt.Errorf("delete ui sessions by user id: %w", err)
	}
	return nil
}

// CleanupExpiredSessions deletes all expired session rows.
func (s *PostgreSQLStore) CleanupExpiredSessions(ctx context.Context) error {
	const q = `DELETE FROM ui_sessions WHERE expires_at <= $1`
	if _, err := s.db.ExecContext(ctx, q, time.Now().UTC()); err != nil {
		return fmt.Errorf("cleanup expired sessions: %w", err)
	}
	return nil
}
