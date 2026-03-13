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

// UpsertOIDCUser inserts a new OIDC user or updates email/display_name on conflict.
// The role, active flag, and created_at are NOT overwritten on update.
// The first user ever inserted automatically gets the 'global_admin' role.
func (s *SQLiteStore) UpsertOIDCUser(ctx context.Context, subject, email, displayName string) (*OIDCUser, error) {
	now := time.Now().UTC()

	// Check if this user already exists.
	existing, err := s.GetOIDCUserBySubject(ctx, subject)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("upsert oidc user — check existing: %w", err)
	}

	if existing != nil {
		// Update mutable fields only.
		const updateQ = `UPDATE oidc_users SET email = ?, display_name = ?, last_seen_at = ? WHERE subject = ?`
		if _, err := s.db.ExecContext(ctx, updateQ, email, displayName, now, subject); err != nil {
			return nil, fmt.Errorf("upsert oidc user — update: %w", err)
		}
		existing.Email = email
		existing.DisplayName = displayName
		existing.LastSeenAt = &now
		return existing, nil
	}

	// New user — determine role.
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
		VALUES (?, ?, ?, ?, ?, 1, ?)`
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
func (s *SQLiteStore) GetOIDCUserByID(ctx context.Context, id string) (*OIDCUser, error) {
	const q = `SELECT
		u.id, u.subject, u.email, u.display_name, u.role, u.active, u.last_seen_at, u.created_at,
		ub.limit_usd, ub.period
	FROM oidc_users u
	LEFT JOIN user_budgets ub ON ub.user_id = u.id
	WHERE u.id = ?`
	return s.scanOIDCUser(s.db.QueryRowContext(ctx, q, id))
}

// GetOIDCUserBySubject returns the OIDC user matching the IDP subject claim.
func (s *SQLiteStore) GetOIDCUserBySubject(ctx context.Context, subject string) (*OIDCUser, error) {
	const q = `SELECT
		u.id, u.subject, u.email, u.display_name, u.role, u.active, u.last_seen_at, u.created_at,
		ub.limit_usd, ub.period
	FROM oidc_users u
	LEFT JOIN user_budgets ub ON ub.user_id = u.id
	WHERE u.subject = ?`
	return s.scanOIDCUser(s.db.QueryRowContext(ctx, q, subject))
}

// scanOIDCUser scans a single OIDCUser row.
func (s *SQLiteStore) scanOIDCUser(row *sql.Row) (*OIDCUser, error) {
	var u OIDCUser
	var activeInt int
	var lastSeen sql.NullTime
	var budgetUSD sql.NullFloat64
	var budgetPeriod sql.NullString

	if err := row.Scan(&u.ID, &u.Subject, &u.Email, &u.DisplayName, &u.Role, &activeInt,
		&lastSeen, &u.CreatedAt, &budgetUSD, &budgetPeriod); err != nil {
		return nil, err
	}
	u.Active = activeInt == 1
	if lastSeen.Valid {
		u.LastSeenAt = &lastSeen.Time
	}
	u.Budget = scanBudgetPolicy(budgetUSD, budgetPeriod)
	return &u, nil
}

// ListOIDCUsers returns all OIDC users ordered by created_at ascending.
func (s *SQLiteStore) ListOIDCUsers(ctx context.Context) ([]OIDCUser, error) {
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
		var activeInt int
		var lastSeen sql.NullTime
		var budgetUSD sql.NullFloat64
		var budgetPeriod sql.NullString

		if err := rows.Scan(&u.ID, &u.Subject, &u.Email, &u.DisplayName, &u.Role, &activeInt,
			&lastSeen, &u.CreatedAt, &budgetUSD, &budgetPeriod); err != nil {
			return nil, fmt.Errorf("scan oidc user: %w", err)
		}
		u.Active = activeInt == 1
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
func (s *SQLiteStore) ListUsersWithTeams(ctx context.Context) ([]UserWithTeam, error) {
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
		var activeInt int
		var lastSeen sql.NullTime
		var teamID sql.NullString
		var teamName sql.NullString

		if err := rows.Scan(
			&u.ID, &u.Email, &u.DisplayName, &u.Role, &activeInt, &lastSeen, &u.CreatedAt,
			&teamID, &teamName,
		); err != nil {
			return nil, fmt.Errorf("scan user with team: %w", err)
		}

		u.Active = activeInt == 1
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
func (s *SQLiteStore) CountGlobalAdmins(ctx context.Context) (int64, error) {
	const q = `SELECT COUNT(*) FROM oidc_users WHERE role = 'global_admin' AND active = 1`
	var n int64
	if err := s.db.QueryRowContext(ctx, q).Scan(&n); err != nil {
		return 0, fmt.Errorf("count global admins: %w", err)
	}
	return n, nil
}

// UpdateOIDCUserRole changes the role of the user with the given ID.
func (s *SQLiteStore) UpdateOIDCUserRole(ctx context.Context, id, role string) error {
	const q = `UPDATE oidc_users SET role = ? WHERE id = ?`
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
func (s *SQLiteStore) SetOIDCUserActive(ctx context.Context, id string, active bool) error {
	activeInt := 0
	if active {
		activeInt = 1
	}
	const q = `UPDATE oidc_users SET active = ? WHERE id = ?`
	res, err := s.db.ExecContext(ctx, q, activeInt, id)
	if err != nil {
		return fmt.Errorf("set oidc user active: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("oidc user not found: %s", id)
	}
	return nil
}

// UpdateOIDCUserLastSeen sets last_seen_at for the given user to now.
func (s *SQLiteStore) UpdateOIDCUserLastSeen(ctx context.Context, id string) error {
	const q = `UPDATE oidc_users SET last_seen_at = ? WHERE id = ?`
	_, err := s.db.ExecContext(ctx, q, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update oidc user last seen: %w", err)
	}
	return nil
}
