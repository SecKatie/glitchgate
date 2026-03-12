// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// CreateTeam inserts a new team record.
func (s *SQLiteStore) CreateTeam(ctx context.Context, id, name, description string) error {
	const q = `INSERT INTO teams (id, name, description, created_at) VALUES (?, ?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, q, id, name, description, time.Now().UTC()); err != nil {
		return fmt.Errorf("create team: %w", err)
	}
	return nil
}

// ListTeams returns all teams ordered by name.
func (s *SQLiteStore) ListTeams(ctx context.Context) ([]Team, error) {
	const q = `SELECT id, name, COALESCE(description,''), created_at, budget_limit_usd, budget_period FROM teams ORDER BY name ASC`

	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list teams: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var teams []Team
	for rows.Next() {
		t, err := s.scanTeam(rows)
		if err != nil {
			return nil, err
		}
		teams = append(teams, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate teams: %w", err)
	}
	if teams == nil {
		teams = []Team{}
	}
	return teams, nil
}

// GetTeamByID returns the team with the given ID.
func (s *SQLiteStore) GetTeamByID(ctx context.Context, id string) (*Team, error) {
	const q = `SELECT id, name, COALESCE(description,''), created_at, budget_limit_usd, budget_period FROM teams WHERE id = ?`
	row := s.db.QueryRowContext(ctx, q, id)

	var t Team
	var budgetUSD sql.NullFloat64
	var budgetPeriod sql.NullString
	if err := row.Scan(&t.ID, &t.Name, &t.Description, &t.CreatedAt, &budgetUSD, &budgetPeriod); err != nil {
		return nil, fmt.Errorf("get team by id: %w", err)
	}
	if budgetUSD.Valid {
		t.BudgetLimitUSD = &budgetUSD.Float64
	}
	if budgetPeriod.Valid {
		t.BudgetPeriod = &budgetPeriod.String
	}
	return &t, nil
}

// scanTeam scans a Team from a *sql.Rows cursor.
func (s *SQLiteStore) scanTeam(rows *sql.Rows) (*Team, error) {
	var t Team
	var budgetUSD sql.NullFloat64
	var budgetPeriod sql.NullString
	if err := rows.Scan(&t.ID, &t.Name, &t.Description, &t.CreatedAt, &budgetUSD, &budgetPeriod); err != nil {
		return nil, fmt.Errorf("scan team: %w", err)
	}
	if budgetUSD.Valid {
		t.BudgetLimitUSD = &budgetUSD.Float64
	}
	if budgetPeriod.Valid {
		t.BudgetPeriod = &budgetPeriod.String
	}
	return &t, nil
}

// AssignUserToTeam assigns a user to a team, replacing any existing membership.
func (s *SQLiteStore) AssignUserToTeam(ctx context.Context, userID, teamID string) error {
	const q = `INSERT OR REPLACE INTO team_memberships (user_id, team_id, joined_at) VALUES (?, ?, ?)`
	if _, err := s.db.ExecContext(ctx, q, userID, teamID, time.Now().UTC()); err != nil {
		return fmt.Errorf("assign user to team: %w", err)
	}
	return nil
}

// RemoveUserFromTeam removes a user's team membership.
func (s *SQLiteStore) RemoveUserFromTeam(ctx context.Context, userID string) error {
	const q = `DELETE FROM team_memberships WHERE user_id = ?`
	if _, err := s.db.ExecContext(ctx, q, userID); err != nil {
		return fmt.Errorf("remove user from team: %w", err)
	}
	return nil
}

// GetTeamMembership returns the team membership for the given user, or nil if unassigned.
func (s *SQLiteStore) GetTeamMembership(ctx context.Context, userID string) (*TeamMembership, error) {
	const q = `SELECT user_id, team_id, joined_at FROM team_memberships WHERE user_id = ?`
	var m TeamMembership
	if err := s.db.QueryRowContext(ctx, q, userID).Scan(&m.UserID, &m.TeamID, &m.JoinedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil //nolint:nilnil // explicit "not found" pattern
		}
		return nil, fmt.Errorf("get team membership: %w", err)
	}
	return &m, nil
}

// ListTeamMembers returns all active users assigned to the given team.
func (s *SQLiteStore) ListTeamMembers(ctx context.Context, teamID string) ([]OIDCUser, error) {
	const q = `SELECT u.id, u.subject, u.email, u.display_name, u.role, u.active,
		u.last_seen_at, u.created_at, u.budget_limit_usd, u.budget_period
		FROM oidc_users u
		JOIN team_memberships tm ON tm.user_id = u.id
		WHERE tm.team_id = ?
		ORDER BY u.email ASC`

	rows, err := s.db.QueryContext(ctx, q, teamID)
	if err != nil {
		return nil, fmt.Errorf("list team members: %w", err)
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
			return nil, fmt.Errorf("scan team member: %w", err)
		}
		u.Active = activeInt == 1
		if lastSeen.Valid {
			u.LastSeenAt = &lastSeen.Time
		}
		if budgetUSD.Valid {
			u.BudgetLimitUSD = &budgetUSD.Float64
		}
		if budgetPeriod.Valid {
			u.BudgetPeriod = &budgetPeriod.String
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate team members: %w", err)
	}
	if users == nil {
		users = []OIDCUser{}
	}
	return users, nil
}
