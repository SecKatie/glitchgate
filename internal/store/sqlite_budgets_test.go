// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func seedBudgetTestData(t *testing.T, st *SQLiteStore) (userID string) {
	t.Helper()
	ctx := context.Background()

	// Create proxy keys.
	require.NoError(t, st.CreateProxyKey(ctx, "pk-budget-1", "hash-b1", "llmp_sk_bg01", "budget-key-1"))
	require.NoError(t, st.CreateProxyKey(ctx, "pk-budget-2", "hash-b2", "llmp_sk_bg02", "budget-key-2"))

	// Create user.
	user, err := st.UpsertOIDCUser(ctx, "sub-user1", "user1@test.com", "User One")
	require.NoError(t, err)
	userID = user.ID

	// Create team.
	_, err = st.db.ExecContext(ctx, `INSERT INTO teams (id, name, created_at) VALUES (?, ?, ?)`,
		"team-budget-1", "Budget Team", time.Now().UTC())
	require.NoError(t, err)

	// Assign user to team.
	_, err = st.db.ExecContext(ctx, `INSERT INTO team_memberships (user_id, team_id, joined_at) VALUES (?, ?, ?)`,
		userID, "team-budget-1", time.Now().UTC())
	require.NoError(t, err)

	// Assign key ownership.
	_, err = st.db.ExecContext(ctx, `INSERT INTO proxy_key_owners (proxy_key_id, owner_user_id, assigned_at) VALUES (?, ?, ?)`,
		"pk-budget-1", userID, time.Now().UTC())
	require.NoError(t, err)

	// Insert request logs with cost_usd.
	costSmall := 0.05
	costLarge := 2.50
	logs := []RequestLogEntry{
		{
			ID: "bl-1", ProxyKeyID: "pk-budget-1",
			Timestamp:    time.Date(2026, 3, 15, 10, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 100, OutputTokens: 500,
			LatencyMs: 1000, Status: 200,
			CostUSD: &costSmall,
		},
		{
			ID: "bl-2", ProxyKeyID: "pk-budget-1",
			Timestamp:    time.Date(2026, 3, 15, 14, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-opus", ModelUpstream: "claude-opus-4-20250514",
			InputTokens: 200, OutputTokens: 800,
			LatencyMs: 2000, Status: 200,
			CostUSD: &costLarge,
		},
	}
	for _, l := range logs {
		require.NoError(t, st.InsertRequestLog(ctx, &l))
	}
	return userID
}

func TestGetApplicableBudgets(t *testing.T) {
	t.Run("no budgets configured returns empty", func(t *testing.T) {
		st := newTestStore(t)
		seedBudgetTestData(t, st)

		budgets, err := st.GetApplicableBudgets(context.Background(), "pk-budget-1")
		require.NoError(t, err)
		require.Empty(t, budgets)
	})

	t.Run("global budget only", func(t *testing.T) {
		st := newTestStore(t)
		seedBudgetTestData(t, st)
		ctx := context.Background()

		_, err := st.db.ExecContext(ctx,
			`UPDATE global_budget_settings SET limit_usd = 100.0, period = 'daily', updated_at = datetime('now') WHERE id = 1`)
		require.NoError(t, err)

		budgets, err := st.GetApplicableBudgets(ctx, "pk-budget-1")
		require.NoError(t, err)
		require.Len(t, budgets, 1)
		require.Equal(t, "global", budgets[0].Scope)
		require.Equal(t, 100.0, budgets[0].LimitUSD)
		require.Equal(t, "daily", budgets[0].Period)
	})

	t.Run("key budget", func(t *testing.T) {
		st := newTestStore(t)
		seedBudgetTestData(t, st)
		ctx := context.Background()

		_, err := st.db.ExecContext(ctx,
			`INSERT INTO proxy_key_budgets (proxy_key_id, limit_usd, period, created_at, updated_at) VALUES (?, 10.0, 'daily', datetime('now'), datetime('now'))`,
			"pk-budget-1")
		require.NoError(t, err)

		budgets, err := st.GetApplicableBudgets(ctx, "pk-budget-1")
		require.NoError(t, err)
		require.Len(t, budgets, 1)
		require.Equal(t, "key", budgets[0].Scope)
		require.Equal(t, "pk-budget-1", budgets[0].ScopeID)
		require.Equal(t, 10.0, budgets[0].LimitUSD)
	})

	t.Run("all scopes configured", func(t *testing.T) {
		st := newTestStore(t)
		userID := seedBudgetTestData(t, st)
		ctx := context.Background()

		_, err := st.db.ExecContext(ctx,
			`INSERT INTO proxy_key_budgets (proxy_key_id, limit_usd, period, created_at, updated_at) VALUES (?, 5.0, 'daily', datetime('now'), datetime('now'))`,
			"pk-budget-1")
		require.NoError(t, err)

		_, err = st.db.ExecContext(ctx,
			`INSERT INTO user_budgets (user_id, limit_usd, period, created_at, updated_at) VALUES (?, 20.0, 'weekly', datetime('now'), datetime('now'))`,
			userID)
		require.NoError(t, err)

		_, err = st.db.ExecContext(ctx,
			`INSERT INTO team_budgets (team_id, limit_usd, period, created_at, updated_at) VALUES (?, 50.0, 'monthly', datetime('now'), datetime('now'))`,
			"team-budget-1")
		require.NoError(t, err)

		_, err = st.db.ExecContext(ctx,
			`UPDATE global_budget_settings SET limit_usd = 100.0, period = 'monthly', updated_at = datetime('now') WHERE id = 1`)
		require.NoError(t, err)

		budgets, err := st.GetApplicableBudgets(ctx, "pk-budget-1")
		require.NoError(t, err)
		require.Len(t, budgets, 4)

		scopes := make(map[string]bool)
		for _, b := range budgets {
			scopes[b.Scope] = true
		}
		require.True(t, scopes["key"])
		require.True(t, scopes["user"])
		require.True(t, scopes["team"])
		require.True(t, scopes["global"])
	})

	t.Run("key without owner returns only key and global budgets", func(t *testing.T) {
		st := newTestStore(t)
		seedBudgetTestData(t, st)
		ctx := context.Background()

		_, err := st.db.ExecContext(ctx,
			`INSERT INTO proxy_key_budgets (proxy_key_id, limit_usd, period, created_at, updated_at) VALUES (?, 10.0, 'daily', datetime('now'), datetime('now'))`,
			"pk-budget-2")
		require.NoError(t, err)

		_, err = st.db.ExecContext(ctx,
			`UPDATE global_budget_settings SET limit_usd = 100.0, period = 'daily', updated_at = datetime('now') WHERE id = 1`)
		require.NoError(t, err)

		budgets, err := st.GetApplicableBudgets(ctx, "pk-budget-2")
		require.NoError(t, err)
		require.Len(t, budgets, 2)
	})
}

func TestGetSpendSince(t *testing.T) {
	st := newTestStore(t)
	userID := seedBudgetTestData(t, st)
	ctx := context.Background()

	// Total seeded cost_usd: 0.05 + 2.50 = 2.55, all on 2026-03-15.
	since := time.Date(2026, 3, 15, 0, 0, 0, 0, time.UTC)

	t.Run("key scope", func(t *testing.T) {
		spend, err := st.GetSpendSince(ctx, "key", "pk-budget-1", since)
		require.NoError(t, err)
		require.InDelta(t, 2.55, spend, 0.001)
	})

	t.Run("user scope", func(t *testing.T) {
		spend, err := st.GetSpendSince(ctx, "user", userID, since)
		require.NoError(t, err)
		require.InDelta(t, 2.55, spend, 0.001)
	})

	t.Run("team scope", func(t *testing.T) {
		spend, err := st.GetSpendSince(ctx, "team", "team-budget-1", since)
		require.NoError(t, err)
		require.InDelta(t, 2.55, spend, 0.001)
	})

	t.Run("global scope", func(t *testing.T) {
		spend, err := st.GetSpendSince(ctx, "global", "", since)
		require.NoError(t, err)
		require.InDelta(t, 2.55, spend, 0.001)
	})

	t.Run("no spend before seeded data", func(t *testing.T) {
		future := time.Date(2026, 3, 16, 0, 0, 0, 0, time.UTC)
		spend, err := st.GetSpendSince(ctx, "key", "pk-budget-1", future)
		require.NoError(t, err)
		require.Equal(t, 0.0, spend)
	})

	t.Run("different key has no spend", func(t *testing.T) {
		spend, err := st.GetSpendSince(ctx, "key", "pk-budget-2", since)
		require.NoError(t, err)
		require.Equal(t, 0.0, spend)
	})
}

func TestGetBudgetsForScope(t *testing.T) {
	t.Run("no budgets returns empty", func(t *testing.T) {
		st := newTestStore(t)
		budgets, err := st.GetBudgetsForScope(context.Background(), "all", "", "")
		require.NoError(t, err)
		require.Empty(t, budgets)
	})

	t.Run("all scope returns global user and team budgets", func(t *testing.T) {
		st := newTestStore(t)
		userID := seedBudgetTestData(t, st)
		ctx := context.Background()

		_, err := st.db.ExecContext(ctx,
			`UPDATE global_budget_settings SET limit_usd = 100.0, period = 'monthly', updated_at = datetime('now') WHERE id = 1`)
		require.NoError(t, err)
		_, err = st.db.ExecContext(ctx,
			`INSERT INTO user_budgets (user_id, limit_usd, period, created_at, updated_at) VALUES (?, 20.0, 'daily', datetime('now'), datetime('now'))`, userID)
		require.NoError(t, err)
		_, err = st.db.ExecContext(ctx,
			`INSERT INTO team_budgets (team_id, limit_usd, period, created_at, updated_at) VALUES (?, 50.0, 'weekly', datetime('now'), datetime('now'))`, "team-budget-1")
		require.NoError(t, err)

		budgets, err := st.GetBudgetsForScope(ctx, "all", "", "")
		require.NoError(t, err)
		require.Len(t, budgets, 3)
	})

	t.Run("user scope returns global and user budgets only", func(t *testing.T) {
		st := newTestStore(t)
		userID := seedBudgetTestData(t, st)
		ctx := context.Background()

		_, err := st.db.ExecContext(ctx,
			`UPDATE global_budget_settings SET limit_usd = 100.0, period = 'monthly', updated_at = datetime('now') WHERE id = 1`)
		require.NoError(t, err)
		_, err = st.db.ExecContext(ctx,
			`INSERT INTO user_budgets (user_id, limit_usd, period, created_at, updated_at) VALUES (?, 20.0, 'daily', datetime('now'), datetime('now'))`, userID)
		require.NoError(t, err)
		_, err = st.db.ExecContext(ctx,
			`INSERT INTO team_budgets (team_id, limit_usd, period, created_at, updated_at) VALUES (?, 50.0, 'weekly', datetime('now'), datetime('now'))`, "team-budget-1")
		require.NoError(t, err)

		budgets, err := st.GetBudgetsForScope(ctx, "user", userID, "")
		require.NoError(t, err)
		require.Len(t, budgets, 2) // global + user only

		scopes := make(map[string]bool)
		for _, b := range budgets {
			scopes[b.Scope] = true
		}
		require.True(t, scopes["global"])
		require.True(t, scopes["user"])
		require.False(t, scopes["team"])
	})

	t.Run("team scope returns global team and user budgets", func(t *testing.T) {
		st := newTestStore(t)
		userID := seedBudgetTestData(t, st)
		ctx := context.Background()

		_, err := st.db.ExecContext(ctx,
			`UPDATE global_budget_settings SET limit_usd = 100.0, period = 'monthly', updated_at = datetime('now') WHERE id = 1`)
		require.NoError(t, err)
		_, err = st.db.ExecContext(ctx,
			`INSERT INTO user_budgets (user_id, limit_usd, period, created_at, updated_at) VALUES (?, 20.0, 'daily', datetime('now'), datetime('now'))`, userID)
		require.NoError(t, err)
		_, err = st.db.ExecContext(ctx,
			`INSERT INTO team_budgets (team_id, limit_usd, period, created_at, updated_at) VALUES (?, 50.0, 'weekly', datetime('now'), datetime('now'))`, "team-budget-1")
		require.NoError(t, err)

		budgets, err := st.GetBudgetsForScope(ctx, "team", userID, "team-budget-1")
		require.NoError(t, err)
		require.Len(t, budgets, 3) // global + team + user
	})
}
