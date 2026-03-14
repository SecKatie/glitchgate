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

	logs := []RequestLogEntry{
		{
			ID: "ml-1", ProxyKeyID: "pk-model-test",
			Timestamp:    time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 100, OutputTokens: 200,
			LatencyMs: 500, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
		{
			ID: "ml-2", ProxyKeyID: "pk-model-test",
			Timestamp:    time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 300, OutputTokens: 400,
			LatencyMs: 600, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
		{
			ID: "ml-3", ProxyKeyID: "pk-model-test",
			Timestamp:    time.Date(2026, 3, 3, 8, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-haiku", ModelUpstream: "claude-haiku-4-5-20251001",
			InputTokens: 50, OutputTokens: 75,
			LatencyMs: 300, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
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
			wantCost:  0.0,
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

func TestGetModelLatencyTimeseries(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)

	err := st.CreateProxyKey(ctx, "pk-latency-test", "hash-latency", "llmp_sk_lt01", "latency-test-key")
	require.NoError(t, err)

	logs := []RequestLogEntry{
		{
			ID: "lt-1", ProxyKeyID: "pk-latency-test",
			Timestamp:    time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 100, OutputTokens: 200,
			LatencyMs: 1000, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
		{
			ID: "lt-2", ProxyKeyID: "pk-latency-test",
			Timestamp:    time.Date(2026, 3, 1, 14, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 100, OutputTokens: 300,
			LatencyMs: 1500, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
		{
			ID: "lt-3", ProxyKeyID: "pk-latency-test",
			Timestamp:    time.Date(2026, 3, 2, 8, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 50, OutputTokens: 100,
			LatencyMs: 800, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
		{
			// Zero output tokens — should be excluded from timeseries.
			ID: "lt-4", ProxyKeyID: "pk-latency-test",
			Timestamp:    time.Date(2026, 3, 2, 9, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 50, OutputTokens: 0,
			LatencyMs: 200, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
	}
	for _, l := range logs {
		require.NoError(t, st.InsertRequestLog(ctx, &l))
	}

	t.Run("returns hourly aggregates excluding zero-output requests", func(t *testing.T) {
		entries, err := st.GetModelLatencyTimeseries(ctx, "claude-sonnet")
		require.NoError(t, err)
		require.Len(t, entries, 3)

		// Hour 1 (10:00): 1000ms / 200 tokens = 5.0 ms/tok, 1 request
		require.Equal(t, "2026-03-01 10", entries[0].Bucket)
		require.Equal(t, int64(1000), entries[0].TotalLatencyMs)
		require.Equal(t, int64(200), entries[0].TotalOutputTokens)
		require.Equal(t, int64(1), entries[0].Requests)
		require.InDelta(t, 5.0, entries[0].AvgMsPerOutputToken, 0.01)

		// Hour 2 (14:00): 1500ms / 300 tokens = 5.0 ms/tok, 1 request
		require.Equal(t, "2026-03-01 14", entries[1].Bucket)
		require.Equal(t, int64(1500), entries[1].TotalLatencyMs)
		require.Equal(t, int64(300), entries[1].TotalOutputTokens)
		require.Equal(t, int64(1), entries[1].Requests)
		require.InDelta(t, 5.0, entries[1].AvgMsPerOutputToken, 0.01)

		// Hour 3 (08:00 next day): 800ms / 100 tokens = 8.0 ms/tok, 1 request (lt-4 excluded)
		require.Equal(t, "2026-03-02 08", entries[2].Bucket)
		require.Equal(t, int64(800), entries[2].TotalLatencyMs)
		require.Equal(t, int64(100), entries[2].TotalOutputTokens)
		require.Equal(t, int64(1), entries[2].Requests)
		require.InDelta(t, 8.0, entries[2].AvgMsPerOutputToken, 0.01)
	})

	t.Run("returns empty slice for unknown model", func(t *testing.T) {
		entries, err := st.GetModelLatencyTimeseries(ctx, "nonexistent-model")
		require.NoError(t, err)
		require.Empty(t, entries)
	})
}
