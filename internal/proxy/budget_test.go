// SPDX-License-Identifier: AGPL-3.0-or-later

package proxy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/seckatie/glitchgate/internal/store"
)

func TestPeriodStart(t *testing.T) {
	utc := time.UTC
	eastern, _ := time.LoadLocation("America/New_York")

	tests := []struct {
		name   string
		period string
		now    time.Time
		tz     *time.Location
		want   time.Time
	}{
		{
			name:   "daily UTC",
			period: "daily",
			now:    time.Date(2026, 3, 15, 14, 30, 0, 0, utc),
			tz:     utc,
			want:   time.Date(2026, 3, 15, 0, 0, 0, 0, utc),
		},
		{
			name:   "daily eastern — UTC date differs",
			period: "daily",
			now:    time.Date(2026, 3, 15, 3, 0, 0, 0, utc), // 11pm Mar 14 Eastern
			tz:     eastern,
			want:   time.Date(2026, 3, 14, 4, 0, 0, 0, utc), // midnight Mar 14 Eastern = 4am UTC (EDT)
		},
		{
			name:   "weekly — mid-week",
			period: "weekly",
			now:    time.Date(2026, 3, 18, 10, 0, 0, 0, utc), // Wednesday
			tz:     utc,
			want:   time.Date(2026, 3, 16, 0, 0, 0, 0, utc), // Monday
		},
		{
			name:   "weekly — Monday",
			period: "weekly",
			now:    time.Date(2026, 3, 16, 10, 0, 0, 0, utc), // Monday
			tz:     utc,
			want:   time.Date(2026, 3, 16, 0, 0, 0, 0, utc), // Same Monday
		},
		{
			name:   "weekly — Sunday",
			period: "weekly",
			now:    time.Date(2026, 3, 15, 10, 0, 0, 0, utc), // Sunday
			tz:     utc,
			want:   time.Date(2026, 3, 9, 0, 0, 0, 0, utc), // Previous Monday
		},
		{
			name:   "monthly",
			period: "monthly",
			now:    time.Date(2026, 3, 15, 14, 0, 0, 0, utc),
			tz:     utc,
			want:   time.Date(2026, 3, 1, 0, 0, 0, 0, utc),
		},
		{
			name:   "monthly — first day",
			period: "monthly",
			now:    time.Date(2026, 1, 1, 0, 0, 0, 0, utc),
			tz:     utc,
			want:   time.Date(2026, 1, 1, 0, 0, 0, 0, utc),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PeriodStart(tt.period, tt.now, tt.tz)
			require.Equal(t, tt.want, got)
		})
	}
}

func TestPeriodResetAt(t *testing.T) {
	utc := time.UTC

	tests := []struct {
		name   string
		period string
		now    time.Time
		want   time.Time
	}{
		{
			name:   "daily",
			period: "daily",
			now:    time.Date(2026, 3, 15, 14, 0, 0, 0, utc),
			want:   time.Date(2026, 3, 16, 0, 0, 0, 0, utc),
		},
		{
			name:   "weekly — Wednesday",
			period: "weekly",
			now:    time.Date(2026, 3, 18, 10, 0, 0, 0, utc), // Wednesday
			want:   time.Date(2026, 3, 23, 0, 0, 0, 0, utc),  // Next Monday
		},
		{
			name:   "weekly — Monday",
			period: "weekly",
			now:    time.Date(2026, 3, 16, 10, 0, 0, 0, utc), // Monday
			want:   time.Date(2026, 3, 23, 0, 0, 0, 0, utc),  // Next Monday
		},
		{
			name:   "monthly",
			period: "monthly",
			now:    time.Date(2026, 3, 15, 14, 0, 0, 0, utc),
			want:   time.Date(2026, 4, 1, 0, 0, 0, 0, utc),
		},
		{
			name:   "monthly — December wraps to January",
			period: "monthly",
			now:    time.Date(2026, 12, 15, 14, 0, 0, 0, utc),
			want:   time.Date(2027, 1, 1, 0, 0, 0, 0, utc),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := PeriodResetAt(tt.period, tt.now, utc)
			require.Equal(t, tt.want, got)
		})
	}
}

// budgetStoreStub is a test double for BudgetCheckStore.
type budgetStoreStub struct {
	budgets  []store.ApplicableBudget
	spendMap map[string]float64 // scope:scopeID → spend
	err      error
}

