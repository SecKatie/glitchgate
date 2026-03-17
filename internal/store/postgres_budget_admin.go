// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
)

// SetGlobalBudget upserts the global budget limit.
func (s *PostgreSQLStore) SetGlobalBudget(ctx context.Context, limitUSD float64, period string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE global_budget_settings SET limit_usd = $1, period = $2, updated_at = NOW() WHERE id = 1`,
		limitUSD, period)
	if err != nil {
		return fmt.Errorf("set global budget: %w", err)
	}
	return nil
}

// ClearGlobalBudget removes the global budget limit (keeps the row).
func (s *PostgreSQLStore) ClearGlobalBudget(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE global_budget_settings SET limit_usd = NULL, period = NULL, updated_at = NOW() WHERE id = 1`)
	if err != nil {
		return fmt.Errorf("clear global budget: %w", err)
	}
	return nil
}

// SetUserBudget upserts a budget limit for the given user.
func (s *PostgreSQLStore) SetUserBudget(ctx context.Context, userID string, limitUSD float64, period string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO user_budgets (user_id, limit_usd, period, created_at, updated_at)
		 VALUES ($1, $2, $3, NOW(), NOW())
		 ON CONFLICT(user_id) DO UPDATE SET limit_usd = EXCLUDED.limit_usd, period = EXCLUDED.period, updated_at = NOW()`,
		userID, limitUSD, period)
	if err != nil {
		return fmt.Errorf("set user budget: %w", err)
	}
	return nil
}

// ClearUserBudget removes the budget limit for the given user (keeps the row).
func (s *PostgreSQLStore) ClearUserBudget(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE user_budgets SET limit_usd = NULL, period = NULL, updated_at = NOW() WHERE user_id = $1`,
		userID)
	if err != nil {
		return fmt.Errorf("clear user budget: %w", err)
	}
	return nil
}

// SetTeamBudget upserts a budget limit for the given team.
func (s *PostgreSQLStore) SetTeamBudget(ctx context.Context, teamID string, limitUSD float64, period string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO team_budgets (team_id, limit_usd, period, created_at, updated_at)
		 VALUES ($1, $2, $3, NOW(), NOW())
		 ON CONFLICT(team_id) DO UPDATE SET limit_usd = EXCLUDED.limit_usd, period = EXCLUDED.period, updated_at = NOW()`,
		teamID, limitUSD, period)
	if err != nil {
		return fmt.Errorf("set team budget: %w", err)
	}
	return nil
}

// ClearTeamBudget removes the budget limit for the given team (keeps the row).
func (s *PostgreSQLStore) ClearTeamBudget(ctx context.Context, teamID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE team_budgets SET limit_usd = NULL, period = NULL, updated_at = NOW() WHERE team_id = $1`,
		teamID)
	if err != nil {
		return fmt.Errorf("clear team budget: %w", err)
	}
	return nil
}

// SetKeyBudget upserts a budget limit for the given proxy key.
func (s *PostgreSQLStore) SetKeyBudget(ctx context.Context, keyID string, limitUSD float64, period string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO proxy_key_budgets (proxy_key_id, limit_usd, period, created_at, updated_at)
		 VALUES ($1, $2, $3, NOW(), NOW())
		 ON CONFLICT(proxy_key_id) DO UPDATE SET limit_usd = EXCLUDED.limit_usd, period = EXCLUDED.period, updated_at = NOW()`,
		keyID, limitUSD, period)
	if err != nil {
		return fmt.Errorf("set key budget: %w", err)
	}
	return nil
}

// ClearKeyBudget removes the budget limit for the given proxy key (keeps the row).
func (s *PostgreSQLStore) ClearKeyBudget(ctx context.Context, keyID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE proxy_key_budgets SET limit_usd = NULL, period = NULL, updated_at = NOW() WHERE proxy_key_id = $1`,
		keyID)
	if err != nil {
		return fmt.Errorf("clear key budget: %w", err)
	}
	return nil
}
