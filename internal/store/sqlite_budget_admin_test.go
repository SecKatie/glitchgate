// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBudgetAdminGlobal(t *testing.T) {
	t.Run("set and read back global budget", func(t *testing.T) {
		st := newTestStore(t)
		ctx := context.Background()

		require.NoError(t, st.SetGlobalBudget(ctx, 100.0, "monthly"))

		budgets, err := st.GetBudgetsForScope(ctx, "all", "", "")
		require.NoError(t, err)
		require.Len(t, budgets, 1)
		require.Equal(t, "global", budgets[0].Scope)
		require.Equal(t, 100.0, budgets[0].LimitUSD)
		require.Equal(t, "monthly", budgets[0].Period)
	})

	t.Run("upsert overwrites existing global budget", func(t *testing.T) {
		st := newTestStore(t)
		ctx := context.Background()

		require.NoError(t, st.SetGlobalBudget(ctx, 100.0, "monthly"))
		require.NoError(t, st.SetGlobalBudget(ctx, 50.0, "daily"))

		budgets, err := st.GetBudgetsForScope(ctx, "all", "", "")
		require.NoError(t, err)
		require.Len(t, budgets, 1)
		require.Equal(t, 50.0, budgets[0].LimitUSD)
		require.Equal(t, "daily", budgets[0].Period)
	})

	t.Run("clear global budget", func(t *testing.T) {
		st := newTestStore(t)
		ctx := context.Background()

		require.NoError(t, st.SetGlobalBudget(ctx, 100.0, "monthly"))
		require.NoError(t, st.ClearGlobalBudget(ctx))

		budgets, err := st.GetBudgetsForScope(ctx, "all", "", "")
		require.NoError(t, err)
		require.Empty(t, budgets)
	})
}

func TestBudgetAdminUser(t *testing.T) {
	t.Run("set and read back user budget", func(t *testing.T) {
		st := newTestStore(t)
		userID := seedBudgetTestData(t, st)
		ctx := context.Background()

		require.NoError(t, st.SetUserBudget(ctx, userID, 25.0, "weekly"))

		budgets, err := st.GetBudgetsForScope(ctx, "user", userID, "")
		require.NoError(t, err)
		require.Len(t, budgets, 1)
		require.Equal(t, "user", budgets[0].Scope)
		require.Equal(t, 25.0, budgets[0].LimitUSD)
		require.Equal(t, "weekly", budgets[0].Period)
	})

	t.Run("upsert overwrites existing user budget", func(t *testing.T) {
		st := newTestStore(t)
		userID := seedBudgetTestData(t, st)
		ctx := context.Background()

		require.NoError(t, st.SetUserBudget(ctx, userID, 25.0, "weekly"))
		require.NoError(t, st.SetUserBudget(ctx, userID, 10.0, "daily"))

		budgets, err := st.GetBudgetsForScope(ctx, "user", userID, "")
		require.NoError(t, err)
		require.Len(t, budgets, 1)
		require.Equal(t, 10.0, budgets[0].LimitUSD)
		require.Equal(t, "daily", budgets[0].Period)
	})

	t.Run("clear user budget", func(t *testing.T) {
		st := newTestStore(t)
		userID := seedBudgetTestData(t, st)
		ctx := context.Background()

		require.NoError(t, st.SetUserBudget(ctx, userID, 25.0, "weekly"))
		require.NoError(t, st.ClearUserBudget(ctx, userID))

		budgets, err := st.GetBudgetsForScope(ctx, "user", userID, "")
		require.NoError(t, err)
		require.Empty(t, budgets)
	})
}