func (s *budgetStoreStub) GetApplicableBudgets(_ context.Context, _ string) ([]store.ApplicableBudget, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.budgets, nil
}

func (s *budgetStoreStub) GetSpendSince(_ context.Context, scope, scopeID string, _ time.Time) (float64, error) {
	if s.err != nil {
		return 0, s.err
	}
	return s.spendMap[scope+":"+scopeID], nil
}

func (s *budgetStoreStub) GetBudgetsForScope(_ context.Context, _, _, _ string) ([]store.ApplicableBudget, error) {
	return s.budgets, nil
}

func TestBudgetChecker_NoBudgets(t *testing.T) {
	bc := NewBudgetChecker(&budgetStoreStub{}, time.UTC)
	violation, err := bc.Check(context.Background(), "pk-1")
	require.NoError(t, err)
	require.Nil(t, violation)
}

func TestBudgetChecker_EmptyProxyKeyID(t *testing.T) {
	bc := NewBudgetChecker(&budgetStoreStub{
		budgets: []store.ApplicableBudget{{Scope: "global", LimitUSD: 1.0, Period: "daily"}},
	}, time.UTC)
	violation, err := bc.Check(context.Background(), "")
	require.NoError(t, err)
	require.Nil(t, violation)
}

func TestBudgetChecker_UnderLimit(t *testing.T) {
	bc := NewBudgetChecker(&budgetStoreStub{
		budgets:  []store.ApplicableBudget{{Scope: "global", LimitUSD: 10.0, Period: "daily"}},
		spendMap: map[string]float64{"global:": 5.0},
	}, time.UTC)

	violation, err := bc.Check(context.Background(), "pk-1")
	require.NoError(t, err)
	require.Nil(t, violation)
}

func TestBudgetChecker_AtLimit(t *testing.T) {
	bc := NewBudgetChecker(&budgetStoreStub{
		budgets:  []store.ApplicableBudget{{Scope: "global", LimitUSD: 10.0, Period: "daily"}},
		spendMap: map[string]float64{"global:": 10.0},
	}, time.UTC)

	violation, err := bc.Check(context.Background(), "pk-1")
	require.NoError(t, err)
	require.NotNil(t, violation)
	require.Equal(t, "global", violation.Scope)
	require.Equal(t, 10.0, violation.LimitUSD)
	require.Equal(t, 10.0, violation.SpendUSD)
}

func TestBudgetChecker_OverLimit(t *testing.T) {
	bc := NewBudgetChecker(&budgetStoreStub{
		budgets:  []store.ApplicableBudget{{Scope: "key", ScopeID: "pk-1", LimitUSD: 5.0, Period: "daily"}},
		spendMap: map[string]float64{"key:pk-1": 7.50},
	}, time.UTC)

	violation, err := bc.Check(context.Background(), "pk-1")
	require.NoError(t, err)
	require.NotNil(t, violation)
	require.Equal(t, "key", violation.Scope)
	require.Equal(t, "pk-1", violation.ScopeID)
	require.Equal(t, "daily", violation.Period)
	require.Equal(t, 5.0, violation.LimitUSD)
	require.Equal(t, 7.50, violation.SpendUSD)
	require.False(t, violation.ResetAt.IsZero())
}

func TestBudgetChecker_MultipleBudgets(t *testing.T) {
	bc := NewBudgetChecker(&budgetStoreStub{
		budgets: []store.ApplicableBudget{
			{Scope: "key", ScopeID: "pk-1", LimitUSD: 100.0, Period: "daily"},
			{Scope: "user", ScopeID: "user-1", LimitUSD: 5.0, Period: "daily"},
			{Scope: "global", LimitUSD: 200.0, Period: "monthly"},
		},
		spendMap: map[string]float64{
			"key:pk-1":    2.0,
			"user:user-1": 6.0, // Over user limit
			"global:":     50.0,
		},
	}, time.UTC)

	violation, err := bc.Check(context.Background(), "pk-1")
	require.NoError(t, err)
	require.NotNil(t, violation)
	require.Equal(t, "user", violation.Scope)
	require.Equal(t, "user-1", violation.ScopeID)
}

func TestBudgetChecker_DBError_FailOpen(t *testing.T) {
	bc := NewBudgetChecker(&budgetStoreStub{
		err: errors.New("db connection failed"),
	}, time.UTC)

	violation, err := bc.Check(context.Background(), "pk-1")
	require.NoError(t, err)
	require.Nil(t, violation) // Fail open
}
