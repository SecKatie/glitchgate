// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
)

// SetGlobalBudget upserts the global budget limit.
func (s *SQLiteStore) SetGlobalBudget(ctx context.Context, limitUSD float64, period string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE global_budget_settings SET limit_usd = ?, period = ?, updated_at = datetime('now') WHERE id = 1`,
		limitUSD, period)
	if err != nil {
		return fmt.Errorf("set global budget: %w", err)
	}
	return nil
}

// ClearGlobalBudget removes the global budget limit (keeps the row).
func (s *SQLiteStore) ClearGlobalBudget(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE global_budget_settings SET limit_usd = NULL, period = NULL, updated_at = datetime('now') WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("clear global budget: %w", err)
	}
	return nil
}

// SetUserBudget upserts a budget limit for the given user.
func (s *SQLiteStore) SetUserBudget(ctx context.Context, userID string, limitUSD float64, period string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_budgets (user_id, limit_usd, period, created_at, updated_at)
		 VALUES (?, ?, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(user_id) DO UPDATE SET limit_usd = excluded.limit_usd, period = excluded.period, updated_at = datetime('now')`,
		userID, limitUSD, period)
	if err != nil {
		return fmt.Errorf("set user budget: %w", err)
	}
	return nil
}

// ClearUserBudget removes the budget limit for the given user (keeps the row).
func (s *SQLiteStore) ClearUserBudget(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE user_budgets SET limit_usd = NULL, period = NULL, updated_at = datetime('now') WHERE user_id = ?`,
		userID)
	if err != nil {
		return fmt.Errorf("clear user budget: %w", err)
	}
	return nil
}

// SetTeamBudget upserts a budget limit for the given team.
func (s *SQLiteStore) SetTeamBudget(ctx context.Context, teamID string, limitUSD float64, period string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO team_budgets (team_id, limit_usd, period, created_at, updated_at)
		 VALUES (?, ?, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(team_id) DO UPDATE SET limit_usd = excluded.limit_usd, period = excluded.period, updated_at = datetime('now')`,
		teamID, limitUSD, period)
	if err != nil {
		return fmt.Errorf("set team budget: %w", err)
	}
	return nil
}

// ClearTeamBudget removes the budget limit for the given team (keeps the row).
func (s *SQLiteStore) ClearTeamBudget(ctx context.Context, teamID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE team_budgets SET limit_usd = NULL, period = NULL, updated_at = datetime('now') WHERE team_id = ?`,
		teamID)
	if err != nil {
		return fmt.Errorf("clear team budget: %w", err)
	}
	return nil
}

// SetKeyBudget upserts a budget limit for the given proxy key.
func (s *SQLiteStore) SetKeyBudget(ctx context.Context, keyID string, limitUSD float64, period string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO proxy_key_budgets (proxy_key_id, limit_usd, period, created_at, updated_at)
		 VALUES (?, ?, ?, datetime('now'), datetime('now'))
		 ON CONFLICT(proxy_key_id) DO UPDATE SET limit_usd = excluded.limit_usd, period = excluded.period, updated_at = datetime('now')`,
		keyID, limitUSD, period)
	if err != nil {
		return fmt.Errorf("set key budget: %w", err)
	}
	return nil
}

// ClearKeyBudget removes the budget limit for the given proxy key (keeps the row).
func (s *SQLiteStore) ClearKeyBudget(ctx context.Context, keyID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE proxy_key_budgets SET limit_usd = NULL, period = NULL, updated_at = datetime('now') WHERE proxy_key_id = ?`,
		keyID)
	if err != nil {
		return fmt.Errorf("clear key budget: %w", err)
	}
	return nil
}
