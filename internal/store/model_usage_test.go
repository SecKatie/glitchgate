// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGetModelUsageSummary(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	// Create a proxy key needed for foreign-key constraint on request_logs.
	err := st.CreateProxyKey(ctx, "pk-model-test", "hash-model", "llmp_sk_mm01", "model-test-key")
	require.NoError(t, err)

	cost1 := 0.010
	cost2 := 0.020

	logs := []RequestLogEntry{
		{
			ID: "ml-1", ProxyKeyID: "pk-model-test",
			Timestamp:    time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 100, OutputTokens: 200,
			LatencyMs: 500, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
			EstimatedCostUSD: &cost1,
		},
		{
			ID: "ml-2", ProxyKeyID: "pk-model-test",
			Timestamp:    time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 300, OutputTokens: 400,
			LatencyMs: 600, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
			EstimatedCostUSD: &cost2,
		},
		{
			ID: "ml-3", ProxyKeyID: "pk-model-test",
			Timestamp:    time.Date(2026, 3, 3, 8, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-haiku", ModelUpstream: "claude-haiku-4-5-20251001",
			InputTokens: 50, OutputTokens: 75,
			LatencyMs: 300, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
			EstimatedCostUSD: nil,
		},
	}
	for _, l := range logs {
		require.NoError(t, st.InsertRequestLog(ctx, &l))
	}

	tests := []struct {
		name      string
		model     string
		wantCount int64
		wantIn    int64
		wantOut   int64
		wantCost  float64
	}{
		{
			name:      "model with two log entries aggregates correctly",
			model:     "claude-sonnet",
			wantCount: 2,
			wantIn:    400,
			wantOut:   600,
			wantCost:  0.030,
		},
		{
			name:      "model with one nil-cost entry returns zero cost",
			model:     "claude-haiku",
			wantCount: 1,
			wantIn:    50,
			wantOut:   75,
			wantCost:  0.0,
		},
		{
			name:      "model with no log entries returns zero-value summary",
			model:     "does-not-exist",
			wantCount: 0,
			wantIn:    0,
			wantOut:   0,
			wantCost:  0.0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			summary, err := st.GetModelUsageSummary(ctx, tc.model)
			require.NoError(t, err)
			require.NotNil(t, summary)
			require.Equal(t, tc.wantCount, summary.RequestCount)
			require.Equal(t, tc.wantIn, summary.InputTokens)
			require.Equal(t, tc.wantOut, summary.OutputTokens)
			require.InDelta(t, tc.wantCost, summary.TotalCostUSD, 0.0001)
		})
	}
}