func TestBudgetAdminTeam(t *testing.T) {
	t.Run("set and read back team budget", func(t *testing.T) {
		st := newTestStore(t)
		seedBudgetTestData(t, st)
		ctx := context.Background()

		require.NoError(t, st.SetTeamBudget(ctx, "team-budget-1", 50.0, "monthly"))

		budgets, err := st.GetBudgetsForScope(ctx, "team", "", "team-budget-1")
		require.NoError(t, err)
		require.Len(t, budgets, 1)
		require.Equal(t, "team", budgets[0].Scope)
		require.Equal(t, 50.0, budgets[0].LimitUSD)
		require.Equal(t, "monthly", budgets[0].Period)
	})

	t.Run("upsert overwrites existing team budget", func(t *testing.T) {
		st := newTestStore(t)
		seedBudgetTestData(t, st)
		ctx := context.Background()

		require.NoError(t, st.SetTeamBudget(ctx, "team-budget-1", 50.0, "monthly"))
		require.NoError(t, st.SetTeamBudget(ctx, "team-budget-1", 30.0, "weekly"))

		budgets, err := st.GetBudgetsForScope(ctx, "team", "", "team-budget-1")
		require.NoError(t, err)
		require.Len(t, budgets, 1)
		require.Equal(t, 30.0, budgets[0].LimitUSD)
		require.Equal(t, "weekly", budgets[0].Period)
	})

	t.Run("clear team budget", func(t *testing.T) {
		st := newTestStore(t)
		seedBudgetTestData(t, st)
		ctx := context.Background()

		require.NoError(t, st.SetTeamBudget(ctx, "team-budget-1", 50.0, "monthly"))
		require.NoError(t, st.ClearTeamBudget(ctx, "team-budget-1"))

		budgets, err := st.GetBudgetsForScope(ctx, "team", "", "team-budget-1")
		require.NoError(t, err)
		require.Empty(t, budgets)
	})
}

func TestBudgetAdminKey(t *testing.T) {
	t.Run("set and read back key budget", func(t *testing.T) {
		st := newTestStore(t)
		seedBudgetTestData(t, st)
		ctx := context.Background()

		require.NoError(t, st.SetKeyBudget(ctx, "pk-budget-1", 5.0, "daily"))

		budgets, err := st.GetApplicableBudgets(ctx, "pk-budget-1")
		require.NoError(t, err)
		require.Len(t, budgets, 1)
		require.Equal(t, "key", budgets[0].Scope)
		require.Equal(t, 5.0, budgets[0].LimitUSD)
		require.Equal(t, "daily", budgets[0].Period)
	})

	t.Run("upsert overwrites existing key budget", func(t *testing.T) {
		st := newTestStore(t)
		seedBudgetTestData(t, st)
		ctx := context.Background()

		require.NoError(t, st.SetKeyBudget(ctx, "pk-budget-1", 5.0, "daily"))
		require.NoError(t, st.SetKeyBudget(ctx, "pk-budget-1", 15.0, "weekly"))

		budgets, err := st.GetApplicableBudgets(ctx, "pk-budget-1")
		require.NoError(t, err)
		require.Len(t, budgets, 1)
		require.Equal(t, 15.0, budgets[0].LimitUSD)
		require.Equal(t, "weekly", budgets[0].Period)
	})

	t.Run("clear key budget", func(t *testing.T) {
		st := newTestStore(t)
		seedBudgetTestData(t, st)
		ctx := context.Background()

		require.NoError(t, st.SetKeyBudget(ctx, "pk-budget-1", 5.0, "daily"))
		require.NoError(t, st.ClearKeyBudget(ctx, "pk-budget-1"))

		budgets, err := st.GetApplicableBudgets(ctx, "pk-budget-1")
		require.NoError(t, err)
		require.Empty(t, budgets)
	})
}

func TestBudgetAdminIntegration(t *testing.T) {
	t.Run("set budgets at all scopes and verify via GetApplicableBudgets", func(t *testing.T) {
		st := newTestStore(t)
		userID := seedBudgetTestData(t, st)
		ctx := context.Background()

		require.NoError(t, st.SetGlobalBudget(ctx, 1000.0, "monthly"))
		require.NoError(t, st.SetUserBudget(ctx, userID, 100.0, "weekly"))
		require.NoError(t, st.SetTeamBudget(ctx, "team-budget-1", 500.0, "monthly"))
		require.NoError(t, st.SetKeyBudget(ctx, "pk-budget-1", 10.0, "daily"))

		budgets, err := st.GetApplicableBudgets(ctx, "pk-budget-1")
		require.NoError(t, err)
		require.Len(t, budgets, 4)

		scopes := make(map[string]float64)
		for _, b := range budgets {
			scopes[b.Scope] = b.LimitUSD
		}
		require.Equal(t, 10.0, scopes["key"])
		require.Equal(t, 100.0, scopes["user"])
		require.Equal(t, 500.0, scopes["team"])
		require.Equal(t, 1000.0, scopes["global"])
	})
}
