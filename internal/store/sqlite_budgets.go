// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"time"
)

// GetApplicableBudgets returns all budget policies that apply to a given proxy key.
// It checks key, user, team, and global budget tables via a single UNION ALL query.
func (s *SQLiteStore) GetApplicableBudgets(ctx context.Context, proxyKeyID string) ([]ApplicableBudget, error) {
	const query = `
		SELECT 'key' AS scope, pkb.proxy_key_id AS scope_id, pkb.limit_usd, pkb.period
		  FROM proxy_key_budgets pkb
		  WHERE pkb.proxy_key_id = ? AND pkb.limit_usd IS NOT NULL
		UNION ALL
		SELECT 'user', ub.user_id, ub.limit_usd, ub.period
		  FROM proxy_key_owners pko
		  JOIN user_budgets ub ON ub.user_id = pko.owner_user_id
		  WHERE pko.proxy_key_id = ? AND ub.limit_usd IS NOT NULL
		UNION ALL
		SELECT 'team', tb.team_id, tb.limit_usd, tb.period
		  FROM proxy_key_owners pko
		  JOIN team_memberships tm ON tm.user_id = pko.owner_user_id
		  JOIN team_budgets tb ON tb.team_id = tm.team_id
		  WHERE pko.proxy_key_id = ? AND tb.limit_usd IS NOT NULL
		UNION ALL
		SELECT 'global', '', gbs.limit_usd, gbs.period
		  FROM global_budget_settings gbs
		  WHERE gbs.id = 1 AND gbs.limit_usd IS NOT NULL`

	rows, err := s.db.QueryContext(ctx, query, proxyKeyID, proxyKeyID, proxyKeyID)
	if err != nil {
		return nil, fmt.Errorf("get applicable budgets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var budgets []ApplicableBudget
	for rows.Next() {
		var b ApplicableBudget
		if err := rows.Scan(&b.Scope, &b.ScopeID, &b.LimitUSD, &b.Period); err != nil {
			return nil, fmt.Errorf("scan applicable budget: %w", err)
		}
		budgets = append(budgets, b)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate applicable budgets: %w", err)
	}
	return budgets, nil
}

// GetSpendSince returns the total cost_usd from request_logs since the given
// timestamp for the specified scope.
func (s *SQLiteStore) GetSpendSince(ctx context.Context, scope, scopeID string, since time.Time) (float64, error) {
	sinceUTC := since.UTC().Format("2006-01-02 15:04:05")

	var query string
	var args []any

	switch scope {
	case "key":
		query = `SELECT COALESCE(SUM(cost_usd), 0) FROM request_logs WHERE proxy_key_id = ? AND timestamp >= ?`
		args = []any{scopeID, sinceUTC}
	case "user":
		query = `SELECT COALESCE(SUM(rl.cost_usd), 0)
			FROM request_logs rl
			JOIN proxy_key_owners pko ON pko.proxy_key_id = rl.proxy_key_id
			WHERE pko.owner_user_id = ? AND rl.timestamp >= ?`
		args = []any{scopeID, sinceUTC}
	case "team":
		query = `SELECT COALESCE(SUM(rl.cost_usd), 0)
			FROM request_logs rl
			JOIN proxy_key_owners pko ON pko.proxy_key_id = rl.proxy_key_id
			JOIN team_memberships tm ON tm.user_id = pko.owner_user_id
			WHERE tm.team_id = ? AND rl.timestamp >= ?`
		args = []any{scopeID, sinceUTC}
	case "global":
		query = `SELECT COALESCE(SUM(cost_usd), 0) FROM request_logs WHERE timestamp >= ?`
		args = []any{sinceUTC}
	default:
		return 0, fmt.Errorf("unknown budget scope: %s", scope)
	}

	var spend float64
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&spend); err != nil {
		return 0, fmt.Errorf("get spend since for %s: %w", scope, err)
	}
	return spend, nil
}
