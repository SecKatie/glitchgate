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

	logs := []RequestLogEntry{
		{
			ID: "log-1", ProxyKeyID: "pk-1",
			Timestamp:    time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 100, OutputTokens: 500,
			LatencyMs: 1200, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
		{
			ID: "log-2", ProxyKeyID: "pk-1",
			Timestamp:    time.Date(2026, 3, 2, 14, 0, 0, 0, time.UTC),
			SourceFormat: "anthropic", ProviderName: "anthropic",
			ModelRequested: "claude-opus", ModelUpstream: "claude-opus-4-20250514",
			InputTokens: 200, OutputTokens: 800,
			LatencyMs: 2500, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
		{
			ID: "log-3", ProxyKeyID: "pk-2",
			Timestamp:    time.Date(2026, 3, 2, 16, 0, 0, 0, time.UTC),
			SourceFormat: "openai", ProviderName: "openai",
			ModelRequested: "claude-sonnet", ModelUpstream: "claude-sonnet-4-20250514",
			InputTokens: 150, OutputTokens: 600,
			LatencyMs: 1800, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		},
		{
			ID: "log-4", ProxyKeyID: "pk-2",
			Timestamp:    time.Date(2026, 3, 5, 9, 0, 0, 0, time.UTC),
			SourceFormat: "openai", ProviderName: "openai",
			ModelRequested: "claude-opus", ModelUpstream: "claude-opus-4-20250514",
			InputTokens: 300, OutputTokens: 1000,
			LatencyMs: 3000, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
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
	})

	t.Run("empty range", func(t *testing.T) {
		params := CostParams{
			From: "2099-01-01",
			To:   "2099-12-31 23:59:59",
		}
		summary, err := st.GetCostSummary(ctx, params)
		require.NoError(t, err)

		require.Equal(t, int64(0), summary.TotalRequests)
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

		// Ordered by request count DESC: opus (2) = sonnet (2), then alphabetical.
		require.Equal(t, int64(2), entries[0].Requests)
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

		require.Contains(t, entries[0].Group, "key-alpha")
		require.Contains(t, entries[1].Group, "key-beta")
	})

	t.Run("by provider", func(t *testing.T) {
		params := CostParams{
			From:    "2026-03-01",
			To:      "2026-03-10 23:59:59",
			GroupBy: "provider",
		}
		entries, err := st.GetCostBreakdown(ctx, params)
		require.NoError(t, err)
		require.Len(t, entries, 2)

		require.Equal(t, int64(2), entries[0].Requests)
		require.Equal(t, int64(2), entries[1].Requests)
	})

	t.Run("by provider preserves host-qualified provider keys", func(t *testing.T) {
		err := st.InsertRequestLog(ctx, &RequestLogEntry{
			ID: "log-5", ProxyKeyID: "pk-1",
			Timestamp:    time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC),
			SourceFormat: "openai", ProviderName: "openai_responses:chatgpt.com",
			ModelRequested: "gpt-5", ModelUpstream: "gpt-5",
			InputTokens: 400, OutputTokens: 900,
			LatencyMs: 900, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		})
		require.NoError(t, err)

		err = st.InsertRequestLog(ctx, &RequestLogEntry{
			ID: "log-6", ProxyKeyID: "pk-2",
			Timestamp:    time.Date(2026, 3, 7, 11, 0, 0, 0, time.UTC),
			SourceFormat: "openai", ProviderName: "openai_responses:api.openai.com",
			ModelRequested: "gpt-5-mini", ModelUpstream: "gpt-5-mini",
			InputTokens: 200, OutputTokens: 300,
			LatencyMs: 850, Status: 200,
			RequestBody: "{}", ResponseBody: "{}",
		})
		require.NoError(t, err)

		params := CostParams{
			From:    "2026-03-01",
			To:      "2026-03-10 23:59:59",
			GroupBy: "provider",
		}
		entries, err := st.GetCostBreakdown(ctx, params)
		require.NoError(t, err)
		require.Len(t, entries, 4)

		require.Equal(t, "anthropic", entries[0].Group)
		require.Equal(t, int64(2), entries[0].Requests)

		require.Equal(t, "openai", entries[1].Group)
		require.Equal(t, int64(2), entries[1].Requests)

		require.Equal(t, "openai_responses:api.openai.com", entries[2].Group)
		require.Equal(t, int64(1), entries[2].Requests)

		require.Equal(t, "openai_responses:chatgpt.com", entries[3].Group)
		require.Equal(t, int64(1), entries[3].Requests)
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
		require.Equal(t, int64(1), entries[0].Requests)

		require.Equal(t, "2026-03-02", entries[1].Date)
		require.Equal(t, int64(2), entries[1].Requests)

		require.Equal(t, "2026-03-05", entries[2].Date)
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
		require.Equal(t, "2026-03-05", entries[1].Date)
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

	t.Run("buckets per timestamp timezone rules across DST", func(t *testing.T) {
		dstStore := newTestStore(t)
		require.NoError(t, dstStore.CreateProxyKey(ctx, "pk-dst", "hash-dst", "llmp_sk_dst1", "dst-key"))

		nyc, err := time.LoadLocation("America/New_York")
		require.NoError(t, err)

		require.NoError(t, dstStore.InsertRequestLog(ctx, &RequestLogEntry{
			ID:             "dst-before",
			ProxyKeyID:     "pk-dst",
			Timestamp:      time.Date(2026, 3, 8, 4, 30, 0, 0, time.UTC),
			SourceFormat:   "responses",
			ProviderName:   "openai_responses",
			ModelRequested: "gpt-4o",
			ModelUpstream:  "gpt-4o",
			Status:         200,
			RequestBody:    "{}",
			ResponseBody:   "{}",
		}))
		require.NoError(t, dstStore.InsertRequestLog(ctx, &RequestLogEntry{
			ID:             "dst-after",
			ProxyKeyID:     "pk-dst",
			Timestamp:      time.Date(2026, 3, 8, 7, 30, 0, 0, time.UTC),
			SourceFormat:   "responses",
			ProviderName:   "openai_responses",
			ModelRequested: "gpt-4o",
			ModelUpstream:  "gpt-4o",
			Status:         200,
			RequestBody:    "{}",
			ResponseBody:   "{}",
		}))

		entries, err := dstStore.GetCostTimeseries(ctx, CostParams{
			From:       "2026-03-07 05:00:00",
			To:         "2026-03-09 03:59:59",
			TzLocation: nyc,
		})
		require.NoError(t, err)
		require.Len(t, entries, 2)
		require.Equal(t, "2026-03-07", entries[0].Date)
		require.Equal(t, "2026-03-08", entries[1].Date)
	})
}

func TestProviderGroupFiltersApplyAcrossCostQueries(t *testing.T) {
	if os.Getenv("SKIP_DB_TESTS") != "" {
		t.Skip("skipping database tests")
	}

	st := newTestStore(t)
	seedCostData(t, st)

	ctx := context.Background()

	err := st.InsertRequestLog(ctx, &RequestLogEntry{
		ID: "log-5", ProxyKeyID: "pk-1",
		Timestamp:      time.Date(2026, 3, 6, 10, 0, 0, 0, time.UTC),
		SourceFormat:   "openai",
		ProviderName:   "openai_responses:chatgpt.com",
		ModelRequested: "gpt-5",
		ModelUpstream:  "gpt-5",
		InputTokens:    400,
		OutputTokens:   900,
		LatencyMs:      900,
		Status:         200,
		RequestBody:    "{}",
		ResponseBody:   "{}",
	})
	require.NoError(t, err)

	err = st.InsertRequestLog(ctx, &RequestLogEntry{
		ID: "log-6", ProxyKeyID: "pk-2",
		Timestamp:      time.Date(2026, 3, 7, 11, 0, 0, 0, time.UTC),
		SourceFormat:   "openai",
		ProviderName:   "openai_responses:api.openai.com",
		ModelRequested: "gpt-5-mini",
		ModelUpstream:  "gpt-5-mini",
		InputTokens:    200,
		OutputTokens:   300,
		LatencyMs:      850,
		Status:         200,
		RequestBody:    "{}",
		ResponseBody:   "{}",
	})
	require.NoError(t, err)

	params := CostParams{
		From:           "2026-03-01",
		To:             "2026-03-10 23:59:59",
		GroupBy:        "provider",
		GroupFilter:    "chatgpt-pro",
		ProviderGroups: []string{"openai", "openai_responses:api.openai.com", "openai_responses:chatgpt.com"},
	}

	summary, err := st.GetCostSummary(ctx, params)
	require.NoError(t, err)
	require.Equal(t, int64(4), summary.TotalRequests)

	breakdown, err := st.GetCostBreakdown(ctx, params)
	require.NoError(t, err)
	require.Len(t, breakdown, 3)
	require.Equal(t, "openai", breakdown[0].Group)
	require.Equal(t, "openai_responses:api.openai.com", breakdown[1].Group)
	require.Equal(t, "openai_responses:chatgpt.com", breakdown[2].Group)

	pricingGroups, err := st.GetCostPricingGroups(ctx, params)
	require.NoError(t, err)
	require.Len(t, pricingGroups, 4)
	require.Equal(t, "openai", pricingGroups[0].ProviderName)
	require.Equal(t, "openai_responses:api.openai.com", pricingGroups[2].ProviderName)
	require.Equal(t, "openai_responses:chatgpt.com", pricingGroups[3].ProviderName)

	timeseries, err := st.GetCostTimeseries(ctx, params)
	require.NoError(t, err)
	require.Len(t, timeseries, 4)
	require.Equal(t, "2026-03-02", timeseries[0].Date)
	require.Equal(t, "2026-03-05", timeseries[1].Date)
	require.Equal(t, "2026-03-06", timeseries[2].Date)
	require.Equal(t, "2026-03-07", timeseries[3].Date)
}

func TestCacheTokenRoundTrip(t *testing.T) {
	if os.Getenv("SKIP_DB_TESTS") != "" {
		t.Skip("skipping database tests")
	}

	st := newTestStore(t)
	ctx := context.Background()

	err := st.CreateProxyKey(ctx, "pk-cache", "hash-cache", "llmp_sk_cc33", "cache-key")
	require.NoError(t, err)

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
