// SPDX-License-Identifier: AGPL-3.0-or-later

package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestStore creates a temporary SQLite store with migrations applied.
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	st, err := NewSQLiteStore(dbPath)
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	err = st.Migrate(context.Background())
	require.NoError(t, err)

	return st
}

// seedCostData inserts a proxy key and several request logs for cost testing.
func seedCostData(t *testing.T, st *SQLiteStore) {
	t.Helper()
	ctx := context.Background()

	// Create two proxy keys.
	err := st.CreateProxyKey(ctx, "pk-1", "hash-1", "llmp_sk_aa11", "key-alpha")
	require.NoError(t, err)
	err = st.CreateProxyKey(ctx, "pk-2", "hash-2", "llmp_sk_bb22", "key-beta")
	require.NoError(t, err)

	cost1 := 0.015
	cost2 := 0.030
	cost3 := 0.010
	cost4 := 0.025

	logs := []RequestLogEntry{
		{
			ID: "log-1", ProxyKeyID: "pk-1",
			Timestamp:    time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 100, OutputTokens: 500,
			LatencyMs: 1200, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
			EstimatedCostUSD: &cost1,
		},
		{
			ID: "log-2", ProxyKeyID: "pk-1",
			Timestamp:    time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-opus", ModelUpstream: "claude-opus-4-20250514",
			InputTokens: 200, OutputTokens: 800,
			LatencyMs: 2500, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
			EstimatedCostUSD: &cost2,
		},
		{
			ID: "log-3", ProxyKeyID: "pk-2",
			Timestamp:    time.Date(2026, 3, 2, 16, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 150, OutputTokens: 600,
			LatencyMs: 1800, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
			EstimatedCostUSD: &cost3,
		},
		{
			ID: "log-4", ProxyKeyID: "pk-2",
			Timestamp:    time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-opus", ModelUpstream: "claude-opus-4-20250514",
			InputTokens: 300, OutputTokens: 1000,
			LatencyMs: 3000, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
			EstimatedCostUSD: &cost4,
		},
	}

	for i := range logs {
		err := st.InsertRequestLog(ctx, &logs[i])
		require.NoError(t, err)
	}
}

func TestGetCostSummary(t *testing.T) {
	// Skip if running in CI without CGO (though modernc.org/sqlite is pure Go).
	if os.Getenv("SKIP_DB_TESTS") != "" {
		t.Skip("skipping database tests")
	}

	st := newTestStore(t)
	seedCostData(t, st)

	ctx := context.Background()

	t.Run("full range", func(t *testing.T) {
		params := CostParams{
			From: "2026-03-01",
			To:   "2026-03-10 23:59:59",
		}
		summary, err := st.GetCostSummary(ctx, params)
		require.NoError(t, err)
		require.NotNil(t, summary)

		require.InDelta(t, 0.080, summary.TotalCostUSD, 0.001)
		require.Equal(t, int64(750), summary.TotalInputTokens)
		require.Equal(t, int64(2900), summary.TotalOutputTokens)
		require.Equal(t, int64(4), summary.TotalRequests)
	})

	t.Run("partial range", func(t *testing.T) {
		params := CostParams{
			From: "2026-03-01",
			To:   "2026-03-02 23:59:59",
		}
		summary, err := st.GetCostSummary(ctx, params)
		require.NoError(t, err)

		require.Equal(t, int64(3), summary.TotalRequests)
		require.InDelta(t, 0.055, summary.TotalCostUSD, 0.001)
	})

	t.Run("filter by key prefix", func(t *testing.T) {
		params := CostParams{
			From:      "2026-03-01",
			To:        "2026-03-10 23:59:59",
			KeyPrefix: "llmp_sk_aa11",
		}
		summary, err := st.GetCostSummary(ctx, params)
		require.NoError(t, err)

		require.Equal(t, int64(2), summary.TotalRequests)
		require.InDelta(t, 0.045, summary.TotalCostUSD, 0.001)
	})

	t.Run("empty range", func(t *testing.T) {
		params := CostParams{
			From: "2099-01-01",
			To:   "2099-12-31 23:59:59",
		}
		summary, err := st.GetCostSummary(ctx, params)
		require.NoError(t, err)

		require.Equal(t, int64(0), summary.TotalRequests)
		require.Equal(t, float64(0), summary.TotalCostUSD)
	})
}

func TestGetCostBreakdown(t *testing.T) {
	if os.Getenv("SKIP_DB_TESTS") != "" {
		t.Skip("skipping database tests")
	}

	st := newTestStore(t)
	seedCostData(t, st)

	ctx := context.Background()

	t.Run("by model", func(t *testing.T) {
		params := CostParams{
			From:    "2026-03-01",
			To:      "2026-03-10 23:59:59",
			GroupBy: "model",
		}
		entries, err := st.GetCostBreakdown(ctx, params)
		require.NoError(t, err)
		require.Len(t, entries, 2)

		// Ordered by cost DESC: opus (0.055) > sonnet (0.025).
		require.Equal(t, "claude-opus-4-20250514", entries[0].Group)
		require.InDelta(t, 0.055, entries[0].CostUSD, 0.001)
		require.Equal(t, int64(2), entries[0].Requests)

		require.Equal(t, "claude-sonnet-4-20250514", entries[1].Group)
		require.InDelta(t, 0.025, entries[1].CostUSD, 0.001)
		require.Equal(t, int64(2), entries[1].Requests)
	})

	t.Run("by key", func(t *testing.T) {
		params := CostParams{
			From:    "2026-03-01",
			To:      "2026-03-10 23:59:59",
			GroupBy: "key",
		}
		entries, err := st.GetCostBreakdown(ctx, params)
		require.NoError(t, err)
		require.Len(t, entries, 2)

		// Ordered by cost DESC: pk-1 (0.045) > pk-2 (0.035).
		require.Contains(t, entries[0].Group, "key-alpha")
		require.InDelta(t, 0.045, entries[0].CostUSD, 0.001)

		require.Contains(t, entries[1].Group, "key-beta")
		require.InDelta(t, 0.035, entries[1].CostUSD, 0.001)
	})

	t.Run("empty", func(t *testing.T) {
		params := CostParams{
			From:    "2099-01-01",
			To:      "2099-12-31 23:59:59",
			GroupBy: "model",
		}
		entries, err := st.GetCostBreakdown(ctx, params)
		require.NoError(t, err)
		require.Empty(t, entries)
	})
}

func TestGetCostTimeseries(t *testing.T) {
	if os.Getenv("SKIP_DB_TESTS") != "" {
		t.Skip("skipping database tests")
	}

	st := newTestStore(t)
	seedCostData(t, st)

	ctx := context.Background()

	t.Run("daily", func(t *testing.T) {
		params := CostParams{
			From: "2026-03-01",
			To:   "2026-03-10 23:59:59",
		}
		entries, err := st.GetCostTimeseries(ctx, params)
		require.NoError(t, err)
		require.Len(t, entries, 3) // Mar 1, Mar 2, Mar 5

		require.Equal(t, "2026-03-01", entries[0].Date)
		require.InDelta(t, 0.015, entries[0].CostUSD, 0.001)
		require.Equal(t, int64(1), entries[0].Requests)

		require.Equal(t, "2026-03-02", entries[1].Date)
		require.InDelta(t, 0.040, entries[1].CostUSD, 0.001)
		require.Equal(t, int64(2), entries[1].Requests)

		require.Equal(t, "2026-03-05", entries[2].Date)
		require.InDelta(t, 0.025, entries[2].CostUSD, 0.001)
		require.Equal(t, int64(1), entries[2].Requests)
	})

	t.Run("filter by key", func(t *testing.T) {
		params := CostParams{
			From:      "2026-03-01",
			To:        "2026-03-10 23:59:59",
			KeyPrefix: "llmp_sk_bb22",
		}
		entries, err := st.GetCostTimeseries(ctx, params)
		require.NoError(t, err)
		require.Len(t, entries, 2) // Mar 2 (pk-2 only), Mar 5

		require.Equal(t, "2026-03-02", entries[0].Date)
		require.InDelta(t, 0.010, entries[0].CostUSD, 0.001)

		require.Equal(t, "2026-03-05", entries[1].Date)
		require.InDelta(t, 0.025, entries[1].CostUSD, 0.001)
	})

	t.Run("empty", func(t *testing.T) {
		params := CostParams{
			From: "2099-01-01",
			To:   "2099-12-31 23:59:59",
		}
		entries, err := st.GetCostTimeseries(ctx, params)
		require.NoError(t, err)
		require.Empty(t, entries)
	})
}

func TestCacheTokenRoundTrip(t *testing.T) {
	if os.Getenv("SKIP_DB_TESTS") != "" {
		t.Skip("skipping database tests")
	}

	st := newTestStore(t)
	ctx := context.Background()

	err := st.CreateProxyKey(ctx, "pk-cache", "hash-cache", "llmp_sk_cc33", "cache-key")
	require.NoError(t, err)

	cost := 0.05
	entry := &RequestLogEntry{
		ID:                       "log-cache",
		ProxyKeyID:               "pk-cache",
		Timestamp:                time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC),
		SourceFormat:             "anthropic",
		ProviderName:             "anthropic",
		ModelRequested:           "claude-sonnet",
		ModelUpstream:            "claude-sonnet-4-20250514",
		InputTokens:              1,
		OutputTokens:             1,
		CacheCreationInputTokens: 173,
		CacheReadInputTokens:     57686,
		LatencyMs:                500,
		Status:                   200,
		RequestBody:              "{}",
		ResponseBody:             "{}",
		EstimatedCostUSD:         &cost,
	}
	err = st.InsertRequestLog(ctx, entry)
	require.NoError(t, err)

	// Verify via GetRequestLog (detail).
	detail, err := st.GetRequestLog(ctx, "log-cache")
	require.NoError(t, err)
	require.Equal(t, int64(173), detail.CacheCreationInputTokens)
	require.Equal(t, int64(57686), detail.CacheReadInputTokens)

	// Verify via ListRequestLogs (summary).
	summaries, total, err := st.ListRequestLogs(ctx, ListLogsParams{Page: 1, PerPage: 10})
	require.NoError(t, err)
	require.Equal(t, int64(1), total)
	require.Equal(t, int64(173), summaries[0].CacheCreationInputTokens)
	require.Equal(t, int64(57686), summaries[0].CacheReadInputTokens)

	// Verify via GetCostSummary.
	summary, err := st.GetCostSummary(ctx, CostParams{
		From: "2026-03-10",
		To:   "2026-03-10 23:59:59",
	})
	require.NoError(t, err)
	require.Equal(t, int64(173), summary.TotalCacheCreationTokens)
	require.Equal(t, int64(57686), summary.TotalCacheReadTokens)

	// Verify via GetCostBreakdown.
	breakdown, err := st.GetCostBreakdown(ctx, CostParams{
		From:    "2026-03-10",
		To:      "2026-03-10 23:59:59",
		GroupBy: "model",
	})
	require.NoError(t, err)
	require.Len(t, breakdown, 1)
	require.Equal(t, int64(173), breakdown[0].CacheCreationTokens)
	require.Equal(t, int64(57686), breakdown[0].CacheReadTokens)
}
